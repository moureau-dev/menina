package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestSchemaUsesTextIDsAndTimestamps(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.SetMeta(ctx, "hello", "world"); err != nil {
		t.Fatalf("set meta: %v", err)
	}
	value, ok, err := db.Meta(ctx, "hello")
	if err != nil || !ok || value != "world" {
		t.Fatalf("meta = %q ok=%v err=%v", value, ok, err)
	}
	var id string
	var createdAt, updatedAt int64
	var deletedAt sql.NullInt64
	row := db.QueryRowContext(ctx, `SELECT id, created_at, updated_at, deleted_at FROM meta WHERE key = ?`, "hello")
	if err := row.Scan(&id, &createdAt, &updatedAt, &deletedAt); err != nil {
		t.Fatalf("scan meta: %v", err)
	}
	if len(id) != 24 {
		t.Fatalf("id = %q, want 24-char hex", id)
	}
	if createdAt == 0 || updatedAt == 0 {
		t.Fatalf("timestamps not defaulted: %d %d", createdAt, updatedAt)
	}
	if deletedAt.Valid {
		t.Fatalf("a fresh row must be active (deleted_at IS NULL), got %d", deletedAt.Int64)
	}
}

func TestRemoteIngestDeduplicates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ev := &Event{DedupKey: "d1", Registry: "registry.moureau.dev", Repository: "p/api", Tag: "prod", Digest: "sha256:abc"}
	jobs := []Job{{DedupKey: "j1", Kind: "deploy", Project: "p", Environment: "prod", App: "api"}}
	created, err := db.IngestRegistryEvent(ctx, ev, jobs)
	if err != nil || !created {
		t.Fatalf("first ingest created=%v err=%v", created, err)
	}
	created, err = db.IngestRegistryEvent(ctx, ev, jobs)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate delivery should not create a second event/job")
	}
	pending, err := db.PendingJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending jobs = %d, want 1", len(pending))
	}
}

func TestJobLifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ev := &Event{DedupKey: "d2", Registry: "r", Repository: "p/api"}
	if _, err := db.IngestRegistryEvent(ctx, ev, []Job{{DedupKey: "j2", Kind: "deploy", Project: "p", Environment: "prod", App: "api"}}); err != nil {
		t.Fatal(err)
	}
	pending, _ := db.PendingJobs(ctx, 10)
	id := pending[0].ID
	if id == "" {
		t.Fatal("job id empty")
	}
	claimed, err := db.ClaimJob(ctx, id)
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if claimed, _ := db.ClaimJob(ctx, id); claimed {
		t.Fatal("double claim should fail")
	}
	if err := db.CompleteJob(ctx, id, "sha256:deadbeef"); err != nil {
		t.Fatal(err)
	}
	if pending, _ := db.PendingJobs(ctx, 10); len(pending) != 0 {
		t.Fatalf("expected no pending jobs, got %d", len(pending))
	}
	counts, err := db.CountJobsByStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[JobStatusDone] != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestFailJobRequeues(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, _ = db.IngestRegistryEvent(ctx, &Event{DedupKey: "d3"}, []Job{{DedupKey: "j3", Kind: "deploy", App: "api"}})
	pending, _ := db.PendingJobs(ctx, 10)
	_, _ = db.ClaimJob(ctx, pending[0].ID)
	if _, err := db.FailJob(ctx, pending[0].ID, "boom"); err != nil {
		t.Fatal(err)
	}
	requeued, _ := db.JobsByStatus(ctx, JobStatusPending)
	if len(requeued) != 1 || requeued[0].Attempts != 1 || requeued[0].LastError != "boom" {
		t.Fatalf("failed job requeue = %+v", requeued)
	}
	// The job is backed off, so it is not due for immediate retry.
	if due, _ := db.PendingJobs(ctx, 10); len(due) != 0 {
		t.Fatalf("backed-off job must not be immediately due: %+v", due)
	}
}

func TestFailJobDeadLettersAfterMaxAttempts(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, _ = db.IngestRegistryEvent(ctx, &Event{DedupKey: "d5"}, []Job{{DedupKey: "j5", Kind: "deploy", App: "api"}})
	pending, _ := db.PendingJobs(ctx, 10)
	id := pending[0].ID
	for i := 0; i < maxJobAttempts; i++ {
		_, _ = db.ClaimJob(ctx, id)
		_, _ = db.FailJob(ctx, id, "boom")
	}
	failed, _ := db.JobsByStatus(ctx, JobStatusFailed)
	if len(failed) != 1 {
		t.Fatalf("expected a dead-lettered job, got %+v", failed)
	}
	if due, _ := db.PendingJobs(ctx, 10); len(due) != 0 {
		t.Fatalf("dead-lettered job must not be pending: %+v", due)
	}
}

