package worker_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"central-automacoes/internal/domain"
	"central-automacoes/internal/executor"
	"central-automacoes/internal/memory"
	"central-automacoes/internal/worker"
)

func create(t *testing.T, s domain.Store, url string, maxAttempts, timeout int) domain.Automation {
	t.Helper()
	a, err := (domain.CreateAutomation{Name: "test", Type: "http_request", Config: domain.HTTPConfig{Method: "GET", URL: url}, MaxAttempts: &maxAttempts, TimeoutSeconds: &timeout}).Automation()
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
	r, err := s.Enqueue(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func start(t *testing.T, s domain.Store, count int, retry time.Duration) (stop, cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	claims, stop := context.WithCancel(context.Background())
	active, cancel := context.WithCancel(context.Background())
	p := &worker.Pool{Store: s, Executor: executor.NewHTTP(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Concurrency: count, PollInterval: 2 * time.Millisecond, RetryBase: retry}
	p.Start(claims, active)
	finished := make(chan struct{})
	go func() { p.Wait(); close(finished) }()
	t.Cleanup(func() {
		stop()
		cancel()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("workers did not stop")
		}
	})
	return stop, cancel, finished
}

func terminal(t *testing.T, s domain.Store, id string) domain.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status.Terminal() {
			return r
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("execution did not reach terminal state")
	return domain.Run{}
}

func TestRetriesHaveIncreasingDelayAndStableIdentity(t *testing.T) {
	var hits atomic.Int32
	var mu sync.Mutex
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		if hits.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		_, _ = io.WriteString(w, "done")
	}))
	t.Cleanup(srv.Close)
	s := memory.New()
	a := create(t, s, srv.URL, 3, 1)
	run := enqueue(t, s, a)
	start(t, s, 1, 30*time.Millisecond)
	final := terminal(t, s, run.ID)
	if final.Status != domain.Succeeded || final.AttemptCount != 3 || final.Result == nil || final.Result.BodyPreview != "done" {
		t.Fatalf("final=%+v", final)
	}
	attempts, err := s.ListAttempts(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 3 {
		t.Fatalf("attempts=%d", len(attempts))
	}
	for i, minDelay := range []time.Duration{30 * time.Millisecond, 60 * time.Millisecond} {
		if attempts[i].FinishedAt == nil {
			t.Fatal("finished timestamp absent")
		}
		delay := attempts[i+1].StartedAt.Sub(*attempts[i].FinishedAt)
		t.Logf("retry %d observed delay=%s configured minimum=%s", i+1, delay, minDelay)
		// The worker calculates available_at immediately before persisting Finish.
		// Allow a small bookkeeping margin between those timestamps.
		if delay < minDelay-2*time.Millisecond {
			t.Fatalf("retry %d delay=%s, minimum=%s", i+1, delay, minDelay)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, key := range keys {
		if key != run.ID {
			t.Fatalf("idempotency key=%q expected=%q", key, run.ID)
		}
	}
}

func TestRetryBudgetAndPermanentFailure(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		status, budget, want int
	}{{"budget", 503, 3, 3}, {"permanent", 400, 3, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(tc.status) }))
			t.Cleanup(srv.Close)
			s := memory.New()
			a := create(t, s, srv.URL, tc.budget, 1)
			run := enqueue(t, s, a)
			start(t, s, 1, 10*time.Millisecond)
			final := terminal(t, s, run.ID)
			if final.Status != domain.Failed || final.AttemptCount != tc.want || int(hits.Load()) != tc.want || final.FinishedAt == nil {
				t.Fatalf("final=%+v calls=%d", final, hits.Load())
			}
			time.Sleep(25 * time.Millisecond)
			if int(hits.Load()) != tc.want {
				t.Fatalf("terminal execution repeated: %d", hits.Load())
			}
		})
	}
}

func TestWorkerAppliesAttemptTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(srv.Close)
	s := memory.New()
	a := create(t, s, srv.URL, 1, 1)
	run := enqueue(t, s, a)
	started := time.Now()
	start(t, s, 1, 10*time.Millisecond)
	final := terminal(t, s, run.ID)
	if final.Status != domain.Failed || final.AttemptCount != 1 || final.Result == nil || final.Result.Error != "context deadline exceeded" {
		t.Fatalf("final=%+v", final)
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("timeout elapsed=%s", elapsed)
	}
}

