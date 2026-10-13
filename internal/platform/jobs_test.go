package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/registry"
)

func resolvedFunc() func() *config.ResolvedConfig {
	return func() *config.ResolvedConfig { return testResolved() }
}

func TestRepositoryMatches(t *testing.T) {
	cases := []struct {
		domain, configured, event string
		want                      bool
	}{
		{"registry.moureau.dev", "registry.moureau.dev/my-platform/api", "my-platform/api", true},
		{"registry.moureau.dev", "registry.moureau.dev/my-platform/api", "my-platform/api:v2", false},
		{"", "my-platform/api", "my-platform/api", true},
		{"registry.moureau.dev", "registry.moureau.dev/my-platform/api", "other/api", false},
	}
	for _, c := range cases {
		if got := repositoryMatches(c.domain, c.configured, c.event); got != c.want {
			t.Errorf("repositoryMatches(%q,%q,%q) = %v, want %v", c.domain, c.configured, c.event, got, c.want)
		}
	}
}

func TestMatchJobs(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{}
	jobs := NewJobs(db, deployment.NewEngine(cluster, db, nil, testLogger()), cluster, resolvedFunc(), "registry.moureau.dev", testLogger())
	got := jobs.MatchJobs(registry.PushEvent{Repository: "my-platform/api", Tag: "production", DeliveryID: "d1"})
	if len(got) != 1 {
		t.Fatalf("matched %d jobs, want 1", len(got))
	}
	if got[0].App != "api" || got[0].Environment != "production" {
		t.Fatalf("job = %+v", got[0])
	}
	if n := jobs.MatchJobs(registry.PushEvent{Repository: "my-platform/api", Tag: "development", DeliveryID: "d2"}); len(n) != 0 {
		t.Fatalf("unexpected match for unknown tag: %+v", n)
	}
}

func TestIngestPersistsDurablyAndDeduplicates(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{}
	jobs := NewJobs(db, deployment.NewEngine(cluster, db, nil, testLogger()), cluster, resolvedFunc(), "registry.moureau.dev", testLogger())
	event := registry.PushEvent{Repository: "my-platform/api", Tag: "production", Digest: "sha256:1", DeliveryID: "delivery-1"}
	created, n, err := jobs.Ingest(context.Background(), event)
	if err != nil || !created || n != 1 {
		t.Fatalf("ingest = %v/%d err=%v", created, n, err)
	}
	created, _, err = jobs.Ingest(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate delivery should be ignored")
	}
	pending, _ := db.PendingJobs(context.Background(), 10)
	if len(pending) != 1 {
		t.Fatalf("pending jobs = %d, want 1", len(pending))
	}
}

func TestProcessJobDeploysAndRecords(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{status: &deployment.WorkloadStatus{Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, Image: "old"}}
	engine := deployment.NewEngine(cluster, db, stubResolver{digest: "sha256:new"}, testLogger())
	jobs := NewJobs(db, engine, cluster, resolvedFunc(), "registry.moureau.dev", testLogger())
	_, _, err := jobs.Ingest(context.Background(), registry.PushEvent{Repository: "my-platform/api", Tag: "production", DeliveryID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := db.PendingJobs(context.Background(), 10)
	if err := jobs.ProcessJob(context.Background(), pending[0]); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(cluster.applied) == 0 {
		t.Fatal("expected workload to be applied")
	}
	if cluster.applied[0].Image != "registry.moureau.dev/my-platform/api@sha256:new" {
		t.Fatalf("applied image = %q", cluster.applied[0].Image)
	}
	releases, _ := db.Releases(context.Background(), "my-platform", "production", "api")
	if len(releases) != 1 || releases[0].Digest != "sha256:new" {
		t.Fatalf("releases = %+v", releases)
	}
	observed, _ := db.Observed(context.Background())
	if len(observed) != 1 || observed[0].Status != "ready" {
		t.Fatalf("observed = %+v", observed)
	}
}

func TestProcessJobIsNoOpWhenAlreadyDesired(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{status: &deployment.WorkloadStatus{Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, Image: "registry.moureau.dev/my-platform/api@sha256:same"}}
	engine := deployment.NewEngine(cluster, db, stubResolver{digest: "sha256:same"}, testLogger())
	jobs := NewJobs(db, engine, cluster, resolvedFunc(), "registry.moureau.dev", testLogger())
	_, _, _ = jobs.Ingest(context.Background(), registry.PushEvent{Repository: "my-platform/api", Tag: "production", DeliveryID: "d"})
	pending, _ := db.PendingJobs(context.Background(), 10)
	if err := jobs.ProcessJob(context.Background(), pending[0]); err != nil {
		t.Fatal(err)
	}
	if len(cluster.applied) != 0 {
		t.Fatalf("expected no-op, but applied %d workloads", len(cluster.applied))
	}
}

func TestProcessJobFailureRequeues(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{}
	engine := deployment.NewEngine(cluster, db, errResolver{}, testLogger())
	jobs := NewJobs(db, engine, cluster, resolvedFunc(), "registry.moureau.dev", testLogger())
	_, _, _ = jobs.Ingest(context.Background(), registry.PushEvent{Repository: "my-platform/api", Tag: "production", DeliveryID: "d"})
	pending, _ := db.PendingJobs(context.Background(), 10)
	if err := jobs.ProcessJob(context.Background(), pending[0]); err == nil {
		t.Fatal("expected process error")
	}
	pending, _ = db.PendingJobs(context.Background(), 10)
	if len(pending) != 1 || pending[0].Attempts != 1 {
		t.Fatalf("expected requeued job with attempts=1, got %+v", pending)
	}
}

type errResolver struct{}

func (errResolver) ResolveTag(context.Context, string, string) (string, error) {
	return "", errors.New("registry down")
}
