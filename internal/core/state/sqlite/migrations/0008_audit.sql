CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  actor_id TEXT NOT NULL DEFAULT '',
  key_fingerprint TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  project TEXT NOT NULL DEFAULT '',
  environment TEXT NOT NULL DEFAULT '',
  app TEXT NOT NULL DEFAULT '',
  plan_id TEXT NOT NULL DEFAULT '',
  state_token TEXT NOT NULL DEFAULT '',
  source_repo TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_events(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_events(actor_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_project ON audit_events(project, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_events(action, created_at);
