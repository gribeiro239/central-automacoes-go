package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"central-automacoes/internal/domain"
	"central-automacoes/internal/httpapi"
	"central-automacoes/internal/memory"
)

func panelRequest(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}
func panelHandler(store domain.Store) http.Handler {
	return httpapi.New(store, "memory", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestEmbeddedPanelRootAssetsAndAPICompatibility(t *testing.T) {
	handler := panelHandler(memory.New())
	root := panelRequest(t, handler, "GET", "/", "")
	if root.Code != 200 || !strings.Contains(root.Body.String(), "<html") || !strings.HasPrefix(root.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("panel root: %d %s", root.Code, root.Body.String())
	}
	csp := root.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("unsafe/missing CSP: %s", csp)
	}
	head := panelRequest(t, handler, "HEAD", "/", "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != root.Header().Get("Content-Length") {
		t.Fatal("HEAD root does not match GET headers")
	}
	if got := panelRequest(t, handler, "POST", "/", ""); got.Code != 405 {
		t.Fatalf("POST root=%d", got.Code)
	}
	for _, asset := range []struct{ path, mime string }{{"app.js", "text/javascript"}, {"ui.js", "text/javascript"}, {"form.js", "text/javascript"}, {"form-model.js", "text/javascript"}, {"styles.css", "text/css"}, {"favicon.svg", "image/svg+xml"}} {
		result := panelRequest(t, handler, "GET", "/assets/"+asset.path, "")
		if result.Code != 200 || !strings.HasPrefix(result.Header().Get("Content-Type"), asset.mime) || result.Body.Len() == 0 {
			t.Fatalf("asset %s: status=%d MIME=%s", asset.path, result.Code, result.Header().Get("Content-Type"))
		}
		req := httptest.NewRequest("GET", "/assets/"+asset.path, nil)
		req.Header.Set("If-None-Match", result.Header().Get("ETag"))
		cached := httptest.NewRecorder()
		handler.ServeHTTP(cached, req)
		if cached.Code != 304 || cached.Body.Len() != 0 {
			t.Fatalf("asset conditional cache: %s status=%d", asset.path, cached.Code)
		}
	}
	for _, path := range []string{"/health", "/automations", "/runs"} {
		result := panelRequest(t, handler, "GET", path, "")
		if result.Code != 200 || !strings.HasPrefix(result.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("existing JSON endpoint changed: %s", path)
		}
	}
}

func TestEmbeddedPanelDoesNotExposeFilesystem(t *testing.T) {
	server := httptest.NewServer(panelHandler(memory.New()))
	defer server.Close()
	for _, path := range []string{"/.env", "/go.mod", "/internal/domain/domain.go", "/assets/.env", "/assets/nope.js", "/assets/index.html", "/assets/%2e%2e/.env", "/assets/%252e%252e/.env", "/assets/%5c.env"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatalf("unsafe/unknown path %s status=%d", path, response.StatusCode)
		}
	}
}

func TestDashboardHTTPRangeZeroAndQueryValidation(t *testing.T) {
	handler := panelHandler(memory.New())
	for _, query := range []string{"", "?timezone=UTC", "?timezone=America%2FSao_Paulo"} {
		result := panelRequest(t, handler, "GET", "/dashboard"+query, "")
		if result.Code != 200 {
			t.Fatalf("dashboard query %s: %d %s", query, result.Code, result.Body.String())
		}
		var report domain.Dashboard
		if err := json.Unmarshal(result.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.Stats.TotalRuns != 0 || report.Stats.SuccessRate != nil || len(report.Days) != 7 || report.RecentRuns == nil || report.Upcoming == nil || report.ActiveByAutomation == nil || report.Range.To != report.GeneratedAt {
			t.Fatalf("dishonest empty dashboard: %+v", report)
		}
	}
	for _, query := range []string{"?timezone=", "?timezone=Local", "?timezone=Invalid%2FTimezone", "?timezone=UTC&timezone=UTC", "?unknown=1", "?timezone=UTC;extra=x"} {
		result := panelRequest(t, handler, "GET", "/dashboard"+query, "")
		if result.Code != 400 {
			t.Fatalf("invalid query %s returned%d", query, result.Code)
		}
	}
	broken := panelHandler(failingPanelStore{Store: memory.New()})
	if result := panelRequest(t, broken, "GET", "/dashboard", ""); result.Code != 500 || strings.Contains(result.Body.String(), "total_runs") {
		t.Fatal("storage failure became invented zero stats")
	}
}

type failingPanelStore struct{ domain.Store }

func (failingPanelStore) PanelSnapshot(context.Context, time.Time, time.Time) (domain.PanelSnapshot, error) {
	return domain.PanelSnapshot{}, errors.New("snapshot offline")
}

func TestPatchRemoveRetryPolicyHTTP(t *testing.T) {
	s := memory.New()
	handler := panelHandler(s)
	created := panelRequest(t, handler, "POST", "/automations", `{"name":"Policy","type":"http_request","config":{"method":"GET","url":"http://127.0.0.1:9090"},"retry_policy":{"base_delay_seconds":3,"max_delay_seconds":30}}`)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var a domain.Automation
	if err := json.Unmarshal(created.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	conflict := panelRequest(t, handler, "PATCH", "/automations/"+a.ID, `{"remove_retry_policy":true,"retry_policy":{"base_delay_seconds":3,"max_delay_seconds":30}}`)
	if conflict.Code != 400 {
		t.Fatalf("conflicting reset not400: %d", conflict.Code)
	}
	reset := panelRequest(t, handler, "PATCH", "/automations/"+a.ID, `{"remove_retry_policy":true}`)
	if reset.Code != 200 {
		t.Fatalf("reset: %d %s", reset.Code, reset.Body.String())
	}
	if err := json.Unmarshal(reset.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	// Decode into a fresh value: omitempty intentionally omits the inherited policy.
	var fresh domain.Automation
	json.Unmarshal(reset.Body.Bytes(), &fresh)
	if fresh.RetryPolicy != nil {
		t.Fatal("reset did not return inherited policy")
	}
}
