package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/proxy"
)

func newProxyCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "proxy",
		Short: "Run the reverse proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			app.Opts.ConfigDir = cfg.Host.ConfigDir
			opts := proxy.Options{
				TrustedProxies: cfg.Security.TrustedProxies,
				TLS: proxy.TLSOptions{
					Terminate: cfg.TLS.Terminate,
					Mode:      cfg.TLS.Mode,
					CertDir:   cfg.TLS.CertDir,
					ACME: proxy.ACMEOptions{
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
			server, err := proxy.NewServer(opts)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			go subscribeRouting(ctx, app, cfg, server)
			return server.Serve(ctx, cfg.Platform.HTTPListen, cfg.Platform.HTTPSListen)
		},
	}
}

func subscribeRouting(ctx context.Context, app *App, cfg *config.Config, server *proxy.Server) {
	for ctx.Err() == nil {
		client, err := app.Control(cfg)
		if err != nil {
			app.Log.Warn("control plane unavailable; retrying", "error", err)
			sleepOrDone(ctx, 2*time.Second)
			continue
		}
		if snap, err := client.Snapshot(ctx); err != nil {
			app.Log.Warn("fetch routing snapshot failed", "error", err)
		} else if err := server.UpdateSnapshot(snap); err != nil {
			app.Log.Error("rejected routing snapshot", "error", err)
		} else {
			app.Log.Info("loaded routing snapshot", "version", snap.Version, "routes", len(snap.Routes))
		}
		err = client.Stream(ctx, func(s *proxy.Snapshot) {
			if err := server.UpdateSnapshot(s); err != nil {
				app.Log.Error("rejected routing snapshot update", "error", err)
				return
			}
			app.Log.Info("routing snapshot updated", "version", s.Version, "routes", len(s.Routes))
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			app.Log.Warn("routing stream ended; reconnecting", "error", err)
		}
		sleepOrDone(ctx, 2*time.Second)
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
