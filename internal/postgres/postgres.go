// Package postgres provides a durable, transactional queue backed by PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"central-automacoes/internal/domain"
	"central-automacoes/migrations"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Store struct{ db *sql.DB }

var _ domain.Store = (*Store)(nil)

func Open(ctx context.Context, url string) (*Store, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("abrir PostgreSQL: %w", err)
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("conectar ao PostgreSQL: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Migrate is repeatable and transactional. The advisory lock serializes startup migrations.
func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ledger sql.NullString
	if err = tx.QueryRowContext(ctx, "SELECT to_regclass('schema_migrations')::text").Scan(&ledger); err != nil {
		return err
	}
	if !ledger.Valid {
		if _, err = tx.ExecContext(ctx, migrations.Initial); err != nil {
			return fmt.Errorf("aplicar migração 001: %w", err)
		}
	}
	var initial, scheduled bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=1),EXISTS(SELECT 1 FROM schema_migrations WHERE version=2)").Scan(&initial, &scheduled); err != nil {
		return err
	}
	if !initial {
		return errors.New("migração 001 ausente no histórico; recusando alterar esquema desconhecido")
	}
	if !scheduled {
		if _, err = tx.ExecContext(ctx, migrations.Scheduling); err != nil {
			return fmt.Errorf("aplicar migração 002: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES(2)"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CreateAutomation(ctx context.Context, a domain.Automation) (domain.Automation, error) {
	var err error
	a, err = a.Prepared(time.Now().UTC())
	if err != nil {
		return domain.Automation{}, domain.ValidationError{Err: err}
	}
	data, err := json.Marshal(a)
	if err != nil {
		return domain.Automation{}, err
	}
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return domain.Automation{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "INSERT INTO automations(id,document,created_at,next_run_at) VALUES($1,$2::jsonb,$3,$4)", a.ID, string(data), a.CreatedAt, a.NextRunAt)
	if err != nil {
		return domain.Automation{}, err
	}
	return domain.CloneAutomation(a), tx.Commit()
}

func (s *Store) GetAutomation(ctx context.Context, id string) (domain.Automation, error) {
	return scanAutomation(s.db.QueryRowContext(ctx, "SELECT document FROM automations WHERE id=$1", id))
}

func (s *Store) ListAutomations(ctx context.Context) ([]domain.Automation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT document FROM automations ORDER BY created_at DESC,id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Automation{}
	for rows.Next() {
		a, err := scanAutomation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *Store) PatchAutomation(ctx context.Context, id string, p domain.PatchAutomation) (domain.Automation, error) {
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return domain.Automation{}, err
	}
	defer tx.Rollback()
	a, err := scanAutomation(tx.QueryRowContext(ctx, "SELECT document FROM automations WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return a, err
	}
	a, err = a.Patch(p)
	if errors.Is(err, domain.ErrArchived) {
		return domain.Automation{}, err
	}
	if err != nil {
		return domain.Automation{}, domain.ValidationError{Err: err}
	}
	data, err := json.Marshal(a)
	if err != nil {
		return domain.Automation{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE automations SET document=$2::jsonb,next_run_at=$3 WHERE id=$1", id, string(data), a.NextRunAt); err != nil {
		return domain.Automation{}, err
	}
	return a, tx.Commit()
}

func (s *Store) Enqueue(ctx context.Context, id string) (domain.Run, error) {
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return domain.Run{}, err
	}
	defer tx.Rollback()
	a, err := scanAutomation(tx.QueryRowContext(ctx, "SELECT document FROM automations WHERE id=$1 FOR SHARE", id))
	if err != nil {
		return domain.Run{}, err
	}
	if err = s.admit(ctx, tx, a); err != nil {
		return domain.Run{}, err
	}
	run := domain.NewRun(a)
	err = insertRun(ctx, tx, run)
	if err != nil {
		return domain.Run{}, err
	}
	return run, tx.Commit()
}

const runColumns = "id,automation_id,snapshot,origin,status,attempt_count,available_at,created_at,finished_at,result,scheduled_for,parent_run_id,cancel_requested_at"

func (s *Store) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return scanRun(s.db.QueryRowContext(ctx, "SELECT "+runColumns+" FROM runs WHERE id=$1", id))
}

func (s *Store) ListRuns(ctx context.Context) ([]domain.Run, error) {
	return s.QueryRuns(ctx, domain.RunFilter{Limit: 100})
}

func (s *Store) ListAttempts(ctx context.Context, id string) ([]domain.Attempt, error) {
	if _, err := s.GetRun(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,run_id,number,worker_id,started_at,finished_at,result FROM run_attempts WHERE run_id=$1 ORDER BY number", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Attempt{}
	for rows.Next() {
		var a domain.Attempt
		var raw []byte
		var finished sql.NullTime
		if err = rows.Scan(&a.ID, &a.RunID, &a.Number, &a.WorkerID, &a.StartedAt, &finished, &raw); err != nil {
			return nil, err
		}
		if finished.Valid {
			utc := finished.Time.UTC()
			a.FinishedAt = &utc
		}
		a.StartedAt = a.StartedAt.UTC()
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &a.Result); err != nil {
				return nil, err
			}
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// Claim recovers abandoned attempts and reserves one eligible run in the same short transaction.
func (s *Store) Claim(ctx context.Context, worker string, lease time.Duration) (*domain.Claim, error) {
	if lease <= 0 {
		return nil, domain.ValidationError{Err: errors.New("lease deve ser positiva")}
	}
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var now time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return nil, err
	}
	now = now.UTC()
	rows, err := tx.QueryContext(ctx, "SELECT "+runColumns+" FROM runs WHERE status='running' AND lease_until<=$1 FOR UPDATE SKIP LOCKED", now)
	if err != nil {
		return nil, err
	}
	expired := []domain.Run{}
	for rows.Next() {
		run, scanErr := scanRun(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		expired = append(expired, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, run := range expired {
		result := domain.Result{Error: "reserva expirada; executor anterior interrompido", Retryable: true}
		if run.CancelRequestedAt != nil {
			result = domain.CancelResult(result)
		}
		raw, _ := json.Marshal(result)
		if _, err = tx.ExecContext(ctx, "UPDATE run_attempts SET finished_at=$3,result=$4::jsonb WHERE run_id=$1 AND number=$2 AND finished_at IS NULL", run.ID, run.AttemptCount, now, string(raw)); err != nil {
			return nil, err
		}
		status := domain.Pending
		var finished any
		if run.AttemptCount >= run.Snapshot.MaxAttempts {
			status = domain.Failed
			finished = now
		}
		if run.CancelRequestedAt != nil {
			status = domain.Canceled
			finished = now
		}
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET status=$2,available_at=$3,finished_at=$4,result=$5::jsonb,lease_token=NULL,lease_until=NULL WHERE id=$1", run.ID, status, now, finished, string(raw)); err != nil {
			return nil, err
		}
	}
	run, err := scanRun(tx.QueryRowContext(ctx, "SELECT "+runColumns+" FROM runs WHERE status='pending' AND available_at<=$1 AND (COALESCE((SELECT document->>'overlap_policy' FROM automations WHERE id=runs.automation_id),'allow')<>'forbid' OR NOT EXISTS(SELECT 1 FROM runs active WHERE active.automation_id=runs.automation_id AND active.status='running')) ORDER BY available_at,created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED", now))
	if errors.Is(err, domain.ErrNotFound) {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run.Status = domain.Running
	run.AttemptCount++
	run.FinishedAt = nil
	run.Result = nil
	token := domain.NewID()
	until := now.Add(lease)
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='running',attempt_count=$2,finished_at=NULL,result=NULL,lease_token=$3,lease_until=$4 WHERE id=$1", run.ID, run.AttemptCount, token, until); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO run_attempts(id,run_id,number,worker_id,started_at) VALUES($1,$2,$3,$4,$5)", domain.NewID(), run.ID, run.AttemptCount, worker, now); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &domain.Claim{Run: run, Token: token}, nil
}

func (s *Store) Finish(ctx context.Context, id, token string, result domain.Result, retryAt *time.Time) (domain.Run, error) {
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return domain.Run{}, err
	}
	defer tx.Rollback()
	var leaseToken sql.NullString
	var leaseUntil sql.NullTime
	run, err := scanRun(tx.QueryRowContext(ctx, "SELECT "+runColumns+",lease_token,lease_until FROM runs WHERE id=$1 FOR UPDATE", id), &leaseToken, &leaseUntil)
	if err != nil {
		return domain.Run{}, err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return domain.Run{}, err
	}
	now = now.UTC()
	if run.Status != domain.Running || !leaseToken.Valid || leaseToken.String != token || !leaseUntil.Valid || !leaseUntil.Time.After(now) {
		return domain.Run{}, domain.ErrLeaseLost
	}
	if run.CancelRequestedAt != nil {
		result = domain.CancelResult(result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return domain.Run{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE run_attempts SET finished_at=$3,result=$4::jsonb WHERE run_id=$1 AND number=$2", id, run.AttemptCount, now, string(raw)); err != nil {
		return domain.Run{}, err
	}
	run.Result = &result
	run.FinishedAt = &now
	run.Status = domain.Succeeded
	if result.Error != "" {
		run.Status = domain.Failed
		if result.Retryable && retryAt != nil && run.AttemptCount < run.Snapshot.MaxAttempts {
			run.Status = domain.Pending
			run.FinishedAt = nil
			run.AvailableAt = retryAt.UTC()
		}
	}
	if run.CancelRequestedAt != nil {
		run.Status = domain.Canceled
		run.FinishedAt = &now
	}
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET status=$2,available_at=$3,finished_at=$4,result=$5::jsonb,lease_token=NULL,lease_until=NULL WHERE id=$1", id, run.Status, run.AvailableAt, run.FinishedAt, string(raw)); err != nil {
		return domain.Run{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

type scanner interface{ Scan(...any) error }

func scanAutomation(row scanner) (domain.Automation, error) {
	var a domain.Automation
	var raw []byte
	err := row.Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return a, domain.ErrNotFound
	}
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(raw, &a)
	return a, err
}

func scanRun(row scanner, extra ...any) (domain.Run, error) {
	var run domain.Run
	var snapshot, result []byte
	var finished, scheduled, cancelRequested sql.NullTime
	var parent sql.NullString
	dest := []any{&run.ID, &run.AutomationID, &snapshot, &run.Origin, &run.Status, &run.AttemptCount, &run.AvailableAt, &run.CreatedAt, &finished, &result, &scheduled, &parent, &cancelRequested}
	dest = append(dest, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return run, domain.ErrNotFound
	}
	if err != nil {
		return run, err
	}
	if err = json.Unmarshal(snapshot, &run.Snapshot); err != nil {
		return run, err
	}
	if finished.Valid {
		utc := finished.Time.UTC()
		run.FinishedAt = &utc
	}
	if scheduled.Valid {
		utc := scheduled.Time.UTC()
		run.ScheduledFor = &utc
	}
	if parent.Valid {
		run.ParentRunID = parent.String
	}
	if cancelRequested.Valid {
		utc := cancelRequested.Time.UTC()
		run.CancelRequestedAt = &utc
	}
	run.CreatedAt = run.CreatedAt.UTC()
	run.AvailableAt = run.AvailableAt.UTC()
	if len(result) > 0 {
		err = json.Unmarshal(result, &run.Result)
	}
	return run, err
}
