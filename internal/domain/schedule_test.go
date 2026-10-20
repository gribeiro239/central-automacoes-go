package domain_test

import (
	"testing"
	"time"

	"central-automacoes/internal/domain"
)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func scheduledAutomation(schedule *domain.Schedule) domain.Automation {
	return domain.Automation{ID: "schedule-test", Name: "scheduled", Type: "http_request", Config: domain.HTTPConfig{Method: "GET", URL: "http://127.0.0.1/test"}, Enabled: true, TimeoutSeconds: 5, MaxAttempts: 3, Schedule: schedule}
}

func TestScheduleIntervalAndCronNext(t *testing.T) {
	start := at(t, "2026-10-20T10:00:00Z")
	for _, tc := range []struct {
		name     string
		schedule domain.Schedule
		want     string
	}{
		{"interval", domain.Schedule{Kind: "interval", IntervalSeconds: 90}, "2026-10-20T10:01:30Z"},
		{"cron UTC", domain.Schedule{Kind: "cron", Cron: "*/15 * * * *", Timezone: "UTC"}, "2026-10-20T10:15:00Z"},
		{"cron Sao Paulo", domain.Schedule{Kind: "cron", Cron: "0 8 * * *", Timezone: "America/Sao_Paulo"}, "2026-10-20T11:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.schedule.Validate(); err != nil {
				t.Fatal(err)
			}
			next, err := tc.schedule.Next(start)
			if err != nil || !next.Equal(at(t, tc.want)) || next.Location() != time.UTC {
				t.Fatalf("next=%s err=%v want=%s UTC", next, err, tc.want)
			}
		})
	}
}

func TestScheduleValidationRejectsInvalidAndImpossible(t *testing.T) {
	cases := []domain.Schedule{
		{}, {Kind: "other"}, {Kind: "interval"}, {Kind: "interval", IntervalSeconds: -1}, {Kind: "interval", IntervalSeconds: 31536001},
		{Kind: "interval", IntervalSeconds: 10, Cron: "* * * * *"}, {Kind: "interval", IntervalSeconds: 10, Timezone: "Invalid/Zone"},
		{Kind: "cron", Cron: "* * * * *"}, {Kind: "cron", Cron: "* * * * *", Timezone: "Local"},
		{Kind: "cron", Cron: "* * * * *", Timezone: "Invalid/Zone"}, {Kind: "cron", Cron: "* * * * * *", Timezone: "UTC"},
		{Kind: "cron", Cron: "@hourly", Timezone: "UTC"}, {Kind: "cron", Cron: "0 0 31 2 *", Timezone: "UTC"},
		{Kind: "cron", Cron: "0 25 * * *", Timezone: "UTC"}, {Kind: "cron", Cron: "0 0 * * *", Timezone: "UTC", IntervalSeconds: 10},
		{Kind: "interval", IntervalSeconds: 10, MisfirePolicy: "all"}, {Kind: "interval", IntervalSeconds: 10, MisfireGraceSeconds: -1}, {Kind: "interval", IntervalSeconds: 10, MisfireGraceSeconds: 3601},
	}
	for i, schedule := range cases {
		if err := schedule.Validate(); err == nil {
			t.Errorf("case %d accepted invalid schedule %+v", i, schedule)
		}
	}
}

