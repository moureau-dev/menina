package host

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/core/config"
)

func newEnvironmentCommand(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "environment [name]",
		Short: "Manage project environments (interactive without a subcommand)",
		Long: "Manage project environments (create/list/delete, or show one).\n\n" +
			"An environment is a deployment target that holds per-app overrides\n" +
			"(enabled, image tag, resources, replicas, exposure, env). Run without a\n" +
			"subcommand on a terminal to pick a project, then pick or create an\n" +
			"environment and print its resolved apps. Use --project to skip the\n" +
			"project prompt.",
		Example: "  menina environment --project my-app                # pick/create, then show\n" +
			"  menina environment production --project my-app     # show production\n" +
			"  menina environment create staging --project my-app\n" +
			"  menina environment list --project my-app",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := newPrompter()
			proj := project
			if proj == "" {
				if !p.enabled {
					return cmd.Help()
				}
				var err error
				if proj, err = selectProject(app, p); err != nil {
					return err
				}
			}
			name := ""
			if len(args) > 0 {
				name = args[0]
			} else {
				if !p.enabled {
					return cmd.Help()
				}
				var err error
				if name, err = selectEnvironmentInteractive(app, p, proj); err != nil {
					return err
				}
			}
			return showEnvironment(app, proj, name)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.AddCommand(environmentCreate(app), environmentList(app), environmentDelete(app), environmentRestore(app))
	return cmd
}

func showEnvironment(app *App, project, name string) error {
	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}
	resolved, err := resolvedConfig(app, cfg)
	if err != nil {
		return err
	}
	rp, ok := resolved.Projects[project]
	if !ok {
		return fmt.Errorf("project %q not found", project)
	}
	re, ok := rp.Environments[name]
	if !ok {
		return fmt.Errorf("environment %q not found in project %q", name, project)
	}
	namespace := ""
	names := make([]string, 0, len(re.Apps))
	for n := range re.Apps {
		names = append(names, n)
	}
	sort.Strings(names)
	app.Infof("Environment %q (project %q)", name, project)
	if len(names) == 0 {
		app.Infof("  (no apps)")
		return nil
	}
	for _, n := range names {
		a := re.Apps[n]
		namespace = a.Namespace
		hosts := ""
		if a.Exposure.Enabled {
			hosts = " hosts=" + strings.Join(a.Exposure.Hosts, ",")
		}
		app.Infof("  %-16s enabled=%-5v tag=%-12s image=%s%s", n, a.Enabled, a.Tag, a.Image(), hosts)
	}
	app.Infof("  namespace: %s", namespace)
	return nil
}

func environmentDelete(app *App) *cobra.Command {
	var project string
	var yes, hard bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Soft-delete an environment (--hard to remove it)",
		Long: "Delete an environment.\n\n" +
			"By default this is a soft delete: its workloads stop (scaled to zero) and\n" +
			"its config is kept so it can be restored with `menina environment\n" +
			"restore`. With --hard the config is removed and the workloads/Services and\n" +
			"routes are pruned. Either way, PVCs and release history are retained.",
		Example: "  menina environment delete staging --project my-app\n" +
			"  menina environment delete staging --project my-app --hard --yes\n" +
			"  menina environment restore staging --project my-app",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := filepath.Join(config.ProjectDir(app.Opts.ConfigDir, project), "environments", name+".yml")
			softPath := config.SoftDeleteEnvironmentPath(app.Opts.ConfigDir, project, name)
			overridesDir := config.EnvironmentOverridesDir(app.Opts.ConfigDir, project, name)
			live := fileExists(path) || dirExists(overridesDir)
			trashed := fileExists(softPath)
			if !live && !trashed {
				return fmt.Errorf("environment %q not found in project %q", name, project)
			}
			prompt := fmt.Sprintf("Soft-delete environment %q (namespace %s)? Its workloads stop (scaled to zero) and its config is kept for restore; PVCs and release history are RETAINED.", name, project+"-"+name)
			if hard {
				prompt = fmt.Sprintf("Delete environment %q (namespace %s)? Its workloads, Services and routes are pruned; PersistentVolumeClaims and release history are RETAINED.", name, project+"-"+name)
			}
			if !confirmDestructive(app, prompt, yes) {
				return fmt.Errorf("aborted")
			}
			if hard {
				if fileExists(path) {
					if err := os.Remove(path); err != nil {
						return err
					}
				}
				if trashed {
					if err := os.Remove(softPath); err != nil {
						return err
					}
				}
				if dirExists(overridesDir) {
					if err := os.RemoveAll(overridesDir); err != nil {
						return err
					}
				}
				app.Infof("Deleted environment %q. Its workloads/routes will be pruned; PVCs and release history are retained.", name)
				return nil
			}
			if trashed {
				app.Infof("Environment %q is already soft-deleted.", name)
				return nil
			}
			if fileExists(path) {
				if err := moveToTrash(path, softPath); err != nil {
					return err
				}
			} else if err := writeYAML(softPath, config.EnvironmentFile{Name: name}); err != nil {
				return err
			}
			app.Infof("Soft-deleted environment %q (workloads stopped; config kept). Restore with `menina environment restore %s --project %s`.", name, name, project)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().BoolVar(&hard, "hard", false, "remove the config and prune workloads instead of soft-deleting")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without prompting")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func environmentRestore(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "restore <name>",
		Short: "Restore a soft-deleted environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := filepath.Join(config.ProjectDir(app.Opts.ConfigDir, project), "environments", name+".yml")
			softPath := config.SoftDeleteEnvironmentPath(app.Opts.ConfigDir, project, name)
			if fileExists(path) {
				return fmt.Errorf("environment %q is not soft-deleted", name)
			}
			if !fileExists(softPath) {
				return fmt.Errorf("no soft-deleted environment %q in project %q", name, project)
			}
			if err := restoreFromTrash(softPath, path); err != nil {
				return err
			}
			app.Infof("Restored environment %q; reconciliation will bring its workloads back.", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
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
