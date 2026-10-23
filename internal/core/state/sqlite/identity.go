package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/moureau-dev/menina/internal/core/identity"
)

// CreateProfile inserts a profile and returns its generated ID.
func (db *DB) CreateProfile(ctx context.Context, p identity.Profile) (string, error) {
	if p.Status == "" {
		p.Status = identity.StatusActive
	}
	if err := p.Validate(); err != nil {
		return "", err
	}
	scopes, err := json.Marshal(p.Scopes)
	if err != nil {
		return "", err
	}
	var id string
	err = db.QueryRowContext(ctx, `INSERT INTO profiles (name, scopes, status) VALUES (?, ?, ?) RETURNING id`,
		p.Name, string(scopes), p.Status).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create profile: %w", err)
	}
	return id, nil
}

// UpdateProfile updates a profile's name, scopes, and status.
func (db *DB) UpdateProfile(ctx context.Context, p identity.Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	scopes, err := json.Marshal(p.Scopes)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx, `UPDATE profiles SET name = ?, scopes = ?, status = ?, updated_at = unixepoch() WHERE id = ?`,
		p.Name, string(scopes), p.Status, p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("profile %q not found", p.ID)
	}
	return nil
}

// ProfileByID returns one profile.
func (db *DB) ProfileByID(ctx context.Context, id string) (identity.Profile, bool, error) {
	row := db.QueryRowContext(ctx, `SELECT id, name, scopes, status, created_at, updated_at, last_seen_at FROM profiles WHERE id = ?`, id)
	p, err := scanProfile(row)
	if err == sql.ErrNoRows {
		return identity.Profile{}, false, nil
	}
	if err != nil {
		return identity.Profile{}, false, err
	}
	return p, true, nil
}

