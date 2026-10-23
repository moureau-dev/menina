CREATE TABLE IF NOT EXISTS profiles (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  name TEXT NOT NULL UNIQUE,
  scopes TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL DEFAULT 'active',
  last_seen_at INTEGER
);

CREATE TABLE IF NOT EXISTS profile_keys (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  fingerprint TEXT NOT NULL UNIQUE,
  public_key TEXT NOT NULL DEFAULT '',
  comment TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  expires_at INTEGER,
  last_seen_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_profile_keys_profile ON profile_keys(profile_id);
