package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
)

func newEnvironmentCommand(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "environment [name]",
		Short: "Manage project environments (interactive without a subcommand)",
		Args:  cobra.MaximumNArgs(1),
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
	cmd.AddCommand(environmentCreate(app), environmentList(app), environmentDelete(app))
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
