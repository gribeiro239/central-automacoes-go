package domain

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	ErrNotFound  = errors.New("recurso não encontrado")
	ErrDisabled  = errors.New("automação desabilitada")
	ErrLeaseLost = errors.New("reserva expirada ou substituída")
	ErrConflict  = errors.New("operação incompatível com o estado atual")
	ErrArchived  = errors.New("automação arquivada")
)

type HTTPConfig struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type Automation struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Type           string       `json:"type"`
	Config         HTTPConfig   `json:"config"`
	Enabled        bool         `json:"enabled"`
	TimeoutSeconds int          `json:"timeout_seconds"`
	MaxAttempts    int          `json:"max_attempts"`
	Schedule       *Schedule    `json:"schedule,omitempty"`
	NextRunAt      *time.Time   `json:"next_run_at,omitempty"`
	ArchivedAt     *time.Time   `json:"archived_at,omitempty"`
	OverlapPolicy  string       `json:"overlap_policy,omitempty"`
	RetryPolicy    *RetryPolicy `json:"retry_policy,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

type CreateAutomation struct {
	Name           string       `json:"name"`
	Type           string       `json:"type"`
	Config         HTTPConfig   `json:"config"`
	Enabled        *bool        `json:"enabled,omitempty"`
	TimeoutSeconds *int         `json:"timeout_seconds,omitempty"`
	MaxAttempts    *int         `json:"max_attempts,omitempty"`
	Schedule       *Schedule    `json:"schedule,omitempty"`
	OverlapPolicy  string       `json:"overlap_policy,omitempty"`
	RetryPolicy    *RetryPolicy `json:"retry_policy,omitempty"`
}

func (c CreateAutomation) Automation() (Automation, error) {
	a := Automation{ID: NewID(), Name: strings.TrimSpace(c.Name), Type: c.Type, Config: CloneConfig(c.Config), Enabled: true, TimeoutSeconds: 15, MaxAttempts: 3, CreatedAt: time.Now().UTC()}
	a.UpdatedAt = a.CreatedAt
	if c.Enabled != nil {
		a.Enabled = *c.Enabled
	}
	if c.TimeoutSeconds != nil {
		a.TimeoutSeconds = *c.TimeoutSeconds
	}
	if c.MaxAttempts != nil {
		a.MaxAttempts = *c.MaxAttempts
	}
	a.Schedule = CloneSchedule(c.Schedule)
	a.OverlapPolicy = c.OverlapPolicy
	a.RetryPolicy = CloneRetryPolicy(c.RetryPolicy)
	return a.Prepared(a.CreatedAt)
}

type PatchAutomation struct {
	Name              *string      `json:"name,omitempty"`
	Config            *HTTPConfig  `json:"config,omitempty"`
	Enabled           *bool        `json:"enabled,omitempty"`
	TimeoutSeconds    *int         `json:"timeout_seconds,omitempty"`
	MaxAttempts       *int         `json:"max_attempts,omitempty"`
	Schedule          *Schedule    `json:"schedule,omitempty"`
	RemoveSchedule    bool         `json:"remove_schedule,omitempty"`
	OverlapPolicy     *string      `json:"overlap_policy,omitempty"`
	RetryPolicy       *RetryPolicy `json:"retry_policy,omitempty"`
	RemoveRetryPolicy bool         `json:"remove_retry_policy,omitempty"`
}

func (a Automation) Patch(p PatchAutomation) (Automation, error) {
	if a.ArchivedAt != nil {
		return a, ErrArchived
	}
	a = CloneAutomation(a)
	wasEnabled := a.Enabled
	if p.Schedule != nil && p.RemoveSchedule {
		return a, errors.New("schedule e remove_schedule não podem ser usados juntos")
	}
	if p.RetryPolicy != nil && p.RemoveRetryPolicy {
		return a, errors.New("retry_policy e remove_retry_policy não podem ser usados juntos")
	}
	if p.Name != nil {
		a.Name = strings.TrimSpace(*p.Name)
	}
	if p.Config != nil {
		a.Config = CloneConfig(*p.Config)
	}
	if p.Enabled != nil {
		a.Enabled = *p.Enabled
	}
	if p.TimeoutSeconds != nil {
		a.TimeoutSeconds = *p.TimeoutSeconds
	}
	if p.MaxAttempts != nil {
		a.MaxAttempts = *p.MaxAttempts
	}
	a.UpdatedAt = time.Now().UTC()
	if p.OverlapPolicy != nil {
		a.OverlapPolicy = *p.OverlapPolicy
	}
	if p.RetryPolicy != nil {
		a.RetryPolicy = CloneRetryPolicy(p.RetryPolicy)
	}
	if p.RemoveRetryPolicy {
		a.RetryPolicy = nil
	}
	changed := p.Schedule != nil || p.RemoveSchedule || (p.Enabled != nil && a.Enabled != wasEnabled)
	if p.Schedule != nil {
		a.Schedule = CloneSchedule(p.Schedule)
	}
	if p.RemoveSchedule {
		a.Schedule = nil
	}
	if changed {
		a.NextRunAt = nil
	}
	return a.Prepared(a.UpdatedAt)
}

func (a Automation) Validate() error {
	if strings.ContainsRune(a.Name, '\x00') || strings.ContainsRune(a.Config.Body, '\x00') {
		return errors.New("name e config.body não podem conter NUL")
	}
	if len(a.Name) == 0 || len(a.Name) > 160 {
		return errors.New("name deve conter entre 1 e 160 bytes")
	}
	if a.Type != "http_request" {
		return errors.New("type deve ser http_request")
	}
	if a.TimeoutSeconds < 1 || a.TimeoutSeconds > 300 {
		return errors.New("timeout_seconds deve estar entre 1 e 300")
	}
	if a.MaxAttempts < 1 || a.MaxAttempts > 10 {
		return errors.New("max_attempts deve estar entre 1 e 10")
	}
	if a.OverlapPolicy != "" && a.OverlapPolicy != "allow" && a.OverlapPolicy != "forbid" {
		return errors.New("overlap_policy deve ser allow ou forbid")
	}
	if a.RetryPolicy != nil {
		if err := a.RetryPolicy.Validate(); err != nil {
			return err
		}
	}
	if a.Schedule != nil {
		if err := a.Schedule.Validate(); err != nil {
			return err
		}
	}
	u, err := url.Parse(a.Config.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return errors.New("config.url deve ser URL HTTP(S) absoluta, sem credenciais ou fragmento")
	}
	switch a.Config.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		return errors.New("config.method inválido: use um método HTTP em maiúsculas")
	}
	if len(a.Config.Body) > 65536 {
		return errors.New("config.body excede 64 KiB")
	}
	if len(a.Config.Headers) > 32 {
		return errors.New("config.headers excede 32 cabeçalhos")
	}
	for k, v := range a.Config.Headers {
		if strings.TrimSpace(k) == "" || len(k) > 128 || len(v) > 8192 || strings.ContainsAny(k+v, "\r\n") || strings.EqualFold(k, "Host") || strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
			return errors.New("config.headers contém cabeçalho inválido ou reservado")
		}
		for _, r := range k {
			if !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", r) {
				return errors.New("nome de cabeçalho HTTP inválido")
			}
		}
		for i := range len(v) {
			if (v[i] < 32 && v[i] != '\t') || v[i] == 127 {
				return errors.New("valor de cabeçalho HTTP contém caractere de controle")
			}
		}
	}
	return nil
}

func NewID() string { return rand.Text() }
func CloneConfig(c HTTPConfig) HTTPConfig {
	if c.Headers != nil {
		h := make(map[string]string, len(c.Headers))
		for k, v := range c.Headers {
			h[k] = v
		}
		c.Headers = h
	}
	return c
}
func CloneAutomation(a Automation) Automation {
	a.Config = CloneConfig(a.Config)
	a.Schedule = CloneSchedule(a.Schedule)
	a.RetryPolicy = CloneRetryPolicy(a.RetryPolicy)
	a.NextRunAt = CloneTime(a.NextRunAt)
	a.ArchivedAt = CloneTime(a.ArchivedAt)
	return a
}

type Status string

const (
	Pending   Status = "pending"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	Canceled  Status = "canceled"
)

type Run struct {
	ID                string     `json:"id"`
	AutomationID      string     `json:"automation_id"`
	Snapshot          Automation `json:"snapshot"`
	Origin            string     `json:"origin"`
	Status            Status     `json:"status"`
	AttemptCount      int        `json:"attempt_count"`
	AvailableAt       time.Time  `json:"available_at"`
	CreatedAt         time.Time  `json:"created_at"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	Result            *Result    `json:"result,omitempty"`
	ScheduledFor      *time.Time `json:"scheduled_for,omitempty"`
	ParentRunID       string     `json:"parent_run_id,omitempty"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
}

func NewRun(a Automation) Run {
	now := time.Now().UTC()
	return Run{ID: NewID(), AutomationID: a.ID, Snapshot: CloneAutomation(a), Origin: "manual", Status: Pending, AvailableAt: now, CreatedAt: now}
}

type Result struct {
	StatusCode  int    `json:"status_code,omitempty"`
	BodyPreview string `json:"body_preview,omitempty"`
	Truncated   bool   `json:"truncated"`
	Error       string `json:"error,omitempty"`
	Retryable   bool   `json:"retryable"`
}

type Attempt struct {
	ID         string     `json:"id"`
	RunID      string     `json:"run_id"`
	Number     int        `json:"number"`
	WorkerID   string     `json:"worker_id"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Result     *Result    `json:"result,omitempty"`
}

