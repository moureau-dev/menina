package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/platform"
)

func newAppCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "app", Short: "Manage app definitions"}
	cmd.AddCommand(appCreate(app), appEdit(app), appList(app), appDeploy(app), appLogs(app), appRollback(app), appDelete(app))
	return cmd
}

func appDelete(app *App) *cobra.Command {
	var project string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an app definition (workload pruned; data retained)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := appPath(app, project, args[0])
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("app %q not found in project %q", args[0], project)
			}
			prompt := fmt.Sprintf("Delete app %q? Its Deployment/Service and routes are pruned; PersistentVolumeClaims and release history are RETAINED.", args[0])
			if !confirmDestructive(app, prompt, yes) {
				return fmt.Errorf("aborted")
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			app.Infof("Deleted app %q. Its workload will be pruned; PVCs and release history are retained.", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without prompting")
	_ = cmd.MarkFlagRequired("project")
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
			var res struct {
				JobID string `json:"jobId"`
			}
			if err := client.PostJSONDecode(cmd.Context(), "/deploy", []byte(body), &res); err != nil {
				return err
			}
			app.Infof("Deployment of %s/%s/%s queued; waiting for the rollout...", project, environment, args[0])
			if err := waitForDeploy(cmd.Context(), client, res.JobID, 5*time.Minute); err != nil {
				return err
			}
			app.Infof("Deployment of %s/%s/%s succeeded", project, environment, args[0])
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
			var res struct {
				JobID string `json:"jobId"`
			}
			if err := client.PostJSONDecode(cmd.Context(), "/rollback", []byte(body), &res); err != nil {
				return err
			}
			app.Infof("Rollback of %s/%s/%s to %s requested; waiting for the rollout...", project, environment, args[0], digest)
			if err := waitForDeploy(cmd.Context(), client, res.JobID, 5*time.Minute); err != nil {
				return err
			}
			app.Infof("Rollback complete: %s is running %s", args[0], digest)
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

// waitForDeploy polls the control plane until the specific job reaches a
// terminal state, so the CLI reports the actual outcome rather than "queued".
func waitForDeploy(ctx context.Context, client *platform.ControlClient, jobID string, timeout time.Duration) error {
	if jobID == "" {
		return fmt.Errorf("control plane did not return a job id; cannot confirm the rollout")
	}
	deadline := time.Now().Add(timeout)
	path := "/job-status?id=" + url.QueryEscape(jobID)
	for {
		var resp struct {
			Found bool `json:"found"`
			Job   struct {
				Status    string `json:"Status"`
				LastError string `json:"LastError"`
			} `json:"job"`
		}
		if err := client.GetJSON(ctx, path, &resp); err == nil && resp.Found {
			switch resp.Job.Status {
			case "done":
				return nil
			case "failed":
				return fmt.Errorf("deployment failed: %s (the previously running version, if healthy, remains available)", resp.Job.LastError)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for the deployment to complete; check `menina doctor` and logs")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
