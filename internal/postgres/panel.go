package postgres

import (
	"context"
	"database/sql"
	"time"

	"central-automacoes/internal/domain"
)

func (s *Store) PanelSnapshot(ctx context.Context, from, to time.Time) (domain.PanelSnapshot, error) {
	// All statements share the first query's snapshot, so a concurrent scheduler,
	// patch, cancel or worker cannot mix automation and run versions in the panel.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return domain.PanelSnapshot{}, err
	}
	defer tx.Rollback()
	result := domain.PanelSnapshot{Automations: []domain.Automation{}, Runs: []domain.Run{}}
	rows, err := tx.QueryContext(ctx, "SELECT document FROM automations ORDER BY created_at DESC,id ASC")
	if err != nil {
		return domain.PanelSnapshot{}, err
	}
	for rows.Next() {
		a, err := scanAutomation(rows)
		if err != nil {
			rows.Close()
			return domain.PanelSnapshot{}, err
		}
		result.Automations = append(result.Automations, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return domain.PanelSnapshot{}, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT "+runColumns+" FROM runs WHERE (created_at >= $1 AND created_at <= $2) OR status IN ('pending','running') ORDER BY created_at DESC,id DESC", from.UTC(), to.UTC())
	if err != nil {
		return domain.PanelSnapshot{}, err
	}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			rows.Close()
			return domain.PanelSnapshot{}, err
		}
		result.Runs = append(result.Runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return domain.PanelSnapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.PanelSnapshot{}, err
	}
	return result, nil
}
