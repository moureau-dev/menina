// Package clientcli implements the cross-platform client commands: scaffolding,
// validating, applying, building and pushing a repository manifest. It
// deliberately avoids the host-only packages (k3s, SQLite, systemd) so the
// client binary builds on Linux, macOS and Windows.
package client

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/clierr"
	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/version"
)

// Options are shared by the host binary (client commands are also available
// there) and the standalone client binary.
type Options struct {
	ConfigDir      *string
	JSON           *bool
	NonInteractive *bool
}

type App struct {
	opts Options
}

func NewCommands(opts Options) []*cobra.Command {
	app := &App{opts: opts}
	return []*cobra.Command{
		newInitCommand(app),
		newValidateCommand(app),
		newApplyCommand(app),
		newBuildCommand(app),
		newPushCommand(app),
		newSchemaCommand(app),
		newCapabilitiesCommand(app),
	}
}

func (a *App) configDir() string {
	if a.opts.ConfigDir != nil && *a.opts.ConfigDir != "" {
		return *a.opts.ConfigDir
	}
	return "/etc/menina"
}

func (a *App) jsonOutput() bool {
	return a.opts.JSON != nil && *a.opts.JSON
}

func (a *App) nonInteractive() bool {
	return a.opts.NonInteractive != nil && *a.opts.NonInteractive
}

func (a *App) loadConfig() (*config.Config, error) {
	return config.LoadPlatform(a.configDir())
}

func (a *App) infof(format string, args ...any) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// NewRootCommand builds the standalone client command tree.
func NewRootCommand() *cobra.Command {
	var configDir string
	var jsonOutput, verbose, nonInteractive bool
	app := &App{opts: Options{ConfigDir: &configDir, JSON: &jsonOutput, NonInteractive: &nonInteractive}}
	root := &cobra.Command{
		Use:   "menina",
		Short: "Menina client: apply, build and push repository manifests",
		Long: "Menina client is the cross-platform side of Menina. It scaffolds and\n" +
			"validates .menina.yml manifests, applies them over SSH, and builds/pushes\n" +
			"images. The host binary exposes the same commands alongside the control\n" +
			"plane commands.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClientMenu(cmd, app)
		},
	}
	root.PersistentFlags().StringVar(&configDir, "config-dir", envOr("MENINA_CONFIG_DIR", "/etc/menina"), "host configuration directory (used for secret refs)")
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output machine-readable JSON")
	root.PersistentFlags().BoolVar(&verbose, "verbose", false, "verbose logging")
	root.PersistentFlags().BoolVar(&nonInteractive, "non-interactive", false, "never prompt or open the TUI; fail instead")
	_ = verbose

	root.AddCommand(NewCommands(app.opts)...)
	root.AddCommand(newInstallCommand(app), newSecretCommand(app), newClientJobCommand(app), newClientConfigCommand(app), newVersionCommand(app), newUpgradeCommand(app))

	info := version.Get()
	root.Version = info.Version
	root.SetVersionTemplate(fmt.Sprintf("menina %s (commit %s, built %s, %s)\n", info.Version, info.Commit, info.Date, info.GoVersion))
	return root
}

// Execute runs the standalone client binary.
func Execute() int {
	root := NewRootCommand()
	if err := root.Execute(); err != nil {
		code, message, hint, exit := clierr.Info(err)
		jsonOut, _ := root.PersistentFlags().GetBool("json")
		if jsonOut {
			enc := json.NewEncoder(os.Stderr)
			enc.SetIndent("", "  ")
			body := map[string]any{"code": code, "message": message}
			if hint != "" {
				body["hint"] = hint
			}
			_ = enc.Encode(map[string]any{"error": body})
		} else {
			fmt.Fprintln(os.Stderr, "error:", message)
			if hint != "" {
				fmt.Fprintln(os.Stderr, "hint:", hint)
			}
		}
		return exit
	}
	return 0
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
