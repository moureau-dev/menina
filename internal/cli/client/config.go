package client

import (
	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/api/errors"
)

func newClientConfigCommand(app *App) *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect host configuration revisions over SSH",
	}
	cmd.PersistentFlags().StringVar(&host, "host", "", "SSH host (user@host)")
	cmd.AddCommand(clientConfigRevisions(app, &host), clientConfigRollback(app, &host))
	return cmd
}

func clientConfigRevisions(app *App, hostFlag *string) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:   "revisions",
		Short: "List apply revisions on the host",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := app.requireClientHost(*hostFlag)
			if err != nil {
				return err
			}
			remote := []string{"config", "revisions"}
			if project != "" {
				remote = append(remote, "--project", project)
			}
			if environment != "" {
				remote = append(remote, "--environment", environment)
			}
			if app.jsonOutput() {
				remote = append(remote, "--json")
			}
			return app.runSSHInherit(cmd, target, remote...)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "filter by project")
	cmd.Flags().StringVar(&environment, "environment", "", "filter by environment")
	return cmd
}

func clientConfigRollback(app *App, hostFlag *string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rollback <revision-id>",
		Short: "Restore an apply revision on the host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := app.requireClientHost(*hostFlag)
			if err != nil {
				return err
			}
			if !yes {
				return apierrors.New(apierrors.CodeValidation, "config rollback requires --yes", "review `config revisions` and re-run with --yes")
			}
			remote := []string{"config", "rollback", args[0], "--yes"}
			if app.jsonOutput() {
				remote = append(remote, "--json")
			}
			return app.runSSHInherit(cmd, target, remote...)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the restore")
	return cmd
}
