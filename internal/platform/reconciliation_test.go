package platform

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/edge"
)

func TestBuildSnapshotResolvesClusterIPRoutes(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{clusterIP: "10.43.0.5"}
	cfg := config.Default()
	cfg.TLS.Terminate = true
	rec := NewReconciler(db, cluster, deployment.NewEngine(cluster, db, nil, testLogger()), resolvedFunc(), &cfg, testLogger())
	snap, err := rec.BuildSnapshot(testResolved())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Routes) != 2 {
		t.Fatalf("routes = %+v, want app + registry routes", snap.Routes)
	}
	byHost := map[string]edge.Route{}
	for _, r := range snap.Routes {
		byHost[r.Host] = r
	}
	appRoute, ok := byHost["api.hello.com"]
	if !ok || appRoute.Backend != "10.43.0.5:8080" {
		t.Fatalf("app route = %+v", appRoute)
	}
	if !appRoute.TLS {
		t.Fatal("expected TLS route when TLS termination is enabled")
	}
	regRoute, ok := byHost[cfg.Registry.Domain]
	if !ok || regRoute.Backend != "10.43.0.5:5000" || regRoute.Service != "zot" {
		t.Fatalf("registry route = %+v (want zot ClusterIP:5000)", regRoute)
	}
	routes, err := db.Routes(context.Background())
	if err != nil || len(routes) != 2 {
		t.Fatalf("db routes = %+v err=%v", routes, err)
	}
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestReconcileWorkloadsAppliesAndPrunes(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{
		clusterIP: "10.43.0.5",
		workloads: []deployment.WorkloadRef{
			{Namespace: "my-platform", Name: "api"},
			{Namespace: "my-platform", Name: "stale"},
			{Namespace: "menina-registry", Name: "zot"},
		},
	}
	cfg := config.Default()
	engine := deployment.NewEngine(cluster, db, stubResolver{digest: "sha256:x"}, testLogger())
	rec := NewReconciler(db, cluster, engine, resolvedFunc(), &cfg, testLogger())

	rec.ReconcileWorkloads(context.Background(), testResolved())

	if len(cluster.applied) == 0 || cluster.applied[0].Name != "api" {
		t.Fatalf("expected api workload to be applied, got %+v", cluster.applied)
	}
	if cluster.applied[0].Image != "registry.moureau.dev/my-platform/api@sha256:x" {
		t.Fatalf("applied image = %q", cluster.applied[0].Image)
	}
	if !containsStr(cluster.deleted, "my-platform/stale") {
		t.Fatalf("stale workload should be pruned, deleted=%v", cluster.deleted)
	}
	if containsStr(cluster.deleted, "my-platform/api") || containsStr(cluster.deleted, "menina-registry/zot") {
		t.Fatalf("must not prune desired apps or the registry, deleted=%v", cluster.deleted)
	}
}

func TestReconcileWorkloadsRemovesDisabledApp(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{
		clusterIP: "10.43.0.5",
		workloads: []deployment.WorkloadRef{{Namespace: "my-platform", Name: "api"}},
	}
	cfg := config.Default()
	engine := deployment.NewEngine(cluster, db, stubResolver{digest: "sha256:x"}, testLogger())
	rec := NewReconciler(db, cluster, engine, resolvedFunc(), &cfg, testLogger())

	rc := testResolved()
	rc.Projects["my-platform"].Environments["production"].Apps["api"].Enabled = false
	rec.ReconcileWorkloads(context.Background(), rc)

	if len(cluster.applied) != 0 {
		t.Fatalf("disabled app must not be applied: %+v", cluster.applied)
	}
	if !containsStr(cluster.deleted, "my-platform/api") {
		t.Fatalf("disabled app workload should be pruned, deleted=%v", cluster.deleted)
	}
}

