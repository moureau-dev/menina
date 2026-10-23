package host

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/cli/tui"
	"github.com/moureau-dev/menina/internal/config"
)

// menuEntry is one row of the top-level menu. command is space-separated
// (e.g. "registry status"). interactive marks commands that open their own TUI
// (their output must not be captured).
type menuEntry struct {
	label       string
	desc        string
	command     string
	interactive bool
}

var quitEntry = menuEntry{label: "Quit", desc: "Exit Menina."}

// allMenuEntries is the full menu, hidden one-time actions included.
var allMenuEntries = []menuEntry{
	{"Setup host", "Configure this machine: k3s, registry, networking, TLS and defaults.", "setup", true},
	{"Deploy platform", "Provision infrastructure and start the control plane.", "deploy", true},
	{"Create project", "Guided: create a project, its environments and optionally an app.", "create", true},
	{"Apps", "Pick a project and app, then Deploy / Scale / Edit / Logs / Rollback / Delete.", "app", true},
	{"Projects", "Pick a project and open its app action menu.", "project", true},
	{"Environments", "Pick a project and inspect, create or show an environment.", "environment", true},
	{"Status", "Show platform, endpoint and workload status.", "status", false},
	{"Routes", "Show the routing table.", "routes", false},
	{"Registry status", "Check the built-in registry health.", "registry status", false},
	{"Registry credential", "Show or regenerate the push credential.", "registry credential", false},
	{"Notifications", "List configured deploy webhooks.", "notify list", false},
	{"Diagnostics", "Run configuration, cluster and certificate checks.", "doctor", false},
	{"Backup", "Write a portable backup archive.", "backup", false},
	{"Restore", "Restore state from a backup archive.", "restore", false},
	{"Upgrade", "Download and install the latest verified release.", "upgrade", false},
	{"Version", "Print version information.", "version", false},
}

// menuEntriesFor returns the entries to show. Before the platform is set up and
// deployed, only the one-time Setup/Deploy actions are offered; afterwards they
// are hidden unless showAll is set.
func menuEntriesFor(app *App, showAll bool) []menuEntry {
	if !platformReady(app) {
		var out []menuEntry
		for _, e := range allMenuEntries {
			if e.command == "setup" || e.command == "deploy" {
				out = append(out, e)
			}
		}
		return append(out, quitEntry)
	}
	out := make([]menuEntry, 0, len(allMenuEntries))
	for _, e := range allMenuEntries {
		if !showAll && (e.command == "setup" || e.command == "deploy") {
			continue
		}
		out = append(out, e)
	}
	return append(out, quitEntry)
}

// platformReady reports whether the host is configured and the platform has
// been deployed (the control-plane database exists).
func platformReady(app *App) bool {
	cfg, err := config.LoadPlatform(app.Opts.ConfigDir)
	if err != nil {
		return false
	}
	if _, err := os.Stat(cfg.Platform.DBPath); err != nil {
		return false
	}
	return true
}

// runMainMenu is the entry point for a bare `menina`: on a terminal it opens a
// full-screen menu and reopens it after each action until the user quits.
// Without a terminal (scripts, pipes) it prints the root help instead.
func runMainMenu(root *cobra.Command, app *App, showAll bool) error {
	if app.Opts.NonInteractive {
		return fmt.Errorf("interactive menu disabled by --non-interactive; specify a subcommand")
	}
	p := newPrompter()
	if !p.enabled {
		return root.Help()
	}
	var prelude string
	selected := 0
	for {
		entries := menuEntriesFor(app, showAll)
		opts := make([]tui.Option, len(entries))
		for i, e := range entries {
			opts[i] = tui.Option{Label: e.label, Description: e.desc}
		}
		if selected >= len(entries) {
			selected = 0
		}
		idx, action := tui.SelectWithPreludeBadge("Menina", "host", opts, selected, false, prelude)
		switch action {
		case tui.ActionBack:
			continue // Esc on the root menu: stay put instead of closing.
		case tui.ActionQuit:
			return nil
		}
		if idx == len(entries)-1 { // Quit
			return nil
		}
		selected = idx // keep the same item selected when the menu returns
		out, err := runMenuEntry(root, app, p, entries[idx])
		switch {
		case errors.Is(err, errExit), errors.Is(err, errBack):
			prelude = ""
		case err != nil:
			if out = strings.TrimRight(out, "\n"); out != "" {
				out += "\n"
			}
			prelude = tui.Block(entries[idx].label, out+"error: "+err.Error())
		case strings.TrimSpace(out) == "":
			prelude = ""
		default:
			prelude = tui.Block(entries[idx].label, out)
		}
	}
}

// pickArchive lets the user choose a backup archive: a file picker on a
// terminal (starting in the backups directory when it exists), or a typed path
// otherwise.
func pickArchive(app *App, p *prompter) string {
	if !p.enabled {
		return ""
	}
	if !p.tui {
		return p.Ask("Backup archive path", "")
	}
	start, err := os.Getwd()
	if err != nil {
		start = ""
	}
	if cfg, err := app.LoadConfig(); err == nil && cfg.Host.DataDir != "" {
		if dir := filepath.Join(cfg.Host.DataDir, "backups"); dirExists(dir) {
			start = dir
		}
	}
	path, _ := tui.PickFile("Select backup archive", start)
	return path
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// runMenuEntry dispatches a menu action. For non-interactive commands it
// captures stdout and returns it so it can be printed above the menu; for
// interactive commands it returns the terminal to them directly.
func runMenuEntry(root *cobra.Command, app *App, p *prompter, e menuEntry) (string, error) {
	fields := strings.Fields(e.command)
	child, _, err := root.Find(fields)
	if err != nil {
		return "", err
	}
	var extra []string
	if e.command == "restore" {
		archive := pickArchive(app, p)
		if archive == "" {
			return "", nil
		}
		extra = append(extra, archive)
	}
	if e.interactive {
		return "", runChild(root.Context(), child, nil, extra...)
	}
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", runChild(root.Context(), child, nil, extra...)
	}
	os.Stdout = w
	runErr := runChild(root.Context(), child, nil, extra...)
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), runErr
}
