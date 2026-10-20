// Package memory provides a volatile, concurrency-safe development store.
package memory

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"central-automacoes/internal/domain"
)

type reservation struct {
	token   string
	expires time.Time
}

type Store struct {
	mu          sync.Mutex
	automations map[string]domain.Automation
	runs        map[string]domain.Run
	attempts    map[string][]domain.Attempt
	leases      map[string]reservation
	occurrences map[string]struct{}
}

var _ domain.Store = (*Store)(nil)

func New() *Store {
	return &Store{automations: map[string]domain.Automation{}, runs: map[string]domain.Run{}, attempts: map[string][]domain.Attempt{}, leases: map[string]reservation{}, occurrences: map[string]struct{}{}}
}

func (s *Store) Ping(ctx context.Context) error { return ctx.Err() }

func (s *Store) CreateAutomation(ctx context.Context, a domain.Automation) (domain.Automation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Automation{}, err
	}
	var err error
	a, err = a.Prepared(time.Now().UTC())
	if err != nil {
		return domain.Automation{}, domain.ValidationError{Err: err}
	}
	if _, exists := s.automations[a.ID]; exists {
		return domain.Automation{}, domain.ValidationError{Err: errors.New("id já existente")}
	}
	s.automations[a.ID] = domain.CloneAutomation(a)
	return domain.CloneAutomation(a), nil
}

func (s *Store) ListAutomations(ctx context.Context) ([]domain.Automation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]domain.Automation, 0, len(s.automations))
	for _, a := range s.automations {
		out = append(out, domain.CloneAutomation(a))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Store) GetAutomation(ctx context.Context, id string) (domain.Automation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Automation{}, err
	}
	a, ok := s.automations[id]
	if !ok {
		return domain.Automation{}, domain.ErrNotFound
	}
	return domain.CloneAutomation(a), nil
}

func (s *Store) PatchAutomation(ctx context.Context, id string, p domain.PatchAutomation) (domain.Automation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Automation{}, err
	}
	a, ok := s.automations[id]
	if !ok {
		return domain.Automation{}, domain.ErrNotFound
	}
	patched, err := a.Patch(p)
	if err != nil {
		if errors.Is(err, domain.ErrArchived) {
			return domain.Automation{}, err
		}
		return domain.Automation{}, domain.ValidationError{Err: err}
	}
	s.automations[id] = domain.CloneAutomation(patched)
	return domain.CloneAutomation(patched), nil
}

func (s *Store) Enqueue(ctx context.Context, id string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Run{}, err
	}
	a, ok := s.automations[id]
	if !ok {
		return domain.Run{}, domain.ErrNotFound
	}
	if err := s.admit(a); err != nil {
		return domain.Run{}, err
	}
	run := domain.NewRun(a)
	s.runs[run.ID] = cloneRun(run)
	s.attempts[run.ID] = []domain.Attempt{}
	return cloneRun(run), nil
}

func (s *Store) ListRuns(ctx context.Context) ([]domain.Run, error) {
	return s.QueryRuns(ctx, domain.RunFilter{})
}

func (s *Store) GetRun(ctx context.Context, id string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Run{}, err
	}
	r, ok := s.runs[id]
	if !ok {
		return domain.Run{}, domain.ErrNotFound
	}
	return cloneRun(r), nil
}

func (s *Store) ListAttempts(ctx context.Context, id string) ([]domain.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := s.runs[id]; !ok {
		return nil, domain.ErrNotFound
	}
	out := make([]domain.Attempt, 0, len(s.attempts[id]))
	for _, a := range s.attempts[id] {
		out = append(out, cloneAttempt(a))
	}
	return out, nil
}

