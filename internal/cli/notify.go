package cli

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/notify"
	"github.com/moureau-dev/menina/internal/secrets"
)

func newNotifyCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Manage outbound deploy notifications",
		Long:  "Inspect and test the outbound deploy webhooks (webhooks.deploy): list configured hooks and send a synthetic event to verify a receiver.",
	}
	cmd.AddCommand(notifyList(app), notifyTest(app))
	return cmd
}

func resolvedConfig(app *App, cfg *config.Config) (*config.ResolvedConfig, error) {
	projects, err := config.LoadAllProjects(config.ProjectsDir(cfg.Host.ConfigDir))
	if err != nil {
		return nil, err
	}
	return config.NewResolver(cfg).Resolve(projects, 0)
}

func notifyList(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured deploy webhooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			resolved, err := resolvedConfig(app, cfg)
			if err != nil {
				return err
			}
			if cfg.Webhooks.Deploy.Enabled {
				app.Infof("platform default: %s events=%v", cfg.Webhooks.Deploy.URLRef, cfg.Webhooks.Deploy.Events)
			}
			names := make([]string, 0, len(resolved.Projects))
			for name := range resolved.Projects {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				w := resolved.Projects[name].DeployWebhook
				if w == nil || !w.Enabled || w.URLRef == "" {
					app.Infof("%-20s (none)", name)
					continue
				}
				app.Infof("%-20s %s events=%v", name, w.URLRef, w.Events)
			}
			return nil
		},
	}
}

func notifyTest(app *App) *cobra.Command {
	var project, event string
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Send a test deploy notification",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			resolved, err := resolvedConfig(app, cfg)
			if err != nil {
				return err
			}
			var wh *config.DeployWebhook
			if project != "" {
				rp, ok := resolved.Projects[project]
				if !ok {
					return fmt.Errorf("project %q not found", project)
				}
				wh = rp.DeployWebhook
			} else {
				g := cfg.Webhooks.Deploy
				wh = &g
			}
			if wh == nil || !wh.Enabled || wh.URLRef == "" {
				return fmt.Errorf("no deploy webhook configured")
			}
			resolver := secrets.NewResolver()
			url, err := resolver.Resolve(wh.URLRef)
			if err != nil {
				return err
			}
			secret := ""
			if wh.SecretRef != "" {
				if secret, err = resolver.Resolve(wh.SecretRef); err != nil {
					return err
				}
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
			defer cancel()
			ev := notify.Event{
				Event:       event,
				Action:      "deploy",
				Project:     project,
				Environment: "test",
				App:         "test",
				Status:      "succeeded",
				Actor:       "manual",
				Message:     "menina notify test",
			}
			sender := notify.NewSender(app.Log)
			if err := sender.Send(ctx, notify.Webhook{URL: url, Secret: secret}, ev); err != nil {
				return fmt.Errorf("test notification failed: %w", err)
			}
			app.Infof("Test notification (%s) delivered", event)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project whose webhook to test (default: platform webhook)")
	cmd.Flags().StringVar(&event, "event", "deploy.succeeded", "event name to send")
	return cmd
}
