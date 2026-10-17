package clientcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/moureau-dev/menina/internal/tui"
)

var (
	errClientBack = errors.New("client back")
	errClientQuit = errors.New("client quit")
)

type clientMenuEntry struct {
	label string
	desc  string
	run   func(*cobra.Command, *App) (string, error)
}

func runClientMenu(cmd *cobra.Command, app *App) error {
	if app.nonInteractive() {
		return fmt.Errorf("interactive menu disabled by --non-interactive; specify a subcommand")
	}
	if app.jsonOutput() || !term.IsTerminal(int(os.Stdin.Fd())) {
		return cmd.Help()
	}
	selected := 0
	var prelude string
	if !clientInstalled() {
		install, err := clientConfirm("Install menina and add it to your PATH?", true)
		switch err {
		case nil:
			if install {
				target, note, ierr := installSelf("", false, false)
				if ierr != nil {
					prelude = tui.Block("Install", "error: "+ierr.Error())
				} else {
					prelude = tui.Block("Install", "Installed "+target+"\n"+note)
				}
			}
		case errClientQuit:
			return nil
		}
	}
	for {
		entries := clientMenuEntries(clientInstalled())
		opts := make([]tui.Option, len(entries))
		for i, e := range entries {
			opts[i] = tui.Option{Label: e.label, Description: e.desc}
		}
		if selected >= len(entries) {
			selected = 0
		}
		idx, action := tui.SelectWithPreludeBadge("Menina", "client", opts, selected, false, prelude)
		switch action {
		case tui.ActionBack:
			continue
		case tui.ActionQuit:
			return nil
		}
		if idx == len(entries)-1 { // Quit
			return nil
		}
		selected = idx
		out, err := entries[idx].run(cmd, app)
		switch {
		case errors.Is(err, errClientBack):
			prelude = ""
		case errors.Is(err, errClientQuit):
			return nil
		case err != nil:
			out = strings.TrimRight(out, "\n")
			if out != "" {
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

func clientMenuEntries(installed bool) []clientMenuEntry {
	install := clientMenuEntry{"Install", "Copy this client to your user bin directory and add it to PATH.", menuInstall}
	version := clientMenuEntry{"Version", "Show the client version or check for updates.", menuVersion}
	quit := clientMenuEntry{"Quit", "Exit Menina.", nil}
	if !installed {
		return []clientMenuEntry{install, version, quit}
	}
	return []clientMenuEntry{
		{"Init", "Scaffold a .menina.yml in the current directory.", menuInit},
		install,
		{"Validate", "Parse and render a manifest without writing anything.", menuValidate},
		{"Apply", "Write project/app/environment config, locally or over SSH.", menuApply},
		{"Build", "Build the manifest's images with Docker.", menuBuild},
		{"Push", "Push images to the registry; optionally build first.", menuPush},
		version,
		{"Upgrade", "Check for or install a newer client binary.", menuUpgrade},
		quit,
	}
}

func clientAsk(title, def string) (string, error) {
	value, action := tui.Input(title, def)
	switch action {
	case tui.ActionSelected:
		return strings.TrimSpace(value), nil
	case tui.ActionBack:
		return "", errClientBack
	default:
		return "", errClientQuit
	}
}

func clientChoose(title string, options []tui.Option, def int) (int, error) {
	idx, action := tui.Select(title, options, def, false)
	switch action {
	case tui.ActionSelected:
		return idx, nil
	case tui.ActionBack:
		return def, errClientBack
	default:
		return def, errClientQuit
	}
}

func clientConfirm(title string, def bool) (bool, error) {
	idx, action := tui.Select(title, []tui.Option{{Label: "Yes"}, {Label: "No"}}, boolIndex(def), false)
	switch action {
	case tui.ActionSelected:
		return idx == 0, nil
	case tui.ActionBack:
		return def, errClientBack
	default:
		return def, errClientQuit
	}
}

func boolIndex(v bool) int {
	if v {
		return 0
	}
	return 1
}

// runClientProcess re-executes this binary so every subcommand keeps its normal
// flag parsing and output. The menu has already left the alternate screen, so
// stdin is available for ssh password prompts.
func runClientProcess(ctx context.Context, app *App, args ...string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	full := make([]string, 0, len(args)+4)
	if dir := app.configDir(); dir != "" {
		full = append(full, "--config-dir", dir)
	}
	full = append(full, args...)
	c := exec.CommandContext(ctx, exe, full...)
	c.Stdin = os.Stdin
	out, err := c.CombinedOutput()
	return string(out), err
}

func menuInit(cmd *cobra.Command, app *App) (string, error) {
	file, err := clientAsk("Manifest path", defaultManifestFile)
	if err != nil {
		return "", err
	}
	environment, err := clientAsk("Environment", "production")
	if err != nil {
		return "", err
	}
	project, err := clientAsk("Project", defaultProjectName())
	if err != nil {
		return "", err
	}
	args := []string{"init", "--file", file, "--environment", environment, "--project", project}
	if fileExists(file) {
		overwrite, err := clientConfirm("Overwrite "+file+"?", false)
		if err != nil {
			return "", err
		}
		if !overwrite {
			return "", errClientBack
		}
		args = append(args, "--force")
	}
	return runClientProcess(cmd.Context(), app, args...)
}

func menuInstall(cmd *cobra.Command, app *App) (string, error) {
	ok, err := clientConfirm("Install this client and update PATH?", true)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errClientBack
	}
	return runClientProcess(cmd.Context(), app, "install")
}

func menuValidate(cmd *cobra.Command, app *App) (string, error) {
	file, err := clientAsk("Manifest path", defaultManifestFile)
	if err != nil {
		return "", err
	}
	registry, err := clientAsk("Registry (blank = manifest/host)", "")
	if err != nil {
		return "", err
	}
	args := []string{"validate", "--file", file}
	if registry != "" {
		args = append(args, "--registry", registry)
	}
	return runClientProcess(cmd.Context(), app, args...)
}

func menuApply(cmd *cobra.Command, app *App) (string, error) {
	file, err := clientAsk("Manifest path", defaultManifestFile)
	if err != nil {
		return "", err
	}
	host, _ := readCachedHost()
	host, err = clientAsk("SSH host (blank = local)", strings.TrimSpace(host))
	if err != nil {
		return "", err
	}
	environment, err := clientAsk("Environment override (blank = manifest)", "")
	if err != nil {
		return "", err
	}
	mode, err := clientChoose("Apply mode", []tui.Option{
		{Label: "Apply", Description: "Write desired state."},
		{Label: "Check only", Description: "Validate and render without writing."},
	}, 0)
	if err != nil {
		return "", err
	}
	args := []string{"apply", "--file", file}
	if host != "" {
		args = append(args, "--host", host)
	}
	if environment != "" {
		args = append(args, "--environment", environment)
	}
	if mode == 1 {
		args = append(args, "--check")
	}
	return runClientProcess(cmd.Context(), app, args...)
}

func menuBuild(cmd *cobra.Command, app *App) (string, error) {
	file, err := clientAsk("Manifest path", defaultManifestFile)
	if err != nil {
		return "", err
	}
	filter, err := clientAsk("Filter (blank = all)", "")
	if err != nil {
		return "", err
	}
	mode, err := clientChoose("Build mode", []tui.Option{
		{Label: "Build only", Description: "Build images locally."},
		{Label: "Build + push", Description: "Build, then push to the registry."},
	}, 0)
	if err != nil {
		return "", err
	}
	args := []string{"build", "--file", file}
	if filter != "" {
		args = append(args, "--filter", filter)
	}
	if mode == 1 {
		args = append(args, "--push")
	}
	return runClientProcess(cmd.Context(), app, args...)
}

func menuPush(cmd *cobra.Command, app *App) (string, error) {
	file, err := clientAsk("Manifest path", defaultManifestFile)
	if err != nil {
		return "", err
	}
	filter, err := clientAsk("Filter (blank = all)", "")
	if err != nil {
		return "", err
	}
	build, err := clientConfirm("Build before pushing?", false)
	if err != nil {
		return "", err
	}
	args := []string{"push", "--file", file}
	if filter != "" {
		args = append(args, "--filter", filter)
	}
	if build {
		args = append(args, "--build")
	}
	return runClientProcess(cmd.Context(), app, args...)
}

func menuVersion(cmd *cobra.Command, app *App) (string, error) {
	mode, err := clientChoose("Version", []tui.Option{
		{Label: "Show version", Description: "Print the installed client version."},
		{Label: "Check for updates", Description: "Compare with the latest GitHub release."},
	}, 0)
	if err != nil {
		return "", err
	}
	args := []string{"version"}
	if mode == 1 {
		args = append(args, "--check")
	}
	return runClientProcess(cmd.Context(), app, args...)
}

func menuUpgrade(cmd *cobra.Command, app *App) (string, error) {
	mode, err := clientChoose("Upgrade", []tui.Option{
		{Label: "Check for updates", Description: "Compare without installing."},
		{Label: "Install latest", Description: "Download, verify and replace the client."},
	}, 0)
	if err != nil {
		return "", err
	}
	args := []string{"upgrade"}
	if mode == 0 {
		args = append(args, "--check")
	} else {
		ok, err := clientConfirm("Replace the running client binary?", false)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errClientBack
		}
	}
	return runClientProcess(cmd.Context(), app, args...)
}