func TestWorkerConcurrencyIsBoundedAndQueueDrains(t *testing.T) {
	var active, peak atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	s := memory.New()
	a := create(t, s, srv.URL, 1, 3)
	var runs []domain.Run
	for i := 0; i < 6; i++ {
		runs = append(runs, enqueue(t, s, a))
	}
	start(t, s, 2, 10*time.Millisecond)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not reach fixture")
		}
	}
	time.Sleep(30 * time.Millisecond)
	if active.Load() != 2 || peak.Load() != 2 {
		t.Fatalf("active=%d peak=%d", active.Load(), peak.Load())
	}
	close(release)
	for _, run := range runs {
		if final := terminal(t, s, run.ID); final.Status != domain.Succeeded || final.AttemptCount != 1 {
			t.Fatalf("final=%+v", final)
		}
	}
	if peak.Load() > 2 {
		t.Fatalf("concurrency exceeded: %d", peak.Load())
	}
	t.Logf("six executions drained; peak outbound concurrency=%d", peak.Load())
}

func TestStopClaimsAllowsActiveRequestToFinish(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
			_, _ = io.WriteString(w, "done")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	s := memory.New()
	a := create(t, s, srv.URL, 2, 3)
	first := enqueue(t, s, a)
	stop, _, done := start(t, s, 1, 10*time.Millisecond)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("no active request")
	}
	// Queue the next run only after the first is definitely running. Windows can
	// give consecutive enqueues identical timestamps, so enqueue order alone is
	// not a reliable way to identify which run the worker reserved.
	second := enqueue(t, s, a)
	stop()
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("graceful stop did not finish")
	}
	final, _ := s.GetRun(context.Background(), first.ID)
	pending, _ := s.GetRun(context.Background(), second.ID)
	// The queued run existed before shutdown and must remain unclaimed.
	if final.Status != domain.Succeeded || pending.Status != domain.Pending || pending.AttemptCount != 0 {
		t.Fatalf("first=%+v second=%+v", final, pending)
	}
}

func TestActiveCancellationPersistsRecoverableWork(t *testing.T) {
	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-r.Context().Done() }))
	t.Cleanup(srv.Close)
	s := memory.New()
	a := create(t, s, srv.URL, 2, 3)
	run := enqueue(t, s, a)
	stop, cancel, done := start(t, s, 1, 10*time.Millisecond)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("no active request")
	}
	stop()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled worker did not stop")
	}
	final, err := s.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := s.ListAttempts(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != domain.Pending || final.AttemptCount != 1 || len(attempts) != 1 || attempts[0].FinishedAt == nil || attempts[0].Result == nil || attempts[0].Result.Error != "context canceled" {
		t.Fatalf("run=%+v attempts=%+v", final, attempts)
	}
}

func TestPendingCancellationNeverCallsDestination(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	t.Cleanup(srv.Close)
	s := memory.New()
	a := create(t, s, srv.URL, 3, 2)
	run := enqueue(t, s, a)
	if _, err := s.CancelRun(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	stop, _, done := start(t, s, 1, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	final, err := s.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := s.ListAttempts(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != domain.Canceled || final.AttemptCount != 0 || len(attempts) != 0 || calls.Load() != 0 {
		t.Fatalf("run=%+v attempts=%d HTTP calls=%d", final, len(attempts), calls.Load())
	}
}

func TestRunningUserCancellationInterruptsHTTPWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 1)
	httpCanceled := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-r.Context().Done()
		httpCanceled <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	s := memory.New()
	a := create(t, s, srv.URL, 3, 5)
	run := enqueue(t, s, a)
	start(t, s, 1, 10*time.Millisecond)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("no HTTP call")
	}
	requested, err := s.CancelRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requested.Status != domain.Running || requested.CancelRequestedAt == nil {
		t.Fatalf("cancel did not retain running reservation: %+v", requested)
	}
	select {
	case <-httpCanceled:
	case <-time.After(time.Second):
		t.Fatal("persistent cancellation did not cancel outbound request")
	}
	final := terminal(t, s, run.ID)
	attempts, err := s.ListAttempts(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != domain.Canceled || final.AttemptCount != 1 || final.Result == nil || final.Result.Retryable || len(attempts) != 1 || attempts[0].FinishedAt == nil || attempts[0].Result == nil || attempts[0].Result.Retryable {
		t.Fatalf("run=%+v attempts=%+v", final, attempts)
	}
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("cancel retried HTTP: %d calls", calls.Load())
	}
}

