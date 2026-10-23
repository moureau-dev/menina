package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/moureau-dev/menina/internal/core/identity"
	"github.com/moureau-dev/menina/internal/core/state/sqlite"
)

func TestEnrollmentTokenIsHashedAtRest(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	profileID, err := db.CreateProfile(ctx, identity.Profile{Name: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	plaintext, token, err := identity.NewEnrollmentToken(profileID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateEnrollmentToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRowContext(ctx, `SELECT token_hash FROM enrollment_tokens WHERE id = ?`, tokenID(t, db, token.TokenHash)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != token.TokenHash || stored == plaintext {
		t.Fatalf("stored hash = %q, plaintext = %q", stored, plaintext)
	}
}

func tokenID(t *testing.T, db *sqlite.DB, hash string) string {
	t.Helper()
	token, ok, err := db.EnrollmentTokenByHash(context.Background(), hash)
	if err != nil || !ok {
		t.Fatalf("token found=%v err=%v", ok, err)
	}
	return token.ID
}