// Profiles lists profiles by name.
func (db *DB) Profiles(ctx context.Context) ([]identity.Profile, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, scopes, status, created_at, updated_at, last_seen_at FROM profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddProfileKey attaches a key to a profile.
func (db *DB) AddProfileKey(ctx context.Context, k identity.Key) (string, error) {
	if k.Status == "" {
		k.Status = identity.StatusActive
	}
	if err := k.Validate(); err != nil {
		return "", err
	}
	var expires any
	if !k.ExpiresAt.IsZero() {
		expires = k.ExpiresAt.Unix()
	}
	var id string
	err := db.QueryRowContext(ctx, `INSERT INTO profile_keys (profile_id, fingerprint, public_key, comment, status, expires_at)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		k.ProfileID, k.Fingerprint, k.PublicKey, k.Comment, k.Status, expires).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("add profile key: %w", err)
	}
	return id, nil
}

// ProfileKeys lists keys attached to a profile.
func (db *DB) ProfileKeys(ctx context.Context, profileID string) ([]identity.Key, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, profile_id, fingerprint, public_key, comment, status, expires_at, created_at, updated_at, last_seen_at
		FROM profile_keys WHERE profile_id = ? ORDER BY created_at`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.Key
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ResolveKey maps an authenticated key fingerprint to its active profile and
// updates last-seen timestamps. It returns false for unknown, disabled,
// revoked, or expired keys.
func (db *DB) ResolveKey(ctx context.Context, fingerprint string) (identity.Profile, identity.Key, bool, error) {
	row := db.QueryRowContext(ctx, `SELECT k.id, k.profile_id, k.fingerprint, k.public_key, k.comment, k.status, k.expires_at, k.created_at, k.updated_at, k.last_seen_at,
		p.id, p.name, p.scopes, p.status, p.created_at, p.updated_at, p.last_seen_at
		FROM profile_keys k JOIN profiles p ON p.id = k.profile_id
		WHERE k.fingerprint = ?`, fingerprint)
	var k identity.Key
	var p identity.Profile
	var keyExpires, keyLastSeen, profileLastSeen sql.NullInt64
	var keyCreated, keyUpdated, profileCreated, profileUpdated int64
	var keyStatus, profileStatus, scopes string
	err := row.Scan(&k.ID, &k.ProfileID, &k.Fingerprint, &k.PublicKey, &k.Comment, &keyStatus, &keyExpires, &keyCreated, &keyUpdated, &keyLastSeen,
		&p.ID, &p.Name, &scopes, &profileStatus, &profileCreated, &profileUpdated, &profileLastSeen)
	if err == sql.ErrNoRows {
		return identity.Profile{}, identity.Key{}, false, nil
	}
	if err != nil {
		return identity.Profile{}, identity.Key{}, false, err
	}
	k.Status = identity.Status(keyStatus)
	p.Status = identity.Status(profileStatus)
	if err := json.Unmarshal([]byte(scopes), &p.Scopes); err != nil {
		return identity.Profile{}, identity.Key{}, false, err
	}
	k.CreatedAt = time.Unix(keyCreated, 0).UTC()
	k.UpdatedAt = time.Unix(keyUpdated, 0).UTC()
	p.CreatedAt = time.Unix(profileCreated, 0).UTC()
	p.UpdatedAt = time.Unix(profileUpdated, 0).UTC()
	if keyExpires.Valid {
		k.ExpiresAt = time.Unix(keyExpires.Int64, 0).UTC()
	}
	if keyLastSeen.Valid {
		k.LastSeenAt = time.Unix(keyLastSeen.Int64, 0).UTC()
	}
	if profileLastSeen.Valid {
		p.LastSeenAt = time.Unix(profileLastSeen.Int64, 0).UTC()
	}
	now := time.Now().UTC()
	if k.Status != identity.StatusActive || p.Status != identity.StatusActive || k.Expired(now) {
		return identity.Profile{}, identity.Key{}, false, nil
	}
	if err := db.touchKey(ctx, k.ID, p.ID, now); err != nil {
		return identity.Profile{}, identity.Key{}, false, err
	}
	k.LastSeenAt, p.LastSeenAt = now, now
	return p, k, true, nil
}

func (db *DB) touchKey(ctx context.Context, keyID, profileID string, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE profile_keys SET last_seen_at = ?, updated_at = unixepoch() WHERE id = ?`, now.Unix(), keyID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE profiles SET last_seen_at = ?, updated_at = unixepoch() WHERE id = ?`, now.Unix(), profileID); err != nil {
		return err
	}
	return tx.Commit()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProfile(row rowScanner) (identity.Profile, error) {
	var p identity.Profile
	var scopes, status string
	var created, updated int64
	var lastSeen sql.NullInt64
	if err := row.Scan(&p.ID, &p.Name, &scopes, &status, &created, &updated, &lastSeen); err != nil {
		return identity.Profile{}, err
	}
	if err := json.Unmarshal([]byte(scopes), &p.Scopes); err != nil {
		return identity.Profile{}, err
	}
	p.Status = identity.Status(status)
	p.CreatedAt = time.Unix(created, 0).UTC()
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	if lastSeen.Valid {
		p.LastSeenAt = time.Unix(lastSeen.Int64, 0).UTC()
	}
	return p, nil
}

func scanKey(row rowScanner) (identity.Key, error) {
	var k identity.Key
	var status string
	var expires, lastSeen sql.NullInt64
	var created, updated int64
	if err := row.Scan(&k.ID, &k.ProfileID, &k.Fingerprint, &k.PublicKey, &k.Comment, &status, &expires, &created, &updated, &lastSeen); err != nil {
		return identity.Key{}, err
	}
	k.Status = identity.Status(status)
	k.CreatedAt = time.Unix(created, 0).UTC()
	k.UpdatedAt = time.Unix(updated, 0).UTC()
	if expires.Valid {
		k.ExpiresAt = time.Unix(expires.Int64, 0).UTC()
	}
	if lastSeen.Valid {
		k.LastSeenAt = time.Unix(lastSeen.Int64, 0).UTC()
	}
	return k, nil
}

// CreateEnrollmentToken stores a hashed one-time enrollment token.
func (db *DB) CreateEnrollmentToken(ctx context.Context, t identity.EnrollmentToken) (string, error) {
	if t.ProfileID == "" || t.TokenHash == "" || t.ExpiresAt.IsZero() {
		return "", fmt.Errorf("enrollment token requires profile, hash, and expiry")
	}
	var id string
	err := db.QueryRowContext(ctx, `INSERT INTO enrollment_tokens (profile_id, token_hash, expires_at) VALUES (?, ?, ?) RETURNING id`,
		t.ProfileID, t.TokenHash, t.ExpiresAt.Unix()).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create enrollment token: %w", err)
	}
	return id, nil
}

// EnrollmentTokenByHash returns a stored enrollment token by hash.
func (db *DB) EnrollmentTokenByHash(ctx context.Context, hash string) (identity.EnrollmentToken, bool, error) {
	row := db.QueryRowContext(ctx, `SELECT id, profile_id, token_hash, expires_at, created_at, used_at FROM enrollment_tokens WHERE token_hash = ?`, hash)
	var t identity.EnrollmentToken
	var expires, created int64
	var used sql.NullInt64
	if err := row.Scan(&t.ID, &t.ProfileID, &t.TokenHash, &expires, &created, &used); err != nil {
		if err == sql.ErrNoRows {
			return identity.EnrollmentToken{}, false, nil
		}
		return identity.EnrollmentToken{}, false, err
	}
	t.ExpiresAt = time.Unix(expires, 0).UTC()
	t.CreatedAt = time.Unix(created, 0).UTC()
	if used.Valid {
		t.UsedAt = time.Unix(used.Int64, 0).UTC()
	}
	return t, true, nil
}

// ConsumeEnrollmentToken atomically consumes a token and attaches a key to its
// profile. Unknown, expired, used, or disabled profiles all return
// identity.ErrEnrollmentInvalid.
func (db *DB) ConsumeEnrollmentToken(ctx context.Context, tokenHash string, now time.Time, key identity.Key) (identity.Profile, identity.Key, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return identity.Profile{}, identity.Key{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var tokenID, profileID, profileStatus string
	var expires int64
	var used sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT t.id, t.profile_id, t.expires_at, t.used_at, p.status
		FROM enrollment_tokens t JOIN profiles p ON p.id = t.profile_id
		WHERE t.token_hash = ?`, tokenHash).Scan(&tokenID, &profileID, &expires, &used, &profileStatus)
	if err == sql.ErrNoRows {
		return identity.Profile{}, identity.Key{}, identity.ErrEnrollmentInvalid
	}
	if err != nil {
		return identity.Profile{}, identity.Key{}, err
	}
	if used.Valid || now.Unix() >= expires || profileStatus != string(identity.StatusActive) {
		return identity.Profile{}, identity.Key{}, identity.ErrEnrollmentInvalid
	}

	res, err := tx.ExecContext(ctx, `UPDATE enrollment_tokens SET used_at = ?, updated_at = unixepoch() WHERE id = ? AND used_at IS NULL`, now.Unix(), tokenID)
	if err != nil {
		return identity.Profile{}, identity.Key{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return identity.Profile{}, identity.Key{}, identity.ErrEnrollmentInvalid
	}

	key.ProfileID = profileID
	if key.Status == "" {
		key.Status = identity.StatusActive
	}
	if err := key.Validate(); err != nil {
		return identity.Profile{}, identity.Key{}, err
	}
	var expiresAt any
	if !key.ExpiresAt.IsZero() {
		expiresAt = key.ExpiresAt.Unix()
	}
	var keyID string
	err = tx.QueryRowContext(ctx, `INSERT INTO profile_keys (profile_id, fingerprint, public_key, comment, status, expires_at)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		key.ProfileID, key.Fingerprint, key.PublicKey, key.Comment, key.Status, expiresAt).Scan(&keyID)
	if err != nil {
		return identity.Profile{}, identity.Key{}, fmt.Errorf("attach enrollment key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return identity.Profile{}, identity.Key{}, err
	}

	profile, ok, err := db.ProfileByID(ctx, profileID)
	if err != nil {
		return identity.Profile{}, identity.Key{}, err
	}
	if !ok {
		return identity.Profile{}, identity.Key{}, fmt.Errorf("profile %q missing after enrollment", profileID)
	}
	key.ID = keyID
	key.CreatedAt = now.UTC()
	key.UpdatedAt = now.UTC()
	return profile, key, nil
}

// KeyByID returns one profile key.
func (db *DB) KeyByID(ctx context.Context, id string) (identity.Key, bool, error) {
	row := db.QueryRowContext(ctx, `SELECT id, profile_id, fingerprint, public_key, comment, status, expires_at, created_at, updated_at, last_seen_at
		FROM profile_keys WHERE id = ?`, id)
	k, err := scanKey(row)
	if err == sql.ErrNoRows {
		return identity.Key{}, false, nil
	}
	if err != nil {
		return identity.Key{}, false, err
	}
	return k, true, nil
}

// KeyByFingerprint returns one profile key by fingerprint.
func (db *DB) KeyByFingerprint(ctx context.Context, fingerprint string) (identity.Key, bool, error) {
	row := db.QueryRowContext(ctx, `SELECT id, profile_id, fingerprint, public_key, comment, status, expires_at, created_at, updated_at, last_seen_at
		FROM profile_keys WHERE fingerprint = ?`, fingerprint)
	k, err := scanKey(row)
	if err == sql.ErrNoRows {
		return identity.Key{}, false, nil
	}
	if err != nil {
		return identity.Key{}, false, err
	}
	return k, true, nil
}

// RevokeKey marks one key revoked without touching other keys.
func (db *DB) RevokeKey(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `UPDATE profile_keys SET status = ?, updated_at = unixepoch() WHERE id = ?`, identity.StatusRevoked, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("key %q not found", id)
	}
	return nil
}

// UpdateKeyExpiry sets or clears a key expiry.
func (db *DB) UpdateKeyExpiry(ctx context.Context, id string, expiresAt time.Time) error {
	var expires any
	if !expiresAt.IsZero() {
		expires = expiresAt.Unix()
	}
	res, err := db.ExecContext(ctx, `UPDATE profile_keys SET expires_at = ?, updated_at = unixepoch() WHERE id = ?`, expires, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("key %q not found", id)
	}
	return nil
}

// RotateKey attaches a new key to the old key's profile and optionally revokes
// the old key in the same transaction.
func (db *DB) RotateKey(ctx context.Context, oldKeyID string, newKey identity.Key, revokeOld bool) (identity.Key, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return identity.Key{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var profileID string
	if err := tx.QueryRowContext(ctx, `SELECT profile_id FROM profile_keys WHERE id = ?`, oldKeyID).Scan(&profileID); err != nil {
		if err == sql.ErrNoRows {
			return identity.Key{}, fmt.Errorf("key %q not found", oldKeyID)
		}
		return identity.Key{}, err
	}
	newKey.ProfileID = profileID
	if newKey.Status == "" {
		newKey.Status = identity.StatusActive
	}
	if err := newKey.Validate(); err != nil {
		return identity.Key{}, err
	}
	var expires any
	if !newKey.ExpiresAt.IsZero() {
		expires = newKey.ExpiresAt.Unix()
	}
	var id string
	if err := tx.QueryRowContext(ctx, `INSERT INTO profile_keys (profile_id, fingerprint, public_key, comment, status, expires_at)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		newKey.ProfileID, newKey.Fingerprint, newKey.PublicKey, newKey.Comment, newKey.Status, expires).Scan(&id); err != nil {
		return identity.Key{}, fmt.Errorf("rotate key: %w", err)
	}
	if revokeOld {
		if _, err := tx.ExecContext(ctx, `UPDATE profile_keys SET status = ?, updated_at = unixepoch() WHERE id = ?`, identity.StatusRevoked, oldKeyID); err != nil {
			return identity.Key{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return identity.Key{}, err
	}
	now := time.Now().UTC()
	newKey.ID = id
	newKey.CreatedAt = now
	newKey.UpdatedAt = now
	return newKey, nil
}
