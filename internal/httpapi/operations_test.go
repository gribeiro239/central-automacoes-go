package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"central-automacoes/internal/domain"
	"central-automacoes/internal/httpapi"
	"central-automacoes/internal/memory"
)

type operationsAPI struct {
	t       *testing.T
	store   *memory.Store
	handler http.Handler
}

func operations(t *testing.T) *operationsAPI {
	s := memory.New()
	return &operationsAPI{t: t, store: s, handler: httpapi.New(s, "memory", slog.New(slog.NewTextHandler(io.Discard, nil)))}
}
func (a *operationsAPI) request(method, path, body string, status int, out any) {
	a.t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	a.handler.ServeHTTP(w, r)
	if w.Code != status {
		a.t.Fatalf("%s %s = %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	if out != nil {
		reflect.ValueOf(out).Elem().SetZero()
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			a.t.Fatal(err)
		}
	}
}
func (a *operationsAPI) create(extra string) domain.Automation {
	a.t.Helper()
	body := `{"name":"Operacoes","type":"http_request","config":{"method":"GET","url":"http://localhost:9090/original"}` + extra + `}`
	var v domain.Automation
	a.request("POST", "/automations", body, 201, &v)
	return v
}

func TestScheduleAPIControls(t *testing.T) {
	api := operations(t)
	a := api.create(`,"schedule":{"kind":"interval","interval_seconds":30},"overlap_policy":"forbid"`)
	if a.Schedule == nil || a.NextRunAt == nil || a.Schedule.MisfirePolicy != "run_once" {
		t.Fatalf("schedule=%+v", a)
	}
	var got domain.Automation
	api.request("GET", "/automations/"+a.ID, "", 200, &got)
	if !got.NextRunAt.Equal(*a.NextRunAt) {
		t.Fatal("next_run_at absent")
	}
	api.request("POST", "/automations/"+a.ID+"/schedule/pause", "", 200, &got)
	if !got.Schedule.Paused || got.NextRunAt != nil {
		t.Fatalf("paused=%+v", got)
	}
	var run domain.Run
	api.request("POST", "/automations/"+a.ID+"/runs", "", 202, &run)
	api.request("POST", "/automations/"+a.ID+"/runs", "", 409, nil)
	api.request("POST", "/runs/"+run.ID+"/cancel", "", 200, &run)
	if run.Status != domain.Canceled || run.AttemptCount != 0 {
		t.Fatal(run)
	}
	api.request("POST", "/runs/"+run.ID+"/cancel", "", 200, nil)
	api.request("POST", "/automations/"+a.ID+"/schedule/resume", "", 200, &got)
	if got.Schedule.Paused || got.NextRunAt == nil {
		t.Fatalf("resumed=%+v", got)
	}
	next := *got.NextRunAt
	api.request("POST", "/automations/"+a.ID+"/schedule/resume", "", 200, &got)
	if !got.NextRunAt.Equal(next) {
		t.Fatal("idempotent resume shifted phase")
	}
	api.request("PATCH", "/automations/"+a.ID, `{"schedule":{"kind":"cron","cron":"0 8 * * *","timezone":"America/Sao_Paulo"}}`, 200, &got)
	if got.Schedule.Kind != "cron" || got.NextRunAt == nil {
		t.Fatal(got)
	}
	api.request("PATCH", "/automations/"+a.ID, `{"remove_schedule":true}`, 200, &got)
	if got.Schedule != nil || got.NextRunAt != nil {
		t.Fatal(got)
	}
	api.request("POST", "/automations/"+a.ID+"/schedule/pause", "", 409, nil)
	api.request("POST", "/automations/missing/schedule/resume", "", 404, nil)
}

func TestRetryArchiveAndCancelAPI(t *testing.T) {
	api := operations(t)
	ctx := context.Background()
	a := api.create("")
	var old domain.Run
	api.request("POST", "/automations/"+a.ID+"/runs", "", 202, &old)
	api.request("POST", "/runs/"+old.ID+"/retry", "", 409, nil)
	claim, err := api.store.Claim(ctx, "worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim=%v err=%v", claim, err)
	}
	failed, err := api.store.Finish(ctx, old.ID, claim.Token, domain.Result{Error: "permanent", StatusCode: 400}, nil)
	if err != nil {
		t.Fatal(err)
	}
	api.request("POST", "/runs/"+old.ID+"/cancel", "", 409, nil)
	api.request("PATCH", "/automations/"+a.ID, `{"config":{"method":"GET","url":"http://localhost:9090/edited"},"timeout_seconds":10,"max_attempts":4,"retry_policy":{"base_delay_seconds":1,"max_delay_seconds":5}}`, 200, &a)
	var retry domain.Run
	api.request("POST", "/runs/"+old.ID+"/retry", "", 202, &retry)
	if retry.ID == old.ID || retry.ParentRunID != old.ID || retry.Origin != "retry" || retry.AttemptCount != 0 || retry.Snapshot.Config.URL != old.Snapshot.Config.URL || retry.Snapshot.RetryPolicy != nil {
		t.Fatalf("retry=%+v", retry)
	}
	var original domain.Run
	api.request("GET", "/runs/"+old.ID, "", 200, &original)
	if original.Status != failed.Status || original.AttemptCount != 1 || original.Result.Error != "permanent" {
		t.Fatal(original)
	}
	var attempts []domain.Attempt
	api.request("GET", "/runs/"+old.ID+"/attempts", "", 200, &attempts)
	if len(attempts) != 1 || attempts[0].Result.Error != "permanent" {
		t.Fatal(attempts)
	}
	claim, err = api.store.Claim(ctx, "worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	api.request("POST", "/runs/"+retry.ID+"/cancel", "", 202, &retry)
	if retry.CancelRequestedAt == nil || retry.Status != domain.Running {
		t.Fatal(retry)
	}
	retryAt := time.Now().Add(time.Second)
	canceled, err := api.store.Finish(ctx, retry.ID, claim.Token, domain.Result{Error: "transient", Retryable: true}, &retryAt)
	if err != nil || canceled.Status != domain.Canceled {
		t.Fatalf("cancel race=%+v %v", canceled, err)
	}
	api.request("POST", "/automations/"+a.ID+"/archive", "", 200, &a)
	if a.ArchivedAt == nil || a.Enabled || a.NextRunAt != nil {
		t.Fatal(a)
	}
	api.request("POST", "/automations/"+a.ID+"/archive", "", 200, nil)
	var visible []domain.Automation
	api.request("GET", "/automations", "", 200, &visible)
	if len(visible) != 0 {
		t.Fatal(visible)
	}
	api.request("GET", "/automations?include_archived=true", "", 200, &visible)
	if len(visible) != 1 {
		t.Fatal(visible)
	}
	api.request("GET", "/automations/"+a.ID, "", 200, nil)
	api.request("GET", "/runs/"+old.ID, "", 200, nil)
	api.request("POST", "/automations/"+a.ID+"/runs", "", 409, nil)
	api.request("POST", "/runs/"+old.ID+"/retry", "", 409, nil)
	api.request("PATCH", "/automations/"+a.ID, `{"name":"changed"}`, 409, nil)
	api.request("POST", "/automations/"+a.ID+"/schedule/resume", "", 409, nil)
	api.request("POST", "/runs/missing/retry", "", 404, nil)
	api.request("POST", "/runs/missing/cancel", "", 404, nil)
	api.request("POST", "/automations/missing/archive", "", 404, nil)
}

