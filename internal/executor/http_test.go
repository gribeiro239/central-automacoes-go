package executor_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"central-automacoes/internal/domain"
	"central-automacoes/internal/executor"
)

func automation(url string) domain.Automation {
	return domain.Automation{Config: domain.HTTPConfig{Method: http.MethodGet, URL: url}, TimeoutSeconds: 1, MaxAttempts: 3}
}

func TestHTTPRealRequestAndPreview(t *testing.T) {
	type requestValues struct{ key, header, body string }
	received := make(chan requestValues, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- requestValues{r.Header.Get("Idempotency-Key"), r.Header.Get("X-Test"), string(body)}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, strings.Repeat("x", executor.PreviewLimit+100))
	}))
	defer srv.Close()
	a := automation(srv.URL)
	a.Config.Method = http.MethodPost
	a.Config.Body = "payload"
	a.Config.Headers = map[string]string{"X-Test": "value", "Idempotency-Key": "overridden"}
	result := executor.NewHTTP().Execute(context.Background(), a, "stable-run-id")
	if result.Error != "" || result.Retryable || result.StatusCode != 201 || !result.Truncated || len(result.BodyPreview) != executor.PreviewLimit {
		t.Fatalf("unexpected result: %+v", result)
	}
	values := <-received
	if values.key != "stable-run-id" || values.header != "value" || values.body != "payload" {
		t.Fatalf("request key=%q header=%q body=%q", values.key, values.header, values.body)
	}
}

func TestHTTPStatusClassification(t *testing.T) {
	for _, status := range []int{200, 204, 301, 400, 401, 403, 404, 408, 409, 422, 429, 500, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer srv.Close()
			result := executor.NewHTTP().Execute(context.Background(), automation(srv.URL), "run")
			wantRetry := status == 408 || status == 429 || status >= 500
			if result.StatusCode != status || result.Retryable != wantRetry || (result.Error != "") != (status < 200 || status >= 300) {
				t.Fatalf("status=%d result=%+v", status, result)
			}
		})
	}
}

func TestHTTPDoesNotFollowRedirect(t *testing.T) {
	var hits atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer source.Close()
	result := executor.NewHTTP().Execute(context.Background(), automation(source.URL), "run")
	if result.StatusCode != 302 || result.Error == "" || result.Retryable || hits.Load() != 0 {
		t.Fatalf("redirect result=%+v destination hits=%d", result, hits.Load())
	}
}

func TestHTTPTimeoutAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		started := time.Now()
		result := executor.NewHTTP().Execute(ctx, automation(srv.URL), "run")
		if result.Error != "context deadline exceeded" || !result.Retryable || time.Since(started) > time.Second {
			t.Fatalf("timeout result=%+v elapsed=%s", result, time.Since(started))
		}
	})
	t.Run("already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result := executor.NewHTTP().Execute(ctx, automation(srv.URL), "run")
		if result.Error != "context canceled" || !result.Retryable {
			t.Fatalf("cancel result=%+v", result)
		}
	})
}

func TestHTTPTransportFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + listener.Addr().String()
	_ = listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := executor.NewHTTP().Execute(ctx, automation(url), "run")
	if result.Error == "" || !result.Retryable || result.StatusCode != 0 {
		t.Fatalf("transport result=%+v", result)
	}
}

func TestHTTPIncompleteBodyPreservesStatusRetryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		wantRetry bool
	}{
		{"permanent HTTP failure", http.StatusBadRequest, false},
		{"successful status with failed read", http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "100")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "short")
			}))
			defer srv.Close()
			result := executor.NewHTTP().Execute(context.Background(), automation(srv.URL), "run")
			if result.StatusCode != tc.status || result.Error == "" || result.Retryable != tc.wantRetry || result.BodyPreview != "short" {
				t.Fatalf("incomplete body status=%d result=%+v", tc.status, result)
			}
		})
	}
}

func TestHTTPBinaryPreviewIsSafeForJSONBAndByteBounded(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body          []byte
		wantTruncated bool
	}{
		{"short binary", []byte("start\x00\xffend"), false},
		{"large binary", bytes.Repeat([]byte{0xff, 0, 'a'}, 2000), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(tc.body)
			}))
			defer srv.Close()
			result := executor.NewHTTP().Execute(context.Background(), automation(srv.URL), "run")
			if result.Error != "" || result.StatusCode != http.StatusOK || result.Retryable || result.Truncated != tc.wantTruncated {
				t.Fatalf("binary response result=%+v", result)
			}
			if !utf8.ValidString(result.BodyPreview) || strings.ContainsRune(result.BodyPreview, 0) || len(result.BodyPreview) > executor.PreviewLimit {
				t.Fatalf("unsafe normalized preview: validUTF8=%v hasNUL=%v bytes=%d", utf8.ValidString(result.BodyPreview), strings.ContainsRune(result.BodyPreview, 0), len(result.BodyPreview))
			}
			if !tc.wantTruncated && (!strings.HasPrefix(result.BodyPreview, "start") || !strings.HasSuffix(result.BodyPreview, "end")) {
				t.Fatalf("normalization damaged valid surrounding text: %q", result.BodyPreview)
			}
		})
	}
}
