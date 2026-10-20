package domain

import (
	"sort"
	"time"
)

// PanelSnapshot contains every automation and every period run OR globally
// pending/running run. It deliberately has no pagination or artificial cap.
type PanelSnapshot struct {
	Automations []Automation
	Runs        []Run
}

type DashboardRange struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Timezone string    `json:"timezone"`
}
type DashboardStats struct {
	Automations          int      `json:"automations"`
	ActiveAutomations    int      `json:"active_automations"`
	ScheduledAutomations int      `json:"scheduled_automations"`
	TotalRuns            int      `json:"total_runs"`
	SuccessfulRuns       int      `json:"successful_runs"`
	FailedRuns           int      `json:"failed_runs"`
	CanceledRuns         int      `json:"canceled_runs"`
	PendingRuns          int      `json:"pending_runs"`
	RunningRuns          int      `json:"running_runs"`
	SuccessRate          *float64 `json:"success_rate"`
}
type DashboardDay struct {
	Date      string `json:"date"`
	Total     int    `json:"total"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Canceled  int    `json:"canceled"`
}
type ActiveRuns struct {
	Pending int `json:"pending"`
	Running int `json:"running"`
}
type Dashboard struct {
	GeneratedAt        time.Time             `json:"generated_at"`
	Range              DashboardRange        `json:"range"`
	Stats              DashboardStats        `json:"stats"`
	Days               []DashboardDay        `json:"days"`
	RecentRuns         []Run                 `json:"recent_runs"`
	Upcoming           []Automation          `json:"upcoming"`
	ActiveByAutomation map[string]ActiveRuns `json:"active_by_automation"`
}

func PanelStart(now time.Time, location *time.Location) time.Time {
	local := now.In(location)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	return midnight.AddDate(0, 0, -6).UTC()
}

func BuildDashboard(snapshot PanelSnapshot, now time.Time, location *time.Location) Dashboard {
	now = now.UTC()
	from := PanelStart(now, location)
	out := Dashboard{GeneratedAt: now, Range: DashboardRange{From: from, To: now, Timezone: location.String()}, Days: []DashboardDay{}, RecentRuns: []Run{}, Upcoming: []Automation{}, ActiveByAutomation: map[string]ActiveRuns{}}
	dayIndex := map[string]int{}
	for day := range 7 {
		date := from.In(location).AddDate(0, 0, day).Format("2006-01-02")
		dayIndex[date] = day
		out.Days = append(out.Days, DashboardDay{Date: date})
	}
	for _, a := range snapshot.Automations {
		if a.ArchivedAt != nil {
			continue
		}
		out.Stats.Automations++
		if a.Enabled {
			out.Stats.ActiveAutomations++
		}
		if a.ScheduleActive() {
			out.Stats.ScheduledAutomations++
			if a.NextRunAt != nil {
				out.Upcoming = append(out.Upcoming, CloneAutomation(a))
			}
		}
	}
	for _, run := range snapshot.Runs {
		active := out.ActiveByAutomation[run.AutomationID]
		switch run.Status {
		case Pending:
			out.Stats.PendingRuns++
			active.Pending++
		case Running:
			out.Stats.RunningRuns++
			active.Running++
		}
		if active.Pending > 0 || active.Running > 0 {
			out.ActiveByAutomation[run.AutomationID] = active
		}
		if run.CreatedAt.Before(from) || run.CreatedAt.After(now) {
			continue
		}
		out.Stats.TotalRuns++
		index := dayIndex[run.CreatedAt.In(location).Format("2006-01-02")]
		out.Days[index].Total++
		switch run.Status {
		case Succeeded:
			out.Stats.SuccessfulRuns++
			out.Days[index].Succeeded++
		case Failed:
			out.Stats.FailedRuns++
			out.Days[index].Failed++
		case Canceled:
			out.Stats.CanceledRuns++
			out.Days[index].Canceled++
		}
		out.RecentRuns = append(out.RecentRuns, CloneRun(run))
	}
	finished := out.Stats.SuccessfulRuns + out.Stats.FailedRuns
	if finished > 0 {
		rate := 100 * float64(out.Stats.SuccessfulRuns) / float64(finished)
		out.Stats.SuccessRate = &rate
	}
	sort.Slice(out.RecentRuns, func(i, j int) bool {
		if out.RecentRuns[i].CreatedAt.Equal(out.RecentRuns[j].CreatedAt) {
			return out.RecentRuns[i].ID > out.RecentRuns[j].ID
		}
		return out.RecentRuns[i].CreatedAt.After(out.RecentRuns[j].CreatedAt)
	})
	if len(out.RecentRuns) > 8 {
		out.RecentRuns = out.RecentRuns[:8]
	}
	sort.Slice(out.Upcoming, func(i, j int) bool {
		if out.Upcoming[i].NextRunAt.Equal(*out.Upcoming[j].NextRunAt) {
			return out.Upcoming[i].ID < out.Upcoming[j].ID
		}
		return out.Upcoming[i].NextRunAt.Before(*out.Upcoming[j].NextRunAt)
	})
	if len(out.Upcoming) > 5 {
		out.Upcoming = out.Upcoming[:5]
	}
	return out
}

func CloneRun(run Run) Run {
	run.Snapshot = CloneAutomation(run.Snapshot)
	run.FinishedAt = CloneTime(run.FinishedAt)
	run.ScheduledFor = CloneTime(run.ScheduledFor)
	run.CancelRequestedAt = CloneTime(run.CancelRequestedAt)
	if run.Result != nil {
		result := *run.Result
		run.Result = &result
	}
	return run
}
