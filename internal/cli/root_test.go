package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
)

func execute(args ...string) error {
	root := NewRootCommand()
	root.SetArgs(args)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	return root.Execute()
}

func TestRootRegistersAllCommands(t *testing.T) {
	root := NewRootCommand()
	want := []string{"setup", "deploy", "status", "project", "environment", "app", "routes", "registry", "doctor", "serve", "proxy", "upgrade", "version", "install"}
	for _, name := range want {
		if _, _, err := root.Find([]string{name}); err != nil {
			t.Errorf("command %q not registered: %v", name, err)
		}
	}
}

func TestSetupNonInteractiveWritesValidConfig(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "setup", "--non-interactive"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yml")); err != nil {
		t.Fatalf("config.yml not written: %v", err)
	}
	cfg, err := config.LoadPlatform(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.ValidatePlatform(cfg); err != nil {
		t.Fatalf("written config invalid: %v", err)
	}
	if cfg.Registry.Domain == "" {
		t.Fatal("expected registry domain default")
	}
}

func TestProjectAndAppCreate(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "my-platform", "--namespace", "my-platform"); err != nil {
		t.Fatalf("project create: %v", err)
	}
	if err := execute("--config-dir", dir, "environment", "create", "production", "--project", "my-platform"); err != nil {
		t.Fatalf("environment create: %v", err)
	}
	if err := execute("--config-dir", dir, "app", "create", "api", "--project", "my-platform", "--repository", "registry.moureau.dev/my-platform/api"); err != nil {
		t.Fatalf("app create: %v", err)
	}
	p, err := config.LoadProject(config.ProjectsDir(dir), "my-platform")
	if err != nil {
		t.Fatal(err)
	}
	if p.Apps["api"] == nil {
		t.Fatal("api app not created")
	}
	if p.Envs["production"] == nil {
		t.Fatal("production environment not created")
	}
}

func TestDeleteCommandsRemoveConfigFiles(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--namespace", "p"); err != nil {
		t.Fatalf("project create: %v", err)
	}
	if err := execute("--config-dir", dir, "environment", "create", "production", "--project", "p"); err != nil {
		t.Fatalf("environment create: %v", err)
	}
	if err := execute("--config-dir", dir, "app", "create", "api", "--project", "p", "--repository", "registry.example.com/p/api"); err != nil {
		t.Fatalf("app create: %v", err)
	}
	if err := execute("--config-dir", dir, "app", "delete", "api", "--project", "p", "--yes"); err != nil {
		t.Fatalf("app delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "p", "apps", "api.yml")); !os.IsNotExist(err) {
		t.Fatal("app file should be removed")
	}
	if err := execute("--config-dir", dir, "environment", "delete", "production", "--project", "p", "--yes"); err != nil {
		t.Fatalf("environment delete: %v", err)
	}
	if err := execute("--config-dir", dir, "project", "delete", "p", "--yes"); err != nil {
		t.Fatalf("project delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "p")); !os.IsNotExist(err) {
		t.Fatal("project directory should be removed")
	}
}

func TestDeleteWithoutYesIsRefusedNonInteractively(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "project", "delete", "p"); err == nil {
		t.Fatal("non-interactive delete without --yes must be refused")
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "p")); err != nil {
		t.Fatal("project must still exist after a refused delete")
	}
}

func TestBackupCommandCreatesDatabaseCopy(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	cfg := "host:\n  configDir: " + dir + "\n  dataDir: " + dataDir + "\nplatform:\n  dbPath: " + filepath.Join(dataDir, "menina.db") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(cfg), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "backup"); err != nil {
		t.Fatalf("backup: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "backups"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a backup file: entries=%v err=%v", entries, err)
	}
}

func TestAppCreateAndScaleReplicas(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--namespace", "p"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "app", "create", "api", "--project", "p", "--repository", "r/api", "--yes"); err != nil {
		t.Fatal(err)
	}
	apps, _ := config.LoadApps(config.ProjectsDir(dir), "p")
	if apps["api"].Replicas != nil {
		t.Fatalf("creating without a replica flag must inherit the default, got %+v", apps["api"].Replicas)
	}

	if err := execute("--config-dir", dir, "app", "scale", "api", "--project", "p", "--replicas", "5"); err != nil {
		t.Fatal(err)
	}
	apps, _ = config.LoadApps(config.ProjectsDir(dir), "p")
	if r := apps["api"].Replicas; r == nil || r.Min != 5 || r.Max != 5 || r.Autoscaling() {
		t.Fatalf("fixed scale = %+v", r)
	}

	if err := execute("--config-dir", dir, "app", "scale", "api", "--project", "p", "--min", "2", "--max", "8"); err != nil {
		t.Fatal(err)
	}
	apps, _ = config.LoadApps(config.ProjectsDir(dir), "p")
	if r := apps["api"].Replicas; r == nil || r.Min != 2 || r.Max != 8 || !r.Autoscaling() {
		t.Fatalf("range scale = %+v", r)
	}

	if err := execute("--config-dir", dir, "app", "scale", "api", "--project", "p", "--reset"); err != nil {
		t.Fatal(err)
	}
	apps, _ = config.LoadApps(config.ProjectsDir(dir), "p")
	if apps["api"].Replicas != nil {
		t.Fatalf("reset must remove the replica override, got %+v", apps["api"].Replicas)
	}
}

func TestVersionFlags(t *testing.T) {
	for _, flag := range []string{"--version", "-v"} {
		out, err := executeCapture(flag)
		if err != nil {
			t.Fatalf("%s: %v", flag, err)
		}
		if !strings.Contains(out, "menina") {
			t.Fatalf("%s output = %q", flag, out)
		}
	}
}

func TestCreateCommandCreatesProject(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "create", "demo"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "demo", "project.yml")); err != nil {
		t.Fatalf("project.yml not written: %v", err)
	}
}

func TestBareCommandsAreNonInteractiveWithoutTTY(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"app"}, {"project"}, {"environment"}} {
		full := append([]string{"--config-dir", dir}, args...)
		if err := execute(full...); err != nil {
			t.Fatalf("%v should fall back to help without a TTY: %v", args, err)
		}
	}
}

func TestCompletionGenerate(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		if err := execute("completion", shell); err != nil {
			t.Errorf("completion %s: %v", shell, err)
		}
	}
}
