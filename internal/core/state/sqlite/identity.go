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
