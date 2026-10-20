package domain_test

import (
	"testing"
	"time"

	"central-automacoes/internal/domain"
)

func TestDashboardCalendarTimezoneAndHonestSuccessRate(t *testing.T) {
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	from := time.Date(2026, 10, 13, 3, 0, 0, 0, time.UTC)
	old := from.Add(-time.Nanosecond)
	future := now.Add(time.Nanosecond)
	next := now.Add(time.Hour)
	archived := now.Add(-time.Hour)
	snapshot := domain.PanelSnapshot{Automations: []domain.Automation{
		{ID: "scheduled", Enabled: true, Schedule: &domain.Schedule{Kind: "interval", IntervalSeconds: 60}, NextRunAt: &next},
		{ID: "disabled", Enabled: false},
		{ID: "archived", Enabled: true, ArchivedAt: &archived},
		{ID: "paused", Enabled: true, Schedule: &domain.Schedule{Kind: "interval", IntervalSeconds: 60, Paused: true}, NextRunAt: &next},
	}, Runs: []domain.Run{
		{ID: "at-from", AutomationID: "scheduled", CreatedAt: from, Status: domain.Succeeded},
		{ID: "at-to", AutomationID: "scheduled", CreatedAt: now, Status: domain.Failed},
		{ID: "canceled", AutomationID: "scheduled", CreatedAt: now.Add(-time.Minute), Status: domain.Canceled},
		{ID: "old-terminal", CreatedAt: old, Status: domain.Succeeded},
		{ID: "old-pending", AutomationID: "archived", CreatedAt: old, Status: domain.Pending},
		{ID: "old-running", AutomationID: "archived", CreatedAt: old, Status: domain.Running},
		{ID: "future-terminal", CreatedAt: future, Status: domain.Succeeded},
	}}
	report := domain.BuildDashboard(snapshot, now, location)
	if !report.Range.From.Equal(from) || !report.Range.To.Equal(now) || report.Range.Timezone != "America/Sao_Paulo" || report.Days[0].Date != "2026-10-13" || report.Days[6].Date != "2026-10-19" {
		t.Fatalf("calendar range: %+v %+v", report.Range, report.Days)
	}
	stats := report.Stats
	if stats.TotalRuns != 3 || stats.SuccessfulRuns != 1 || stats.FailedRuns != 1 || stats.CanceledRuns != 1 || stats.PendingRuns != 1 || stats.RunningRuns != 1 || stats.SuccessRate == nil || *stats.SuccessRate != 50 {
		t.Fatalf("stats: %+v", stats)
	}
	if stats.Automations != 3 || stats.ActiveAutomations != 2 || stats.ScheduledAutomations != 1 || len(report.Upcoming) != 1 || report.Upcoming[0].ID != "scheduled" {
		t.Fatalf("automation stats/upcoming: %+v %+v", stats, report.Upcoming)
	}
	if len(report.RecentRuns) != 3 || report.RecentRuns[0].ID != "at-to" || report.Days[0].Total != 1 || report.Days[6].Total != 2 {
		t.Fatalf("period: %+v %+v", report.RecentRuns, report.Days)
	}
	if active := report.ActiveByAutomation["archived"]; active.Pending != 1 || active.Running != 1 {
		t.Fatal("archived global work excluded")
	}
}

func TestDashboardCalendarAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	beforeFallback := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	afterFallback := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
	report := domain.BuildDashboard(domain.PanelSnapshot{Runs: []domain.Run{{ID: "first", CreatedAt: beforeFallback, Status: domain.Succeeded}, {ID: "second", CreatedAt: afterFallback, Status: domain.Succeeded}}}, now, location)
	if !report.Range.From.Equal(time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)) || len(report.Days) != 7 || report.Days[6].Date != "2026-11-01" || report.Days[6].Total != 2 || report.Stats.TotalRuns != 2 {
		t.Fatalf("DST calendar: %+v", report)
	}
	if report.Stats.SuccessRate == nil || *report.Stats.SuccessRate != 100 {
		t.Fatal("two successes should be100%")
	}
	empty := domain.BuildDashboard(domain.PanelSnapshot{}, now, location)
	if empty.Stats.SuccessRate != nil {
		t.Fatal("empty results invented success rate")
	}
}

func TestDashboardRecentUpcomingLimitsAndTieOrder(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	snapshot := domain.PanelSnapshot{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		next := now.Add(time.Hour)
		snapshot.Automations = append(snapshot.Automations, domain.Automation{ID: id, Enabled: true, Schedule: &domain.Schedule{Kind: "interval", IntervalSeconds: 60}, NextRunAt: &next})
		snapshot.Runs = append(snapshot.Runs, domain.Run{ID: id, CreatedAt: now, Status: domain.Pending})
	}
	report := domain.BuildDashboard(snapshot, now, time.UTC)
	if len(report.Upcoming) != 5 || report.Upcoming[0].ID != "a" || report.Upcoming[4].ID != "e" || len(report.RecentRuns) != 8 || report.RecentRuns[0].ID != "j" || report.RecentRuns[7].ID != "c" {
		t.Fatalf("stable limits/ties: %+v %+v", report.Upcoming, report.RecentRuns)
	}
	*report.Upcoming[0].NextRunAt = time.Time{}
	if snapshot.Automations[0].NextRunAt.IsZero() {
		t.Fatal("aggregation leaked timestamp alias")
	}
}
