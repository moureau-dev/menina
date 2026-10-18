-- Per-app release identity, explicit release states, and deterministic jobs.
--
-- A release row is the durable record of one app rollout attempt. `status` is
-- the release state (see internal/release). A job may point at the release it
-- is driving, and carries the resolved configuration snapshot it was queued
-- against so a deploy cannot execute against config written later.

ALTER TABLE releases ADD COLUMN image TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN previous_id TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN config_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE releases ADD COLUMN config_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN config_snapshot TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN secret_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN verification TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN migration TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN recovery TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN source_repo TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN source_revision TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN actor TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN message TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN finished_at INTEGER;

CREATE INDEX IF NOT EXISTS idx_releases_state ON releases(project, environment, app, status, created_at);
CREATE INDEX IF NOT EXISTS idx_releases_previous ON releases(previous_id);

CREATE TABLE IF NOT EXISTS release_events (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER,
  release_id TEXT NOT NULL,
  state TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  actor TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_release_events_release ON release_events(release_id, created_at);

ALTER TABLE jobs ADD COLUMN config_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN config_snapshot TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN config_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN release_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN actor TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN source TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN started_at INTEGER;
ALTER TABLE jobs ADD COLUMN finished_at INTEGER;
ALTER TABLE jobs ADD COLUMN exit_code INTEGER;

CREATE INDEX IF NOT EXISTS idx_jobs_release ON jobs(release_id);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER,
  dedup_key TEXT NOT NULL UNIQUE,
  project TEXT NOT NULL DEFAULT '',
  environment TEXT NOT NULL DEFAULT '',
  app TEXT NOT NULL DEFAULT '',
  event TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  attempts INTEGER NOT NULL DEFAULT 0,
  response_code INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  payload TEXT NOT NULL DEFAULT '',
  next_attempt_at INTEGER NOT NULL DEFAULT 0,
  delivered_at INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_deliveries_dedup ON webhook_deliveries(dedup_key);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_status ON webhook_deliveries(status, created_at);

CREATE TABLE IF NOT EXISTS job_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  job_id TEXT NOT NULL,
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  message TEXT NOT NULL DEFAULT '',
  exit_code INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_job_events_job ON job_events(job_id, id);

-- Normalize historical release statuses into the explicit state machine.
UPDATE releases SET status = 'active' WHERE status = 'deployed';
UPDATE releases SET status = 'rolled_back' WHERE status IN ('rolled-back', 'rolledback', 'rolled back');
UPDATE releases SET finished_at = created_at WHERE finished_at IS NULL AND status IN ('active', 'rolled_back', 'failed');