type Claim struct {
	Run   Run
	Token string
}

// Store reserves work atomically. Finish must reject stale tokens even after a reclaim.
// Lease must outlast a task timeout. Workers use 330 seconds; tasks are capped at 300.
type Store interface {
	CreateAutomation(context.Context, Automation) (Automation, error)
	ListAutomations(context.Context) ([]Automation, error)
	GetAutomation(context.Context, string) (Automation, error)
	PatchAutomation(context.Context, string, PatchAutomation) (Automation, error)
	Enqueue(context.Context, string) (Run, error)
	ListRuns(context.Context) ([]Run, error)
	GetRun(context.Context, string) (Run, error)
	ListAttempts(context.Context, string) ([]Attempt, error)
	Claim(context.Context, string, time.Duration) (*Claim, error)
	Finish(context.Context, string, string, Result, *time.Time) (Run, error)
	Ping(context.Context) error
	ScheduleDue(context.Context, time.Time, int) ([]Run, error)
	SetSchedulePaused(context.Context, string, bool) (Automation, error)
	ArchiveAutomation(context.Context, string) (Automation, error)
	QueryRuns(context.Context, RunFilter) ([]Run, error)
	Rerun(context.Context, string) (Run, error)
	CancelRun(context.Context, string) (Run, error)
	PanelSnapshot(context.Context, time.Time, time.Time) (PanelSnapshot, error)
}

type ValidationError struct{ Err error }

func (e ValidationError) Error() string { return fmt.Sprintf("configuração inválida: %v", e.Err) }
func (e ValidationError) Unwrap() error { return e.Err }
