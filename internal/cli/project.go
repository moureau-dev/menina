package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
)

func newProjectCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects (interactive when run without a subcommand)",
		Long: "Manage projects (create/list/delete).\n\n" +
			"Run without a subcommand on a terminal to pick a project (or create one),\n" +
			"then pick or create an app and open its action menu. Without a terminal it\n" +
			"prints this help. See also `menina create` for the guided setup.",
		Example: "  menina project                   # pick a project → app menu\n" +
			"  menina project create my-app --environments production,staging\n" +
			"  menina project list --json",
		Args: cobra.NoArgs,
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
	var namespace, description, environments string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project",
		Long: "Create a project and its environments. Environments default to\n" +
			"\"production\"; use --environments to list them or \"none\" to create none.",
		Example: "  menina project create my-app --namespace my-app\n" +
			"  menina project create my-app --environments development,staging,production\n" +
			"  menina project create my-app --environments none",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := createProject(app, name, namespace, description); err != nil {
				return err
			}
			return createEnvironments(app, name, resolveEnvironmentNames(newPrompter(), environments, false))
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "Kubernetes namespace for workloads")
	cmd.Flags().StringVar(&description, "description", "", "project description")
	cmd.Flags().StringVar(&environments, "environments", "", "comma-separated environments to create (default: none)")
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
