package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func testJobsDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestListJobsAndCancel(t *testing.T) {
	ctx := context.Background()
	db := testJobsDB(t)
	_, err := db.IngestRegistryEvent(ctx, &Event{DedupKey: "e1", Repository: "p/api", Tag: "dev"},
		[]Job{{DedupKey: "j1", Kind: "manual", Project: "p", Environment: "dev", App: "api", Repository: "p/api", Tag: "dev"}})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ListJobs(ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Status != JobStatusPending || jobs[0].App != "api" {
		t.Fatalf("jobs = %+v", jobs)
	}
	filtered, err := db.ListJobs(ctx, JobStatusFailed, 50)
	if err != nil || len(filtered) != 0 {
		t.Fatalf("filtered = %+v err=%v", filtered, err)
	}

	ok, err := db.CancelJob(ctx, jobs[0].ID)
	if err != nil || !ok {
		t.Fatalf("cancel = %v err=%v", ok, err)
	}
	j, found, err := db.JobByID(ctx, jobs[0].ID)
	if err != nil || !found || j.Status != JobStatusCancelled {
		t.Fatalf("job after cancel = %+v found=%v err=%v", j, found, err)
	}
	// Cancelling again (already terminal) must report false.
	if ok, _ := db.CancelJob(ctx, jobs[0].ID); ok {
		t.Fatal("cancelling a terminal job must return false")
	}
}
