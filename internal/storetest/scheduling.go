package storetest

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"central-automacoes/internal/domain"
)

// Scheduling is the adapter-independent contract for schedules and operations.
// The factory must create an isolated store for each subtest.
func Scheduling(t *testing.T, factory func(*testing.T) domain.Store) {
	ctx := context.Background()
	t.Run("schedule_patch_remove_and_copy", func(t *testing.T) {
		s := factory(t)
		now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		a := scheduledAutomation(t, s, now.Add(-5*time.Second), "run_once", "allow")
		original := *a.NextRunAt
		a.Schedule.IntervalSeconds = 999
		a.RetryPolicy.BaseDelaySeconds = 999
		*a.NextRunAt = time.Time{}
		stored, e := s.GetAutomation(ctx, a.ID)
		if e != nil || stored.Schedule.IntervalSeconds != 10 || stored.RetryPolicy.BaseDelaySeconds != 2 || !stored.NextRunAt.Equal(original) {
			t.Fatalf("create aliases stored schedule: %+v %v", stored, e)
		}
		*stored.NextRunAt = time.Time{}
		stored.Schedule.Paused = true
		list, _ := s.ListAutomations(ctx)
		if list[0].Schedule.Paused || !list[0].NextRunAt.Equal(original) {
			t.Fatal("GetAutomation aliases schedule")
		}
		*list[0].NextRunAt = time.Time{}
		replacement := domain.Schedule{Kind: "interval", IntervalSeconds: 20}
		before := time.Now().UTC()
		patched, e := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{Schedule: &replacement})
		after := time.Now().UTC()
		if e != nil || patched.NextRunAt == nil || patched.NextRunAt.Before(before.Add(20*time.Second)) || patched.NextRunAt.After(after.Add(20*time.Second)) {
			t.Fatalf("replacement next: %+v %v", patched, e)
		}
		replacement.IntervalSeconds = 30
		patched.Schedule.IntervalSeconds = 40
		stored, _ = s.GetAutomation(ctx, a.ID)
		if stored.Schedule.IntervalSeconds != 20 {
			t.Fatal("patch aliases schedule")
		}
		removed, e := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{RemoveSchedule: true})
		if e != nil || removed.Schedule != nil || removed.NextRunAt != nil {
			t.Fatalf("remove: %+v %v", removed, e)
		}
		if _, e = s.SetSchedulePaused(ctx, a.ID, true); !errors.Is(e, domain.ErrConflict) {
			t.Fatalf("pause absent schedule: %v", e)
		}
	})
	t.Run("scheduled_snapshot_and_next", func(t *testing.T) {
		s := factory(t)
		now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		due := now.Add(-5 * time.Second)
		a := scheduledAutomation(t, s, due, "run_once", "allow")
		runs, e := s.ScheduleDue(ctx, now, 100)
		if e != nil || len(runs) != 1 {
			t.Fatalf("tick: %+v %v", runs, e)
		}
		r := runs[0]
		if r.AutomationID != a.ID || r.Origin != "scheduled" || r.Status != domain.Pending || r.ScheduledFor == nil || !r.ScheduledFor.Equal(due) || !r.CreatedAt.Equal(now) || !r.AvailableAt.Equal(now) || r.Snapshot.Schedule.IntervalSeconds != 10 || !r.Snapshot.NextRunAt.Equal(due) {
			t.Fatalf("scheduled run: %+v", r)
		}
		stored, _ := s.GetAutomation(ctx, a.ID)
		if !stored.NextRunAt.Equal(due.Add(10 * time.Second)) {
			t.Fatalf("next lost interval phase: %s", stored.NextRunAt)
		}
		*r.ScheduledFor = time.Time{}
		r.Snapshot.Schedule.IntervalSeconds = 999
		*r.Snapshot.NextRunAt = time.Time{}
		actual, _ := s.GetRun(ctx, r.ID)
		if !actual.ScheduledFor.Equal(due) || actual.Snapshot.Schedule.IntervalSeconds != 10 || !actual.Snapshot.NextRunAt.Equal(due) {
			t.Fatal("scheduled result aliases state")
		}
		if again, e := s.ScheduleDue(ctx, now, 100); e != nil || len(again) != 0 {
			t.Fatalf("duplicate tick: %+v %v", again, e)
		}
	})
	t.Run("concurrent_ticks_unique_occurrence", func(t *testing.T) {
		s := factory(t)
		now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		scheduledAutomation(t, s, now.Add(-time.Second), "run_once", "allow")
		var count atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 12 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				r, e := s.ScheduleDue(ctx, now, 100)
				if e != nil {
					t.Error(e)
					return
				}
				count.Add(int32(len(r)))
			}()
		}
		close(start)
		wg.Wait()
		all, e := s.ListRuns(ctx)
		if e != nil || count.Load() != 1 || len(all) != 1 {
			t.Fatalf("duplicate occurrence count=%d runs=%d error=%v", count.Load(), len(all), e)
		}
	})
	t.Run("misfires_and_tick_limit", func(t *testing.T) {
		s := factory(t)
		now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		due := now.Add(-125 * time.Second)
		once := scheduledAutomation(t, s, due, "run_once", "allow")
		skipped := scheduledAutomation(t, s, due, "skip", "allow")
		withinGrace := scheduledAutomation(t, s, now.Add(-time.Second), "skip", "allow")
		generated := []domain.Run{}
		for range 3 {
			rs, e := s.ScheduleDue(ctx, now, 1)
			if e != nil {
				t.Fatal(e)
			}
			generated = append(generated, rs...)
		}
		if len(generated) != 2 {
			t.Fatalf("run_once/skip count=%d", len(generated))
		}
		seen := map[string]bool{}
		for _, r := range generated {
			seen[r.AutomationID] = true
		}
		if !seen[once.ID] || !seen[withinGrace.ID] || seen[skipped.ID] {
			t.Fatalf("misfire admissions: %+v", seen)
		}
		for _, a := range []domain.Automation{once, skipped, withinGrace} {
			stored, _ := s.GetAutomation(ctx, a.ID)
			if stored.NextRunAt == nil || !stored.NextRunAt.After(now) {
				t.Fatalf("next not future: %+v", stored)
			}
		}
		for _, limit := range []int{0, -1, 1001} {
			if _, e := s.ScheduleDue(ctx, now, limit); e == nil {
				t.Fatalf("accepted limit=%d", limit)
			}
		}
	})
	t.Run("pause_resume_disabled_and_reenable", func(t *testing.T) {
		s := factory(t)
		tick := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		a := scheduledAutomation(t, s, tick.Add(-time.Second), "run_once", "allow")
		paused, e := s.SetSchedulePaused(ctx, a.ID, true)
		if e != nil || !paused.Schedule.Paused || paused.NextRunAt != nil {
			t.Fatalf("pause: %+v %v", paused, e)
		}
		paused.Schedule.Paused = false
		again, _ := s.SetSchedulePaused(ctx, a.ID, true)
		if !again.Schedule.Paused {
			t.Fatal("pause return aliases state")
		}
		if runs, e := s.ScheduleDue(ctx, tick, 100); e != nil || len(runs) != 0 {
			t.Fatalf("paused tick: %+v %v", runs, e)
		}
		if _, e := s.Enqueue(ctx, a.ID); e != nil {
			t.Fatalf("pause blocked manual: %v", e)
		}
		before := time.Now().UTC()
		resumed, e := s.SetSchedulePaused(ctx, a.ID, false)
		after := time.Now().UTC()
		if e != nil || resumed.NextRunAt == nil || resumed.NextRunAt.Before(before.Add(10*time.Second)) || resumed.NextRunAt.After(after.Add(10*time.Second)) {
			t.Fatalf("resume next: %+v %v", resumed, e)
		}
		idempotent, _ := s.SetSchedulePaused(ctx, a.ID, false)
		if !idempotent.NextRunAt.Equal(*resumed.NextRunAt) {
			t.Fatal("idempotent resume moved next")
		}
		off := false
		disabled, e := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{Enabled: &off})
		if e != nil || disabled.NextRunAt != nil {
			t.Fatalf("disabled: %+v %v", disabled, e)
		}
		if runs, e := s.ScheduleDue(ctx, tick, 100); e != nil || len(runs) != 0 {
			t.Fatalf("disabled tick: %+v %v", runs, e)
		}
		on := true
		before = time.Now().UTC()
		enabled, e := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{Enabled: &on})
		after = time.Now().UTC()
		if e != nil || enabled.NextRunAt == nil || enabled.NextRunAt.Before(before.Add(10*time.Second)) || enabled.NextRunAt.After(after.Add(10*time.Second)) {
			t.Fatalf("reenabled next: %+v %v", enabled, e)
		}
	})
	t.Run("forbid_cross_source_admission_and_busy_tick", func(t *testing.T) {
		s := factory(t)
		tick := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		a := scheduledAutomation(t, s, tick.Add(-time.Second), "run_once", "forbid")
		old := enqueue(t, s, a)
		oldClaim := claim(t, s, time.Minute)
		if _, e := s.Finish(ctx, old.ID, oldClaim.Token, domain.Result{Error: "failed"}, nil); e != nil {
			t.Fatal(e)
		}
		var admitted atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range 12 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				switch i % 3 {
				case 0:
					_, e := s.Enqueue(ctx, a.ID)
					if e == nil {
						admitted.Add(1)
					} else if !errors.Is(e, domain.ErrConflict) {
						t.Error(e)
					}
				case 1:
					_, e := s.Rerun(ctx, old.ID)
					if e == nil {
						admitted.Add(1)
					} else if !errors.Is(e, domain.ErrConflict) {
						t.Error(e)
					}
				case 2:
					rs, e := s.ScheduleDue(ctx, tick, 100)
					if e != nil {
						t.Error(e)
					} else {
						admitted.Add(int32(len(rs)))
					}
				}
			}(i)
		}
		close(start)
		wg.Wait()
		if admitted.Load() != 1 {
			t.Fatalf("admitted=%d", admitted.Load())
		}
		stored, _ := s.GetAutomation(ctx, a.ID)
		if stored.NextRunAt == nil || !stored.NextRunAt.After(tick) {
			t.Fatal("busy tick failed to advance")
		}
		all, _ := s.ListRuns(ctx)
		if len(all) != 2 {
			t.Fatalf("extra runs=%d", len(all))
		}
	})
	t.Run("forbid_claim_serializes_existing_queue", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		first := enqueue(t, s, a)
		c1 := claim(t, s, time.Minute)
		second := enqueue(t, s, a)
		other := automation(t, s, 3)
		third := enqueue(t, s, other)
		forbid := "forbid"
		if _, e := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{OverlapPolicy: &forbid}); e != nil {
			t.Fatal(e)
		}
		if c1.Run.ID != first.ID {
			t.Fatalf("first claim=%s want %s", c1.Run.ID, first.ID)
		}
		c2 := claim(t, s, time.Minute)
		if c2.Run.ID != third.ID {
			t.Fatalf("other automation starved, got %s want %s", c2.Run.ID, third.ID)
		}
		if c, e := s.Claim(ctx, "blocked", time.Minute); e != nil || c != nil {
			t.Fatalf("overlap claim: %+v %v", c, e)
		}
		if _, e := s.Finish(ctx, first.ID, c1.Token, domain.Result{StatusCode: 200}, nil); e != nil {
			t.Fatal(e)
		}
		c3 := claim(t, s, time.Minute)
		if c3.Run.ID != second.ID {
			t.Fatalf("queued execution lost: %s", c3.Run.ID)
		}
	})
	t.Run("forbid_concurrent_claim_serializes_existing_queue", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		first := enqueue(t, s, a)
		second := enqueue(t, s, a)
		other := automation(t, s, 3)
		third := enqueue(t, s, other)
		forbid := "forbid"
		if _, e := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{OverlapPolicy: &forbid}); e != nil {
			t.Fatal(e)
		}
		start := make(chan struct{})
		claims := make(chan *domain.Claim, 8)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				c, e := s.Claim(ctx, "concurrent-forbid", time.Minute)
				if e != nil {
					t.Error(e)
					return
				}
				if c != nil {
					claims <- c
				}
			}()
		}
		close(start)
		wg.Wait()
		close(claims)
		var same *domain.Claim
		var sameCount, otherCount int
		for c := range claims {
			switch c.Run.AutomationID {
			case a.ID:
				sameCount++
				same = c
			case other.ID:
				otherCount++
				if c.Run.ID != third.ID {
					t.Fatalf("unrelated claim changed: %+v", c.Run)
				}
			default:
				t.Fatalf("unexpected automation claimed: %+v", c.Run)
			}
		}
		if sameCount != 1 || otherCount != 1 {
			t.Fatalf("concurrent claims overlap or starve: same=%d other=%d", sameCount, otherCount)
		}
		remaining := first.ID
		if same.Run.ID == first.ID {
			remaining = second.ID
		} else if same.Run.ID != second.ID {
			t.Fatalf("unknown same-automation claim: %+v", same.Run)
		}
		if _, e := s.Finish(ctx, same.Run.ID, same.Token, domain.Result{StatusCode: 200}, nil); e != nil {
			t.Fatal(e)
		}
		next := claim(t, s, time.Minute)
		if next.Run.ID != remaining {
			t.Fatalf("remaining queue not eligible after Finish: got %s want %s", next.Run.ID, remaining)
		}
	})
	t.Run("query_filters_before_pagination_and_stable_ties", func(t *testing.T) {
		s := factory(t)
		tick := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		automations := make([]domain.Automation, 4)
		for i := range automations {
			automations[i] = scheduledAutomation(t, s, tick, "run_once", "allow")
		}
		first, e := s.ScheduleDue(ctx, tick, 100)
		if e != nil || len(first) != 4 {
			t.Fatalf("first=%d %v", len(first), e)
		}
		next := tick.Add(10 * time.Second)
		second, e := s.ScheduleDue(ctx, next, 100)
		if e != nil || len(second) != 4 {
			t.Fatalf("second=%d %v", len(second), e)
		}
		for _, r := range first {
			if _, e := s.CancelRun(ctx, r.ID); e != nil {
				t.Fatal(e)
			}
		}
		all, e := s.QueryRuns(ctx, domain.RunFilter{})
		if e != nil || len(all) != 8 {
			t.Fatalf("all=%d %v", len(all), e)
		}
		for i := 1; i < len(all); i++ {
			if all[i-1].CreatedAt.Before(all[i].CreatedAt) || (all[i-1].CreatedAt.Equal(all[i].CreatedAt) && all[i-1].ID < all[i].ID) {
				t.Fatal("unstable created/id DESC order")
			}
		}
		filtered, e := s.QueryRuns(ctx, domain.RunFilter{AutomationID: automations[0].ID, Status: domain.Canceled, From: &tick, To: &tick, Limit: 1})
		if e != nil || len(filtered) != 1 || filtered[0].AutomationID != automations[0].ID || filtered[0].Status != domain.Canceled {
			t.Fatalf("combined filter %+v %v", filtered, e)
		}
		paged, e := s.QueryRuns(ctx, domain.RunFilter{AutomationID: automations[0].ID, Limit: 1, Offset: 1})
		if e != nil || len(paged) != 1 || !paged[0].CreatedAt.Equal(tick) {
			t.Fatalf("filter before pagination %+v %v", paged, e)
		}
		equalTime, e := s.QueryRuns(ctx, domain.RunFilter{From: &tick, To: &tick})
		if e != nil || len(equalTime) != 4 {
			t.Fatalf("inclusive endpoints=%d %v", len(equalTime), e)
		}
		sorted := append([]domain.Run(nil), first...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID > sorted[j].ID })
		page, e := s.QueryRuns(ctx, domain.RunFilter{Status: domain.Canceled, Limit: 2, Offset: 1})
		if e != nil || len(page) != 2 || page[0].ID != sorted[1].ID || page[1].ID != sorted[2].ID {
			t.Fatalf("stable filtered page: %+v %v", page, e)
		}
		absent, e := s.QueryRuns(ctx, domain.RunFilter{AutomationID: "absent"})
		if e != nil || absent == nil || len(absent) != 0 {
			t.Fatalf("empty filtered list: %+v %v", absent, e)
		}
		for _, f := range []domain.RunFilter{{Status: "invalid"}, {Limit: 501}, {Limit: -1}, {Offset: -1}, {From: &next, To: &tick}} {
			if _, e := s.QueryRuns(ctx, f); e == nil {
				t.Fatalf("invalid filter accepted: %+v", f)
			}
		}
	})
	t.Run("rerun_preserves_original_snapshot_and_history", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		old := enqueue(t, s, a)
		c := claim(t, s, time.Minute)
		failed, e := s.Finish(ctx, old.ID, c.Token, domain.Result{Error: "original failure", StatusCode: 400}, nil)
		if e != nil {
			t.Fatal(e)
		}
		name := "changed"
		config := domain.HTTPConfig{Method: "POST", URL: "http://127.0.0.1:9091/new"}
		if _, e := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{Name: &name, Config: &config}); e != nil {
			t.Fatal(e)
		}
		rerun, e := s.Rerun(ctx, old.ID)
		if e != nil || rerun.ID == old.ID || rerun.ParentRunID != old.ID || rerun.Origin != "retry" || rerun.Status != domain.Pending || rerun.AttemptCount != 0 || rerun.Snapshot.Config.URL != old.Snapshot.Config.URL || rerun.Snapshot.Name != old.Snapshot.Name {
			t.Fatalf("rerun %+v %v", rerun, e)
		}
		original, e := s.GetRun(ctx, old.ID)
		history, he := s.ListAttempts(ctx, old.ID)
		if e != nil || he != nil || original.Status != domain.Failed || original.Result.Error != failed.Result.Error || len(history) != 1 || history[0].Result.Error != "original failure" {
			t.Fatalf("original changed %+v %+v %v %v", original, history, e, he)
		}
		if _, e := s.CancelRun(ctx, rerun.ID); e != nil {
			t.Fatal(e)
		}
		fromCanceled, e := s.Rerun(ctx, rerun.ID)
		if e != nil || fromCanceled.ParentRunID != rerun.ID || fromCanceled.Snapshot.Name != old.Snapshot.Name {
			t.Fatalf("canceled rerun %+v %v", fromCanceled, e)
		}
	})
	t.Run("rerun_conflicts_current_automation_state", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		pending := enqueue(t, s, a)
		if _, e := s.Rerun(ctx, pending.ID); !errors.Is(e, domain.ErrConflict) {
			t.Fatalf("pending rerun: %v", e)
		}
		c := claim(t, s, time.Minute)
		if _, e := s.Rerun(ctx, pending.ID); !errors.Is(e, domain.ErrConflict) {
			t.Fatalf("running rerun: %v", e)
		}
		if _, e := s.Finish(ctx, pending.ID, c.Token, domain.Result{StatusCode: 200}, nil); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Rerun(ctx, pending.ID); !errors.Is(e, domain.ErrConflict) {
			t.Fatalf("succeeded rerun: %v", e)
		}
		failed := enqueue(t, s, a)
		c = claim(t, s, time.Minute)
		if _, e := s.Finish(ctx, failed.ID, c.Token, domain.Result{Error: "failed"}, nil); e != nil {
			t.Fatal(e)
		}
		disabled := false
		if _, e := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{Enabled: &disabled}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Rerun(ctx, failed.ID); !errors.Is(e, domain.ErrDisabled) {
			t.Fatalf("disabled rerun: %v", e)
		}
		if _, e := s.ArchiveAutomation(ctx, a.ID); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Rerun(ctx, failed.ID); !errors.Is(e, domain.ErrArchived) {
			t.Fatalf("archived rerun: %v", e)
		}
		all, _ := s.ListRuns(ctx)
		if len(all) != 2 {
			t.Fatalf("rejected reruns created rows: %d", len(all))
		}
	})
	t.Run("pending_cancel_idempotent_no_attempt", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		r := enqueue(t, s, a)
		canceled, e := s.CancelRun(ctx, r.ID)
		if e != nil || canceled.Status != domain.Canceled || canceled.FinishedAt == nil || canceled.CancelRequestedAt == nil || canceled.Result == nil || canceled.Result.Retryable {
			t.Fatalf("cancel pending %+v %v", canceled, e)
		}
		again, e := s.CancelRun(ctx, r.ID)
		if e != nil || !again.CancelRequestedAt.Equal(*canceled.CancelRequestedAt) {
			t.Fatalf("idempotent cancel %+v %v", again, e)
		}
		*again.CancelRequestedAt = time.Time{}
		*again.FinishedAt = time.Time{}
		actual, _ := s.GetRun(ctx, r.ID)
		if actual.CancelRequestedAt.IsZero() || actual.FinishedAt.IsZero() {
			t.Fatal("cancel pointer alias")
		}
		if c, e := s.Claim(ctx, "skip-canceled", time.Minute); e != nil || c != nil {
			t.Fatalf("claimed canceled %+v %v", c, e)
		}
		attempts, e := s.ListAttempts(ctx, r.ID)
		if e != nil || len(attempts) != 0 {
			t.Fatalf("cancel added attempts %+v %v", attempts, e)
		}
	})
	t.Run("pending_retry_cancel_preserves_last_response", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		r := enqueue(t, s, a)
		c := claim(t, s, time.Minute)
		retryAt := time.Now().UTC().Add(time.Hour)
		previous := domain.Result{StatusCode: 503, BodyPreview: "service temporarily unavailable", Truncated: true, Error: "destino retornou HTTP 503", Retryable: true}
		pending, e := s.Finish(ctx, r.ID, c.Token, previous, &retryAt)
		if e != nil || pending.Status != domain.Pending {
			t.Fatalf("retry setup: %+v %v", pending, e)
		}
		canceled, e := s.CancelRun(ctx, r.ID)
		if e != nil || canceled.Status != domain.Canceled || canceled.Result == nil || canceled.Result.StatusCode != previous.StatusCode || canceled.Result.BodyPreview != previous.BodyPreview || canceled.Result.Truncated != previous.Truncated || canceled.Result.Error == previous.Error || canceled.Result.Error == "" || canceled.Result.Retryable {
			t.Fatalf("pending retry response lost or cancel incorrect: %+v %v", canceled, e)
		}
		attempts, e := s.ListAttempts(ctx, r.ID)
		if e != nil || len(attempts) != 1 || attempts[0].FinishedAt == nil || attempts[0].Result == nil || *attempts[0].Result != previous {
			t.Fatalf("cancel changed previous attempt or added another: %+v %v", attempts, e)
		}
		if next, e := s.Claim(ctx, "after-cancel", time.Minute); e != nil || next != nil {
			t.Fatalf("canceled retry was claimed: %+v %v", next, e)
		}
	})
	for _, test := range []struct {
		name   string
		result domain.Result
		retry  bool
	}{{"success", domain.Result{StatusCode: 200}, false}, {"permanent_failure", domain.Result{Error: "permanent", StatusCode: 400}, false}, {"transient_failure", domain.Result{Error: "transient", Retryable: true}, true}} {
		t.Run("running_cancel_wins_"+test.name, func(t *testing.T) {
			s := factory(t)
			a := automation(t, s, 3)
			forbid := "forbid"
			a, e := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{OverlapPolicy: &forbid})
			if e != nil {
				t.Fatal(e)
			}
			r := enqueue(t, s, a)
			c := claim(t, s, time.Minute)
			requested, e := s.CancelRun(ctx, r.ID)
			if e != nil || requested.Status != domain.Running || requested.CancelRequestedAt == nil {
				t.Fatalf("request %+v %v", requested, e)
			}
			if _, e := s.Enqueue(ctx, a.ID); !errors.Is(e, domain.ErrConflict) {
				t.Fatalf("cancel released active slot: %v", e)
			}
			again, e := s.CancelRun(ctx, r.ID)
			if e != nil || !again.CancelRequestedAt.Equal(*requested.CancelRequestedAt) {
				t.Fatal("running cancel not idempotent")
			}
			var retryAt *time.Time
			if test.retry {
				at := time.Now().Add(time.Second)
				retryAt = &at
			}
			finished, e := s.Finish(ctx, r.ID, c.Token, test.result, retryAt)
			if e != nil || finished.Status != domain.Canceled || finished.Result == nil || finished.Result.Retryable || finished.FinishedAt == nil {
				t.Fatalf("cancel lost to result %+v %v", finished, e)
			}
			attempts, e := s.ListAttempts(ctx, r.ID)
			if e != nil || len(attempts) != 1 || attempts[0].FinishedAt == nil || attempts[0].Result == nil || attempts[0].Result.Retryable {
				t.Fatalf("cancel history %+v %v", attempts, e)
			}
			if _, e := s.Enqueue(ctx, a.ID); e != nil {
				t.Fatalf("finished cancel held slot: %v", e)
			}
		})
	}
	t.Run("finish_before_cancel_conflicts", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		for _, result := range []domain.Result{{StatusCode: 200}, {Error: "permanent"}} {
			r := enqueue(t, s, a)
			c := claim(t, s, time.Minute)
			if _, e := s.Finish(ctx, r.ID, c.Token, result, nil); e != nil {
				t.Fatal(e)
			}
			if _, e := s.CancelRun(ctx, r.ID); !errors.Is(e, domain.ErrConflict) {
				t.Fatalf("terminal cancel accepted: %v", e)
			}
		}
	})
	t.Run("concurrent_cancel_finish_terminal_consistency", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		for range 4 {
			r := enqueue(t, s, a)
			c := claim(t, s, time.Minute)
			start := make(chan struct{})
			var cancelErr, finishErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); <-start; _, cancelErr = s.CancelRun(ctx, r.ID) }()
			go func() {
				defer wg.Done()
				<-start
				_, finishErr = s.Finish(ctx, r.ID, c.Token, domain.Result{StatusCode: 200}, nil)
			}()
			close(start)
			wg.Wait()
			if finishErr != nil || (cancelErr != nil && !errors.Is(cancelErr, domain.ErrConflict)) {
				t.Fatalf("cancel/finish error: cancel=%v finish=%v", cancelErr, finishErr)
			}
			got, e := s.GetRun(ctx, r.ID)
			if e != nil || got.FinishedAt == nil || (got.Status != domain.Succeeded && got.Status != domain.Canceled) {
				t.Fatalf("nonterminal race result: %+v %v", got, e)
			}
			if (got.Status == domain.Canceled) != (cancelErr == nil) {
				t.Fatalf("cancel acknowledgment disagrees with final state: %+v %v", got, cancelErr)
			}
			attempts, e := s.ListAttempts(ctx, r.ID)
			if e != nil || len(attempts) != 1 || attempts[0].FinishedAt == nil {
				t.Fatalf("race attempt history: %+v %v", attempts, e)
			}
		}
	})
	t.Run("cancel_expired_reservation_never_retries", func(t *testing.T) {
		s := factory(t)
		a := automation(t, s, 3)
		r := enqueue(t, s, a)
		c := claim(t, s, 40*time.Millisecond)
		if _, e := s.CancelRun(ctx, r.ID); e != nil {
			t.Fatal(e)
		}
		time.Sleep(70 * time.Millisecond)
		next, e := s.Claim(ctx, "replacement", time.Minute)
		if e != nil || next != nil {
			t.Fatalf("canceled lease retried %+v %v", next, e)
		}
		got, e := s.GetRun(ctx, r.ID)
		if e != nil || got.Status != domain.Canceled || got.FinishedAt == nil || got.Result.Retryable {
			t.Fatalf("expired canceled %+v %v", got, e)
		}
		attempts, _ := s.ListAttempts(ctx, r.ID)
		if len(attempts) != 1 || attempts[0].FinishedAt == nil || attempts[0].Result.Retryable {
			t.Fatalf("expired history %+v", attempts)
		}
		if _, e := s.Finish(ctx, r.ID, c.Token, domain.Result{StatusCode: 200}, nil); !errors.Is(e, domain.ErrLeaseLost) {
			t.Fatalf("expired cancel token accepted: %v", e)
		}
	})
	t.Run("archive_preserves_queue_and_history", func(t *testing.T) {
		s := factory(t)
		tick := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
		a := scheduledAutomation(t, s, tick.Add(-time.Second), "run_once", "allow")
		running := enqueue(t, s, a)
		c := claim(t, s, time.Minute)
		pending := enqueue(t, s, a)
		archived, e := s.ArchiveAutomation(ctx, a.ID)
		if e != nil || archived.ArchivedAt == nil || archived.Enabled || archived.NextRunAt != nil {
			t.Fatalf("archive %+v %v", archived, e)
		}
		again, e := s.ArchiveAutomation(ctx, a.ID)
		if e != nil || !again.ArchivedAt.Equal(*archived.ArchivedAt) {
			t.Fatal("archive not idempotent")
		}
		*again.ArchivedAt = time.Time{}
		actual, _ := s.GetAutomation(ctx, a.ID)
		if actual.ArchivedAt.IsZero() {
			t.Fatal("archive pointer aliased")
		}
		if _, e := s.Enqueue(ctx, a.ID); !errors.Is(e, domain.ErrArchived) {
			t.Fatalf("archived enqueue: %v", e)
		}
		name := "should reject"
		if _, e := s.PatchAutomation(ctx, a.ID, domain.PatchAutomation{Name: &name}); !errors.Is(e, domain.ErrArchived) {
			t.Fatalf("archived patch: %v", e)
		}
		if _, e := s.SetSchedulePaused(ctx, a.ID, false); !errors.Is(e, domain.ErrConflict) && !errors.Is(e, domain.ErrArchived) {
			t.Fatalf("archived resume: %v", e)
		}
		if rs, e := s.ScheduleDue(ctx, tick, 100); e != nil || len(rs) != 0 {
			t.Fatalf("archived schedule: %+v %v", rs, e)
		}
		if _, e := s.Finish(ctx, running.ID, c.Token, domain.Result{StatusCode: 200}, nil); e != nil {
			t.Fatal(e)
		}
		c = claim(t, s, time.Minute)
		if c.Run.ID != pending.ID {
			t.Fatalf("archived queue lost %s", c.Run.ID)
		}
		if _, e := s.Finish(ctx, pending.ID, c.Token, domain.Result{StatusCode: 200}, nil); e != nil {
			t.Fatal(e)
		}
		rs, _ := s.QueryRuns(ctx, domain.RunFilter{AutomationID: a.ID})
		if len(rs) != 2 {
			t.Fatalf("archived history count=%d", len(rs))
		}
		for _, r := range rs {
			if r.Status != domain.Succeeded {
				t.Fatalf("archived work stopped: %+v", r)
			}
		}
	})
	t.Run("new_operations_missing_and_cancelled_context", func(t *testing.T) {
		s := factory(t)
		if _, e := s.SetSchedulePaused(ctx, "missing", true); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		if _, e := s.ArchiveAutomation(ctx, "missing"); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		if _, e := s.Rerun(ctx, "missing"); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		if _, e := s.CancelRun(ctx, "missing"); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, e := s.ScheduleDue(canceled, time.Now(), 100); !errors.Is(e, context.Canceled) {
			t.Fatalf("cancel tick: %v", e)
		}
		if _, e := s.QueryRuns(canceled, domain.RunFilter{}); !errors.Is(e, context.Canceled) {
			t.Fatalf("cancel query: %v", e)
		}
	})
}

func scheduledAutomation(t *testing.T, s domain.Store, due time.Time, misfire, overlap string) domain.Automation {
	t.Helper()
	a, e := (domain.CreateAutomation{Name: "Agendada", Type: "http_request", Config: domain.HTTPConfig{Method: "GET", URL: "http://127.0.0.1:9090/scheduled", Headers: map[string]string{"X-Test": "original"}}, Schedule: &domain.Schedule{Kind: "interval", IntervalSeconds: 10, MisfirePolicy: misfire, MisfireGraceSeconds: 60}, OverlapPolicy: overlap, RetryPolicy: &domain.RetryPolicy{BaseDelaySeconds: 2, MaxDelaySeconds: 10}}).Automation()
	if e != nil {
		t.Fatal(e)
	}
	a.NextRunAt = domain.CloneTime(&due)
	a, e = s.CreateAutomation(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
