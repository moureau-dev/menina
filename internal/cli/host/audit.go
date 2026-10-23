package host

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/api"
	"github.com/moureau-dev/menina/internal/core/audit"
)

func newAuditCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "audit", Short: "Query the audit log"}
	var actor, project, action, status string
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List audit events",
		RunE: func(cmd *cobra.Command, args []string) error {
			filter := audit.Filter{ActorID: actor, Project: project, Action: action, Status: status, Limit: limit}
			events, err := auditEvents(app, filter)
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]any{"events": events})
			}
			for _, e := range events {
				app.Infof("%s\t%s\t%s\t%s\t%s\t%s", e.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), e.ActorID, e.Action, e.Target, e.Status, e.Message)
			}
			return nil
		},
	}
	list.Flags().StringVar(&actor, "actor", "", "filter by actor id")
	list.Flags().StringVar(&project, "project", "", "filter by project")
	list.Flags().StringVar(&action, "action", "", "filter by action")
	list.Flags().StringVar(&status, "status", "", "filter by status")
	list.Flags().IntVar(&limit, "limit", 100, "maximum events")
	cmd.AddCommand(list)
	return cmd
}

func auditEvents(app *App, filter audit.Filter) ([]audit.Event, error) {
	if cfg, err := app.LoadConfig(); err == nil {
		if client, err := api.Dial(cfg.Platform.ControlSocket, cfg.Security.ControlTokenFile); err == nil {
			return client.AuditEvents(context.Background(), filter)
		}
		db, err := app.OpenDB(cfg)
		if err != nil {
			return nil, err
		}
		return db.AuditEvents(context.Background(), filter)
	}
	return nil, fmt.Errorf("configuration is unavailable")
}
