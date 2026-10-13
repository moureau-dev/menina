package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/deployment"
)

func newAppCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "app", Short: "Manage app definitions"}
	cmd.AddCommand(appCreate(app), appEdit(app), appList(app), appDeploy(app), appLogs(app), appRollback(app))
	return cmd
}

func appPath(app *App, project, name string) string {
	return filepath.Join(config.ProjectDir(app.Opts.ConfigDir, project), "apps", name+".yml")
}

func appCreate(app *App) *cobra.Command {
	var project, repository, workload, runAs, cpu, memory, storageSize, mountPath string
	var port, replicas int
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an app definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := config.EnsureProjectDirs(app.Opts.ConfigDir, project); err != nil {
				return err
			}
			af := config.AppFile{
				Name:      name,
				Image:     config.ImageSpec{Repository: repository},
				Workload:  config.WorkloadSpec{Type: workload, Port: port},
				Resources: config.ResourceSpec{CPU: cpu, Memory: memory},
				Env:       map[string]string{},
			}
			if runAs != "" {
				af.Workload.Command = []string{runAs}
			}
			if replicas > 0 {
				af.Replicas = &replicas
			}
			if storageSize != "" {
				af.Storage = &config.StorageSpec{Enabled: true, Size: storageSize, MountPath: mountPath}
			}
			if err := writeYAML(appPath(app, project, name), af); err != nil {
				return err
			}
			app.Infof("Created app %q in project %q", name, project)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&repository, "repository", "", "image repository")
	cmd.Flags().StringVar(&workload, "type", "service", "workload type (service|worker)")
	cmd.Flags().IntVar(&port, "port", 8080, "service port")
	cmd.Flags().StringVar(&cpu, "cpu", "250m", "CPU request/limit")
	cmd.Flags().StringVar(&memory, "memory", "256Mi", "memory request/limit")
	cmd.Flags().IntVar(&replicas, "replicas", 1, "replica count")
	cmd.Flags().StringVar(&storageSize, "storage", "", "persistent volume size (e.g. 1Gi)")
	cmd.Flags().StringVar(&mountPath, "mount", "/data", "storage mount path")
	cmd.Flags().StringVar(&runAs, "command", "", "container command override")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("repository")
	return cmd
}

func appEdit(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit an app definition in $EDITOR",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := appPath(app, project, args[0])
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			c := exec.Command(editor, path)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func appList(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List app definitions in a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			apps, err := config.LoadApps(config.ProjectsDir(app.Opts.ConfigDir), project)
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(apps)
			}
			for name, a := range apps {
				app.Infof("%-16s %-10s %-40s port=%d", name, a.Workload.Type, a.Image.Repository, a.Workload.Port)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func appDeploy(app *App) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:   "deploy <name>",
		Short: "Deploy an app into an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			client, err := app.Control(cfg)
			if err != nil {
				return fmt.Errorf("control plane not reachable (%v); start it with `menina serve`", err)
			}
			body := fmt.Sprintf(`{"project":%q,"environment":%q,"app":%q}`, project, environment, args[0])
			if err := client.PostJSON(cmd.Context(), "/deploy", []byte(body)); err != nil {
				return err
			}
			app.Infof("Deployment of %s/%s/%s queued", project, environment, args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func appLogs(app *App) *cobra.Command {
	var project, environment string
	var follow bool
	var tail int64
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Retrieve workload logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			projects, err := config.LoadAllProjects(config.ProjectsDir(app.Opts.ConfigDir))
			if err != nil {
				return err
			}
			resolved, err := config.NewResolver(cfg).Resolve(projects, 0)
			if err != nil {
				return err
			}
			resolvedApp, err := resolved.FindApp(project, environment, args[0])
			if err != nil {
				return err
			}
			cluster, err := deployment.NewKubernetesCluster(cfg.Kubernetes.Kubeconfig)
			if err != nil {
				return err
			}
			opts := deployment.LogOptions{Follow: follow}
			if cmd.Flags().Changed("tail") {
				opts.TailLines = &tail
			}
			stream, err := cluster.PodLogs(cmd.Context(), resolvedApp.Namespace, args[0], opts)
			if err != nil {
				return err
			}
			defer stream.Close()
			_, err = io.Copy(os.Stdout, stream)
			return err
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	cmd.Flags().BoolVar(&follow, "follow", false, "follow log output")
	cmd.Flags().Int64Var(&tail, "tail", 200, "number of lines from the end")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func appRollback(app *App) *cobra.Command {
	var project, environment, digest string
	cmd := &cobra.Command{
		Use:   "rollback <name>",
		Short: "Restore a previously recorded immutable image digest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			if digest == "" {
				db, err := app.OpenDB(cfg)
				if err != nil {
					return err
				}
				releases, err := db.Releases(cmd.Context(), project, environment, args[0])
				if err != nil {
					return err
				}
				if len(releases) < 2 {
					return fmt.Errorf("no earlier release to roll back to for %s/%s/%s", project, environment, args[0])
				}
				digest = releases[1].Digest
				app.Infof("Rolling back to digest %s (release #%d)", digest, releases[1].ID)
			}
			client, err := app.Control(cfg)
			if err != nil {
				return fmt.Errorf("control plane not reachable (%v); start it with `menina serve`", err)
			}
			body := fmt.Sprintf(`{"project":%q,"environment":%q,"app":%q,"digest":%q}`, project, environment, args[0], digest)
			if err := client.PostJSON(cmd.Context(), "/rollback", []byte(body)); err != nil {
				return err
			}
			app.Infof("Rollback of %s/%s/%s to %s queued", project, environment, args[0], digest)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	cmd.Flags().StringVar(&digest, "digest", "", "image digest to restore")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}
