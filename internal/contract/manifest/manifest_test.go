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

func TestWorkerKeepsExplicitStorageAndDoesNotInherit(t *testing.T) {
	doc := `project: demo
environment: production
storages:
  appdata: { size: 1Gi, path: /app }
  jobdata: { size: 1Gi, path: /job }
apps:
  api:
    dockerfile: Dockerfile
    port: 8080
    storage: [appdata]
workers:
  jobs:
    from: api
    storage: [jobdata]
`
	f, err := Load([]byte(doc), ".")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Render(RenderOptions{ConfigDir: "/etc/menina", Registry: "registry.test"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Apps["api"].Storage; got == nil || got.MountPath != "/app" {
		t.Fatalf("api storage = %+v", got)
	}
	if got := r.Apps["jobs"].Storage; got == nil || got.MountPath != "/job" {
		t.Fatalf("worker storage = %+v, want its explicit volume", got)
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

func TestRenderMergesBuildArgs(t *testing.T) {
	doc := `project: demo
environment: production
defaults:
  buildArgs:
    BASE: default
apps:
  api:
    dockerfile: Dockerfile
    port: 8080
    buildArgs:
      API: "1"
workers:
  jobs:
    from: api
`
	f, err := Load([]byte(doc), ".")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Render(RenderOptions{ConfigDir: "/etc/menina", Registry: "registry.test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(r.Targets))
	}
	got := r.Targets[0].BuildArgs
	if got["BASE"] != "default" || got["API"] != "1" {
		t.Fatalf("build args = %#v", got)
	}
}

func TestRenderRejectsConflictingBuildArgs(t *testing.T) {
	doc := `project: demo
environment: production
apps:
  api:
    dockerfile: Dockerfile
    port: 8080
    buildArgs: { MODE: api }
workers:
  jobs:
    from: api
    buildArgs: { MODE: worker }
`
	f, err := Load([]byte(doc), ".")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Render(RenderOptions{ConfigDir: "/etc/menina", Registry: "registry.test"})
	if err == nil || !strings.Contains(err.Error(), "different buildArgs") {
		t.Fatalf("expected build-arg conflict, got %v", err)
	}
}

func TestRenderNestedServiceAndExposure(t *testing.T) {
	doc := `project: demo
environment: production
apps:
  game:
    dockerfile: Dockerfile
    service: { port: 8080 }
    exposure:
      http:
        hosts: [game.example.com]
        pathPrefix: /play
        redirectHttp: true
        headers: { X-Test: yes }
        compression: true
        h2c: true
        streaming: true
        maxBodyBytes: 1024
        timeouts: { read: 30s, write: 60s, idle: 120s }
      ports: { tcp: [25565], udp: [19132] }
`
	f, err := Load([]byte(doc), ".")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Render(RenderOptions{ConfigDir: "/etc/menina", Registry: "registry.test"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Apps["game"].Workload.Port; got != 8080 {
		t.Fatalf("service port = %d", got)
	}
	exp := r.Overrides["game"].Exposure
	if exp == nil || !exp.Enabled || len(exp.Hosts) != 1 || exp.Hosts[0] != "game.example.com" {
		t.Fatalf("exposure = %+v", exp)
	}
	if exp.PathPrefix != "/play" || !exp.RedirectHTTP || !exp.Compression || !exp.H2C || !exp.Streaming || exp.MaxBodyBytes != 1024 {
		t.Fatalf("http exposure = %+v", exp)
	}
	if exp.Timeouts.Read != "30s" || exp.Headers["X-Test"] != "yes" {
		t.Fatalf("http details = %+v", exp)
	}
	if len(exp.Ports.TCP) != 1 || exp.Ports.TCP[0] != 25565 || len(exp.Ports.UDP) != 1 || exp.Ports.UDP[0] != 19132 {
		t.Fatalf("ports = %+v", exp.Ports)
	}
}

func TestRenderL4OnlyExposure(t *testing.T) {
	doc := `project: demo
environment: production
apps:
  game:
    image: itzg/minecraft-server
    service: { port: 25565 }
    exposure:
      ports: 25565
`
	f, err := Load([]byte(doc), ".")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Render(RenderOptions{ConfigDir: "/etc/menina", Registry: "registry.test"})
	if err != nil {
		t.Fatal(err)
	}
	exp := r.Overrides["game"].Exposure
	if exp == nil || exp.Enabled || len(exp.Ports.TCP) != 1 || exp.Ports.TCP[0] != 25565 {
		t.Fatalf("exposure = %+v", exp)
	}
}

func TestRenderRejectsBadExposure(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "http without hosts",
			doc:  "project: p\nenvironment: production\napps:\n  a:\n    image: nginx\n    service: { port: 80 }\n    exposure:\n      http: { compression: true }\n",
			want: "requires at least one host",
		},
		{
			name: "conflicting ports",
			doc:  "project: p\nenvironment: production\napps:\n  a:\n    image: nginx\n    port: 8080\n    service: { port: 9090 }\n",
			want: "must match",
		},
		{
			name: "worker exposure",
			doc:  "project: p\nenvironment: production\nworkers:\n  w:\n    image: busybox\n    exposure:\n      ports: 9000\n",
			want: "cannot declare exposure",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Load([]byte(tc.doc), ".")
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Render(RenderOptions{ConfigDir: "/etc/menina", Registry: "registry.test"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}
