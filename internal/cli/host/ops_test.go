package host

import (
	"os"
	"path/filepath"
	"testing"
)

func mustExec(t *testing.T, args ...string) {
	t.Helper()
	if err := execute(args...); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
}

func TestProjectGet(t *testing.T) {
	dir := t.TempDir()
	mustExec(t, "--config-dir", dir, "project", "create", "p", "--environments", "production,staging")
	if err := execute("--config-dir", dir, "project", "get", "p"); err != nil {
		t.Fatalf("project get: %v", err)
	}
	if err := execute("--config-dir", dir, "project", "get", "nope"); err == nil {
		t.Fatal("expected an error for an unknown project")
	}
}

func TestConfigValidate(t *testing.T) {
	dir := t.TempDir()
	mustExec(t, "--config-dir", dir, "setup", "--non-interactive")
	if err := execute("--config-dir", dir, "config", "validate"); err != nil {
		t.Fatalf("valid config should pass: %v", err)
	}

	p := filepath.Join(dir, "projects", "p")
	for _, d := range []string{"apps", "environments"} {
		if err := os.MkdirAll(filepath.Join(p, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(p, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("project.yml", "name: p\n")
	write("apps/api.yml", "name: api\nimage:\n  repository: r\nworkload:\n  type: service\n  port: 8080\nexposure:\n  enabled: true\n  hosts:\n    - not_a_host\n")
	write("environments/production.yml", "name: production\napps:\n  api:\n    enabled: true\n")
	if err := execute("--config-dir", dir, "config", "validate"); err == nil {
		t.Fatal("an invalid exposure host must fail validation")
	}
}

func TestAppResetVolumeRequiresStorage(t *testing.T) {
	dir := t.TempDir()
	mustExec(t, "--config-dir", dir, "setup", "--non-interactive")
	mustExec(t, "--config-dir", dir, "project", "create", "p", "--environments", "production")
	mustExec(t, "--config-dir", dir, "app", "create", "api", "--project", "p", "--repository", "r", "--yes")
	if err := execute("--config-dir", dir, "app", "reset-volume", "api", "--project", "p", "--environment", "production", "--yes"); err == nil {
		t.Fatal("expected an error for an app without persistent storage")
	}
}
