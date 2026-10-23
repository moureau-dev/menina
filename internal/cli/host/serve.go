package host

import (
	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/api"
	httpapi "github.com/moureau-dev/menina/internal/api/transport/http"
	"github.com/moureau-dev/menina/internal/core/config"
	"github.com/moureau-dev/menina/internal/core/controller"
	"github.com/moureau-dev/menina/internal/core/registry"
)

func newServeCommand(app *App) *cobra.Command {
	var apiListen string
	cmd := &cobra.Command{
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
			svc, err := controller.NewService(cfg, app.Opts.ConfigDir, app.Log)
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
			if apiListen != "" {
				token, err := api.EnsureToken(cfg.Security.ControlTokenFile)
				if err != nil {
					return err
				}
				go func() {
					if err := httpapi.Serve(cmd.Context(), apiListen, token, svc.Handler()); err != nil {
						app.Log.Error("api http listener stopped", "error", err)
					}
				}()
			}
			app.Log.Info("starting menina control plane", "configDir", app.Opts.ConfigDir)
			return svc.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&apiListen, "api-listen", "", "optional host:port for the HTTP API; bind to loopback")
	return cmd
}
