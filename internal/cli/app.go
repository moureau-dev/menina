package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/platform"
)

func requireProdConfirmation(cfg *config.Config, environment string, yes bool) error {
	if cfg.Security.EnforceProdPolicy && strings.EqualFold(environment, "production") && !yes {
		return fmt.Errorf("deploying to the %q environment requires --yes (security.enforceProdPolicy)", environment)
	}
	return nil
}

func newAppCommand(app *App) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:   "app [name]",
		Short: "Manage app definitions (interactive when run without a subcommand)",
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
				if name, err = selectApp(app, p, proj); err != nil {
					return err
				}
			}
			if !p.enabled {
				return cmd.Help()
			}
			return appMenu(cmd.Context(), app, p, proj, name, environment)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment for the action menu")
	cmd.AddCommand(appCreate(app), appEdit(app), appList(app), appDeploy(app), appLogs(app), appRollback(app), appDelete(app), appScale(app))
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
	var yes bool
	cmd := &cobra.Command{
		Use:   "deploy <name>",
		Short: "Deploy an app into an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			if err := requireProdConfirmation(cfg, environment, yes); err != nil {
				return err
			}
			client, err := app.Control(cfg)
			if err != nil {
				return fmt.Errorf("control plane not reachable (%v); start it with `menina serve`", err)
			}
			body := fmt.Sprintf(`{"project":%q,"environment":%q,"app":%q,"confirm":%t}`, project, environment, args[0], yes)
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
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm a production deployment")
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
	var yes bool
	cmd := &cobra.Command{
		Use:   "rollback <name>",
		Short: "Restore a previously recorded immutable image digest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			if err := requireProdConfirmation(cfg, environment, yes); err != nil {
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
			body := fmt.Sprintf(`{"project":%q,"environment":%q,"app":%q,"digest":%q,"confirm":%t}`, project, environment, args[0], digest, yes)
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
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm a production rollback")
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
