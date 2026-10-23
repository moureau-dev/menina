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

func TestWatcherInvalidYAMLPreservesLastValid(t *testing.T) {
	dir := t.TempDir()
	seedProject(t, dir)
	cfg := Default()
	w := NewWatcher(dir, &cfg, NewResolver(&cfg), time.Millisecond, testLoggerConfig())
	if _, err := w.Load(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "projects", "my-platform", "apps", "api.yml"), "name: api\nimage: [oops\n")
	if _, err := w.Load(); err == nil {
		t.Fatal("expected malformed YAML to be rejected")
	}
	if w.Snapshot().Version != 1 {
		t.Fatalf("last valid snapshot must be retained, version=%d", w.Snapshot().Version)
	}
}

func TestWatcherDeletedAppReferencedByEnvIsRejected(t *testing.T) {
	dir := t.TempDir()
	seedProject(t, dir)
	cfg := Default()
	w := NewWatcher(dir, &cfg, NewResolver(&cfg), time.Millisecond, testLoggerConfig())
	if _, err := w.Load(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "projects", "my-platform", "apps", "api.yml")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Load(); err == nil {
		t.Fatal("deleting an app still referenced by an environment must be rejected (no partial deploy)")
	}
	if w.Snapshot().Version != 1 {
		t.Fatalf("last valid snapshot must be retained, version=%d", w.Snapshot().Version)
	}
}

func TestWatcherDeletedEnvironmentIsApplied(t *testing.T) {
	dir := t.TempDir()
	seedProject(t, dir)
	cfg := Default()
	w := NewWatcher(dir, &cfg, NewResolver(&cfg), time.Millisecond, testLoggerConfig())
	if _, err := w.Load(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "projects", "my-platform", "environments", "production.yml")); err != nil {
		t.Fatal(err)
	}
	snap, err := w.Load()
	if err != nil {
		t.Fatalf("removing an environment should be valid: %v", err)
	}
	if len(snap.Resolved.Projects["my-platform"].Environments) != 0 {
		t.Fatalf("environment should be gone from the resolved snapshot: %+v", snap.Resolved.Projects["my-platform"].Environments)
	}
	if snap.Version != 2 {
		t.Fatalf("version = %d, want 2", snap.Version)
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
