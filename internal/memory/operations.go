package memory

import (
	"context"
	"errors"
	"sort"
	"time"

	"central-automacoes/internal/domain"
)

// admit runs under the store lock, shared by every source of new executions.
func (s *Store) admit(a domain.Automation) error {
	if a.ArchivedAt != nil {
		return domain.ErrArchived
	}
	if !a.Enabled {
		return domain.ErrDisabled
	}
	if !a.AllowsOverlap() && s.busy(a.ID) {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) busy(id string) bool {
	for _, r := range s.runs {
		if r.AutomationID == id && (r.Status == domain.Pending || r.Status == domain.Running) {
			return true
		}
	}
	return false
}

func (s *Store) runningOther(automationID, runID string) bool {
	for _, r := range s.runs {
		if r.AutomationID == automationID && r.ID != runID && r.Status == domain.Running {
			return true
		}
	}
	return false
}

func (s *Store) SetSchedulePaused(ctx context.Context, id string, paused bool) (domain.Automation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Automation{}, err
	}
	a, ok := s.automations[id]
	if !ok {
		return domain.Automation{}, domain.ErrNotFound
	}
	if a.ArchivedAt != nil || a.Schedule == nil {
		return domain.Automation{}, domain.ErrConflict
	}
	if a.Schedule.Paused == paused {
		return domain.CloneAutomation(a), nil
	}
	a = domain.CloneAutomation(a)
	a.Schedule.Paused = paused
	a.NextRunAt = nil
	a.UpdatedAt = time.Now().UTC()
	prepared, err := a.Prepared(a.UpdatedAt)
	if err != nil {
		return domain.Automation{}, domain.ValidationError{Err: err}
	}
	s.automations[id] = prepared
	return domain.CloneAutomation(prepared), nil
}

func (s *Store) ArchiveAutomation(ctx context.Context, id string) (domain.Automation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Automation{}, err
	}
	a, ok := s.automations[id]
	if !ok {
		return domain.Automation{}, domain.ErrNotFound
	}
	if a.ArchivedAt != nil {
		return domain.CloneAutomation(a), nil
	}
	now := time.Now().UTC()
	a = domain.CloneAutomation(a)
	a.ArchivedAt = &now
	a.Enabled = false
	a.NextRunAt = nil
	a.UpdatedAt = now
	s.automations[id] = a
	return domain.CloneAutomation(a), nil
}

func (s *Store) QueryRuns(ctx context.Context, f domain.RunFilter) ([]domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.Validate(); err != nil {
		return nil, domain.ValidationError{Err: err}
	}
	out := make([]domain.Run, 0)
	for _, r := range s.runs {
		if f.AutomationID != "" && r.AutomationID != f.AutomationID {
			continue
		}
		if f.Status != "" && r.Status != f.Status {
			continue
		}
		if f.From != nil && r.CreatedAt.Before(*f.From) {
			continue
		}
		if f.To != nil && r.CreatedAt.After(*f.To) {
			continue
		}
		out = append(out, cloneRun(r))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if f.Offset >= len(out) {
		return []domain.Run{}, nil
	}
	out = out[f.Offset:]
	limit := f.Limit
	if limit == 0 {
		limit = 100
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) Rerun(ctx context.Context, id string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Run{}, err
	}
	old, ok := s.runs[id]
	if !ok {
		return domain.Run{}, domain.ErrNotFound
	}
	if old.Status != domain.Failed && old.Status != domain.Canceled {
		return domain.Run{}, domain.ErrConflict
	}
	a, ok := s.automations[old.AutomationID]
	if !ok {
		return domain.Run{}, domain.ErrNotFound
	}
	if err := s.admit(a); err != nil {
		return domain.Run{}, err
	}
	r := domain.NewRerun(old)
	s.runs[r.ID] = cloneRun(r)
	s.attempts[r.ID] = []domain.Attempt{}
	return cloneRun(r), nil
}

func (s *Store) CancelRun(ctx context.Context, id string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Run{}, err
	}
	r, ok := s.runs[id]
	if !ok {
		return domain.Run{}, domain.ErrNotFound
	}
	if r.Status == domain.Canceled || r.CancelRequestedAt != nil {
		return cloneRun(r), nil
	}
	if r.Status.Terminal() {
		return domain.Run{}, domain.ErrConflict
	}
	now := time.Now().UTC()
	r.CancelRequestedAt = &now
	if r.Status == domain.Pending {
		r.Status = domain.Canceled
		r.FinishedAt = &now
		result := domain.Result{}
		if r.Result != nil {
			result = *r.Result
		}
		result = domain.CancelResult(result)
		r.Result = &result
	}
	s.runs[id] = r
	return cloneRun(r), nil
}

func (s *Store) ScheduleDue(ctx context.Context, now time.Time, limit int) ([]domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 1000 {
		return nil, domain.ValidationError{Err: errors.New("limit deve estar entre 1 e 1000")}
	}
	now = now.UTC()
	due := make([]domain.Automation, 0)
	for _, a := range s.automations {
		if a.ScheduleActive() && a.NextRunAt != nil && !a.NextRunAt.After(now) {
			due = append(due, domain.CloneAutomation(a))
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].NextRunAt.Equal(*due[j].NextRunAt) {
			return due[i].ID < due[j].ID
		}
		return due[i].NextRunAt.Before(*due[j].NextRunAt)
	})
	if len(due) > limit {
		due = due[:limit]
	}
	// Stage all changes before committing so an invalid schedule cannot half-apply a tick.
	updated := make([]domain.Automation, 0, len(due))
	runs := make([]domain.Run, 0, len(due))
	keys := make([]string, 0, len(due))
	for _, a := range due {
		next, emit, err := a.AdvanceSchedule(now)
		if err != nil {
			return nil, domain.ValidationError{Err: err}
		}
		updated = append(updated, next)
		key := occurrenceKey(a.ID, *a.NextRunAt)
		if _, exists := s.occurrences[key]; exists {
			continue
		}
		if emit && (a.AllowsOverlap() || !s.busy(a.ID)) {
			runs = append(runs, domain.NewScheduledRun(a, *a.NextRunAt, now))
			keys = append(keys, key)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, a := range updated {
		s.automations[a.ID] = a
	}
	out := make([]domain.Run, 0, len(runs))
	for i, r := range runs {
		s.runs[r.ID] = cloneRun(r)
		s.attempts[r.ID] = []domain.Attempt{}
		s.occurrences[keys[i]] = struct{}{}
		out = append(out, cloneRun(r))
	}
	return out, nil
}

func occurrenceKey(id string, at time.Time) string {
	return id + "/" + at.UTC().Format(time.RFC3339Nano)
}
