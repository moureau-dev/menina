package host

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

	"github.com/moureau-dev/menina/internal/api"
	"github.com/moureau-dev/menina/internal/api/errors"
	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/deployment"
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
		Long: "Manage app definitions (create/edit/delete), deploy them, stream logs, roll\n" +
			"back and scale.\n\n" +
			"Run without a subcommand on a terminal to open the interactive flow: pick a\n" +
			"project (or create one), pick or create an app, then a menu of\n" +
			"Deploy / Scale / Edit / Logs / Rollback / Delete. Use --project (and\n" +
			"--environment) to skip the prompts. Without a terminal it prints this help.",
		Example: "  menina app --project my-app                 # choose an app, then act on it\n" +
			"  menina app api --project my-app             # action menu for api\n" +
			"  menina app deploy api --project my-app --environment production --yes\n" +
			"  menina app scale api --project my-app --min 2 --max 10\n" +
			"  menina app logs api --project my-app --environment production --follow",
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
	cmd.AddCommand(appCreate(app), appEdit(app), appList(app), appStatus(app), appDeploy(app), appLogs(app), appRollback(app), appDelete(app), appRestore(app), appScale(app), appResetVolume(app), appPromote(app))
	return cmd
}

func appStatus(app *App) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show an app's deployment status (last job, digest, phase)",
		Long: "Show the most recent deployment job, release digest and observed status for\n" +
			"an app. Requires the control plane (`menina serve`).",
		Example: "  menina app status api --project my-app --environment production",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: menina app status <app> --project P --environment E (app status is a host command; run it over SSH)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			client, err := app.Control(cfg)
			if err != nil {
				return fmt.Errorf("control plane not reachable (%v); start it with `menina serve`", err)
			}
			path := fmt.Sprintf("/job-status?project=%s&environment=%s&app=%s",
				url.QueryEscape(project), url.QueryEscape(environment), url.QueryEscape(args[0]))
			var resp map[string]any
			if err := client.GetJSON(cmd.Context(), path, &resp); err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(resp)
			}
			app.Infof("App %s/%s/%s", project, environment, args[0])
			if job, ok := resp["job"].(map[string]any); ok {
				app.Infof("  last job:    %v (%v)", job["Status"], job["Kind"])
				if e, _ := job["LastError"].(string); e != "" {
					app.Infof("  last error:  %s", e)
				}
			} else {
				app.Infof("  last job:    (none)")
			}
			if rel, ok := resp["latestRelease"].(map[string]any); ok {
				app.Infof("  release:     %v (%v)", rel["Digest"], rel["State"])
				app.Infof("  config:      version %v", rel["ConfigVersion"])
			}
			if stale, _ := resp["secretStale"].(bool); stale {
				app.Infof("  secrets:     %v", resp["secretHint"])
			}
			if obs, ok := resp["observed"].(map[string]any); ok {
				app.Infof("  phase:       %v", obs["Status"])
				app.Infof("  observed:    %v", obs["ObservedDigest"])
				app.Infof("  image:       %v", obs["Image"])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func appDelete(app *App) *cobra.Command {
	var project string
	var yes, hard bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Soft-delete an app definition (--hard to remove it)",
		Long: "Delete an app definition.\n\n" +
			"By default this is a soft delete: the app stops (its workload is scaled to\n" +
			"zero) and its config is kept so it can be restored with `menina app\n" +
			"restore`. With --hard the config is removed and the Deployment/Service and\n" +
			"routes are pruned. Either way, PVCs and release history are retained.",
		Example: "  menina app delete api --project my-app\n" +
			"  menina app delete api --project my-app --hard --yes\n" +
			"  menina app restore api --project my-app",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := appPath(app, project, name)
			softPath := config.SoftDeleteAppPath(app.Opts.ConfigDir, project, name)
			live, trashed := fileExists(path), fileExists(softPath)
			if !live && !trashed {
				return fmt.Errorf("app %q not found in project %q", name, project)
			}
			prompt := fmt.Sprintf("Soft-delete app %q? It is stopped (scaled to zero) and its config kept for restore; PVCs and release history are RETAINED.", name)
			if hard {
				prompt = fmt.Sprintf("Delete app %q? Its config is removed and its Deployment/Service and routes are pruned; PVCs and release history are RETAINED.", name)
			}
			if !confirmDestructive(app, prompt, yes) {
				return fmt.Errorf("aborted")
			}
			if hard {
				if live {
					if err := os.Remove(path); err != nil {
						return err
					}
				}
				if trashed {
					if err := os.Remove(softPath); err != nil {
						return err
					}
				}
				app.Infof("Deleted app %q. Its workload will be pruned; PVCs and release history are retained.", name)
				return nil
			}
			if trashed {
				app.Infof("App %q is already soft-deleted.", name)
				return nil
			}
			if err := moveToTrash(path, softPath); err != nil {
				return err
			}
			app.Infof("Soft-deleted app %q (stopped; config kept). Restore with `menina app restore %s --project %s`.", name, name, project)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().BoolVar(&hard, "hard", false, "remove the config and prune the workload instead of soft-deleting")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without prompting")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func appRestore(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "restore <name>",
		Short: "Restore a soft-deleted app definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := appPath(app, project, name)
			softPath := config.SoftDeleteAppPath(app.Opts.ConfigDir, project, name)
			if fileExists(path) {
				return fmt.Errorf("app %q is not soft-deleted", name)
			}
			if !fileExists(softPath) {
				return fmt.Errorf("no soft-deleted app %q in project %q", name, project)
			}
			if err := restoreFromTrash(softPath, path); err != nil {
				return err
			}
			app.Infof("Restored app %q; reconciliation will bring its workload back.", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
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
	var yes, noWait, verify bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "deploy <name>",
		Short: "Deploy an app into an environment",
		Long: "Queue a deployment and wait for the rollout. The control plane must be\n" +
			"running (`menina serve`). Deploying to \"production\" requires --yes\n" +
			"(security.enforceProdPolicy). Use --no-wait to queue and return immediately\n" +
			"(track it with `menina job list` / `menina app status`).",
		Example: "  menina app deploy api --project my-app --environment production --yes\n" +
			"  menina app deploy api --project my-app --environment staging --no-wait",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			if noWait && verify {
				return fmt.Errorf("--verify cannot be combined with --no-wait")
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
				JobID           string `json:"jobId"`
				SnapshotVersion int64  `json:"snapshotVersion"`
			}
			if err := client.PostJSONDecode(cmd.Context(), "/deploy", []byte(body), &res); err != nil {
				return err
			}
			app.Infof("Deployment of %s/%s/%s queued (job %s, config snapshot v%d)", project, environment, args[0], res.JobID, res.SnapshotVersion)
			if noWait {
				app.Infof("Not waiting (--no-wait). Track it with `menina app status %s --project %s --environment %s`.", args[0], project, environment)
				return nil
			}
			app.Infof("Waiting for the rollout...")
			if err := waitForDeploy(cmd.Context(), client, res.JobID, timeout); err != nil {
				return err
			}
			app.Infof("Deployment of %s/%s/%s succeeded", project, environment, args[0])
			if verify {
				if err := verifyDeployed(cmd.Context(), cfg, app, project, environment, args[0]); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm a production deployment")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "queue the deployment and return without waiting for the rollout")
	cmd.Flags().BoolVar(&verify, "verify", false, "after the rollout, check public HTTP routes and TCP L4 ports")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for the rollout")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func appResetVolume(app *App) *cobra.Command {
	var project, environment string
	var yes bool
	cmd := &cobra.Command{
		Use:   "reset-volume <name>",
		Short: "Delete and recreate an app's persistent volume (data is lost)",
		Long: "Scale the app to zero, delete its PersistentVolumeClaim, and queue a\n" +
			"deployment so the volume is recreated empty and the app comes back up.\n" +
			"This destroys the volume's data. The scale-to-zero wait ensures the\n" +
			"running pod has released the volume before it is deleted.",
		Example: "  menina app reset-volume api --project my-app --environment production --yes",
		Args:    cobra.ExactArgs(1),
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
			if resolvedApp.Storage == nil || !resolvedApp.Storage.Enabled {
				return fmt.Errorf("app %q has no persistent storage to reset", args[0])
			}
			prompt := fmt.Sprintf("Delete and recreate the volume for %s/%s/%s? All of its data will be LOST.", project, environment, args[0])
			if !confirmDestructive(app, prompt, yes) {
				return fmt.Errorf("aborted")
			}
			cluster, err := deployment.NewKubernetesCluster(cfg.Kubernetes.Kubeconfig)
			if err != nil {
				return err
			}
			if err := cluster.ScaleWorkload(cmd.Context(), resolvedApp.Namespace, args[0], 0); err != nil {
				return err
			}
			deadline := time.Now().Add(2 * time.Minute)
			for {
				st, err := cluster.WorkloadStatus(cmd.Context(), resolvedApp.Namespace, args[0])
				if err == nil && st.Replicas == 0 {
					break
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("timed out waiting for %s to scale down before resetting the volume", args[0])
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(2 * time.Second):
				}
			}
			if err := cluster.DeletePVC(cmd.Context(), resolvedApp.Namespace, args[0]+"-data"); err != nil {
				return err
			}
			app.Infof("Deleted volume %s/%s-data", resolvedApp.Namespace, args[0])
			if client, err := app.Control(cfg); err == nil {
				body := fmt.Sprintf(`{"project":%q,"environment":%q,"app":%q,"confirm":%t}`, project, environment, args[0], yes)
				var res struct {
					JobID string `json:"jobId"`
				}
				if err := client.PostJSONDecode(cmd.Context(), "/deploy", []byte(body), &res); err != nil {
					app.Infof("Volume deleted, but queueing the redeploy failed: %v", err)
					app.Infof("Run `menina app deploy %s --project %s --environment %s` to bring it back.", args[0], project, environment)
					return nil
				}
				app.Infof("Redeploy queued (job %s); the volume will be recreated empty.", res.JobID)
				return nil
			}
			app.Infof("Volume deleted. Reconciliation will recreate it; or run `menina app deploy %s --project %s --environment %s`.", args[0], project, environment)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the reset without prompting")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func appLogs(app *App) *cobra.Command {
	var project, environment string
	var follow bool
	var tail int64
	var since time.Duration
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
			opts := deployment.LogOptions{Follow: follow, Since: since}
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
	cmd.Flags().DurationVar(&since, "since", 0, "only logs newer than this duration (e.g. 15m)")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func appRollback(app *App) *cobra.Command {
	var project, environment, digest string
	var yes, noWait bool
	var timeout time.Duration
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
				latest, ok, err := db.LatestRelease(cmd.Context(), project, environment, args[0])
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("no release to roll back from for %s/%s/%s", project, environment, args[0])
				}
				previous, ok, err := db.PreviousRelease(cmd.Context(), project, environment, args[0], latest.ID)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("no earlier known-good release to roll back to for %s/%s/%s", project, environment, args[0])
				}
				digest = previous.Digest
				app.Infof("Rolling back to digest %s (release %s)", digest, previous.ID)
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
			app.Infof("Rollback of %s/%s/%s to %s requested (job %s)", project, environment, args[0], digest, res.JobID)
			if noWait {
				app.Infof("Not waiting (--no-wait). Track it with `menina job list`.")
				return nil
			}
			app.Infof("Waiting for the rollout...")
			if err := waitForDeploy(cmd.Context(), client, res.JobID, timeout); err != nil {
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
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "queue the rollback and return without waiting for the rollout")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for the rollout")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

// waitForDeploy polls the control plane until the specific job reaches a
// terminal state, so the CLI reports the actual outcome rather than "queued".
func waitForDeploy(ctx context.Context, client *api.Client, jobID string, timeout time.Duration) error {
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
			return apierrors.New(apierrors.CodeTimeout, fmt.Sprintf("timed out after %s waiting for deployment job %s", timeout, jobID), "retry with a larger --timeout (for example --timeout 10m), or use --no-wait and poll `menina app status`")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
