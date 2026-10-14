package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
	"github.com/moureau-dev/menina/internal/platform"
	"github.com/moureau-dev/menina/internal/version"
)

type RootOptions struct {
	ConfigDir string
	JSON      bool
	Verbose   bool
}

type App struct {
	Opts RootOptions
	Log  *slog.Logger
}

func NewRootCommand() *cobra.Command {
	app := &App{}
	root := &cobra.Command{
		Use:           "menina",
		Short:         "Menina is a self-hosted application platform",
		Long:          "Menina deploys applications from pushed container images to k3s, routing public hostnames through its own reverse proxy.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			level := slog.LevelInfo
			if app.Opts.Verbose {
				level = slog.LevelDebug
			}
			app.Log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
		},
	}
	root.PersistentFlags().StringVar(&app.Opts.ConfigDir, "config-dir", envOr("MENINA_CONFIG_DIR", "/etc/menina"), "configuration directory")
	root.PersistentFlags().BoolVar(&app.Opts.JSON, "json", false, "output machine-readable JSON")
	root.PersistentFlags().BoolVar(&app.Opts.Verbose, "verbose", false, "verbose logging")

	root.AddCommand(
		newSetupCommand(app),
		newDeployCommand(app),
		newStatusCommand(app),
		newProjectCommand(app),
		newEnvironmentCommand(app),
		newAppCommand(app),
		newRoutesCommand(app),
		newRegistryCommand(app),
		newDoctorCommand(app),
		newBackupCommand(app),
		newServeCommand(app),
		newProxyCommand(app),
		newUpgradeCommand(app),
		newVersionCommand(app),
		newInstallCommand(app),
	)
	return root
}

func Execute() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	root := NewRootCommand()
	root.SetContext(ctx)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
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