func TestReconcileWorkloadsStopsSoftDeleted(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Host.ConfigDir = dir

	// A previously-running app has an observed row.
	if err := db.UpsertObserved(ctx, database.Observed{Project: "my-platform", Environment: "production", App: "api"}); err != nil {
		t.Fatal(err)
	}
	// It is now soft-deleted: its config is in the trash directory of a live project.
	projDir := filepath.Join(dir, "projects", "my-platform")
	trash := filepath.Join(projDir, "apps", ".trash")
	if err := os.MkdirAll(trash, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "project.yml"), []byte("name: my-platform\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trash, "api.yml"), []byte("name: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cluster := &fakeCluster{
		clusterIP: "10.43.0.5",
		workloads: []deployment.WorkloadRef{{Namespace: "my-platform", Name: "api", Project: "my-platform", Environment: "production", App: "api"}},
	}
	engine := deployment.NewEngine(cluster, db, stubResolver{digest: "sha256:x"}, testLogger())
	rec := NewReconciler(db, cluster, engine, resolvedFunc(), &cfg, testLogger())

	rc := testResolved()
	delete(rc.Projects["my-platform"].Environments["production"].Apps, "api")
	rec.ReconcileWorkloads(ctx, rc)

	if cluster.scaled["my-platform/api"] != 0 {
		t.Fatalf("soft-deleted workload should be scaled to zero, scaled=%v", cluster.scaled)
	}
	if containsStr(cluster.deleted, "my-platform/api") {
		t.Fatalf("soft-deleted workload must not be deleted, deleted=%v", cluster.deleted)
	}
}

func TestBuildSnapshotDropsRemovedExposure(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{clusterIP: "10.43.0.5"}
	cfg := config.Default()
	rec := NewReconciler(db, cluster, deployment.NewEngine(cluster, db, nil, testLogger()), resolvedFunc(), &cfg, testLogger())
	rc := testResolved()
	rc.Projects["my-platform"].Environments["production"].Apps["api"].Exposure = config.ExposureSpec{}
	snap, err := rec.BuildSnapshot(rc)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Routes {
		if r.Host == "api.hello.com" {
			t.Fatalf("route for a de-exposed app must be removed: %+v", snap.Routes)
		}
	}
}

func TestReconcileWorkloadsAppliesChangedPort(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{clusterIP: "10.43.0.5"}
	cfg := config.Default()
	engine := deployment.NewEngine(cluster, db, stubResolver{digest: "sha256:x"}, testLogger())
	rec := NewReconciler(db, cluster, engine, resolvedFunc(), &cfg, testLogger())
	rc := testResolved()
	rc.Projects["my-platform"].Environments["production"].Apps["api"].Workload.Port = 9090
	rec.ReconcileWorkloads(context.Background(), rc)
	if len(cluster.applied) == 0 || cluster.applied[0].Port != 9090 {
		t.Fatalf("changed port must be applied, got %+v", cluster.applied)
	}
}

func TestReconcileAllRepublishesOnClusterIPDrift(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{clusterIP: "10.0.0.1"}
	cfg := config.Default()
	cfg.TLS.Terminate = true
	rec := NewReconciler(db, cluster, deployment.NewEngine(cluster, db, stubResolver{digest: "sha256:x"}, testLogger()), resolvedFunc(), &cfg, testLogger())
	var published []*edge.Snapshot
	rec.OnPublish(func(s *edge.Snapshot) { published = append(published, s) })

	rec.ReconcileAll(context.Background())
	if len(published) != 1 {
		t.Fatalf("expected one publish, got %d", len(published))
	}
	rec.ReconcileAll(context.Background())
	if len(published) != 1 {
		t.Fatalf("unchanged routes must not republish, got %d", len(published))
	}
	cluster.clusterIP = "10.0.0.2"
	rec.ReconcileAll(context.Background())
	if len(published) != 2 {
		t.Fatalf("ClusterIP drift must republish routes, got %d", len(published))
	}
	if backend := published[1].Routes[0].Backend; backend != "10.0.0.2:8080" && backend != "10.0.0.2:5000" {
		t.Fatalf("republished backend = %q, want the new ClusterIP", backend)
	}
}

func TestBuildSnapshotSkipsDegradedService(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{clusterIP: ""}
	cfg := config.Default()
	rec := NewReconciler(db, cluster, deployment.NewEngine(cluster, db, nil, testLogger()), resolvedFunc(), &cfg, testLogger())
	snap, err := rec.BuildSnapshot(testResolved())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Routes) != 0 {
		t.Fatalf("expected no routes for a service without ClusterIP, got %+v", snap.Routes)
	}
}

func TestReconcileTagsEnqueuesOnDrift(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{status: &deployment.WorkloadStatus{Replicas: 2, ReadyReplicas: 2, Image: "registry.moureau.dev/my-platform/api@sha256:old"}}
	engine := deployment.NewEngine(cluster, db, stubResolver{digest: "sha256:new"}, testLogger())
	cfg := config.Default()
	rec := NewReconciler(db, cluster, engine, resolvedFunc(), &cfg, testLogger())
	rec.ReconcileTags(context.Background())
	pending, _ := db.PendingJobs(context.Background(), 10)
	if len(pending) != 1 || pending[0].Kind != "reconcile" {
		t.Fatalf("expected reconciliation job, got %+v", pending)
	}
	observed, _ := db.Observed(context.Background())
	if len(observed) != 1 {
		t.Fatalf("expected observed status, got %+v", observed)
	}
}

func TestReconcileTagsNoopWhenConverged(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{status: &deployment.WorkloadStatus{Replicas: 2, ReadyReplicas: 2, Image: "registry.moureau.dev/my-platform/api@sha256:same"}}
	engine := deployment.NewEngine(cluster, db, stubResolver{digest: "sha256:same"}, testLogger())
	cfg := config.Default()
	rec := NewReconciler(db, cluster, engine, resolvedFunc(), &cfg, testLogger())
	rec.ReconcileTags(context.Background())
	pending, _ := db.PendingJobs(context.Background(), 10)
	if len(pending) != 0 {
		t.Fatalf("converged workload should not enqueue a job: %+v", pending)
	}
}
