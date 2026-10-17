package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
)

const sample = `version: 1
project: demo
environment: production

vars:
  DOMAIN: example.com
  ENV: production

defaults:
  context: .
  tag: ${ENV}

storages:
  data:
    size: 1Gi
    path: /data
    access: single-writer

apps:
  api:
    dockerfile: apps/api/Dockerfile
    port: 8080
    host: api.${DOMAIN}
    env: { REDIS_URL: "redis://redis:6379" }
    secrets: [DATABASE_URL]
    storage: [data]
    replicas: 1

  redis:
    image: redis:7-alpine
    port: 6379

workers:
  jobs:
    from: api
    command: ["/app/worker"]
    replicas: 2
`

func TestLoadInterpolatesVarsAndRejectsUnknownFields(t *testing.T) {
	f, err := Load([]byte(sample), ".")
	if err != nil {
		t.Fatal(err)
	}
	if f.Environment != "production" || f.Vars["DOMAIN"] != "example.com" {
		t.Fatalf("unexpected manifest: %+v", f)
	}
	if f.Apps["api"].Host != "api.example.com" {
		t.Fatalf("host not interpolated: %q", f.Apps["api"].Host)
	}

	bad := strings.Replace(sample, "port: 8080", "prt: 8080", 1)
	if _, err := Load([]byte(bad), "."); err == nil || !strings.Contains(err.Error(), "prt") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}

	undef := strings.Replace(sample, "${ENV}", "${MISSING}", 1)
	if _, err := Load([]byte(undef), "."); err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("expected undefined-variable error, got %v", err)
	}
}

func TestRenderMapsAppsWorkersAndStorage(t *testing.T) {
	f, err := Load([]byte(sample), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Render(RenderOptions{ConfigDir: "/etc/menina", Registry: "registry.test"})
	if err != nil {
		t.Fatal(err)
	}

	api := r.Apps["api"]
	if api == nil {
		t.Fatal("api missing")
	}
	if api.Image.Repository != "registry.test/demo/api" || api.Image.Tag != "production" {
		t.Fatalf("api image = %+v", api.Image)
	}
	if api.Workload.Type != config.WorkloadService || api.Workload.Port != 8080 {
		t.Fatalf("api workload = %+v", api.Workload)
	}
	if api.Storage == nil || !api.Storage.Enabled || api.Storage.MountPath != "/data" {
		t.Fatalf("api storage = %+v", api.Storage)
	}
	if got := r.Overrides["api"]; got == nil || got.Exposure == nil || len(got.Exposure.Hosts) != 1 || got.Exposure.Hosts[0] != "api.example.com" {
		t.Fatalf("api override = %+v", got)
	}
	if got := api.Secrets["DATABASE_URL"]; got != "file:/etc/menina/secrets/demo/production/DATABASE_URL" {
		t.Fatalf("secret ref = %q", got)
	}

	jobs := r.Apps["jobs"]
	if jobs == nil || jobs.Workload.Type != config.WorkloadWorker {
		t.Fatalf("jobs = %+v", jobs)
	}
	if jobs.Storage != nil {
		t.Fatalf("worker must not inherit storage: %+v", jobs.Storage)
	}
	if jobs.Image.Repository != api.Image.Repository {
		t.Fatalf("jobs image = %+v, want same repository as api", jobs.Image)
	}
	if jobs.Env["REDIS_URL"] != "redis://redis:6379" {
		t.Fatalf("worker env = %+v", jobs.Env)
	}

	redis := r.Apps["redis"]
	if redis.Image.Repository != "redis" || redis.Image.Tag != "7-alpine" {
		t.Fatalf("redis image = %+v", redis.Image)
	}
	if redis.Storage != nil {
		t.Fatalf("redis storage = %+v", redis.Storage)
	}

	if len(r.Targets) != 1 {
		t.Fatalf("targets = %+v, want one deduped api/jobs image", r.Targets)
	}
	if !strings.HasSuffix(r.Targets[0].Dockerfile, "apps/api/Dockerfile") {
		t.Fatalf("dockerfile = %q", r.Targets[0].Dockerfile)
	}
}

func TestRenderRejectsUnsafeStorage(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string) string
		want   string
	}{
		{
			name: "autoscaling with single-writer",
			mutate: func(s string) string {
				return strings.Replace(s, "replicas: 1", "replicas: { min: 1, max: 3 }", 1)
			},
			want: "single-writer",
		},
		{
			name: "multi-writer",
			mutate: func(s string) string {
				return strings.Replace(s, "access: single-writer", "access: multi-writer", 1)
			},
			want: "ReadWriteMany",
		},
		{
			name: "shared storage",
			mutate: func(s string) string {
				return strings.Replace(s, "  redis:\n", "  redis:\n    storage: [data]\n", 1)
			},
			want: "cannot be shared",
		},
		{
			name: "multiple storages",
			mutate: func(s string) string {
				return strings.Replace(s, "storage: [data]", "storage: [data, data]", 1)
			},
			want: "one storage",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Load([]byte(tc.mutate(sample)), ".")
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Render(RenderOptions{ConfigDir: "/etc/menina", Registry: "registry.test"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestWriteCreatesConfigAndPreservesProjectMetadata(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Host.ConfigDir = dir
	if err := config.SavePlatform(dir, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config.ProjectDir(dir, "demo"), "apps"), 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.ProjectDir(dir, "demo"), "project.yml"), []byte("name: demo\nnamespace: custom\ndescription: keep me\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	f, err := Load([]byte(sample), ".")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Render(RenderOptions{ConfigDir: dir, Registry: "registry.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}

	project, err := config.LoadProject(config.ProjectsDir(dir), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if project.Description != "keep me" || project.Namespace != "custom" {
		t.Fatalf("project metadata was overwritten: %+v", project)
	}
	if len(project.Apps) != 3 {
		t.Fatalf("apps = %d, want 3", len(project.Apps))
	}
	rc, err := config.NewResolver(&cfg).Resolve([]*config.Project{project}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.ValidateResolved(rc); err != nil {
		t.Fatal(err)
	}
}

func TestWithEnvironmentRewritesPlaceholders(t *testing.T) {
	data, err := WithEnvironment([]byte(sample), "staging")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "environment: staging") || !strings.Contains(string(data), "${ENV}") {
		t.Fatalf("unexpected rewritten manifest:\n%s", data)
	}
	f, err := Load(data, ".")
	if err != nil {
		t.Fatal(err)
	}
	if f.Environment != "staging" {
		t.Fatalf("environment = %q", f.Environment)
	}
}
