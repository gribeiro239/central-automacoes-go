package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"central-automacoes/internal/domain"
)

func Panel(t *testing.T, factory func(*testing.T) domain.Store) {
	ctx := context.Background()
	t.Run("retry_policy_reset_preserves_snapshot_and_schedule", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		policy := domain.RetryPolicy{BaseDelaySeconds: 3, MaxDelaySeconds: 30}
		schedule := domain.Schedule{Kind: "interval", IntervalSeconds: 60}
		a, err := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{RetryPolicy: &policy, Schedule: &schedule})
		if err != nil {
			t.Fatal(err)
		}
		old := enqueue(t, s, a)
		reset, err := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{RemoveRetryPolicy: true})
		if err != nil || reset.RetryPolicy != nil || reset.NextRunAt == nil || !reset.NextRunAt.Equal(*a.NextRunAt) {
			t.Fatalf("reset changed schedule/policy: %+v %v", reset, err)
		}
		original, err := s.GetRun(ctx, old.ID)
		if err != nil || original.Snapshot.RetryPolicy == nil || *original.Snapshot.RetryPolicy != policy {
			t.Fatalf("reset changed old snapshot: %+v %v", original, err)
		}
		fresh := enqueue(t, s, reset)
		if fresh.Snapshot.RetryPolicy != nil {
			t.Fatal("future run did not inherit default policy")
		}
		_, err = s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{RemoveRetryPolicy: true, RetryPolicy: &policy})
		var validation domain.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("conflicting policy/reset accepted: %v", err)
		}
		current, _ := s.GetAutomation(ctx, a.ID)
		if current.RetryPolicy != nil {
			t.Fatal("rejected patch mutated policy")
		}
	})
	t.Run("snapshot_and_dashboard_over_500_with_old_active", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		success := enqueue(t, s, a)
		c := claim(t, s, time.Minute)
		if _, err := s.Finish(ctx, success.ID, c.Token, domain.Result{StatusCode: 200}, nil); err != nil {
			t.Fatal(err)
		}
		failure := enqueue(t, s, a)
		c = claim(t, s, time.Minute)
		if _, err := s.Finish(ctx, failure.ID, c.Token, domain.Result{Error: "permanent", StatusCode: 400}, nil); err != nil {
			t.Fatal(err)
		}
		canceled := enqueue(t, s, a)
		if _, err := s.CancelRun(ctx, canceled.ID); err != nil {
			t.Fatal(err)
		}
		for range 505 {
			enqueue(t, s, a)
		}
		oldTime := time.Now().UTC().Truncate(time.Microsecond).AddDate(0, 0, -10)
		oldA, err := (domain.CreateAutomation{Name: "Old active", Type: "http_request", Config: domain.HTTPConfig{Method: "GET", URL: "http://127.0.0.1:9090/old"}, Schedule: &domain.Schedule{Kind: "interval", IntervalSeconds: 1}}).Automation()
		if err != nil {
			t.Fatal(err)
		}
		oldA.NextRunAt = &oldTime
		oldA, err = s.CreateAutomation(ctx, oldA)
		if err != nil {
			t.Fatal(err)
		}
		for i := range 3 {
			runs, err := s.ScheduleDue(ctx, oldTime.Add(time.Duration(i)*time.Second), 100)
			if err != nil || len(runs) != 1 {
				t.Fatalf("old tick: %d %v", len(runs), err)
			}
		}
		oldFinished := claim(t, s, time.Minute)
		if _, err := s.Finish(ctx, oldFinished.Run.ID, oldFinished.Token, domain.Result{StatusCode: 200}, nil); err != nil {
			t.Fatal(err)
		}
		oldRunning := claim(t, s, time.Minute)
		if oldRunning.Run.AutomationID != oldA.ID {
			t.Fatal("expected historical run reserved")
		}
		now := time.Now().UTC()
		from := domain.PanelStart(now, time.UTC)
		snapshot, err := s.PanelSnapshot(ctx, from, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Runs) != 510 {
			t.Fatalf("uncapped period OR active snapshot size=%d expected510", len(snapshot.Runs))
		}
		dashboard := domain.BuildDashboard(snapshot, now, time.UTC)
		stats := dashboard.Stats
		if stats.TotalRuns != 508 || stats.SuccessfulRuns != 1 || stats.FailedRuns != 1 || stats.CanceledRuns != 1 || stats.PendingRuns != 506 || stats.RunningRuns != 1 || stats.SuccessRate == nil || *stats.SuccessRate != 50 {
			t.Fatalf("uncapped stats: %+v", stats)
		}
		if stats.Automations != 2 || stats.ActiveAutomations != 2 || stats.ScheduledAutomations != 1 {
			t.Fatalf("automation stats: %+v", stats)
		}
		if got := dashboard.ActiveByAutomation[oldA.ID]; got.Pending != 1 || got.Running != 1 {
			t.Fatalf("old active map: %+v", got)
		}
		if got := dashboard.ActiveByAutomation[a.ID]; got.Pending != 505 {
			t.Fatalf("uncapped active map: %+v", got)
		}
		total := 0
		for _, day := range dashboard.Days {
			total += day.Total
		}
		if len(dashboard.Days) != 7 || total != 508 || len(dashboard.RecentRuns) != 8 || len(dashboard.Upcoming) != 1 {
			t.Fatalf("dashboard period lists: days=%+v recent=%d upcoming=%d", dashboard.Days, len(dashboard.RecentRuns), len(dashboard.Upcoming))
		}
		for _, r := range dashboard.RecentRuns {
			if r.AutomationID == oldA.ID {
				t.Fatal("old active run leaked into period recent list")
			}
		}
		for _, item := range snapshot.Automations {
			if item.ID == a.ID {
				item.Config.Headers["X-Test"] = "mutated"
			}
		}
		for _, r := range snapshot.Runs {
			if r.Snapshot.Config.Headers != nil {
				r.Snapshot.Config.Headers["X-Test"] = "mutated"
			}
			if r.Result != nil {
				r.Result.Error = "mutated"
			}
			if r.ScheduledFor != nil {
				*r.ScheduledFor = time.Time{}
			}
		}
		stored, err := s.GetAutomation(ctx, a.ID)
		if err != nil || stored.Config.Headers["X-Test"] != "original" {
			t.Fatal("snapshot exposed automation map")
		}
		storedRun, err := s.GetRun(ctx, success.ID)
		if err != nil || storedRun.Result.Error != "" || storedRun.Snapshot.Config.Headers["X-Test"] != "original" {
			t.Fatal("snapshot exposed run result/config")
		}
		again, err := s.PanelSnapshot(ctx, from, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range again.Runs {
			if r.ScheduledFor != nil && r.ScheduledFor.IsZero() {
				t.Fatal("snapshot exposed timestamp pointer")
			}
		}
	})
	t.Run("snapshot_consistent_with_atomic_scheduled_ticks", func(t *testing.T) {
		s := factory(t)
		due := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
		a, err := (domain.CreateAutomation{Name: "Concurrent snapshot", Type: "http_request", Config: domain.HTTPConfig{Method: "GET", URL: "http://127.0.0.1:9090/concurrent"}, Schedule: &domain.Schedule{Kind: "interval", IntervalSeconds: 1}}).Automation()
		if err != nil {
			t.Fatal(err)
		}
		a.NextRunAt = &due
		a, err = s.CreateAutomation(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			for i := range 12 {
				if _, err := s.ScheduleDue(ctx, due.Add(time.Duration(i)*time.Second), 100); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
		for range 30 {
			snapshot, err := s.PanelSnapshot(ctx, due.Add(-time.Second), due.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Automations) != 1 || snapshot.Automations[0].NextRunAt == nil {
				t.Fatal("missing automation in snapshot")
			}
			expected := int(snapshot.Automations[0].NextRunAt.Sub(due) / time.Second)
			if len(snapshot.Runs) != expected {
				t.Fatalf("incoherent snapshot: next implies %d runs; got%d", expected, len(snapshot.Runs))
			}
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err = s.PanelSnapshot(canceled, due, due.Add(time.Minute)); !errors.Is(err, context.Canceled) {
			t.Fatalf("snapshot cancellation: %v", err)
		}
	})
	t.Run("empty_dashboard_zero_and_null_rate", func(t *testing.T) {
		s := factory(t)
		now := time.Now().UTC()
		snapshot, err := s.PanelSnapshot(ctx, domain.PanelStart(now, time.UTC), now)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Automations == nil || snapshot.Runs == nil {
			t.Fatal("empty snapshot must use arrays")
		}
		report := domain.BuildDashboard(snapshot, now, time.UTC)
		if report.Stats.TotalRuns != 0 || report.Stats.SuccessRate != nil || len(report.Days) != 7 || report.RecentRuns == nil || report.Upcoming == nil || report.ActiveByAutomation == nil {
			t.Fatal(fmt.Sprintf("invalid empty dashboard: %+v", report))
		}
	})
}
