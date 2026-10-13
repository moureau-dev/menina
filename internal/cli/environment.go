package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
)

func newEnvironmentCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "environment", Short: "Manage project environments"}
	cmd.AddCommand(environmentCreate(app), environmentList(app))
	return cmd
}

func environmentCreate(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an environment in a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			env := config.EnvironmentFile{Name: name, Apps: map[string]*config.AppOverride{}}
			path := filepath.Join(config.ProjectDir(app.Opts.ConfigDir, project), "environments", name+".yml")
			if err := writeYAML(path, env); err != nil {
				return err
			}
			app.Infof("Created environment %q in project %q", name, project)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func environmentList(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List environments in a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			envs, order, err := config.LoadEnvironments(config.ProjectsDir(app.Opts.ConfigDir), project)
			if err != nil {
				return err
			}
			_ = envs
			if app.Opts.JSON {
				return app.PrintJSON(order)
			}
			for _, name := range order {
				app.Infof("%s", name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}
