package memory

import (
	"context"
	"time"

	"central-automacoes/internal/domain"
)

func (s *Store) PanelSnapshot(ctx context.Context, from, to time.Time) (domain.PanelSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.PanelSnapshot{}, err
	}
	result := domain.PanelSnapshot{Automations: []domain.Automation{}, Runs: []domain.Run{}}
	for _, a := range s.automations {
		result.Automations = append(result.Automations, domain.CloneAutomation(a))
	}
	for _, run := range s.runs {
		if run.Status == domain.Pending || run.Status == domain.Running || (!run.CreatedAt.Before(from) && !run.CreatedAt.After(to)) {
			result.Runs = append(result.Runs, cloneRun(run))
		}
	}
	return result, nil
}
