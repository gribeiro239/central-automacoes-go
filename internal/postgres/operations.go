package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"central-automacoes/internal/domain"
)

// Every mutation takes this short schema-scoped control lock before row locks.
// This keeps admission, cancellation and claims consistent across API/scheduler/
// worker processes without holding a database lock during outbound HTTP calls.
func (s *Store) beginMutation(ctx context.Context) (*sql.Tx, error) {
	// Each query after waiting for the control lock must see the preceding
	// mutation's commit, regardless of a server/session isolation default.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(current_database()),hashtext('central-control:'||current_schema()))"); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func insertRun(ctx context.Context, tx *sql.Tx, run domain.Run) error {
	snapshot, err := json.Marshal(run.Snapshot)
	if err != nil {
		return err
	}
	var parent any
	if run.ParentRunID != "" {
		parent = run.ParentRunID
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO runs(id,automation_id,snapshot,origin,status,attempt_count,available_at,created_at,scheduled_for,parent_run_id,cancel_requested_at) VALUES($1,$2,$3::jsonb,$4,$5,$6,$7,$8,$9,$10,$11)", run.ID, run.AutomationID, string(snapshot), run.Origin, run.Status, run.AttemptCount, run.AvailableAt, run.CreatedAt, run.ScheduledFor, parent, run.CancelRequestedAt)
	return err
}

func saveAutomation(ctx context.Context, tx *sql.Tx, a domain.Automation) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE automations SET document=$2::jsonb,next_run_at=$3 WHERE id=$1", a.ID, string(raw), a.NextRunAt)
	return err
}

func busy(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runs WHERE automation_id=$1 AND status IN ('pending','running'))", id).Scan(&exists)
	return exists, err
}

