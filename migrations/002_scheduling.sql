ALTER TABLE automations ADD COLUMN next_run_at timestamptz;
ALTER TABLE runs ADD COLUMN scheduled_for timestamptz;
ALTER TABLE runs ADD COLUMN parent_run_id text REFERENCES runs(id);
ALTER TABLE runs ADD COLUMN cancel_requested_at timestamptz;

ALTER TABLE runs DROP CONSTRAINT runs_origin_check;
ALTER TABLE runs ADD CONSTRAINT runs_origin_check CHECK (origin IN ('manual','scheduled','retry'));
ALTER TABLE runs DROP CONSTRAINT runs_status_check;
ALTER TABLE runs ADD CONSTRAINT runs_status_check CHECK (status IN ('pending','running','succeeded','failed','canceled'));

CREATE INDEX automations_due_idx ON automations(next_run_at,id) WHERE next_run_at IS NOT NULL;
CREATE UNIQUE INDEX runs_scheduled_occurrence_idx ON runs(automation_id,scheduled_for) WHERE scheduled_for IS NOT NULL;
CREATE INDEX runs_active_automation_idx ON runs(automation_id,status) WHERE status IN ('pending','running');
CREATE INDEX runs_automation_history_idx ON runs(automation_id,created_at DESC,id DESC);
CREATE INDEX runs_status_history_idx ON runs(status,created_at DESC,id DESC);
