package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/cli/client"
	"github.com/moureau-dev/menina/internal/clierr"
	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
	"github.com/moureau-dev/menina/internal/platform"
	"github.com/moureau-dev/menina/internal/version"
)

type RootOptions struct {
	ConfigDir      string
	JSON           bool
	Verbose        bool
	NonInteractive bool
}

type App struct {
	Opts RootOptions
	Log  *slog.Logger
}

func NewRootCommand() *cobra.Command {
	app := &App{}
	var showAll bool
	root := &cobra.Command{
		Use:   "menina",
		Short: "Menina is a self-hosted application platform",
		Long:  "Menina deploys applications from pushed container images to k3s, routing public hostnames through its own reverse proxy.",
		Example: "  menina                           # open the interactive menu\n" +
			"  menina setup                     # configure the host (root)\n" +
			"  menina deploy                    # provision k3s, registry, services\n" +
			"  menina create my-app             # create a project (interactive)\n" +
			"  menina app --project my-app      # pick an app, then Deploy/Scale/Logs\n" +
			"  menina serve                     # run the control plane\n" +
			"  menina proxy                     # run the reverse proxy",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMainMenu(cmd, app, showAll)
		},
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			nonInteractive = app.Opts.NonInteractive
			level := slog.LevelInfo
			if app.Opts.Verbose {
				level = slog.LevelDebug
			}
			app.Log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
		},
	}
	root.Flags().BoolVar(&showAll, "show-all", false, "show one-time setup/deploy actions in the interactive menu")
	root.PersistentFlags().StringVar(&app.Opts.ConfigDir, "config-dir", envOr("MENINA_CONFIG_DIR", "/etc/menina"), "configuration directory")
	root.PersistentFlags().BoolVar(&app.Opts.JSON, "json", false, "output machine-readable JSON")
	root.PersistentFlags().BoolVar(&app.Opts.Verbose, "verbose", false, "verbose logging")
	root.PersistentFlags().BoolVar(&app.Opts.NonInteractive, "non-interactive", false, "never prompt or open the TUI; fail instead")

	info := version.Get()
	root.Version = info.Version
	root.SetVersionTemplate(fmt.Sprintf("menina %s (commit %s, built %s, %s)\n", info.Version, info.Commit, info.Date, info.GoVersion))

	root.AddCommand(
		newCreateCommand(app),
		newSetupCommand(app),
		newDeployCommand(app),
		newStatusCommand(app),
		newProjectCommand(app),
		newEnvironmentCommand(app),
		newAppCommand(app),
		newRoutesCommand(app),
		newRegistryCommand(app),
		newSecretCommand(app),
		newJobCommand(app),
		newReleaseCommand(app),
		newConfigCommand(app),
		newDoctorCommand(app),
		newBackupCommand(app),
		newRestoreCommand(app),
		newNotifyCommand(app),
		newServeCommand(app),
		newProxyCommand(app),
		newUpgradeCommand(app),
		newVersionCommand(app),
		newInstallCommand(app),
	)
	root.AddCommand(client.NewCommands(client.Options{
		ConfigDir: &app.Opts.ConfigDir,
		JSON:      &app.Opts.JSON,
	})...)
	return root
}

func Execute() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel on the first SIGINT/SIGTERM so long-running services (serve, proxy)
	// shut down gracefully. Stop handling afterwards so a second signal takes the
	// default action (immediate termination) if shutdown hangs.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		if _, ok := <-sigCh; ok {
			cancel()
			signal.Stop(sigCh)
		}
	}()
	setPromptContext(ctx)

	root := NewRootCommand()
	root.SetContext(ctx)
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(backSignal); ok {
					err = errBack
					return
				}
				panic(r)
			}
		}()
		return root.Execute()
	}()
	if err != nil {
		if errors.Is(err, errExit) || errors.Is(err, errBack) {
			return 0
		}
		printCLIError(root, err)
		_, _, _, exit := clierr.Info(err)
		return exit
	}
	return 0
}

func printCLIError(root *cobra.Command, err error) {
	code, message, hint, _ := clierr.Info(err)
	jsonOut, _ := root.PersistentFlags().GetBool("json")
	if jsonOut {
		enc := json.NewEncoder(os.Stderr)
		enc.SetIndent("", "  ")
		body := map[string]any{"code": code, "message": message}
		if hint != "" {
			body["hint"] = hint
		}
		_ = enc.Encode(map[string]any{"error": body})
		return
	}
	fmt.Fprintln(os.Stderr, "error:", message)
	if hint != "" {
		fmt.Fprintln(os.Stderr, "hint:", hint)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (a *App) LoadConfig() (*config.Config, error) {
	cfg, err := config.LoadPlatform(a.Opts.ConfigDir)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (a *App) OpenDB(cfg *config.Config) (*database.DB, error) {
	db, err := database.Open(cfg.Platform.DBPath)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(context.Background()); err != nil {
		return nil, err
	}
	return db, nil
}

func (a *App) Control(cfg *config.Config) (*platform.ControlClient, error) {
	return platform.DialControl(cfg.Platform.ControlSocket, cfg.Security.ControlTokenFile)
}

func (a *App) PrintJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *App) Infof(format string, args ...any) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

func newVersionCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Get()
			if app.Opts.JSON {
				return app.PrintJSON(info)
			}
			app.Infof("menina %s (commit %s, built %s, %s)", info.Version, info.Commit, info.Date, info.GoVersion)
			return nil
		},
	}
}