func (s *Store) Claim(ctx context.Context, worker string, lease time.Duration) (*domain.Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lease <= 0 {
		return nil, domain.ValidationError{Err: errors.New("prazo da reserva deve ser positivo")}
	}
	now := time.Now().UTC()
	for id, l := range s.leases {
		if now.Before(l.expires) {
			continue
		}
		r := s.runs[id]
		result := domain.Result{Error: "reserva expirada", Retryable: true}
		if r.CancelRequestedAt != nil {
			result = domain.CancelResult(result)
		}
		r.Result = &result
		s.closeAttempt(id, now, result)
		if r.CancelRequestedAt != nil {
			r.Status = domain.Canceled
			r.FinishedAt = &now
		} else if r.AttemptCount >= r.Snapshot.MaxAttempts {
			r.Status = domain.Failed
			r.FinishedAt = &now
		} else {
			r.Status = domain.Pending
			r.AvailableAt = now
		}
		s.runs[id] = r
		delete(s.leases, id)
	}
	var candidate *domain.Run
	for _, r := range s.runs {
		if r.Status != domain.Pending || r.AvailableAt.After(now) {
			continue
		}
		if a, ok := s.automations[r.AutomationID]; ok && !a.AllowsOverlap() && s.runningOther(r.AutomationID, r.ID) {
			continue
		}
		if candidate == nil || r.AvailableAt.Before(candidate.AvailableAt) || (r.AvailableAt.Equal(candidate.AvailableAt) && (r.CreatedAt.Before(candidate.CreatedAt) || (r.CreatedAt.Equal(candidate.CreatedAt) && r.ID < candidate.ID))) {
			copy := r
			candidate = &copy
		}
	}
	if candidate == nil {
		return nil, nil
	}
	r := *candidate
	r.Status = domain.Running
	r.AttemptCount++
	r.Result = nil
	r.FinishedAt = nil
	token := domain.NewID()
	s.leases[r.ID] = reservation{token: token, expires: now.Add(lease)}
	s.runs[r.ID] = r
	s.attempts[r.ID] = append(s.attempts[r.ID], domain.Attempt{ID: domain.NewID(), RunID: r.ID, Number: r.AttemptCount, WorkerID: worker, StartedAt: now})
	return &domain.Claim{Run: cloneRun(r), Token: token}, nil
}

func (s *Store) Finish(ctx context.Context, id, token string, result domain.Result, retryAt *time.Time) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Run{}, err
	}
	r, ok := s.runs[id]
	if !ok {
		return domain.Run{}, domain.ErrNotFound
	}
	now := time.Now().UTC()
	l, ok := s.leases[id]
	if !ok || r.Status != domain.Running || l.token != token || !now.Before(l.expires) {
		return domain.Run{}, domain.ErrLeaseLost
	}
	if r.CancelRequestedAt != nil {
		result = domain.CancelResult(result)
	}
	s.closeAttempt(id, now, result)
	r.Result = cloneResult(&result)
	if r.CancelRequestedAt != nil {
		r.Status = domain.Canceled
		r.FinishedAt = &now
	} else if result.Error == "" {
		r.Status = domain.Succeeded
		r.FinishedAt = &now
	} else if result.Retryable && retryAt != nil && r.AttemptCount < r.Snapshot.MaxAttempts {
		r.Status = domain.Pending
		r.AvailableAt = retryAt.UTC()
		r.FinishedAt = nil
	} else {
		r.Status = domain.Failed
		r.FinishedAt = &now
	}
	s.runs[id] = r
	delete(s.leases, id)
	return cloneRun(r), nil
}

func (s *Store) closeAttempt(id string, now time.Time, result domain.Result) {
	attempts := s.attempts[id]
	if len(attempts) == 0 {
		return
	}
	i := len(attempts) - 1
	attempts[i].FinishedAt = cloneTime(&now)
	attempts[i].Result = cloneResult(&result)
}

func cloneResult(r *domain.Result) *domain.Result {
	if r == nil {
		return nil
	}
	copy := *r
	return &copy
}
func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	copy := *t
	return &copy
}
func cloneRun(r domain.Run) domain.Run {
	r.Snapshot = domain.CloneAutomation(r.Snapshot)
	r.Result = cloneResult(r.Result)
	r.FinishedAt = cloneTime(r.FinishedAt)
	r.ScheduledFor = cloneTime(r.ScheduledFor)
	r.CancelRequestedAt = cloneTime(r.CancelRequestedAt)
	return r
}
func cloneAttempt(a domain.Attempt) domain.Attempt {
	a.Result = cloneResult(a.Result)
	a.FinishedAt = cloneTime(a.FinishedAt)
	return a
}
