package deployment

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
)

func deployLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func deployDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestBuildWorkloadUsesDigestAndResources(t *testing.T) {
	engine := NewEngine(&stubCluster{}, nil, nil, deployLogger())
	app := &config.ResolvedApp{
		Project: "p", Environment: "prod", Name: "api", Namespace: "ns",
		Repository: "registry/api", Tag: "prod", Replicas: config.ReplicasSpec{Min: 3, Max: 3},
		Workload:  config.WorkloadSpec{Type: config.WorkloadService, Port: 8080},
		Resources: config.ResourceSpec{CPU: "500m", Memory: "512Mi"},
		Health:    &config.HealthSpec{Readiness: "/healthz"},
		Storage:   &config.StorageSpec{Enabled: true, Size: "1Gi", MountPath: "/data"},
	}
	w := engine.buildWorkload(app, "sha256:abc")
	if w.Image != "registry/api@sha256:abc" {
		t.Fatalf("image = %q", w.Image)
	}
	if w.Replicas != 3 || w.Port != 8080 || w.CPU != "500m" || w.HealthPath != "/healthz" {
		t.Fatalf("workload = %+v", w)
	}
	if w.StorageSize != "1Gi" || w.MountPath != "/data" {
		t.Fatalf("storage = %+v", w)
	}
	w2 := engine.buildWorkload(app, "")
	if w2.Image != "registry/api:prod" {
		t.Fatalf("tag image = %q", w2.Image)
	}
}

func TestDeployResolvedAppliesWorkload(t *testing.T) {
	cluster := &stubCluster{}
	db := deployDB(t)
	engine := NewEngine(cluster, db, nil, deployLogger())
	app := &config.ResolvedApp{
		Project: "p", Environment: "prod", Name: "api", Namespace: "ns", Enabled: true,
		Repository: "registry/api", Tag: "prod", Replicas: config.ReplicasSpec{Min: 1, Max: 1},
		Workload: config.WorkloadSpec{Type: config.WorkloadService, Port: 80},
	}
	res, err := engine.DeployResolved(context.Background(), app, "sha256:dead")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if !res.RolledOut || res.Image != "registry/api@sha256:dead" {
		t.Fatalf("result = %+v", res)
	}
	if len(cluster.applied) != 1 {
		t.Fatalf("expected service+workload applies, got %d", len(cluster.applied))
	}
	// Release history is recorded by the job worker only after the rollout
	// succeeds, not by the engine itself.
	releases, err := db.Releases(context.Background(), "p", "prod", "api")
	if err != nil || len(releases) != 0 {
		t.Fatalf("engine must not record a release before the rollout succeeds: %+v err=%v", releases, err)
	}
}

func TestDeployDisabledAppRejected(t *testing.T) {
	engine := NewEngine(&stubCluster{}, nil, nil, deployLogger())
	app := &config.ResolvedApp{Project: "p", Environment: "prod", Name: "api", Enabled: false}
	if _, err := engine.DeployResolved(context.Background(), app, ""); err == nil {
		t.Fatal("expected disabled app error")
	}
}

func TestApplyResolvesAppSecretsIntoSecretAndEnv(t *testing.T) {
	cluster := &stubCluster{}
	engine := NewEngine(cluster, nil, nil, deployLogger())
	app := &config.ResolvedApp{
		Project: "p", Environment: "prod", Name: "api", Namespace: "ns", Enabled: true,
		Repository: "registry/api", Tag: "prod", Replicas: config.ReplicasSpec{Min: 1, Max: 1},
		Workload: config.WorkloadSpec{Type: config.WorkloadService, Port: 80},
		Secrets:  map[string]string{"DATABASE_URL": "literal:postgres://x"},
	}
	if _, err := engine.Apply(context.Background(), app, "registry/api:prod"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(cluster.secretApplied) != 1 {
		t.Fatalf("expected one secret apply, got %d", len(cluster.secretApplied))
	}
	got := cluster.secretApplied[0]
	if got.Name != "api-secrets" || string(got.Data["DATABASE_URL"]) != "postgres://x" {
		t.Fatalf("secret = %+v", got)
	}
	if len(cluster.applied) != 1 {
		t.Fatalf("workload not applied: %d", len(cluster.applied))
	}
	w := cluster.applied[0]
	if w.SecretEnvName != "api-secrets" || w.SecretEnv["DATABASE_URL"] != "DATABASE_URL" {
		t.Fatalf("workload secret env = %+v / %q", w.SecretEnv, w.SecretEnvName)
	}
}

func TestApplyFailsOnUnresolvableSecret(t *testing.T) {
	cluster := &stubCluster{}
	engine := NewEngine(cluster, nil, nil, deployLogger())
	app := &config.ResolvedApp{
		Project: "p", Environment: "prod", Name: "api", Namespace: "ns", Enabled: true,
		Repository: "registry/api", Tag: "prod", Replicas: config.ReplicasSpec{Min: 1, Max: 1},
		Workload: config.WorkloadSpec{Type: config.WorkloadService, Port: 80},
		Secrets:  map[string]string{"DATABASE_URL": "file:/does/not/exist"},
	}
	if _, err := engine.Apply(context.Background(), app, "registry/api:prod"); err == nil {
		t.Fatal("expected an error for an unresolvable secret reference")
	}
}

func TestApplyWithoutSecretsSkipsSecret(t *testing.T) {
	cluster := &stubCluster{}
	engine := NewEngine(cluster, nil, nil, deployLogger())
	app := &config.ResolvedApp{
		Project: "p", Environment: "prod", Name: "api", Namespace: "ns", Enabled: true,
		Repository: "registry/api", Tag: "prod", Replicas: config.ReplicasSpec{Min: 1, Max: 1},
		Workload: config.WorkloadSpec{Type: config.WorkloadService, Port: 80},
	}
	if _, err := engine.Apply(context.Background(), app, "registry/api:prod"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(cluster.secretApplied) != 0 {
		t.Fatalf("no secret expected, got %+v", cluster.secretApplied)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{"My_App.1": "my-app-1", "  ": "app", "API": "api"}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
