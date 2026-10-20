package domain

import (
	"errors"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

type Schedule struct {
	Kind                string `json:"kind"`
	IntervalSeconds     int    `json:"interval_seconds,omitempty"`
	Cron                string `json:"cron,omitempty"`
	Timezone            string `json:"timezone,omitempty"`
	Paused              bool   `json:"paused"`
	MisfirePolicy       string `json:"misfire_policy,omitempty"`
	MisfireGraceSeconds int    `json:"misfire_grace_seconds,omitempty"`
}

func (s Schedule) Validate() error {
	if s.MisfirePolicy != "" && s.MisfirePolicy != "run_once" && s.MisfirePolicy != "skip" {
		return errors.New("misfire_policy deve ser run_once ou skip")
	}
	if s.MisfireGraceSeconds < 0 || s.MisfireGraceSeconds > 3600 {
		return errors.New("misfire_grace_seconds deve estar entre 1 e 3600, ou omitido")
	}
	switch s.Kind {
	case "interval":
		if s.IntervalSeconds < 1 || s.IntervalSeconds > 31536000 || s.Cron != "" {
			return errors.New("interval requer interval_seconds entre 1 e 31536000 e não aceita cron")
		}
		if s.Timezone != "" {
			if _, err := time.LoadLocation(s.Timezone); err != nil {
				return errors.New("timezone IANA inválido")
			}
		}
	case "cron":
		if s.IntervalSeconds != 0 || s.Timezone == "" || s.Timezone == "Local" || len(strings.Fields(s.Cron)) != 5 {
			return errors.New("cron requer cinco campos e timezone IANA explícito, sem interval_seconds")
		}
		if _, err := s.cronSchedule(); err != nil {
			return err
		}
	default:
		return errors.New("schedule.kind deve ser interval ou cron")
	}
	_, err := s.Next(time.Now().UTC())
	return err
}

func (s Schedule) cronSchedule() (cron.Schedule, error) {
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return nil, errors.New("timezone IANA inválido")
	}
	// Timezone is a separate field; reject embedded prefixes before calling parser.
	if strings.Contains(s.Cron, "TZ=") || strings.ContainsAny(s.Timezone, " \t\r\n\x00") {
		return nil, errors.New("informe timezone separadamente da expressão cron")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	parsed, err := parser.Parse("CRON_TZ=" + s.Timezone + " " + s.Cron)
	if err != nil {
		return nil, errors.New("expressão cron inválida")
	}
	return parsed, nil
}

func (s Schedule) Next(after time.Time) (time.Time, error) {
	if s.Kind == "interval" {
		if s.IntervalSeconds < 1 || s.IntervalSeconds > 31536000 {
			return time.Time{}, errors.New("interval_seconds inválido")
		}
		return after.UTC().Add(time.Duration(s.IntervalSeconds) * time.Second), nil
	}
	if s.Kind != "cron" {
		return time.Time{}, errors.New("tipo de agendamento inválido")
	}
	parsed, err := s.cronSchedule()
	if err != nil {
		return time.Time{}, err
	}
	next := parsed.Next(after)
	if next.IsZero() {
		return time.Time{}, errors.New("cron sem próxima ocorrência possível")
	}
	return next.UTC(), nil
}

func (a Automation) Prepared(now time.Time) (Automation, error) {
	a = CloneAutomation(a)
	if a.OverlapPolicy == "" {
		a.OverlapPolicy = "allow"
	}
	if a.Schedule != nil {
		if a.Schedule.MisfirePolicy == "" {
			a.Schedule.MisfirePolicy = "run_once"
		}
		if a.Schedule.MisfireGraceSeconds == 0 {
			a.Schedule.MisfireGraceSeconds = 60
		}
	}
	if err := a.Validate(); err != nil {
		return a, err
	}
	if !a.ScheduleActive() {
		a.NextRunAt = nil
		return a, nil
	}
	if a.NextRunAt == nil {
		next, err := a.Schedule.Next(now)
		if err != nil {
			return a, err
		}
		a.NextRunAt = &next
	}
	return a, nil
}

func (a Automation) ScheduleActive() bool {
	return a.Enabled && a.ArchivedAt == nil && a.Schedule != nil && !a.Schedule.Paused
}
func (a Automation) AllowsOverlap() bool { return a.OverlapPolicy != "forbid" }

// AdvanceSchedule coalesces downtime into at most one run; caller atomically saves
// this automation together with any emitted run, or neither. Busy ticks also advance.
func (a Automation) AdvanceSchedule(now time.Time) (Automation, bool, error) {
	a = CloneAutomation(a)
	if !a.ScheduleActive() || a.NextRunAt == nil || a.NextRunAt.After(now) {
		return a, false, nil
	}
	due := *a.NextRunAt
	next, err := a.Schedule.Next(now)
	if err != nil {
		return a, false, err
	}
	if a.Schedule.Kind == "interval" {
		step := time.Duration(a.Schedule.IntervalSeconds) * time.Second
		next = due.Add((now.Sub(due)/step + 1) * step)
	}
	a.NextRunAt = &next
	a.UpdatedAt = now.UTC()
	grace := a.Schedule.MisfireGraceSeconds
	if grace == 0 {
		grace = 60
	}
	emit := a.Schedule.MisfirePolicy != "skip" || now.Sub(due) <= time.Duration(grace)*time.Second
	return a, emit, nil
}

func CloneSchedule(s *Schedule) *Schedule {
	if s == nil {
		return nil
	}
	copy := *s
	return &copy
}
func CloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	copy := *t
	return &copy
}

type RetryPolicy struct {
	BaseDelaySeconds int `json:"base_delay_seconds"`
	MaxDelaySeconds  int `json:"max_delay_seconds"`
}

func (p RetryPolicy) Validate() error {
	if p.BaseDelaySeconds < 1 || p.BaseDelaySeconds > 3600 || p.MaxDelaySeconds < p.BaseDelaySeconds || p.MaxDelaySeconds > 86400 {
		return errors.New("retry_policy requer base_delay_seconds entre 1 e 3600 e max_delay_seconds entre base e 86400")
	}
	return nil
}
func CloneRetryPolicy(p *RetryPolicy) *RetryPolicy {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

type RunFilter struct {
	AutomationID string
	Status       Status
	From         *time.Time
	To           *time.Time
	Limit        int
	Offset       int
}

func (f RunFilter) Validate() error {
	if f.Status != "" && f.Status != Pending && f.Status != Running && f.Status != Succeeded && f.Status != Failed && f.Status != Canceled {
		return errors.New("status inválido")
	}
	if f.Limit < 0 || f.Limit > 500 || f.Offset < 0 {
		return errors.New("limit deve estar entre 1 e 500 e offset deve ser >=0")
	}
	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		return errors.New("from deve ser anterior ou igual a to")
	}
	return nil
}

func NewScheduledRun(a Automation, due, now time.Time) Run {
	r := NewRun(a)
	r.Origin = "scheduled"
	r.ScheduledFor = CloneTime(&due)
	r.CreatedAt = now.UTC()
	r.AvailableAt = r.CreatedAt
	return r
}
func NewRerun(old Run) Run {
	r := NewRun(old.Snapshot)
	r.AutomationID = old.AutomationID
	r.ParentRunID = old.ID
	r.Origin = "retry"
	return r
}
func CancelResult(r Result) Result {
	r.Error = "execução cancelada pelo usuário"
	r.Retryable = false
	return r
}
func (s Status) Terminal() bool { return s == Succeeded || s == Failed || s == Canceled }
