package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
)

func newEnvironmentCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "environment", Short: "Manage project environments"}
	cmd.AddCommand(environmentCreate(app), environmentList(app), environmentDelete(app))
	return cmd
}

func environmentDelete(app *App) *cobra.Command {
	var project string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an environment (its workloads are pruned; data retained)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := filepath.Join(config.ProjectDir(app.Opts.ConfigDir, project), "environments", args[0]+".yml")
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("environment %q not found in project %q", args[0], project)
			}
			prompt := fmt.Sprintf("Delete environment %q (namespace %s)? Its workloads, Services and routes are pruned; PersistentVolumeClaims and release history are RETAINED.", args[0], project+"-"+args[0])
			if !confirmDestructive(app, prompt, yes) {
				return fmt.Errorf("aborted")
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			app.Infof("Deleted environment %q. Its workloads/routes will be pruned; PVCs and release history are retained.", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without prompting")
	_ = cmd.MarkFlagRequired("project")
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
