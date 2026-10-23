CREATE TABLE IF NOT EXISTS enrollment_tokens (
  id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(12)))),
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
  profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at INTEGER NOT NULL,
  used_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_enrollment_tokens_expiry ON enrollment_tokens(expires_at);
