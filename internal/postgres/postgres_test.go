package postgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"central-automacoes/internal/domain"
	"central-automacoes/internal/executor"
	"central-automacoes/internal/postgres"
	"central-automacoes/internal/storetest"
	"github.com/jackc/pgx/v5"
)

func schemaURL(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL real: defina TEST_DATABASE_URL para executar os testes de integração")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "central_test_" + strings.ToLower(domain.NewID())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("limpar schema isolado: %v", err)
		}
		admin.Close()
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	return parsed.String()
}

func openTestStore(t *testing.T, dsn string) *postgres.Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPostgresContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) domain.Store { return openTestStore(t, schemaURL(t)) })
}

func automation(t *testing.T) domain.Automation {
	t.Helper()
	a, err := (domain.CreateAutomation{Name: "Persistência", Type: "http_request", Config: domain.HTTPConfig{Method: "GET", URL: "http://localhost:9090/health"}}).Automation()
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestMigrationRepeatabilityPreservesData(t *testing.T) {
	s := openTestStore(t, schemaURL(t))
	ctx := context.Background()
	a, err := s.CreateAutomation(ctx, automation(t))
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Enqueue(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetAutomation(ctx, a.ID); err != nil {
		t.Fatalf("automação perdida na segunda migração: %v", err)
	}
	if got, err := s.GetRun(ctx, run.ID); err != nil || got.Status != domain.Pending {
		t.Fatalf("run após migração: %+v, %v", got, err)
	}
}

func TestReopenRecoversAbandonedLeaseAndKeepsHistory(t *testing.T) {
	dsn := schemaURL(t)
	s := openTestStore(t, dsn)
	ctx := context.Background()
	a, err := s.CreateAutomation(ctx, automation(t))
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Enqueue(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.Claim(ctx, "processo-anterior", 30*time.Millisecond)
	if err != nil || old == nil {
		t.Fatalf("claim inicial: %+v %v", old, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(70 * time.Millisecond)
	s = openTestStore(t, dsn)
	persisted, err := s.GetRun(ctx, run.ID)
	if err != nil || persisted.Status != domain.Running || persisted.AttemptCount != 1 {
		t.Fatalf("run abandonado não persistiu: %+v %v", persisted, err)
	}
	if _, err = s.Finish(ctx, run.ID, old.Token, domain.Result{StatusCode: 200}, nil); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("Finish vencido = %v", err)
	}
	replacement, err := s.Claim(ctx, "processo-novo", time.Minute)
	if err != nil || replacement == nil {
		t.Fatalf("retomada: %+v %v", replacement, err)
	}
	if replacement.Run.ID != run.ID || replacement.Run.AttemptCount != 2 {
		t.Fatalf("claim retomado: %+v", replacement.Run)
	}
	if _, err = s.Finish(ctx, run.ID, old.Token, domain.Result{StatusCode: 200}, nil); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("Finish obsoleto = %v", err)
	}
	if _, err = s.Finish(ctx, run.ID, replacement.Token, domain.Result{StatusCode: 200, BodyPreview: "recuperado"}, nil); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTestStore(t, dsn)
	persisted, err = s.GetRun(ctx, run.ID)
	if err != nil || persisted.Status != domain.Succeeded || persisted.Result == nil || persisted.Result.BodyPreview != "recuperado" {
		t.Fatalf("resultado não persistiu: %+v %v", persisted, err)
	}
	attempts, err := s.ListAttempts(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].FinishedAt == nil || attempts[0].Result == nil || attempts[0].Result.Error == "" || !attempts[0].Result.Retryable || attempts[1].Result == nil || attempts[1].Result.StatusCode != 200 {
		t.Fatalf("histórico após reabrir: %+v", attempts)
	}
}

// Exercise the actual HTTP-to-JSONB producer/consumer boundary: a binary response
// must not turn an otherwise successful request into a persistence failure.
func TestBinaryPreviewPersistence(t *testing.T) {
	dsn := schemaURL(t)
	s := openTestStore(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	payload := bytes.Repeat([]byte{'x', 0, 0xff, 'y'}, 1300)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer destination.Close()
	a := automation(t)
	a.Config.URL = destination.URL
	a, err := s.CreateAutomation(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Enqueue(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(ctx, "binary-http-worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	result := executor.NewHTTP().Execute(ctx, claim.Run.Snapshot, run.ID)
	if result.Error != "" || result.StatusCode != http.StatusOK || !result.Truncated {
		t.Fatalf("HTTP binary result: %+v", result)
	}
	if len(result.BodyPreview) == 0 || len(result.BodyPreview) > executor.PreviewLimit || !utf8.ValidString(result.BodyPreview) || strings.ContainsRune(result.BodyPreview, '\x00') {
		t.Fatalf("unsafe/beyond-bound HTTP preview: bytes=%d valid=%t", len(result.BodyPreview), utf8.ValidString(result.BodyPreview))
	}
	if _, err = s.Finish(ctx, run.ID, claim.Token, result, nil); err != nil {
		t.Fatalf("persist actual HTTP result in JSONB: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, dsn)
	persisted, err := s.GetRun(ctx, run.ID)
	if err != nil || persisted.Status != domain.Succeeded || persisted.Result == nil {
		t.Fatalf("reopened binary run: %+v %v", persisted, err)
	}
	if *persisted.Result != result {
		t.Fatalf("binary result changed after reopen: %+v", persisted.Result)
	}
	attempts, err := s.ListAttempts(ctx, run.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Result == nil || *attempts[0].Result != result {
		t.Fatalf("binary attempt result not retained: %+v %v", attempts, err)
	}
}

func TestInvalidControlConfigurationRejectedBeforeWrite(t *testing.T) {
	s := openTestStore(t, schemaURL(t))
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*domain.Automation)
	}{
		{"name_nul", func(a *domain.Automation) { a.Name = "invalid\x00name" }},
		{"body_nul", func(a *domain.Automation) { a.Config.Body = "invalid\x00body" }},
		{"header_nul", func(a *domain.Automation) { a.Config.Headers = map[string]string{"X-Test": "bad\x00value"} }},
		{"header_del", func(a *domain.Automation) { a.Config.Headers = map[string]string{"X-Test": "bad\x7fvalue"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := automation(t)
			tc.mutate(&a)
			_, err := s.CreateAutomation(ctx, a)
			var validation domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("must reject before SQL serialization with ValidationError: %v", err)
			}
		})
	}
	stored, err := s.ListAutomations(ctx)
	if err != nil || len(stored) != 0 {
		t.Fatalf("invalid config reached database: %+v %v", stored, err)
	}
}
