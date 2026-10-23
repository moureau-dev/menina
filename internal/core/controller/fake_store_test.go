package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/moureau-dev/menina/internal/core/cluster"
	"github.com/moureau-dev/menina/internal/core/config"
	"github.com/moureau-dev/menina/internal/core/state/fake"
)

func TestReconcileWorkloadsUsesInjectedStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Host.ConfigDir = dir

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

	store := fake.New()
	cluster := &fakeCluster{
		clusterIP: "10.43.0.5",
		workloads: []k8s.WorkloadRef{{Namespace: "my-platform", Name: "api", Project: "my-platform", Environment: "production", App: "api"}},
	}
	engine := k8s.NewEngine(cluster, store, stubResolver{digest: "sha256:x"}, testLogger())
	rec := NewReconciler(store, cluster, engine, resolvedFunc(), &cfg, testLogger())

	rc := testResolved()
	delete(rc.Projects["my-platform"].Environments["production"].Apps, "api")
	rec.ReconcileWorkloads(ctx, rc)

	if len(store.SyncedSoft) != 1 || store.SyncedSoft[0].Project != "my-platform" || store.SyncedSoft[0].App != "api" {
		t.Fatalf("soft deletes = %+v", store.SyncedSoft)
	}
	if cluster.scaled["my-platform/api"] != 0 {
		t.Fatalf("soft-deleted workload should be scaled to zero, scaled=%v", cluster.scaled)
	}
}
