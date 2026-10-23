package sqlite_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/moureau-dev/menina/internal/core/state/sqlite"
)

func TestMigrateExistingDatabasePreservesData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "existing.db")

	before, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := before.IngestRegistryEvent(ctx, &sqlite.Event{DedupKey: "existing-event", Repository: "p/api", Tag: "production"}, []sqlite.Job{{
		DedupKey: "existing-job", Kind: "manual", Project: "p", Environment: "production", App: "api", Repository: "registry.example.com/p/api", Tag: "production",
	}}); err != nil {
		t.Fatal(err)
	}
	id, found, err := before.JobIDByDedupKey(ctx, "existing-job")
	if err != nil || !found {
		t.Fatalf("job id found=%v err=%v", found, err)
	}
	if err := before.Close(); err != nil {
		t.Fatal(err)
	}

	after, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = after.Close() }()
	if err := after.Migrate(ctx); err != nil {
		t.Fatalf("migrate existing database: %v", err)
	}
	job, found, err := after.JobByID(ctx, id)
	if err != nil || !found || job.Project != "p" || job.App != "api" {
		t.Fatalf("job=%+v found=%v err=%v", job, found, err)
	}

	rows, err := after.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		got = append(got, version)
	}
	want := []string{"0001_init", "0002_job_backoff", "0003_fix_deleted_at", "0004_releases_and_pinning", "0005_release_backfill"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migrations = %v, want %v", got, want)
	}
}