func (s *Store) admit(ctx context.Context, tx *sql.Tx, a domain.Automation) error {
	if a.ArchivedAt != nil {
		return domain.ErrArchived
	}
	if !a.Enabled {
		return domain.ErrDisabled
	}
	if !a.AllowsOverlap() {
		active, err := busy(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		if active {
			return domain.ErrConflict
		}
	}
	return nil
}

func (s *Store) QueryRuns(ctx context.Context, f domain.RunFilter) ([]domain.Run, error) {
	if err := f.Validate(); err != nil {
		return nil, domain.ValidationError{Err: err}
	}
	if f.Limit == 0 {
		f.Limit = 100
	}
	conditions := []string{"TRUE"}
	args := []any{}
	add := func(column, operator string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf("%s %s $%d", column, operator, len(args)))
	}
	if f.AutomationID != "" {
		add("automation_id", "=", f.AutomationID)
	}
	if f.Status != "" {
		add("status", "=", f.Status)
	}
	if f.From != nil {
		add("created_at", ">=", f.From.UTC())
	}
	if f.To != nil {
		add("created_at", "<=", f.To.UTC())
	}
	args = append(args, f.Limit, f.Offset)
	query := "SELECT " + runColumns + " FROM runs WHERE " + strings.Join(conditions, " AND ") + fmt.Sprintf(" ORDER BY created_at DESC,id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (s *Store) ScheduleDue(ctx context.Context, now time.Time, limit int) ([]domain.Run, error) {
	if limit < 1 || limit > 1000 {
		return nil, domain.ValidationError{Err: errors.New("limite de tick deve estar entre 1 e 1000")}
	}
	now = now.UTC()
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT document FROM automations WHERE next_run_at<=$1 ORDER BY next_run_at,id LIMIT $2 FOR UPDATE", now, limit)
	if err != nil {
		return nil, err
	}
	due := []domain.Automation{}
	for rows.Next() {
		a, err := scanAutomation(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		due = append(due, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	emitted := []domain.Run{}
	for _, a := range due {
		if !a.ScheduleActive() || a.NextRunAt == nil {
			a.NextRunAt = nil
			if err = saveAutomation(ctx, tx, a); err != nil {
				return nil, err
			}
			continue
		}
		scheduledFor := *a.NextRunAt
		updated, emit, advanceErr := a.AdvanceSchedule(now)
		if advanceErr != nil {
			return nil, advanceErr
		}
		if emit && !a.AllowsOverlap() {
			active, busyErr := busy(ctx, tx, a.ID)
			if busyErr != nil {
				return nil, busyErr
			}
			emit = !active
		}
		// Advancing and inserting are part of this one transaction, including a
		// trigger failure on either statement. Never consume an occurrence alone.
		if err = saveAutomation(ctx, tx, updated); err != nil {
			return nil, err
		}
		if emit {
			run := domain.NewScheduledRun(a, scheduledFor, now)
			var exists bool
			if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runs WHERE automation_id=$1 AND scheduled_for=$2)", a.ID, scheduledFor).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				if err = insertRun(ctx, tx, run); err != nil {
					return nil, err
				}
				emitted = append(emitted, run)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return emitted, nil
}

func (s *Store) SetSchedulePaused(ctx context.Context, id string, paused bool) (domain.Automation, error) {
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return domain.Automation{}, err
	}
	defer tx.Rollback()
	a, err := scanAutomation(tx.QueryRowContext(ctx, "SELECT document FROM automations WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return a, err
	}
	if a.ArchivedAt != nil {
		return domain.Automation{}, domain.ErrArchived
	}
	if a.Schedule == nil {
		return domain.Automation{}, domain.ErrConflict
	}
	if a.Schedule.Paused == paused {
		return a, tx.Commit()
	}
	a.Schedule.Paused = paused
	a.NextRunAt = nil
	a.UpdatedAt = time.Now().UTC()
	a, err = a.Prepared(a.UpdatedAt)
	if err != nil {
		return domain.Automation{}, domain.ValidationError{Err: err}
	}
	if err = saveAutomation(ctx, tx, a); err != nil {
		return domain.Automation{}, err
	}
	return a, tx.Commit()
}

func (s *Store) ArchiveAutomation(ctx context.Context, id string) (domain.Automation, error) {
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return domain.Automation{}, err
	}
	defer tx.Rollback()
	a, err := scanAutomation(tx.QueryRowContext(ctx, "SELECT document FROM automations WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return a, err
	}
	if a.ArchivedAt != nil {
		return a, tx.Commit()
	}
	now := time.Now().UTC()
	a.ArchivedAt = &now
	a.UpdatedAt = now
	a.Enabled = false
	a.NextRunAt = nil
	if err = saveAutomation(ctx, tx, a); err != nil {
		return domain.Automation{}, err
	}
	return a, tx.Commit()
}

func (s *Store) Rerun(ctx context.Context, id string) (domain.Run, error) {
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return domain.Run{}, err
	}
	defer tx.Rollback()
	old, err := scanRun(tx.QueryRowContext(ctx, "SELECT "+runColumns+" FROM runs WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return domain.Run{}, err
	}
	if old.Status != domain.Failed && old.Status != domain.Canceled {
		return domain.Run{}, domain.ErrConflict
	}
	a, err := scanAutomation(tx.QueryRowContext(ctx, "SELECT document FROM automations WHERE id=$1 FOR UPDATE", old.AutomationID))
	if err != nil {
		return domain.Run{}, err
	}
	if err = s.admit(ctx, tx, a); err != nil {
		return domain.Run{}, err
	}
	run := domain.NewRerun(old)
	if err = insertRun(ctx, tx, run); err != nil {
		return domain.Run{}, err
	}
	return run, tx.Commit()
}

func (s *Store) CancelRun(ctx context.Context, id string) (domain.Run, error) {
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return domain.Run{}, err
	}
	defer tx.Rollback()
	run, err := scanRun(tx.QueryRowContext(ctx, "SELECT "+runColumns+" FROM runs WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return domain.Run{}, err
	}
	if run.Status == domain.Canceled || run.CancelRequestedAt != nil {
		return run, tx.Commit()
	}
	if run.Status.Terminal() {
		return domain.Run{}, domain.ErrConflict
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return domain.Run{}, err
	}
	now = now.UTC()
	run.CancelRequestedAt = &now
	if run.Status == domain.Pending {
		result := domain.Result{}
		if run.Result != nil {
			result = *run.Result
		}
		result = domain.CancelResult(result)
		run.Result = &result
		run.Status = domain.Canceled
		run.FinishedAt = &now
		raw, _ := json.Marshal(result)
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='canceled',cancel_requested_at=$2,finished_at=$2,result=$3::jsonb WHERE id=$1", id, now, string(raw)); err != nil {
			return domain.Run{}, err
		}
	} else {
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET cancel_requested_at=$2 WHERE id=$1", id, now); err != nil {
			return domain.Run{}, err
		}
	}
	return run, tx.Commit()
}
