package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"central-automacoes/internal/domain"
	"central-automacoes/internal/postgres"
)

func TestPanelSnapshotReadOnlyRepeatableReadAgainstConcurrentCommit(t *testing.T) {
	dsn := schemaURL(t)
	s := openTestStore(t, dsn)
	db := rawDatabase(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a, err := s.CreateAutomation(ctx, automation(t))
	if err != nil {
		t.Fatal(err)
	}
	lockA, lockB := 197711, int(time.Now().UnixNano()%2000000000)
	lockConnection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConnection.Close()
	if _, err = lockConnection.ExecContext(ctx, "SELECT pg_advisory_lock($1,$2)", lockA, lockB); err != nil {
		t.Fatal(err)
	}
	defer lockConnection.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1,$2)", lockA, lockB)
	// Only this disposable schema gets a view that pauses the first query after
	// its MVCC snapshot, while a separate connection commits automation AND run.
	viewSQL := fmt.Sprintf(`ALTER TABLE automations RENAME TO snapshot_automations;
 CREATE FUNCTION panel_document(doc jsonb) RETURNS jsonb LANGUAGE plpgsql AS $$ BEGIN
 IF current_setting('transaction_read_only') <> 'on' OR current_setting('transaction_isolation') <> 'repeatable read' THEN RAISE EXCEPTION 'panel requires read-only repeatable-read'; END IF;
 PERFORM pg_advisory_xact_lock(%d,%d); RETURN doc; END $$;
 CREATE VIEW automations AS SELECT id,panel_document(document) AS document,created_at,next_run_at FROM snapshot_automations`, lockA, lockB)
	if _, err = db.ExecContext(ctx, viewSQL); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	application := "panel_probe_" + domain.NewID()
	query := parsed.Query()
	query.Set("application_name", application)
	parsed.RawQuery = query.Encode()
	reader, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	type outcome struct {
		snapshot domain.PanelSnapshot
		err      error
	}
	done := make(chan outcome, 1)
	now := time.Now().UTC()
	go func() {
		snapshot, err := reader.PanelSnapshot(ctx, now.Add(-time.Hour), now.Add(time.Hour))
		done <- outcome{snapshot, err}
	}()
	waiting := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event='advisory')", application).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("snapshot did not reach controlled between-version boundary")
	}
	updated := domain.CloneAutomation(a)
	updated.Name = "After concurrent commit"
	document, _ := json.Marshal(updated)
	run := domain.NewRun(updated)
	runSnapshot, _ := json.Marshal(run.Snapshot)
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err = writer.ExecContext(ctx, "UPDATE snapshot_automations SET document=$2::jsonb WHERE id=$1", a.ID, string(document)); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.ExecContext(ctx, "INSERT INTO runs(id,automation_id,snapshot,origin,status,attempt_count,available_at,created_at) VALUES($1,$2,$3::jsonb,'manual','pending',0,$4,$4)", run.ID, a.ID, string(runSnapshot), run.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = lockConnection.ExecContext(ctx, "SELECT pg_advisory_unlock($1,$2)", lockA, lockB); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || len(result.snapshot.Automations) != 1 || result.snapshot.Automations[0].Name != a.Name || len(result.snapshot.Runs) != 0 {
		t.Fatalf("mixed old/new snapshot: %+v %v", result.snapshot, result.err)
	}
	fresh, err := reader.PanelSnapshot(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil || len(fresh.Automations) != 1 || fresh.Automations[0].Name != updated.Name || len(fresh.Runs) != 1 || fresh.Runs[0].ID != run.ID {
		t.Fatalf("subsequent snapshot did not see complete commit: %+v %v", fresh, err)
	}
}
