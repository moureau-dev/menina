package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moureau-dev/menina/internal/core/audit"
	"github.com/moureau-dev/menina/internal/core/config"
)

func TestAuditEventsFallsBackWhenControlPlaneIsDown(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Host.ConfigDir = dir
	cfg.Platform.DBPath = filepath.Join(dir, "state.db")
	cfg.Platform.ControlSocket = filepath.Join(dir, "missing.sock")
	cfg.Security.ControlTokenFile = filepath.Join(dir, "control.token")
	if err := os.WriteFile(cfg.Security.ControlTokenFile, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.SavePlatform(dir, &cfg); err != nil {
		t.Fatal(err)
	}
	app := &App{Opts: RootOptions{ConfigDir: dir}}
	db, err := app.OpenDB(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecordAudit(t.Context(), audit.Event{Action: "test", Status: audit.StatusOK}); err != nil {
		t.Fatal(err)
	}
	events, err := auditEvents(app, audit.Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != "test" {
		t.Fatalf("events = %+v", events)
	}
}
