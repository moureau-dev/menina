CREATE TABLE IF NOT EXISTS meta (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER NOT NULL DEFAULT (unixepoch()),
  key TEXT NOT NULL UNIQUE,
  value TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meta_key ON meta(key);

CREATE TABLE IF NOT EXISTS events (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER NOT NULL DEFAULT (unixepoch()),
  dedup_key TEXT NOT NULL UNIQUE,
  registry TEXT NOT NULL,
  repository TEXT NOT NULL,
  tag TEXT NOT NULL DEFAULT '',
  digest TEXT NOT NULL DEFAULT '',
  delivery TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  processed_at INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_events_dedup ON events(dedup_key);
CREATE INDEX IF NOT EXISTS idx_events_status ON events(status, created_at);
CREATE INDEX IF NOT EXISTS idx_events_repo ON events(repository, tag);

CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER NOT NULL DEFAULT (unixepoch()),
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
  last_error TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_dedup ON jobs(dedup_key);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status, created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_target ON jobs(project, environment, app);

CREATE TABLE IF NOT EXISTS releases (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER NOT NULL DEFAULT (unixepoch()),
  project TEXT NOT NULL,
  environment TEXT NOT NULL,
  app TEXT NOT NULL,
  repository TEXT NOT NULL,
  tag TEXT NOT NULL DEFAULT '',
  digest TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'deployed'
);
CREATE INDEX IF NOT EXISTS idx_releases_target ON releases(project, environment, app, created_at);
CREATE INDEX IF NOT EXISTS idx_releases_digest ON releases(digest);

CREATE TABLE IF NOT EXISTS routes (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER NOT NULL DEFAULT (unixepoch()),
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
CREATE UNIQUE INDEX IF NOT EXISTS idx_routes_host_path ON routes(host, path_prefix);
CREATE INDEX IF NOT EXISTS idx_routes_host ON routes(host);

CREATE TABLE IF NOT EXISTS observed (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  deleted_at INTEGER NOT NULL DEFAULT (unixepoch()),
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
CREATE UNIQUE INDEX IF NOT EXISTS idx_observed_target ON observed(project, environment, app);
