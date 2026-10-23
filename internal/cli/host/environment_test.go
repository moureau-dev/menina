package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moureau-dev/menina/internal/core/config"
)

func writeOverride(t *testing.T, dir, project, env string) string {
	t.Helper()
	path := config.EnvironmentOverridesDir(dir, project, env)
	if err := os.MkdirAll(path, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "api.yml"), []byte("enabled: true\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEnvironmentHardDeleteRemovesOverrideDirectory(t *testing.T) {
	dir := t.TempDir()
	mustExec(t, "--config-dir", dir, "project", "create", "p", "--environments", "production")
	overrides := writeOverride(t, dir, "p", "production")
	mustExec(t, "--config-dir", dir, "environment", "delete", "production", "--project", "p", "--hard", "--yes")
	if _, err := os.Stat(overrides); !os.IsNotExist(err) {
		t.Fatalf("override directory still exists: %v", err)
	}
	project, err := config.LoadProject(config.ProjectsDir(dir), "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := project.Envs["production"]; ok {
		t.Fatal("hard-deleted environment is still listed")
	}
}

func TestEnvironmentSoftDeleteHidesOverrideDirectory(t *testing.T) {
	dir := t.TempDir()
	mustExec(t, "--config-dir", dir, "project", "create", "p", "--environments", "production")
	writeOverride(t, dir, "p", "production")
	mustExec(t, "--config-dir", dir, "environment", "delete", "production", "--project", "p", "--yes")
	project, err := config.LoadProject(config.ProjectsDir(dir), "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := project.Envs["production"]; ok {
		t.Fatal("soft-deleted environment is still listed")
	}
	mustExec(t, "--config-dir", dir, "environment", "restore", "production", "--project", "p")
	project, err = config.LoadProject(config.ProjectsDir(dir), "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := project.Envs["production"]; !ok {
		t.Fatal("restored environment is missing")
	}
}

func TestNonInteractiveBareCommandFails(t *testing.T) {
	if err := execute("--non-interactive"); err == nil {
		t.Fatal("bare command with --non-interactive should fail")
	}
}
