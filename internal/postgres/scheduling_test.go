package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"central-automacoes/internal/domain"
	"central-automacoes/internal/storetest"
	"central-automacoes/migrations"
)

func TestPostgresSchedulingContract(t *testing.T) {
	storetest.Scheduling(t, func(t *testing.T) domain.Store { return openTestStore(t, schemaURL(t)) })
}

func rawDatabase(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUpgradePopulatedInitialMigration(t *testing.T) {
	dsn := schemaURL(t)
	db := rawDatabase(t, dsn)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, migrations.Initial); err != nil {
		t.Fatal(err)
	}
	a := automation(t)
	a.OverlapPolicy = ""
	document, _ := json.Marshal(a)
	if _, err := db.ExecContext(ctx, "INSERT INTO automations(id,document,created_at) VALUES($1,$2::jsonb,$3)", a.ID, string(document), a.CreatedAt); err != nil {
		t.Fatal(err)
	}
	r := domain.NewRun(a)
	now := time.Now().UTC()
	result := domain.Result{StatusCode: 503, Error: "histórico anterior", Retryable: true}
	rawResult, _ := json.Marshal(result)
	if _, err := db.ExecContext(ctx, "INSERT INTO runs(id,automation_id,snapshot,origin,status,attempt_count,available_at,created_at,finished_at,result) VALUES($1,$2,$3::jsonb,'manual','failed',1,$4,$4,$4,$5::jsonb)", r.ID, a.ID, string(document), now, string(rawResult)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO run_attempts(id,run_id,number,worker_id,started_at,finished_at,result) VALUES($1,$2,1,'legacy-worker',$3,$3,$4::jsonb)", domain.NewID(), r.ID, now, string(rawResult)); err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t, dsn)
	got, err := s.GetAutomation(ctx, a.ID)
	if err != nil || got.Name != a.Name || got.Config.URL != a.Config.URL {
		t.Fatalf("legacy automation: %+v %v", got, err)
	}
	old, err := s.GetRun(ctx, r.ID)
	if err != nil || old.Status != domain.Failed || old.Result == nil || *old.Result != result || old.ParentRunID != "" || old.ScheduledFor != nil {
		t.Fatalf("legacy run: %+v %v", old, err)
	}
	attempts, err := s.ListAttempts(ctx, r.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Result == nil || *attempts[0].Result != result {
		t.Fatalf("legacy attempts: %+v %v", attempts, err)
	}
	var versions int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version IN (1,2)").Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("version ledger count=%d error=%v", versions, err)
	}
	// Even an INSERT ... ON CONFLICT would fire this trigger. A successful second
	// migrate therefore proves that the strings for versions 001/002 are skipped.
	if _, err = db.ExecContext(ctx, `CREATE FUNCTION reject_ledger_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'applied migrations must not run again'; END $$; CREATE TRIGGER reject_ledger BEFORE INSERT ON schema_migrations FOR EACH ROW EXECUTE FUNCTION reject_ledger_insert()`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatalf("replayed an applied migration: %v", err)
	}
	// Newly introduced origins/states and link work against an upgraded schema.
	rerun, err := s.Rerun(ctx, r.ID)
	if err != nil || rerun.Origin != "retry" || rerun.ParentRunID != r.ID {
		t.Fatalf("rerun after upgrade: %+v %v", rerun, err)
	}
	canceled, err := s.CancelRun(ctx, rerun.ID)
	if err != nil || canceled.Status != domain.Canceled {
		t.Fatalf("cancel after upgrade: %+v %v", canceled, err)
	}
}

func TestScheduleTransactionRollbackAndReopenDedupe(t *testing.T) {
	for _, failure := range []string{"advance", "insert"} {
		t.Run(failure, func(t *testing.T) {
			dsn := schemaURL(t)
			db := rawDatabase(t, dsn)
			s := openTestStore(t, dsn)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			due := now.Add(-time.Minute)
			a := automation(t)
			a.Schedule = &domain.Schedule{Kind: "interval", IntervalSeconds: 5}
			a.NextRunAt = &due
			a, err := s.CreateAutomation(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			target := "BEFORE UPDATE ON automations"
			if failure == "insert" {
				target = "BEFORE INSERT ON runs"
			}
			if _, err = db.ExecContext(ctx, `CREATE FUNCTION fail_schedule_tick() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'controlled tick failure'; END $$; CREATE TRIGGER fail_tick `+target+` FOR EACH ROW EXECUTE FUNCTION fail_schedule_tick()`); err != nil {
				t.Fatal(err)
			}
			if emitted, err := s.ScheduleDue(ctx, now, 100); err == nil {
				t.Fatalf("expected controlled error, got runs=%+v", emitted)
			}
			unchanged, err := s.GetAutomation(ctx, a.ID)
			if err != nil || unchanged.NextRunAt == nil || !unchanged.NextRunAt.Equal(due) {
				t.Fatalf("next occurrence consumed despite rollback: %+v %v", unchanged, err)
			}
			var persistedDue time.Time
			if err = db.QueryRowContext(ctx, "SELECT next_run_at FROM automations WHERE id=$1", a.ID).Scan(&persistedDue); err != nil || !persistedDue.Equal(due) {
				t.Fatalf("indexed due changed: %v %v", persistedDue, err)
			}
			runs, err := s.QueryRuns(ctx, domain.RunFilter{AutomationID: a.ID})
			if err != nil || len(runs) != 0 {
				t.Fatalf("partial run survived rollback: %+v %v", runs, err)
			}
			table := "automations"
			if failure == "insert" {
				table = "runs"
			}
			if _, err = db.ExecContext(ctx, "DROP TRIGGER fail_tick ON "+table+"; DROP FUNCTION fail_schedule_tick()"); err != nil {
				t.Fatal(err)
			}
			generated, err := s.ScheduleDue(ctx, now, 100)
			if err != nil || len(generated) != 1 || generated[0].Origin != "scheduled" || generated[0].ScheduledFor == nil || !generated[0].ScheduledFor.Equal(due) {
				t.Fatalf("healthy tick: %+v %v", generated, err)
			}
			_, err = db.ExecContext(ctx, "INSERT INTO runs(id,automation_id,snapshot,origin,status,attempt_count,available_at,created_at,scheduled_for) SELECT $1,automation_id,snapshot,origin,'pending',0,available_at,created_at,scheduled_for FROM runs WHERE id=$2", domain.NewID(), generated[0].ID)
			var uniqueViolation interface{ SQLState() string }
			if !errors.As(err, &uniqueViolation) || uniqueViolation.SQLState() != "23505" {
				t.Fatalf("database did not independently reject duplicate occurrence: %v", err)
			}
			s.Close()
			s = openTestStore(t, dsn)
			duplicate, err := s.ScheduleDue(ctx, now, 100)
			if err != nil || len(duplicate) != 0 {
				t.Fatalf("duplicate occurrence after reopen: %+v %v", duplicate, err)
			}
			runs, err = s.QueryRuns(ctx, domain.RunFilter{AutomationID: a.ID})
			if err != nil || len(runs) != 1 {
				t.Fatalf("expected exactly one persisted run: %+v %v", runs, err)
			}
			advanced, err := s.GetAutomation(ctx, a.ID)
			if err != nil || advanced.NextRunAt == nil || !advanced.NextRunAt.After(now) {
				t.Fatalf("next occurrence not future: %+v %v", advanced, err)
			}
		})
	}
}
