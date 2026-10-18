package database

import (
	"context"
	"testing"
	"time"
)

func TestFailInterruptedReleases(t *testing.T) {
	db := testJobsDB(t)
	ctx := context.Background()
	id, err := db.CreateRelease(ctx, Release{Project: "p", Environment: "prod", App: "api", Repository: "r/api", Digest: "sha256:a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE releases SET updated_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).Unix(), id); err != nil {
		t.Fatal(err)
	}
	n, err := db.FailInterruptedReleases(ctx, 30*time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("failed = %d err=%v", n, err)
	}
	rel, ok, err := db.ReleaseByID(ctx, id)
	if err != nil || !ok || rel.State != "failed" {
		t.Fatalf("release = %+v ok=%v err=%v", rel, ok, err)
	}
	// A release with an active job is not interrupted.
	id2, _ := db.CreateRelease(ctx, Release{Project: "p", Environment: "prod", App: "web", Repository: "r/web", Digest: "sha256:b"})
	if _, err := db.ExecContext(ctx, `UPDATE releases SET updated_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).Unix(), id2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.IngestRegistryEvent(ctx, &Event{DedupKey: "ev"}, []Job{{DedupKey: "job", Kind: "deploy", Project: "p", Environment: "prod", App: "web"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET release_id = ? WHERE dedup_key = 'job'`, id2); err != nil {
		t.Fatal(err)
	}
	if n, err := db.FailInterruptedReleases(ctx, 30*time.Minute); err != nil || n != 0 {
		t.Fatalf("active release was marked interrupted: n=%d err=%v", n, err)
	}
}