func TestFilteredRunsAPI(t *testing.T) {
	api := operations(t)
	ctx := context.Background()
	a := api.create("")
	b := api.create("")
	var run domain.Run
	api.request("POST", "/automations/"+a.ID+"/runs", "", 202, &run)
	claim, err := api.store.Claim(ctx, "worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	if _, err = api.store.Finish(ctx, run.ID, claim.Token, domain.Result{StatusCode: 200}, nil); err != nil {
		t.Fatal(err)
	}
	api.request("POST", "/automations/"+b.ID+"/runs", "", 202, nil)
	api.request("POST", "/automations/"+b.ID+"/runs", "", 202, nil)
	var got []domain.Run
	query := url.Values{"automation_id": {a.ID}, "status": {"succeeded"}, "from": {run.CreatedAt.Format(time.RFC3339Nano)}, "to": {run.CreatedAt.Format(time.RFC3339Nano)}, "limit": {"1"}, "offset": {"0"}}
	api.request("GET", "/runs?"+query.Encode(), "", 200, &got)
	if len(got) != 1 || got[0].ID != run.ID {
		t.Fatal(got)
	}
	query.Set("offset", "1")
	api.request("GET", "/runs?"+query.Encode(), "", 200, &got)
	if len(got) != 0 {
		t.Fatal(got)
	}
	api.request("GET", "/runs?status=canceled", "", 200, &got)
	if len(got) != 0 {
		t.Fatal(got)
	}
	api.request("GET", "/runs?automation_id=missing", "", 200, &got)
	if len(got) != 0 {
		t.Fatal(got)
	}
	for _, query := range []string{"status=unknown", "limit=0", "limit=501", "limit=abc", "offset=-1", "offset=1.2", "from=2026-01-01", "to=invalid", "from=2026-10-21T00:00:00Z&to=2026-10-20T00:00:00Z", "status=failed&status=pending", "unknown=1", "status=", "limit=%ZZ", "status=failed;limit=1"} {
		api.request("GET", "/runs?"+query, "", 400, nil)
	}
	api.request("GET", "/automations?include_archived=invalid", "", 400, nil)
	api.request("GET", "/automations?include_archived=%ZZ", "", 400, nil)
	api.request("GET", "/automations?include_archived=", "", 400, nil)
	api.request("GET", "/automations?unknown=1", "", 400, nil)
}

func TestInvalidSchedulesAndRetryPolicyAPI(t *testing.T) {
	api := operations(t)
	invalid := []string{
		`,"schedule":{"kind":"interval","interval_seconds":0}`,
		`,"schedule":{"kind":"interval","interval_seconds":1,"cron":"* * * * *"}`,
		`,"schedule":{"kind":"cron","cron":"* * * * *"}`,
		`,"schedule":{"kind":"cron","cron":"0 0 31 2 *","timezone":"UTC"}`,
		`,"schedule":{"kind":"cron","cron":"* * * * * *","timezone":"UTC"}`,
		`,"schedule":{"kind":"cron","cron":"* * * * *","timezone":"invalid/timezone"}`,
		`,"schedule":{"kind":"cron","cron":"@every 1s","timezone":"UTC"}`,
		`,"overlap_policy":"invalid"`,
		`,"retry_policy":{"base_delay_seconds":0,"max_delay_seconds":5}`,
		`,"retry_policy":{"base_delay_seconds":10,"max_delay_seconds":5}`,
	}
	for _, extra := range invalid {
		api.request("POST", "/automations", `{"name":"invalid","type":"http_request","config":{"method":"GET","url":"http://localhost"}`+extra+`}`, 400, nil)
	}
	a := api.create("")
	api.request("PATCH", "/automations/"+a.ID, `{"schedule":{"kind":"interval","interval_seconds":5},"remove_schedule":true}`, 400, nil)
	api.request("PATCH", "/automations/"+a.ID, `{"retry_policy":{"base_delay_seconds":3,"max_delay_seconds":1}}`, 400, nil)
}
