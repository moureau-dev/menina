ALTER TABLE jobs ADD COLUMN next_attempt_at INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_jobs_due ON jobs(status, next_attempt_at, created_at);
