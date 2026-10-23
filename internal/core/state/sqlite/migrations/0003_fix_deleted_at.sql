-- Fix the deleted_at convention: it must be NULL when a row is active and hold
-- the deletion time when the row is soft-deleted. The old schema used
-- `deleted_at INTEGER NOT NULL DEFAULT (unixepoch())` with active = (deleted_at
-- = created_at), which is unrepresentable for NULL and wrong. SQLite cannot
-- alter nullability/default in place, so rebuild each table.
--
-- Normalize existing rows: rows that were active (deleted_at = created_at)
-- become NULL; anything else keeps its timestamp.

DROP TABLE IF EXISTS soft_deletes;

CREATE TABLE meta_new (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER,
  key TEXT NOT NULL UNIQUE,
  value TEXT NOT NULL
);
INSERT INTO meta_new (id, created_at, updated_at, deleted_at, key, value)
  SELECT id, created_at, updated_at, NULLIF(deleted_at, created_at), key, value FROM meta;
DROP TABLE meta;
ALTER TABLE meta_new RENAME TO meta;
CREATE INDEX IF NOT EXISTS idx_meta_key ON meta(key);

CREATE TABLE events_new (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER,
  dedup_key TEXT NOT NULL UNIQUE,
  registry TEXT NOT NULL,
  repository TEXT NOT NULL,
  tag TEXT NOT NULL DEFAULT '',
  digest TEXT NOT NULL DEFAULT '',
  delivery TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  processed_at INTEGER
);
INSERT INTO events_new (id, created_at, updated_at, deleted_at, dedup_key, registry, repository, tag, digest, delivery, status, processed_at)
  SELECT id, created_at, updated_at, NULLIF(deleted_at, created_at), dedup_key, registry, repository, tag, digest, delivery, status, processed_at FROM events;
DROP TABLE events;
ALTER TABLE events_new RENAME TO events;
CREATE UNIQUE INDEX IF NOT EXISTS idx_events_dedup ON events(dedup_key);
CREATE INDEX IF NOT EXISTS idx_events_status ON events(status, created_at);
CREATE INDEX IF NOT EXISTS idx_events_repo ON events(repository, tag);

CREATE TABLE jobs_new (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER,
  dedup_key TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL,
  project TEXT NOT NULL DEFAULT '',
  environment TEXT NOT NULL DEFAULT '',
  app TEXT NOT NULL DEFAULT '',
  repository TEXT NOT NULL DEFAULT '',
  tag TEXT NOT NULL DEFAULT '',
  desired_digest TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  next_attempt_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO jobs_new (id, created_at, updated_at, deleted_at, dedup_key, kind, project, environment, app, repository, tag, desired_digest, status, attempts, last_error, next_attempt_at)
  SELECT id, created_at, updated_at, NULLIF(deleted_at, created_at), dedup_key, kind, project, environment, app, repository, tag, desired_digest, status, attempts, last_error, next_attempt_at FROM jobs;
DROP TABLE jobs;
ALTER TABLE jobs_new RENAME TO jobs;
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_dedup ON jobs(dedup_key);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status, created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_target ON jobs(project, environment, app);
CREATE INDEX IF NOT EXISTS idx_jobs_due ON jobs(status, next_attempt_at, created_at);

CREATE TABLE releases_new (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER,
  project TEXT NOT NULL,
  environment TEXT NOT NULL,
  app TEXT NOT NULL,
  repository TEXT NOT NULL,
  tag TEXT NOT NULL DEFAULT '',
  digest TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'deployed'
);
INSERT INTO releases_new (id, created_at, updated_at, deleted_at, project, environment, app, repository, tag, digest, status)
  SELECT id, created_at, updated_at, NULLIF(deleted_at, created_at), project, environment, app, repository, tag, digest, status FROM releases;
DROP TABLE releases;
ALTER TABLE releases_new RENAME TO releases;
CREATE INDEX IF NOT EXISTS idx_releases_target ON releases(project, environment, app, created_at);
CREATE INDEX IF NOT EXISTS idx_releases_digest ON releases(digest);

CREATE TABLE routes_new (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER,
  host TEXT NOT NULL,
  path_prefix TEXT NOT NULL DEFAULT '/',
  project TEXT NOT NULL,
  environment TEXT NOT NULL,
  app TEXT NOT NULL,
  namespace TEXT NOT NULL,
  service TEXT NOT NULL,
  port INTEGER NOT NULL,
  tls INTEGER NOT NULL DEFAULT 0,
  redirect_http INTEGER NOT NULL DEFAULT 0,
  headers TEXT NOT NULL DEFAULT '',
  timeouts TEXT NOT NULL DEFAULT '',
  UNIQUE(host, path_prefix)
);
INSERT INTO routes_new (id, created_at, updated_at, deleted_at, host, path_prefix, project, environment, app, namespace, service, port, tls, redirect_http, headers, timeouts)
  SELECT id, created_at, updated_at, NULLIF(deleted_at, created_at), host, path_prefix, project, environment, app, namespace, service, port, tls, redirect_http, headers, timeouts FROM routes;
DROP TABLE routes;
ALTER TABLE routes_new RENAME TO routes;
CREATE UNIQUE INDEX IF NOT EXISTS idx_routes_host_path ON routes(host, path_prefix);
CREATE INDEX IF NOT EXISTS idx_routes_host ON routes(host);

CREATE TABLE observed_new (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER,
  project TEXT NOT NULL,
  environment TEXT NOT NULL,
  app TEXT NOT NULL,
  desired_digest TEXT NOT NULL DEFAULT '',
  observed_digest TEXT NOT NULL DEFAULT '',
  image TEXT NOT NULL DEFAULT '',
  replicas INTEGER NOT NULL DEFAULT 0,
  ready_replicas INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT '',
  UNIQUE(project, environment, app)
);
INSERT INTO observed_new (id, created_at, updated_at, deleted_at, project, environment, app, desired_digest, observed_digest, image, replicas, ready_replicas, status)
  SELECT id, created_at, updated_at, NULLIF(deleted_at, created_at), project, environment, app, desired_digest, observed_digest, image, replicas, ready_replicas, status FROM observed;
DROP TABLE observed;
ALTER TABLE observed_new RENAME TO observed;
CREATE UNIQUE INDEX IF NOT EXISTS idx_observed_target ON observed(project, environment, app);
