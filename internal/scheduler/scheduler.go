// Package scheduler emits persistent executions; workers own all task execution.
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"central-automacoes/internal/domain"
)

type Scheduler struct {
	Store    domain.Store
	Logger   *slog.Logger
	Interval time.Duration
}

// Run checks immediately on startup to recover missed occurrences, then polls.
// ScheduleDue owns the transaction that emits runs and advances next_run_at.
func (s *Scheduler) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		tick, cancel := context.WithTimeout(ctx, 5*time.Second)
		runs, err := s.Store.ScheduleDue(tick, time.Now().UTC(), 100)
		cancel()
		if err != nil && ctx.Err() == nil {
			s.Logger.Error("falha no agendador", "error", err)
		} else if len(runs) > 0 {
			s.Logger.Info("execuções agendadas", "count", len(runs))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
