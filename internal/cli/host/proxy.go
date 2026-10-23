package host

import (
	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/api"
	"github.com/moureau-dev/menina/internal/edge"
)

func newProxyCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "proxy",
		Short: "Run the reverse proxy",
		Long:  "Run the reverse proxy: terminates TLS per config and forwards public hostnames to Kubernetes ClusterIPs using the routing snapshot from the control plane. Runs as a systemd service in production.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			app.Opts.ConfigDir = cfg.Host.ConfigDir
			opts := edge.Options{
				TrustedProxies: cfg.Security.TrustedProxies,
				TLS: edge.TLSOptions{
					Terminate: cfg.TLS.Terminate,
					Mode:      cfg.TLS.Mode,
					CertDir:   cfg.TLS.CertDir,
					ACME: edge.ACMEOptions{
						Email:        cfg.TLS.ACME.Email,
						DirectoryURL: cfg.TLS.ACME.DirectoryURL,
						StorageDir:   cfg.TLS.ACME.StorageDir,
						BaseDomains:  cfg.Networking.BaseDomains,
					},
				},
				Log:          app.Log,
				HTTPListen:   cfg.Platform.HTTPListen,
				HTTPSListen:  cfg.Platform.HTTPSListen,
				HTTPRedirect: cfg.TLS.HTTPRedirect,
			}
			server, err := edge.NewServer(opts)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			go api.SubscribeRouting(ctx, cfg.Platform.ControlSocket, cfg.Security.ControlTokenFile, app.Log, server)
			return server.Serve(ctx, cfg.Platform.HTTPListen, cfg.Platform.HTTPSListen)
		},
	}
}
