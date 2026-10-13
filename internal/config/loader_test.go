package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testLoggerConfig() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedProject(t *testing.T, configDir string) {
	writeFile(t, filepath.Join(configDir, "projects", "my-platform", "project.yml"), "name: my-platform\nnamespace: my-platform\n")
	writeFile(t, filepath.Join(configDir, "projects", "my-platform", "apps", "api.yml"), `name: api
image:
  repository: registry.moureau.dev/my-platform/api
workload:
  type: service
  port: 8080
exposure:
  enabled: false
`)
	writeFile(t, filepath.Join(configDir, "projects", "my-platform", "environments", "production.yml"), `name: production
apps:
  api:
    enabled: true
    exposure:
      enabled: true
      hosts:
        - api.hello.com
`)
}

func TestLoadAllProjectsAndResolve(t *testing.T) {
	dir := t.TempDir()
	seedProject(t, dir)
	projects, err := LoadAllProjects(ProjectsDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "my-platform" {
		t.Fatalf("projects = %+v", projects)
	}
	rc, err := NewResolver(&Config{Defaults: Default().Defaults}).Resolve(projects, 0)
	if err != nil {
		t.Fatal(err)
	}
	app, err := rc.FindApp("my-platform", "production", "api")
	if err != nil {
		t.Fatal(err)
	}
	if !app.Exposure.Enabled || app.Exposure.Hosts[0] != "api.hello.com" {
		t.Fatalf("app = %+v", app)
	}
}

func TestWatcherPromotesValidAndRetainsLastValid(t *testing.T) {
	dir := t.TempDir()
	seedProject(t, dir)
	cfg := Default()
	w := NewWatcher(dir, &cfg, NewResolver(&cfg), 10*time.Millisecond, testLoggerConfig())
	snap, err := w.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if snap.Version != 1 || snap.Resolved == nil {
		t.Fatalf("snapshot = %+v", snap)
	}
	writeFile(t, filepath.Join(dir, "projects", "my-platform", "environments", "production.yml"), `name: production
apps:
  ghost:
    enabled: true
`)
	if _, err := w.Load(); err == nil {
		t.Fatal("expected invalid snapshot to be rejected")
	}
	if w.Snapshot().Version != 1 {
		t.Fatalf("last valid snapshot should be retained, version=%d", w.Snapshot().Version)
	}
}