func TestCronDSTSkipsMissingTimeAndRepeatsDistinctUTCInstants(t *testing.T) {
	spring := domain.Schedule{Kind: "cron", Cron: "30 2 * * *", Timezone: "America/New_York"}
	next, err := spring.Next(at(t, "2026-03-08T05:00:00Z"))
	if err != nil || !next.Equal(at(t, "2026-03-09T06:30:00Z")) {
		t.Fatalf("spring next=%s err=%v", next, err)
	}
	fall := domain.Schedule{Kind: "cron", Cron: "30 1 * * *", Timezone: "America/New_York"}
	first, err := fall.Next(at(t, "2026-11-01T04:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := fall.Next(first)
	if err != nil || !first.Equal(at(t, "2026-11-01T05:30:00Z")) || !second.Equal(at(t, "2026-11-01T06:30:00Z")) {
		t.Fatalf("fall first=%s second=%s err=%v", first, second, err)
	}
}

func TestAdvanceScheduleConservesIntervalPhaseAndCoalesces(t *testing.T) {
	due := at(t, "2026-10-20T10:00:00Z")
	a := scheduledAutomation(&domain.Schedule{Kind: "interval", IntervalSeconds: 10, MisfirePolicy: "run_once"})
	a.NextRunAt = &due
	advanced, emit, err := a.AdvanceSchedule(due.Add(35 * time.Second))
	if err != nil || !emit || advanced.NextRunAt == nil || !advanced.NextRunAt.Equal(due.Add(40*time.Second)) {
		t.Fatalf("advance=%+v emit=%v err=%v", advanced, emit, err)
	}
	if !a.NextRunAt.Equal(due) {
		t.Fatal("AdvanceSchedule mutated original automation")
	}
	again, emit, err := advanced.AdvanceSchedule(due.Add(35 * time.Second))
	if err != nil || emit || !again.NextRunAt.Equal(*advanced.NextRunAt) {
		t.Fatalf("second same tick emit=%v err=%v", emit, err)
	}
	for _, tc := range []struct {
		delay    time.Duration
		wantEmit bool
	}{{5 * time.Second, true}, {6 * time.Second, false}, {35 * time.Second, false}} {
		skipped := a
		skipped.Schedule = domain.CloneSchedule(a.Schedule)
		skipped.Schedule.MisfirePolicy = "skip"
		skipped.Schedule.MisfireGraceSeconds = 5
		now := due.Add(tc.delay)
		next, emit, err := skipped.AdvanceSchedule(now)
		if err != nil || emit != tc.wantEmit || next.NextRunAt == nil || !next.NextRunAt.After(now) {
			t.Fatalf("delay=%s next=%+v emit=%v err=%v", tc.delay, next, emit, err)
		}
	}
}

func TestPreparedAndPatchKeepScheduleStateConsistent(t *testing.T) {
	now := at(t, "2026-10-20T10:00:00Z")
	a, err := scheduledAutomation(&domain.Schedule{Kind: "interval", IntervalSeconds: 60}).Prepared(now)
	if err != nil {
		t.Fatal(err)
	}
	if a.NextRunAt == nil || !a.NextRunAt.Equal(now.Add(time.Minute)) || a.Schedule.MisfirePolicy != "run_once" || a.Schedule.MisfireGraceSeconds != 60 || a.OverlapPolicy != "allow" {
		t.Fatalf("prepared=%+v", a)
	}
	enabled := true
	name := "new name"
	patched, err := a.Patch(domain.PatchAutomation{Name: &name, Enabled: &enabled})
	if err != nil || !patched.NextRunAt.Equal(*a.NextRunAt) {
		t.Fatalf("idempotent enabled/name patch recalculated schedule: %+v err=%v", patched, err)
	}
	disabled := false
	paused, err := a.Patch(domain.PatchAutomation{Enabled: &disabled})
	if err != nil || paused.NextRunAt != nil {
		t.Fatalf("disabled=%+v err=%v", paused, err)
	}
	before := time.Now().UTC()
	resumed, err := paused.Patch(domain.PatchAutomation{Enabled: &enabled})
	if err != nil || resumed.NextRunAt == nil || resumed.NextRunAt.Before(before.Add(time.Minute)) {
		t.Fatalf("resumed=%+v err=%v", resumed, err)
	}
	removed, err := a.Patch(domain.PatchAutomation{RemoveSchedule: true})
	if err != nil || removed.Schedule != nil || removed.NextRunAt != nil {
		t.Fatalf("removed=%+v err=%v", removed, err)
	}
	if _, err := a.Patch(domain.PatchAutomation{Schedule: &domain.Schedule{Kind: "interval", IntervalSeconds: 10}, RemoveSchedule: true}); err == nil {
		t.Fatal("accepted conflicting schedule patch")
	}
	for _, mutate := range []func(*domain.Automation){
		func(a *domain.Automation) { a.Schedule.Paused = true }, func(a *domain.Automation) { a.Enabled = false }, func(a *domain.Automation) { a.ArchivedAt = &now },
	} {
		inactive := domain.CloneAutomation(a)
		mutate(&inactive)
		inactive, err = inactive.Prepared(now)
		if err != nil || inactive.ScheduleActive() || inactive.NextRunAt != nil {
			t.Fatalf("inactive=%+v err=%v", inactive, err)
		}
	}
}

func TestRetryPolicyValidationAndSnapshotCloning(t *testing.T) {
	for _, policy := range []domain.RetryPolicy{{BaseDelaySeconds: 1, MaxDelaySeconds: 1}, {BaseDelaySeconds: 3600, MaxDelaySeconds: 86400}} {
		if err := policy.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, policy := range []domain.RetryPolicy{{}, {BaseDelaySeconds: 0, MaxDelaySeconds: 1}, {BaseDelaySeconds: 3601, MaxDelaySeconds: 4000}, {BaseDelaySeconds: 2, MaxDelaySeconds: 1}, {BaseDelaySeconds: 1, MaxDelaySeconds: 86401}} {
		if err := policy.Validate(); err == nil {
			t.Errorf("accepted policy %+v", policy)
		}
	}
	a := scheduledAutomation(&domain.Schedule{Kind: "interval", IntervalSeconds: 60})
	a.RetryPolicy = &domain.RetryPolicy{BaseDelaySeconds: 1, MaxDelaySeconds: 2}
	run := domain.NewRun(a)
	run.Snapshot.RetryPolicy.BaseDelaySeconds = 2
	run.Snapshot.Schedule.IntervalSeconds = 120
	if a.RetryPolicy.BaseDelaySeconds != 1 || a.Schedule.IntervalSeconds != 60 {
		t.Fatal("run snapshot shares mutable schedule/retry policy")
	}
}
