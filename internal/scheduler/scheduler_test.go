package scheduler_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"central-automacoes/internal/domain"
	"central-automacoes/internal/memory"
	"central-automacoes/internal/scheduler"
)

func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestSchedulerImmediatelyEmitsDueWorkWithoutExecutingIt(t *testing.T) {
	store := memory.New()
	a, err := (domain.CreateAutomation{Name: "scheduled", Type: "http_request", Config: domain.HTTPConfig{Method: "GET", URL: "http://127.0.0.1:1/never-called"}, Schedule: &domain.Schedule{Kind: "interval", IntervalSeconds: 60}}).Automation()
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Add(-time.Second)
	a.NextRunAt = &due
	a, err = store.CreateAutomation(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &scheduler.Scheduler{Store: store, Logger: logger(), Interval: time.Hour}
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("scheduler leaked")
		}
	})
	deadline := time.Now().Add(time.Second)
	for {
		runs, err := store.ListRuns(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) > 0 {
			if len(runs) != 1 || runs[0].Origin != "scheduled" || runs[0].ScheduledFor == nil || !runs[0].ScheduledFor.Equal(due) || runs[0].Status != domain.Pending || runs[0].AttemptCount != 0 {
				t.Fatalf("runs=%+v", runs)
			}
			updated, err := store.GetAutomation(context.Background(), a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.NextRunAt == nil || !updated.NextRunAt.After(time.Now().UTC()) {
				t.Fatalf("next not advanced: %+v", updated)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduler did not check immediately")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
}

type tickStore struct {
	domain.Store
	call func(context.Context, time.Time, int) ([]domain.Run, error)
}

func (s tickStore) ScheduleDue(ctx context.Context, now time.Time, limit int) ([]domain.Run, error) {
	return s.call(ctx, now, limit)
}

func TestSchedulerRetriesTickAfterFailureAndStopsPolling(t *testing.T) {
	var calls atomic.Int32
	observed := make(chan struct{}, 8)
	store := tickStore{call: func(ctx context.Context, now time.Time, limit int) ([]domain.Run, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
			t.Error("tick lacks bounded context")
		}
		if now.Location() != time.UTC || limit != 100 {
			t.Errorf("tick now=%s limit=%d", now, limit)
		}
		n := calls.Add(1)
		observed <- struct{}{}
		if n == 1 {
			return nil, errors.New("temporary database failure")
		}
		return nil, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &scheduler.Scheduler{Store: store, Logger: logger(), Interval: 10 * time.Millisecond}
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	for i := 0; i < 2; i++ {
		select {
		case <-observed:
		case <-time.After(time.Second):
			t.Fatal("scheduler did not retry after tick failure")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
	count := calls.Load()
	time.Sleep(25 * time.Millisecond)
	if calls.Load() != count {
		t.Fatal("scheduler kept polling after shutdown")
	}
}

func TestSchedulerCancellationInterruptsActiveTick(t *testing.T) {
	entered := make(chan struct{})
	store := tickStore{call: func(ctx context.Context, now time.Time, limit int) ([]domain.Run, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &scheduler.Scheduler{Store: store, Logger: logger(), Interval: time.Hour}
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no tick")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("active tick ignored cancellation")
	}
}
