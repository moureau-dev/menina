package host

import (
	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/platform"
	"github.com/moureau-dev/menina/internal/registry"
)

func newServeCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the persistent control-plane service",
		Long:  "Run the control plane: config watcher, registry event listener, deployment workers and reconciliation, publishing routing snapshots to the proxy. Runs as a systemd service in production.",
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
					// Only the bundled registry is digest-resolved; images from
					// other registries deploy by tag (no push→auto-deploy).
					svc.SetDigestResolver(registry.BundledResolver{Client: rc})
				} else {
					app.Log.Warn("registry digest resolver unavailable", "error", err)
				}
			}
			app.Log.Info("starting menina control plane", "configDir", app.Opts.ConfigDir)
			return svc.Run(cmd.Context())
		},
	}
}
