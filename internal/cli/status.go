package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show overall platform health",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			client, err := app.Control(cfg)
			if err != nil {
				return fmt.Errorf("control plane not reachable (%v); start it with `menina serve`", err)
			}
			var status map[string]any
			if err := client.GetJSON(cmd.Context(), "/status", &status); err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(status)
			}
			app.Infof("Menina status")
			for _, key := range []string{"pid", "snapshotVersion", "routes", "kubernetes", "registry", "configLastError"} {
				if v, ok := status[key]; ok {
					app.Infof("  %-16s %v", key+":", v)
				}
			}
			if jobs, ok := status["jobs"].(map[string]any); ok {
				app.Infof("  jobs:            %v", jobs)
			}
			return nil
		},
	}
}

func newRoutesCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "routes",
		Short: "Inspect public hostname mappings",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			if client, err := app.Control(cfg); err == nil {
				var routes []map[string]any
				if err := client.GetJSON(cmd.Context(), "/routes", &routes); err != nil {
					return err
				}
				if app.Opts.JSON {
					return app.PrintJSON(routes)
				}
				if len(routes) == 0 {
					app.Infof("No routes configured.")
					return nil
				}
				app.Infof("%-30s %-10s %-30s %s", "HOST", "PORT", "SERVICE", "PROJECT/ENV/APP")
				for _, r := range routes {
					app.Infof("%-30v %-10v %-30v %v/%v/%v", r["host"], r["port"], r["service"], r["project"], r["environment"], r["app"])
				}
				return nil
			}
			db, err := app.OpenDB(cfg)
			if err != nil {
				return err
			}
			routes, err := db.Routes(cmd.Context())
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(routes)
			}
			for _, r := range routes {
				app.Infof("%s -> %s/%s:%d (%s/%s/%s)", r.Host, r.Namespace, r.Service, r.Port, r.Project, r.Environment, r.App)
			}
			return nil
		},
	}
}