func TestCancelBeforeTransientCompletionDoesNotRequeue(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			w.WriteHeader(503)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	s := memory.New()
	a := create(t, s, srv.URL, 3, 5)
	run := enqueue(t, s, a)
	start(t, s, 1, 10*time.Millisecond)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("no active request")
	}
	if _, err := s.CancelRun(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	final := terminal(t, s, run.ID)
	if final.Status != domain.Canceled || final.AttemptCount != 1 || final.Result == nil || final.Result.Retryable {
		t.Fatalf("cancellation lost to completion: %+v", final)
	}
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("canceled transient response retried: %d", calls.Load())
	}
}

func TestArchivePreservesActualExecutionOfRunningAndPendingWork(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			_, _ = io.WriteString(w, "done")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	s := memory.New()
	a := create(t, s, srv.URL, 1, 5)
	first := enqueue(t, s, a)
	start(t, s, 1, 10*time.Millisecond)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("no active request")
	}
	second := enqueue(t, s, a)
	archived, err := s.ArchiveAutomation(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedAt == nil {
		t.Fatal("not archived")
	}
	if _, err := s.Enqueue(context.Background(), a.ID); !errors.Is(err, domain.ErrArchived) {
		t.Fatalf("archive admitted new run: %v", err)
	}
	close(release)
	for _, run := range []domain.Run{first, second} {
		final := terminal(t, s, run.ID)
		attempts, err := s.ListAttempts(context.Background(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.Status != domain.Succeeded || final.AttemptCount != 1 || len(attempts) != 1 || attempts[0].FinishedAt == nil {
			t.Fatalf("archive discarded existing work: run=%+v attempts=%+v", final, attempts)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("expected actual requests for both runs; calls=%d", calls.Load())
	}
}

func TestRetryPolicyUsesFrozenSnapshotAndHardDelayCap(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	s := memory.New()
	a := create(t, s, srv.URL, 2, 2)
	if _, err := s.PatchAutomation(context.Background(), a.ID, domain.PatchAutomation{RetryPolicy: &domain.RetryPolicy{BaseDelaySeconds: 1, MaxDelaySeconds: 1}}); err != nil {
		t.Fatal(err)
	}
	run := enqueue(t, s, a)
	if _, err := s.PatchAutomation(context.Background(), a.ID, domain.PatchAutomation{RetryPolicy: &domain.RetryPolicy{BaseDelaySeconds: 2, MaxDelaySeconds: 2}}); err != nil {
		t.Fatal(err)
	}
	start(t, s, 1, 10*time.Millisecond)
	final := terminal(t, s, run.ID)
	attempts, err := s.ListAttempts(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != domain.Succeeded || len(attempts) != 2 || final.Snapshot.RetryPolicy == nil || final.Snapshot.RetryPolicy.BaseDelaySeconds != 1 {
		t.Fatalf("run=%+v attempts=%+v", final, attempts)
	}
	if attempts[0].FinishedAt == nil {
		t.Fatal("first attempt unfinished")
	}
	scheduledWait := final.AvailableAt.Sub(*attempts[0].FinishedAt)
	actualWait := attempts[1].StartedAt.Sub(*attempts[0].FinishedAt)
	if scheduledWait < 998*time.Millisecond || scheduledWait > time.Second || actualWait < 998*time.Millisecond {
		t.Fatalf("snapshot/cap/global fallback mismatch: scheduled=%s actual=%s", scheduledWait, actualWait)
	}
	t.Logf("policy delay scheduled=%s observed=%s despite 10ms global fallback and edited 2s policy", scheduledWait, actualWait)
}
