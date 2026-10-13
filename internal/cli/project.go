package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
)

func newProjectCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Manage projects"}
	cmd.AddCommand(projectCreate(app), projectList(app))
	return cmd
}

func projectCreate(app *App) *cobra.Command {
	var namespace, description string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := config.EnsureProjectDirs(app.Opts.ConfigDir, name); err != nil {
				return err
			}
			pf := config.ProjectFile{Name: name, Namespace: namespace, Description: description}
			if err := writeYAML(filepath.Join(config.ProjectDir(app.Opts.ConfigDir, name), "project.yml"), pf); err != nil {
				return err
			}
			app.Infof("Created project %q at %s", name, config.ProjectDir(app.Opts.ConfigDir, name))
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "Kubernetes namespace for workloads")
	cmd.Flags().StringVar(&description, "description", "", "project description")
	return cmd
}

func projectList(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := config.ListProjects(config.ProjectsDir(app.Opts.ConfigDir))
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(names)
			}
			for _, n := range names {
				app.Infof("%s", n)
			}
			return nil
		},
	}
}
