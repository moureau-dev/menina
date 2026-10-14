package platform

import (
	"context"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/proxy"
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
	byHost := map[string]proxy.Route{}
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

func TestReconcileAllRepublishesOnClusterIPDrift(t *testing.T) {
	db := testDB(t)
	cluster := &fakeCluster{clusterIP: "10.0.0.1"}
	cfg := config.Default()
	cfg.TLS.Terminate = true
	rec := NewReconciler(db, cluster, deployment.NewEngine(cluster, db, stubResolver{digest: "sha256:x"}, testLogger()), resolvedFunc(), &cfg, testLogger())
	var published []*proxy.Snapshot
	rec.OnPublish(func(s *proxy.Snapshot) { published = append(published, s) })

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
