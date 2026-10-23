package host

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/moureau-dev/menina/internal/provision"
)

func testCLILogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func installVars() map[string]string {
	return map[string]string{
		"USER":       "menina",
		"GROUP":      "menina",
		"BINARY":     "/usr/local/bin/menina",
		"CONFIG_DIR": "/etc/menina",
		"DATA_DIR":   "/var/lib/menina",
	}
}

func TestInstallServicesPropagatesStartupFailure(t *testing.T) {
	runner := provision.NewFakeRunner()
	runner.Errors["systemctl restart menina-serve.service"] = errors.New("unit failed")
	systemd := provision.NewSystemd(runner)
	systemd.UnitDir = t.TempDir()
	app := &App{Log: testCLILogger()}

	err := installServices(context.Background(), app, systemd, []string{"menina-serve.service"}, installVars())
	if err == nil {
		t.Fatal("installServices must return an error when a service fails to start")
	}
}

func TestInstallServicesDetectsInactiveService(t *testing.T) {
	runner := provision.NewFakeRunner()
	// is-active returns empty (not "active") -> treated as failed.
	systemd := provision.NewSystemd(runner)
	systemd.UnitDir = t.TempDir()
	app := &App{Log: testCLILogger()}

	if err := installServices(context.Background(), app, systemd, []string{"menina-serve.service"}, installVars()); err == nil {
		t.Fatal("installServices must fail when the unit is not active")
	}
}

func TestInstallServicesSucceedsWhenActive(t *testing.T) {
	runner := provision.NewFakeRunner()
	runner.Outputs["systemctl is-active menina-serve.service"] = "active"
	systemd := provision.NewSystemd(runner)
	systemd.UnitDir = t.TempDir()
	app := &App{Log: testCLILogger()}

	if err := installServices(context.Background(), app, systemd, []string{"menina-serve.service"}, installVars()); err != nil {
		t.Fatalf("installServices should succeed: %v", err)
	}
}
