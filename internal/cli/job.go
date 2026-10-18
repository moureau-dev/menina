package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/clierr"
)

// jobView is the subset of a deployment job shown by the CLI. The control plane
// serialises database.Job with its Go field names.
type jobView struct {
	ID            string    `json:"ID"`
	Kind          string    `json:"Kind"`
	Project       string    `json:"Project"`
	Environment   string    `json:"Environment"`
	App           string    `json:"App"`
	Status        string    `json:"Status"`
	Attempts      int       `json:"Attempts"`
	LastError     string    `json:"LastError"`
	ConfigVersion int64     `json:"ConfigVersion"`
	ReleaseID     string    `json:"ReleaseID"`
	ExitCode      int       `json:"ExitCode"`
	UpdatedAt     time.Time `json:"UpdatedAt"`
}

type jobStreamEvent struct {
	EventID  int64   `json:"eventId"`
	Terminal bool    `json:"terminal"`
	Job      jobView `json:"job"`
}

func newJobCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Inspect and cancel deployment jobs",
		Long: "Inspect the deployment job queue and cancel a pending or processing job.\n" +
			"Requires the control plane (`menina serve`).",
		Example: "  menina job list\n" +
			"  menina job list --status pending\n" +
			"  menina job cancel <id>",
	}
	cmd.AddCommand(jobList(app), jobCancel(app), jobWatch(app))
	return cmd
}

func jobList(app *App) *cobra.Command {
	var status string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List deployment jobs (newest first)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			client, err := app.Control(cfg)
			if err != nil {
				return fmt.Errorf("control plane not reachable (%v); start it with `menina serve`", err)
			}
			q := url.Values{}
			if status != "" {
				q.Set("status", status)
			}
			if limit > 0 {
				q.Set("limit", fmt.Sprintf("%d", limit))
			}
			path := "/jobs"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			var resp struct {
				Jobs []jobView `json:"jobs"`
			}
			if err := client.GetJSON(cmd.Context(), path, &resp); err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(resp.Jobs)
			}
			if len(resp.Jobs) == 0 {
				app.Infof("No jobs.")
				return nil
			}
			app.Infof("%-38s %-11s %-9s %-30s %s", "ID", "STATUS", "KIND", "TARGET", "UPDATED")
			for _, j := range resp.Jobs {
				target := fmt.Sprintf("%s/%s/%s", j.Project, j.Environment, j.App)
				app.Infof("%-38s %-11s %-9s %-30s %s", j.ID, j.Status, j.Kind, target, j.UpdatedAt.Format(time.RFC3339))
				if j.LastError != "" {
					app.Infof("    error: %s", j.LastError)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status (pending, processing, done, failed, superseded, cancelled)")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum number of jobs to show")
	return cmd
}

func jobCancel(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a pending or processing deployment job",
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
			body := fmt.Sprintf(`{"id":%q}`, args[0])
			var res struct {
				Cancelled bool `json:"cancelled"`
			}
			if err := client.PostJSONDecode(cmd.Context(), "/jobs/cancel", []byte(body), &res); err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]any{"id": args[0], "cancelled": res.Cancelled})
			}
			if !res.Cancelled {
				return fmt.Errorf("job %q is not cancellable (not pending or processing, or not found)", args[0])
			}
			app.Infof("Cancelled job %s", args[0])
			return nil
		},
	}
	return cmd
}

func jobWatch(app *App) *cobra.Command {
	var timeout time.Duration
	var cursor int64
	cmd := &cobra.Command{
		Use:   "watch <id>",
		Short: "Stream a deployment job until it reaches a terminal state",
		Long: "Reads NDJSON job events from the control plane. --cursor resumes after\n" +
			"the last event ID (for example after an SSH drop). The final event carries\n" +
			"terminal=true and the remote exit code. With --json each event is emitted\n" +
			"as a standalone JSON object.",
		Example: "  menina job watch <id>\n  menina job watch <id> --json --timeout 10m\n  menina job watch <id> --cursor 42",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			client, err := app.Control(cfg)
			if err != nil {
				return fmt.Errorf("control plane not reachable (%v); start it with `menina serve`", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			watchPath := "/jobs/watch?id=" + url.QueryEscape(args[0])
			if cursor > 0 {
				watchPath += fmt.Sprintf("&cursor=%d", cursor)
			}
			body, err := client.GetStream(ctx, watchPath)
			if err != nil {
				if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
					return clierr.Wrap(clierr.CodeNotFound, err)
				}
				return err
			}
			defer body.Close()
			scanner := bufio.NewScanner(body)
			scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
			lastStatus := ""
			enc := json.NewEncoder(os.Stdout)
			for scanner.Scan() {
				line := scanner.Bytes()
				if len(line) == 0 {
					continue
				}
				var event jobStreamEvent
				if err := json.Unmarshal(line, &event); err != nil {
					continue
				}
				job := event.Job
				if app.Opts.JSON {
					if err := enc.Encode(event); err != nil {
						return err
					}
				} else if job.Status != lastStatus || job.LastError != "" {
					app.Infof("%s %s (event %d, config v%d)", job.ID, job.Status, event.EventID, job.ConfigVersion)
					if job.ReleaseID != "" {
						app.Infof("  release: %s", job.ReleaseID)
					}
					if job.LastError != "" {
						app.Infof("  error: %s", job.LastError)
					}
				}
				lastStatus = job.Status
				if !event.Terminal {
					continue
				}
				switch job.Status {
				case "done":
					return nil
				case "failed", "cancelled", "superseded":
					if job.LastError != "" {
						return fmt.Errorf("job %s %s (exit %d): %s", job.ID, job.Status, job.ExitCode, job.LastError)
					}
					return fmt.Errorf("job %s %s (exit %d)", job.ID, job.Status, job.ExitCode)
				}
			}
			if err := scanner.Err(); err != nil {
				if ctx.Err() != nil {
					return clierr.New(clierr.CodeTimeout, fmt.Sprintf("timed out watching job %s", args[0]), "increase --timeout or use `menina app status`")
				}
				return err
			}
			return fmt.Errorf("job stream ended before a terminal state")
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to watch before failing")
	cmd.Flags().Int64Var(&cursor, "cursor", 0, "resume after this event ID")
	return cmd
}