func TestRoutesReplaceAndQuery(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	routes := []Route{
		{Host: "a.com", PathPrefix: "/", Project: "p", Environment: "prod", App: "web", Namespace: "ns", Service: "web", Port: 80, TLS: true},
		{Host: "b.com", PathPrefix: "/", Project: "p", Environment: "prod", App: "api", Namespace: "ns", Service: "api", Port: 8080},
	}
	if err := db.ReplaceRoutes(ctx, routes); err != nil {
		t.Fatal(err)
	}
	got, err := db.Routes(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("routes = %+v err=%v", got, err)
	}
	if err := db.ReplaceRoutes(ctx, routes[:1]); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Routes(ctx)
	if len(got) != 1 || got[0].Host != "a.com" || !got[0].TLS {
		t.Fatalf("after replace routes = %+v", got)
	}
}

func TestHasRelease(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.RecordRelease(ctx, Release{Project: "p", Environment: "prod", App: "api", Repository: "r/api", Tag: "prod", Digest: "sha256:one", State: "active"}); err != nil {
		t.Fatal(err)
	}
	ok, err := db.HasRelease(ctx, "p", "prod", "api", "sha256:one")
	if err != nil || !ok {
		t.Fatalf("expected existing release: ok=%v err=%v", ok, err)
	}
	if ok, _ := db.HasRelease(ctx, "p", "prod", "api", "sha256:other"); ok {
		t.Fatal("unexpected match for a different digest")
	}
	if ok, _ := db.HasRelease(ctx, "p", "prod", "api", ""); ok {
		t.Fatal("empty digest must never match")
	}
}

func TestReleases(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.RecordRelease(ctx, Release{Project: "p", Environment: "prod", App: "api", Repository: "r/api", Tag: "prod", Digest: "sha256:one", State: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordRelease(ctx, Release{Project: "p", Environment: "prod", App: "api", Repository: "r/api", Tag: "prod", Digest: "sha256:two", State: "rolled_back"}); err != nil {
		t.Fatal(err)
	}
	releases, err := db.Releases(ctx, "p", "prod", "api")
	if err != nil || len(releases) != 2 {
		t.Fatalf("releases = %+v err=%v", releases, err)
	}
}

func TestObservedUpsert(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertObserved(ctx, Observed{Project: "p", Environment: "prod", App: "api", DesiredDigest: "sha256:1", Replicas: 2, ReadyReplicas: 1, Status: "progressing"}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertObserved(ctx, Observed{Project: "p", Environment: "prod", App: "api", DesiredDigest: "sha256:2", Replicas: 2, ReadyReplicas: 2, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	observed, err := db.Observed(ctx)
	if err != nil || len(observed) != 1 {
		t.Fatalf("observed = %+v err=%v", observed, err)
	}
	if observed[0].DesiredDigest != "sha256:2" || observed[0].Status != "ready" {
		t.Fatalf("observed not updated: %+v", observed[0])
	}
}

func TestObservedSoftDelete(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, o := range []Observed{
		{Project: "p", Environment: "prod", App: "api"},
		{Project: "p", Environment: "prod", App: "web"},
		{Project: "p", Environment: "staging", App: "api"},
	} {
		if err := db.UpsertObserved(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	// Soft-delete the app across all environments.
	if err := db.SyncObservedSoftDeletes(ctx, []SoftDeleteTarget{{Project: "p", App: "api"}}); err != nil {
		t.Fatal(err)
	}
	deleted, err := db.SoftDeletedTargets(ctx)
	if err != nil || len(deleted) != 2 {
		t.Fatalf("soft-deleted = %+v err=%v, want api in prod and staging", deleted, err)
	}
	observed, _ := db.Observed(ctx)
	if len(observed) != 1 || observed[0].App != "web" {
		t.Fatalf("active observed = %+v, want only web", observed)
	}
	// Clearing reactivates everything.
	if err := db.SyncObservedSoftDeletes(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if deleted, _ := db.SoftDeletedTargets(ctx); len(deleted) != 0 {
		t.Fatalf("after clear = %+v, want none", deleted)
	}
	if observed, _ := db.Observed(ctx); len(observed) != 3 {
		t.Fatalf("after clear observed = %d, want 3", len(observed))
	}
	// Upserting one target reactivates only that target (app-level delete marks
	// the app in every environment).
	_ = db.SyncObservedSoftDeletes(ctx, []SoftDeleteTarget{{Project: "p", App: "api"}})
	if err := db.UpsertObserved(ctx, Observed{Project: "p", Environment: "prod", App: "api", Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	deleted, _ = db.SoftDeletedTargets(ctx)
	if len(deleted) != 1 || deleted[0].Environment != "staging" || deleted[0].App != "api" {
		t.Fatalf("after reactivating prod, deleted = %+v, want staging/api only", deleted)
	}
}

func TestRequeueProcessingJobs(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, _ = db.IngestRegistryEvent(ctx, &Event{DedupKey: "d4"}, []Job{{DedupKey: "j4", Kind: "deploy", App: "api"}})
	pending, _ := db.PendingJobs(ctx, 10)
	_, _ = db.ClaimJob(ctx, pending[0].ID)
	n, err := db.RequeueProcessingJobs(ctx)
	if err != nil || n != 1 {
		t.Fatalf("requeued = %d err=%v", n, err)
	}
	pending, _ = db.PendingJobs(ctx, 10)
	if len(pending) != 1 {
		t.Fatalf("expected requeued job, got %d", len(pending))
	}
}
