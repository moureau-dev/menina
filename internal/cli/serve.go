package cli

import (
	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/platform"
)

func newServeCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the persistent control-plane service",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			if err := config.ValidatePlatform(cfg); err != nil {
				return err
			}
			app.Opts.ConfigDir = cfg.Host.ConfigDir
			svc, err := platform.NewService(cfg, app.Opts.ConfigDir, app.Log)
			if err != nil {
				return err
			}
			if cfg.Registry.Enabled {
				if rc, err := registryClient(cfg); err == nil {
					svc.SetDigestResolver(rc)
				} else {
					app.Log.Warn("registry digest resolver unavailable", "error", err)
				}
			}
			app.Log.Info("starting menina control plane", "configDir", app.Opts.ConfigDir)
			return svc.Run(cmd.Context())
		},
	}
}
