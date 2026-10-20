package worker

import (
	"testing"
	"time"

	"central-automacoes/internal/domain"
)

func TestRetryDelayCapAppliedAfterJitterAndGlobalFallback(t *testing.T) {
	p := Pool{RetryBase: 30 * time.Millisecond}
	run := domain.Run{AttemptCount: 2}
	for range 100 {
		delay := p.retryDelay(run)
		if delay < 60*time.Millisecond || delay >= 75*time.Millisecond {
			t.Fatalf("fallback delay=%s", delay)
		}
	}
	run.Snapshot.RetryPolicy = &domain.RetryPolicy{BaseDelaySeconds: 1, MaxDelaySeconds: 2}
	for _, number := range []int{2, 3, 10} {
		run.AttemptCount = number
		for range 100 {
			if delay := p.retryDelay(run); delay != 2*time.Second {
				t.Fatalf("cap after jitter attempt=%d delay=%s", number, delay)
			}
		}
	}
}
