package cli

import (
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

// jobView is the subset of a deployment job shown by the CLI. The control plane
// serialises database.Job with its Go field names.
type jobView struct {
	ID          string    `json:"ID"`
	Kind        string    `json:"Kind"`
	Project     string    `json:"Project"`
	Environment string    `json:"Environment"`
	App         string    `json:"App"`
	Status      string    `json:"Status"`
	Attempts    int       `json:"Attempts"`
	LastError   string    `json:"LastError"`
	UpdatedAt   time.Time `json:"UpdatedAt"`
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
	cmd.AddCommand(jobList(app), jobCancel(app))
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
