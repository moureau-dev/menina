package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
)

func newProjectCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects (interactive when run without a subcommand)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := newPrompter()
			if !p.enabled {
				return cmd.Help()
			}
			project, err := selectProject(app, p)
			if err != nil {
				return err
			}
			name, err := selectApp(app, p, project)
			if err != nil {
				return err
			}
			return appMenu(cmd.Context(), app, p, project, name, "")
		},
	}
	cmd.AddCommand(projectCreate(app), projectList(app), projectDelete(app))
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

func projectDelete(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a project (config only; workloads are pruned, data is retained)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			dir := config.ProjectDir(app.Opts.ConfigDir, name)
			if _, err := os.Stat(dir); err != nil {
				return fmt.Errorf("project %q not found", name)
			}
			prompt := fmt.Sprintf("Delete project %q?\n  - its config files are removed\n  - Menina will prune its Kubernetes Deployments/Services and routes\n  - PersistentVolumeClaims and release history are RETAINED", name)
			if !confirmDestructive(app, prompt, yes) {
				return fmt.Errorf("aborted")
			}
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			app.Infof("Deleted project %q. Workloads/routes will be pruned by reconciliation; PVCs and release history are retained.", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without prompting")
	return cmd
}
