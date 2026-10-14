package infrastructure

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestK3sWriteRegistriesConfigSetsRootOnlyMode(t *testing.T) {
	runner := NewFakeRunner()
	k := NewK3s(runner, testLogger())
	if err := k.WriteRegistriesConfig(context.Background(), "/etc/rancher/k3s/registries.yaml", "configs: {}\n"); err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, c := range runner.Commands {
		joined += strings.Join(c, " ") + "\n"
	}
	if !strings.Contains(joined, "chmod 0600") || !strings.Contains(joined, "registries.yaml") {
		t.Fatalf("registries.yaml with credentials must be chmod 0600:\n%s", joined)
	}
}

func TestK3sRestart(t *testing.T) {
	runner := NewFakeRunner()
	k := NewK3s(runner, testLogger())
	if err := k.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.Commands) != 1 || strings.Join(runner.Commands[0], " ") != "systemctl restart k3s" {
		t.Fatalf("unexpected restart command: %v", runner.Commands)
	}
}

func TestUserManagerEnsureCreatesUser(t *testing.T) {
	runner := NewFakeRunner()
	um := NewUserManager(runner)
	name := "menina-test-nonexistent-xyz"
	if err := um.Ensure(context.Background(), name); err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	if len(runner.Commands) == 0 {
		t.Fatal("expected a useradd command to be run")
	}
	joined := strings.Join(runner.Commands[0], " ")
	if !strings.Contains(joined, "useradd") || !strings.Contains(joined, name) {
		t.Fatalf("unexpected command: %s", joined)
	}
}

func TestUserManagerEnsureSkipsExisting(t *testing.T) {
	runner := NewFakeRunner()
	um := NewUserManager(runner)
	if err := um.Ensure(context.Background(), "root"); err != nil {
		t.Fatalf("ensure existing user: %v", err)
	}
	if len(runner.Commands) != 0 {
		t.Fatalf("should not run commands for an existing user: %v", runner.Commands)
	}
}

func TestUserManagerAddToGroup(t *testing.T) {
	runner := NewFakeRunner()
	um := NewUserManager(runner)
	if err := um.AddToGroup(context.Background(), "alice", "menina"); err != nil {
		t.Fatal(err)
	}
	if len(runner.Commands) != 1 || strings.Join(runner.Commands[0], " ") != "usermod -aG menina alice" {
		t.Fatalf("unexpected commands: %v", runner.Commands)
	}
	if err := um.AddToGroup(context.Background(), "root", "menina"); err != nil {
		t.Fatal(err)
	}
	if len(runner.Commands) != 1 {
		t.Fatal("root should not be modified")
	}
}

func TestK3sEnsureInstalledSkipsWhenPresent(t *testing.T) {
	runner := NewFakeRunner()
	runner.Outputs["k3s --version"] = "k3s version v1.30"
	k := NewK3s(runner, testLogger())
	if !k.IsInstalled(context.Background()) {
		t.Fatal("expected k3s to be detected")
	}
	if err := k.EnsureInstalled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	for _, c := range runner.Commands {
		if strings.Contains(strings.Join(c, " "), "get.k3s.io") {
			t.Fatal("should not reinstall an existing k3s")
		}
	}
}

func TestK3sInstallDisablesTraefik(t *testing.T) {
	runner := NewFakeRunner()
	runner.Errors["k3s --version"] = errors.New("not found")
	runner.Errors["systemctl is-active k3s"] = errors.New("inactive")
	k := NewK3s(runner, testLogger())
	if err := k.EnsureInstalled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, c := range runner.Commands {
		joined += strings.Join(c, " ") + "\n"
	}
	if !strings.Contains(joined, "--disable traefik") {
		t.Fatalf("install command did not disable traefik:\n%s", joined)
	}
}

func TestSystemdApplyWritesAndStarts(t *testing.T) {
	runner := NewFakeRunner()
	s := NewSystemd(runner)
	s.UnitDir = t.TempDir()
	content := "[Unit]\nDescription=test\n[Service]\nExecStart=/bin/true\n"
	runner.Outputs["systemctl is-active menina-serve.service"] = "active"
	if err := s.Apply(context.Background(), "menina-serve.service", content); err != nil {
		t.Fatal(err)
	}
	if !s.IsActive(context.Background(), "menina-serve.service") {
		t.Fatal("expected active after apply")
	}
	joined := ""
	for _, c := range runner.Commands {
		joined += strings.Join(c, " ") + "\n"
	}
	for _, want := range []string{"systemctl daemon-reload", "systemctl enable menina-serve.service", "systemctl restart menina-serve.service"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestParseListenPort(t *testing.T) {
	port, err := ParseListenPort(":443")
	if err != nil || port != 443 {
		t.Fatalf("port=%d err=%v", port, err)
	}
	if _, err := ParseListenPort("not-an-address"); err == nil {
		t.Fatal("expected error")
	}
}

func TestStorageEnsureDirsAndBackup(t *testing.T) {
	dir := t.TempDir()
	s := NewStorage(dir)
	if err := s.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DiskUsage(dir); err != nil {
		t.Fatalf("disk usage: %v", err)
	}
}
