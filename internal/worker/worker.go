package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"central-automacoes/internal/domain"
)

type Executor interface {
	Execute(context.Context, domain.Automation, string) domain.Result
}

type Pool struct {
	Store        domain.Store
	Executor     Executor
	Logger       *slog.Logger
	Concurrency  int
	PollInterval time.Duration
	RetryBase    time.Duration
	wg           sync.WaitGroup
}

// Start uses separate contexts: stop stops claims; active cancels in-flight HTTP.
func (p *Pool) Start(stop, active context.Context) {
	for i := 0; i < p.Concurrency; i++ {
		p.wg.Add(1)
		go p.loop(stop, active, fmt.Sprintf("worker-%d-%s", i+1, domain.NewID()))
	}
}

func (p *Pool) Wait() { p.wg.Wait() }

func (p *Pool) loop(stop, active context.Context, id string) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.PollInterval)
	defer ticker.Stop()
	for {
		if stop.Err() != nil {
			return
		}
		claim, err := p.Store.Claim(stop, id, 330*time.Second)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				p.Logger.Error("falha ao reservar execução", "worker_id", id, "error", err)
			}
		} else if claim != nil {
			p.execute(active, *claim, id)
			continue
		}
		select {
		case <-stop.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Pool) execute(active context.Context, claim domain.Claim, id string) {
	ctx, cancel := context.WithTimeout(active, time.Duration(claim.Run.Snapshot.TimeoutSeconds)*time.Second)
	monitor, stopMonitor := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		p.monitorCancellation(monitor, claim.Run.ID, cancel)
	}()
	result := p.safeExecute(ctx, claim.Run)
	stopMonitor()
	<-monitorDone
	cancel()
	var retryAt *time.Time
	if result.Error != "" && result.Retryable && claim.Run.AttemptCount < claim.Run.Snapshot.MaxAttempts {
		delay := p.retryDelay(claim.Run)
		at := time.Now().UTC().Add(delay)
		retryAt = &at
	}
	// Recording a cancellation needs its own short-lived context.
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	run, err := p.Store.Finish(finishCtx, claim.Run.ID, claim.Token, result, retryAt)
	if err != nil {
		p.Logger.Error("falha ao registrar tentativa", "run_id", claim.Run.ID, "error", err)
		return
	}
	p.Logger.Info("tentativa concluída", "run_id", run.ID, "worker_id", id, "attempt", run.AttemptCount, "status", run.Status, "status_code", result.StatusCode)
}

// Cancellation is stored in the database so any API process can request it.
// Stop this monitor before Finish: it must never outlive its active reservation.
func (p *Pool) monitorCancellation(ctx context.Context, runID string, cancel context.CancelFunc) {
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		lookup, stopLookup := context.WithTimeout(ctx, time.Second)
		run, err := p.Store.GetRun(lookup, runID)
		stopLookup()
		if err == nil && (run.CancelRequestedAt != nil || run.Status == domain.Canceled) {
			cancel()
			return
		}
		if err != nil && ctx.Err() == nil {
			p.Logger.Warn("falha ao consultar cancelamento", "run_id", runID, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *Pool) retryDelay(run domain.Run) time.Duration {
	base := p.RetryBase
	var ceiling time.Duration
	if policy := run.Snapshot.RetryPolicy; policy != nil {
		base = time.Duration(policy.BaseDelaySeconds) * time.Second
		ceiling = time.Duration(policy.MaxDelaySeconds) * time.Second
	}
	delay := base * time.Duration(1<<min(max(run.AttemptCount-1, 0), 8))
	delay += time.Duration(rand.Int64N(max(int64(delay/4), 1)))
	// Clamp after jitter as well, so an explicit policy is a hard upper bound.
	if ceiling > 0 && delay > ceiling {
		delay = ceiling
	}
	return delay
}

func (p *Pool) safeExecute(ctx context.Context, run domain.Run) (result domain.Result) {
	defer func() {
		if recover() != nil {
			result = domain.Result{Error: "falha interna no executor", Retryable: false}
		}
	}()
	return p.Executor.Execute(ctx, run.Snapshot, run.ID)
}
