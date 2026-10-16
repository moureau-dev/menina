package cli

import (
	"fmt"
	"os"
	"strings"

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
	cmd.AddCommand(projectGet(app), projectCreate(app), projectList(app), projectDelete(app), projectRestore(app))
	return cmd
}

func projectGet(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "get <name>",
		Short:   "Show a project and its environments",
		Example: "  menina project get my-app\n  menina project get my-app --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := config.LoadProject(config.ProjectsDir(app.Opts.ConfigDir), args[0])
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]any{
					"name":         p.Name,
					"namespace":    p.Namespace,
					"description":  p.Description,
					"apps":         len(p.Apps),
					"environments": p.EnvOrder,
				})
			}
			app.Infof("Project %q", p.Name)
			app.Infof("  namespace:    %s", p.Namespace)
			if p.Description != "" {
				app.Infof("  description:  %s", p.Description)
			}
			app.Infof("  apps:         %d", len(p.Apps))
			app.Infof("  environments: %s", strings.Join(p.EnvOrder, ", "))
			return nil
		},
	}
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
	var yes, hard bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Soft-delete a project (--hard to remove it)",
		Long: "Delete a project.\n\n" +
			"By default this is a soft delete: its workloads stop (scaled to zero) and\n" +
			"its config is kept so it can be restored with `menina project restore`.\n" +
			"With --hard the config is removed and its Deployments/Services and routes\n" +
			"are pruned. Either way, PersistentVolumeClaims and release history are\n" +
			"retained.",
		Example: "  menina project delete my-app\n" +
			"  menina project delete my-app --hard --yes\n" +
			"  menina project restore my-app",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			dir := config.ProjectDir(app.Opts.ConfigDir, name)
			softDir := config.SoftDeleteProjectPath(app.Opts.ConfigDir, name)
			live, trashed := fileExists(dir), fileExists(softDir)
			if !live && !trashed {
				return fmt.Errorf("project %q not found", name)
			}
			prompt := fmt.Sprintf("Soft-delete project %q?\n  - its workloads stop (scaled to zero)\n  - its config files are kept for restore\n  - PersistentVolumeClaims and release history are RETAINED", name)
			if hard {
				prompt = fmt.Sprintf("Delete project %q?\n  - its config files are removed\n  - Menina will prune its Kubernetes Deployments/Services and routes\n  - PersistentVolumeClaims and release history are RETAINED", name)
			}
			if !confirmDestructive(app, prompt, yes) {
				return fmt.Errorf("aborted")
			}
			if hard {
				if live {
					if err := os.RemoveAll(dir); err != nil {
						return err
					}
				}
				if trashed {
					if err := os.RemoveAll(softDir); err != nil {
						return err
					}
				}
				app.Infof("Deleted project %q. Workloads/routes will be pruned by reconciliation; PVCs and release history are retained.", name)
				return nil
			}
			if trashed {
				app.Infof("Project %q is already soft-deleted.", name)
				return nil
			}
			if err := moveToTrash(dir, softDir); err != nil {
				return err
			}
			app.Infof("Soft-deleted project %q (workloads stopped; config kept). Restore with `menina project restore %s`.", name, name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&hard, "hard", false, "remove the config and prune workloads instead of soft-deleting")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without prompting")
	return cmd
}

func projectRestore(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <name>",
		Short: "Restore a soft-deleted project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			dir := config.ProjectDir(app.Opts.ConfigDir, name)
			softDir := config.SoftDeleteProjectPath(app.Opts.ConfigDir, name)
			if fileExists(dir) {
				return fmt.Errorf("project %q is not soft-deleted", name)
			}
			if !fileExists(softDir) {
				return fmt.Errorf("no soft-deleted project %q", name)
			}
			if err := restoreFromTrash(softDir, dir); err != nil {
				return err
			}
			app.Infof("Restored project %q; reconciliation will bring its workloads back.", name)
			return nil
		},
	}
	return cmd
}
