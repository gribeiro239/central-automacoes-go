// Package storetest exercises the same public Store contract for all adapters.
package storetest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"central-automacoes/internal/domain"
)

func automation(t *testing.T, s domain.Store, max int) domain.Automation {
	t.Helper()
	a, err := (domain.CreateAutomation{Name: "Teste", Type: "http_request", Config: domain.HTTPConfig{Method: "GET", URL: "http://127.0.0.1:9090/health", Headers: map[string]string{"X-Test": "original"}}, MaxAttempts: &max}).Automation()
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.CreateAutomation(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func enqueue(t *testing.T, s domain.Store, a domain.Automation) domain.Run {
	t.Helper()
	r, e := s.Enqueue(context.Background(), a.ID)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func claim(t *testing.T, s domain.Store, lease time.Duration) *domain.Claim {
	t.Helper()
	c, e := s.Claim(context.Background(), "test-worker", lease)
	if e != nil {
		t.Fatal(e)
	}
	if c == nil {
		t.Fatal("expected a claim")
	}
	return c
}

func Run(t *testing.T, factory func(*testing.T) domain.Store) {
	ctx := context.Background()
	t.Run("empty_lists_and_completed_reservation", func(t *testing.T) {
		s := factory(t)
		automations, err := s.ListAutomations(ctx)
		if err != nil || automations == nil || len(automations) != 0 {
			t.Fatalf("empty automations: %+v %v", automations, err)
		}
		runs, err := s.ListRuns(ctx)
		if err != nil || runs == nil || len(runs) != 0 {
			t.Fatalf("empty runs: %+v %v", runs, err)
		}
		a := automation(t, s, 3)
		r := enqueue(t, s, a)
		attempts, err := s.ListAttempts(ctx, r.ID)
		if err != nil || attempts == nil || len(attempts) != 0 {
			t.Fatalf("empty attempts: %+v %v", attempts, err)
		}
		c := claim(t, s, time.Second)
		if _, err := s.Finish(ctx, r.ID, c.Token, domain.Result{StatusCode: 204}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Finish(ctx, r.ID, c.Token, domain.Result{Error: "overwrite"}, nil); !errors.Is(err, domain.ErrLeaseLost) {
			t.Fatalf("completed reservation overwritten: %v", err)
		}
		got, err := s.GetRun(ctx, r.ID)
		if err != nil || got.Status != domain.Succeeded || got.Result.StatusCode != 204 {
			t.Fatalf("completed run changed: %+v %v", got, err)
		}
	})
	t.Run("create_patch_disabled_and_validation", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		name := "Novo nome"
		disabled := false
		changed, err := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{Name: &name, Enabled: &disabled})
		if err != nil {
			t.Fatal(err)
		}
		if changed.Name != name || changed.Enabled || changed.Config.URL != a.Config.URL || changed.TimeoutSeconds != a.TimeoutSeconds {
			t.Fatalf("bad patch: %+v", changed)
		}
		if _, err = s.Enqueue(ctx, a.ID); !errors.Is(err, domain.ErrDisabled) {
			t.Fatalf("disabled enqueue: %v", err)
		}
		empty := ""
		if _, err = s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{Name: &empty}); err == nil {
			t.Fatal("accepted invalid patch")
		} else {
			var validation domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("not ValidationError: %v", err)
			}
		}
		actual, err := s.GetAutomation(ctx, a.ID)
		if err != nil || actual.Name != name {
			t.Fatalf("invalid patch changed data: %+v %v", actual, err)
		}
		invalid := a
		invalid.ID = domain.NewID()
		invalid.Type = "shell"
		if _, err = s.CreateAutomation(ctx, invalid); err == nil {
			t.Fatal("accepted invalid automation")
		}
		list, err := s.ListAutomations(ctx)
		if err != nil || len(list) != 1 {
			t.Fatalf("list: %+v %v", list, err)
		}
	})
	t.Run("snapshot_and_copy_isolation", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		r := enqueue(t, s, a)
		a.Config.Headers["X-Test"] = "mutated create return"
		r.Snapshot.Config.Headers["X-Test"] = "mutated enqueue return"
		got, _ := s.GetAutomation(ctx, a.ID)
		if got.Config.Headers["X-Test"] != "original" {
			t.Fatal("CreateAutomation leaked map")
		}
		got.Config.Headers["X-Test"] = "mutated get"
		list, _ := s.ListAutomations(ctx)
		list[0].Config.Headers["X-Test"] = "mutated list"
		name := "edited"
		newConfig := domain.HTTPConfig{Method: "POST", URL: "http://127.0.0.1:9091/changed", Headers: map[string]string{"New": "value"}}
		patched, err := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{Name: &name, Config: &newConfig})
		if err != nil {
			t.Fatal(err)
		}
		newConfig.Headers["New"] = "mutated input"
		patched.Config.Headers["New"] = "mutated output"
		got, _ = s.GetAutomation(ctx, a.ID)
		if got.Config.Headers["New"] != "value" {
			t.Fatal("PatchAutomation leaked map")
		}
		stored, _ := s.GetRun(ctx, r.ID)
		if stored.Snapshot.Name != "Teste" || stored.Snapshot.Config.Method != "GET" || stored.Snapshot.Config.Headers["X-Test"] != "original" {
			t.Fatalf("snapshot changed: %+v", stored)
		}
		c := claim(t, s, time.Second)
		c.Run.Snapshot.Config.Headers["X-Test"] = "claim mutation"
		finished, err := s.Finish(ctx, r.ID, c.Token, domain.Result{StatusCode: 200, BodyPreview: "ok"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		finished.Result.BodyPreview = "mutated"
		*finished.FinishedAt = time.Time{}
		runs, _ := s.ListRuns(ctx)
		runs[0].Snapshot.Config.Headers["X-Test"] = "list mutation"
		runs[0].Result.BodyPreview = "list result mutation"
		attempts, err := s.ListAttempts(ctx, r.ID)
		if err != nil || len(attempts) != 1 {
			t.Fatalf("attempts: %+v %v", attempts, err)
		}
		attempts[0].Result.BodyPreview = "attempt mutation"
		*attempts[0].FinishedAt = time.Time{}
		actual, _ := s.GetRun(ctx, r.ID)
		if actual.Result.BodyPreview != "ok" || actual.FinishedAt.IsZero() || actual.Snapshot.Config.Headers["X-Test"] != "original" {
			t.Fatalf("run leaked state: %+v", actual)
		}
		attempts, _ = s.ListAttempts(ctx, r.ID)
		if attempts[0].Result.BodyPreview != "ok" || attempts[0].FinishedAt.IsZero() {
			t.Fatal("attempt leaked state")
		}
	})
	t.Run("exclusive_concurrent_claim", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		r := enqueue(t, s, a)
		var wins atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 12 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				c, err := s.Claim(ctx, "concurrent", time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if c != nil {
					wins.Add(1)
					if c.Run.ID != r.ID || c.Run.AttemptCount != 1 {
						t.Errorf("invalid claim: %+v", c)
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("claims=%d", wins.Load())
		}
	})
	t.Run("delayed_retry_and_budget", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 2)
		r := enqueue(t, s, a)
		c := claim(t, s, time.Second)
		at := time.Now().UTC().Add(120 * time.Millisecond)
		pending, err := s.Finish(ctx, r.ID, c.Token, domain.Result{Error: "unavailable", Retryable: true}, &at)
		if err != nil || pending.Status != domain.Pending {
			t.Fatalf("retry: %+v %v", pending, err)
		}
		if next, err := s.Claim(ctx, "early", time.Second); err != nil || next != nil {
			t.Fatalf("early retry: %+v %v", next, err)
		}
		time.Sleep(time.Until(at) + 20*time.Millisecond)
		c = claim(t, s, time.Second)
		if c.Run.AttemptCount != 2 {
			t.Fatalf("attempt=%d", c.Run.AttemptCount)
		}
		exhausted, err := s.Finish(ctx, r.ID, c.Token, domain.Result{Error: "unavailable", Retryable: true}, &at)
		if err != nil || exhausted.Status != domain.Failed || exhausted.FinishedAt == nil {
			t.Fatalf("budget: %+v %v", exhausted, err)
		}
		if next, _ := s.Claim(ctx, "after-budget", time.Second); next != nil {
			t.Fatal("claimed exhausted run")
		}
		attempts, _ := s.ListAttempts(ctx, r.ID)
		if len(attempts) != 2 || attempts[0].Number != 1 || attempts[1].Number != 2 || attempts[0].FinishedAt == nil || attempts[1].Result.Error != "unavailable" {
			t.Fatalf("history: %+v", attempts)
		}
	})
	t.Run("expired_lease_recovery_and_fencing", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 2)
		r := enqueue(t, s, a)
		old := claim(t, s, 40*time.Millisecond)
		time.Sleep(70 * time.Millisecond)
		if _, err := s.Finish(ctx, r.ID, old.Token, domain.Result{StatusCode: 200}, nil); !errors.Is(err, domain.ErrLeaseLost) {
			t.Fatalf("expired finish accepted: %v", err)
		}
		fresh := claim(t, s, time.Second)
		if fresh.Token == old.Token || fresh.Run.AttemptCount != 2 {
			t.Fatalf("bad replacement: %+v", fresh)
		}
		if _, err := s.Finish(ctx, r.ID, old.Token, domain.Result{StatusCode: 200}, nil); !errors.Is(err, domain.ErrLeaseLost) {
			t.Fatalf("stale finish accepted: %v", err)
		}
		if _, err := s.Finish(ctx, r.ID, fresh.Token, domain.Result{StatusCode: 200}, nil); err != nil {
			t.Fatal(err)
		}
		attempts, _ := s.ListAttempts(ctx, r.ID)
		if len(attempts) != 2 || attempts[0].FinishedAt == nil || attempts[0].Result == nil || !attempts[0].Result.Retryable || attempts[0].Result.Error == "" {
			t.Fatalf("recovery history: %+v", attempts)
		}
	})
	t.Run("expired_lease_exhaustion", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 1)
		r := enqueue(t, s, a)
		claim(t, s, 30*time.Millisecond)
		time.Sleep(60 * time.Millisecond)
		if c, err := s.Claim(ctx, "replacement", time.Second); err != nil || c != nil {
			t.Fatalf("exhaustion: %+v %v", c, err)
		}
		got, err := s.GetRun(ctx, r.ID)
		if err != nil || got.Status != domain.Failed || got.FinishedAt == nil {
			t.Fatalf("exhausted state: %+v %v", got, err)
		}
	})
	t.Run("permanent_failure_does_not_retry", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		r := enqueue(t, s, a)
		c := claim(t, s, time.Second)
		at := time.Now().UTC()
		got, err := s.Finish(ctx, r.ID, c.Token, domain.Result{StatusCode: 400, Error: "bad request"}, &at)
		if err != nil || got.Status != domain.Failed {
			t.Fatalf("permanent: %+v %v", got, err)
		}
	})
	t.Run("missing_and_cancellation", func(t *testing.T) {
		s := factory(t)
		if _, e := s.GetAutomation(ctx, "missing"); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		if _, e := s.PatchAutomation(ctx, "missing", domain.PatchAutomation{}); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		if _, e := s.Enqueue(ctx, "missing"); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		if _, e := s.GetRun(ctx, "missing"); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		if _, e := s.ListAttempts(ctx, "missing"); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		a := automation(t, s, 3)
		r := enqueue(t, s, a)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, e := s.GetRun(cancelled, r.ID); !errors.Is(e, context.Canceled) {
			t.Fatalf("GetRun cancellation: %v", e)
		}
		if _, e := s.Claim(cancelled, "cancelled", time.Second); !errors.Is(e, context.Canceled) {
			t.Fatalf("Claim cancellation: %v", e)
		}
		if e := s.Ping(cancelled); !errors.Is(e, context.Canceled) {
			t.Fatalf("Ping cancellation: %v", e)
		}
	})
}
