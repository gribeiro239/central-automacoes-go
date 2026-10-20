package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
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

const valid = `{"name":"Consulta","type":"http_request","config":{"method":"GET","url":"http://localhost:9090/health","headers":{"X-Test":"original"}}}`

func TestAPIWorkflow(t *testing.T) {
	store := memory.New()
	srv := httptest.NewServer(httpapi.New(store, "memory", slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()
	request := func(method, path, body string, status int, out any) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != status {
			t.Fatalf("%s %s: status=%d expected=%d body=%s", method, path, resp.StatusCode, status, raw)
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatal(err)
			}
		}
	}
	var health map[string]string
	request("GET", "/health", "", 200, &health)
	if health["storage"] != "memory" || health["status"] != "ok" {
		t.Fatal(health)
	}
	var empty []domain.Automation
	request("GET", "/automations", "", 200, &empty)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty list=%v", empty)
	}
	var a domain.Automation
	request("POST", "/automations", valid, 201, &a)
	if a.ID == "" || !a.Enabled || a.TimeoutSeconds != 15 || a.MaxAttempts != 3 {
		t.Fatal(a)
	}
	var got domain.Automation
	request("GET", "/automations/"+a.ID, "", 200, &got)
	if got.Config.Headers["X-Test"] != "original" {
		t.Fatal(got)
	}
	request("PATCH", "/automations/"+a.ID, `{"name":"Renomeada"}`, 200, &got)
	if got.Name != "Renomeada" || got.Config.URL != a.Config.URL || !got.Enabled {
		t.Fatal(got)
	}
	var list []domain.Automation
	request("GET", "/automations", "", 200, &list)
	if len(list) != 1 {
		t.Fatal(list)
	}
	var run domain.Run
	request("POST", "/automations/"+a.ID+"/runs", "", 202, &run)
	if run.Status != domain.Pending || run.Snapshot.Name != "Renomeada" {
		t.Fatal(run)
	}
	request("PATCH", "/automations/"+a.ID, `{"enabled":false}`, 200, &got)
	request("POST", "/automations/"+a.ID+"/runs", "", 409, nil)
	request("GET", "/runs/"+run.ID, "", 200, &run)
	if !run.Snapshot.Enabled {
		t.Fatal("snapshot changed")
	}
	var attempts []domain.Attempt
	request("GET", "/runs/"+run.ID+"/attempts", "", 200, &attempts)
	if attempts == nil || len(attempts) != 0 {
		t.Fatal(attempts)
	}
	claim, err := store.Claim(context.Background(), "test", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim=%v err=%v", claim, err)
	}
	_, err = store.Finish(context.Background(), run.ID, claim.Token, domain.Result{StatusCode: 200, BodyPreview: "ok"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	request("GET", "/runs/"+run.ID, "", 200, &run)
	if run.Status != domain.Succeeded || run.Result.BodyPreview != "ok" {
		t.Fatal(run)
	}
	request("GET", "/runs/"+run.ID+"/attempts", "", 200, &attempts)
	if len(attempts) != 1 || attempts[0].FinishedAt == nil || attempts[0].Result.StatusCode != 200 {
		t.Fatal(attempts)
	}
	var runs []domain.Run
	request("GET", "/runs", "", 200, &runs)
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatal(runs)
	}
	for _, path := range []string{"/automations/missing", "/runs/missing", "/runs/missing/attempts"} {
		request("GET", path, "", 404, nil)
	}
	request("PATCH", "/automations/missing", `{"enabled":false}`, 404, nil)
	request("POST", "/automations/missing/runs", "", 404, nil)
}

func TestInvalidInput(t *testing.T) {
	handler := httpapi.New(memory.New(), "memory", slog.New(slog.NewTextHandler(io.Discard, nil)))
	cases := []string{
		``, `null`, `[]`, `{}`, `{`, valid + `{}`, strings.Replace(valid, `"name":"Consulta"`, `"name":""`, 1),
		strings.Replace(valid, `"type":"http_request"`, `"type":"shell"`, 1),
		strings.Replace(valid, `"method":"GET"`, `"method":"get"`, 1),
		strings.Replace(valid, `http://localhost:9090/health`, `file:///secret`, 1),
		strings.Replace(valid, `http://localhost:9090/health`, `http://user:password@localhost/`, 1),
		strings.Replace(valid, `"headers":{"X-Test":"original"}`, `"headers":{"X-Test":"bad\r\nInjected: yes"}`, 1),
		strings.Replace(valid, `"headers":{"X-Test":"original"}`, `"headers":{"X-Test":"bad\u0000value"}`, 1),
		strings.Replace(valid, `"headers":{"X-Test":"original"}`, `"headers":{"X-Test":"bad\u007fvalue"}`, 1),
		strings.Replace(valid, `"name":"Consulta"`, `"name":"bad\u0000value"`, 1),
		strings.Replace(valid, `"method":"GET"`, `"method":"GET","body":"bad\u0000value"`, 1),
		strings.Replace(valid, `"name":"Consulta"`, `"name":"Consulta","max_attempts":0`, 1),
		strings.Replace(valid, `"name":"Consulta"`, `"name":"Consulta","timeout_seconds":301`, 1),
		strings.Replace(valid, `"name":"Consulta"`, `"name":"Consulta","unknown":true`, 1),
		strings.Replace(valid, `"method":"GET"`, `"method":"GET","unknown":true`, 1),
		`{"name":"` + strings.Repeat("x", 129<<10) + `"}`,
	}
	for i, body := range cases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			req := httptest.NewRequest("POST", "/automations", strings.NewReader(body))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != 400 {
				t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	for _, body := range []string{`null`, `[]`, `{"unexpected":true}`, `{"timeout_seconds":0}`,
		`{"config":{"method":"GET","url":"http://localhost","headers":{"X-Test":"bad\u0000"}}}`,
		`{"config":{"method":"GET","url":"http://localhost","headers":{"X-Test":"bad\u007f"}}}`,
	} {
		a, _ := domain.CreateAutomation{Name: "test", Type: "http_request", Config: domain.HTTPConfig{Method: "GET", URL: "http://localhost"}}.Automation()
		s := memory.New()
		_, _ = s.CreateAutomation(context.Background(), a)
		h := httpapi.New(s, "memory", slog.New(slog.NewTextHandler(io.Discard, nil)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("PATCH", "/automations/"+a.ID, strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("PATCH %s code=%d", body, w.Code)
		}
	}
	req := httptest.NewRequest("POST", "/automations", bytes.NewBufferString(valid))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
}
