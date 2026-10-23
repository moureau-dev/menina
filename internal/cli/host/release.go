package host

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/clierr"
)

func newReleaseCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Inspect per-app and per-environment releases",
		Long: "Inspect release state. An environment release is an aggregate of independent\n" +
			"app releases, not an atomic rollout: one failed app never hides behind a\n" +
			"global success.",
	}
	cmd.AddCommand(releaseStatus(app), releaseHistory(app))
	return cmd
}

func releaseStatus(app *App) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the aggregate environment release",
		Example: "  menina release status --project my-app --environment production\n" +
			"  menina release status --project my-app --environment staging --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			client, err := app.Control(cfg)
			if err != nil {
				return fmt.Errorf("control plane not reachable (%v); start it with `menina serve`", err)
			}
			path := fmt.Sprintf("/release?project=%s&environment=%s", url.QueryEscape(project), url.QueryEscape(environment))
			var resp map[string]any
			if err := client.GetJSON(cmd.Context(), path, &resp); err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(resp)
			}
			app.Infof("release %s/%s: %v", project, environment, resp["status"])
			components, _ := resp["components"].([]any)
			for _, raw := range components {
				c, _ := raw.(map[string]any)
				message, _ := c["message"].(string)
				line := fmt.Sprintf("  %-20v %-14v", c["app"], c["state"])
				if message != "" {
					line += " " + message
				}
				app.Infof("%s", line)
				app.Infof("  %-20s code=%v config=%v data=%v", "", c["codeState"], c["configState"], c["dataState"])
			}
			app.Infof("  code:   %v", resp["code"])
			app.Infof("  config: %v", resp["config"])
			app.Infof("  data:   %v", resp["data"])
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func releaseHistory(app *App) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:   "history <app>",
		Short: "Show an app's release history",
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
			path := fmt.Sprintf("/release?project=%s&environment=%s&app=%s",
				url.QueryEscape(project), url.QueryEscape(environment), url.QueryEscape(args[0]))
			var resp struct {
				Releases []map[string]any `json:"releases"`
			}
			if err := client.GetJSON(cmd.Context(), path, &resp); err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(resp.Releases)
			}
			if len(resp.Releases) == 0 {
				app.Infof("No releases.")
				return nil
			}
			app.Infof("%-28s %-14s %-22s %-12s %s", "CREATED", "STATE", "DIGEST", "CONFIG", "MESSAGE")
			for _, r := range resp.Releases {
				app.Infof("%-28v %-14v %-22v %-12v %v", r["CreatedAt"], r["State"], r["Digest"], r["ConfigVersion"], r["Message"])
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

func appPromote(app *App) *cobra.Command {
	var project, from, to, stateToken string
	var yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "promote <app>",
		Short: "Promote an existing digest between environments without rebuilding",
		Long: "Move the active image digest of an app from one environment to another.\n" +
			"Promotion never rebuilds. Review the target plan with --dry-run, then pass\n" +
			"its --state-token to guard against config changing between review and apply.",
		Example: "  menina app promote api --project my-app --from staging --to production --dry-run\n" +
			"  menina app promote api --project my-app --from staging --to production --yes",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			client, err := app.Control(cfg)
			if err != nil {
				return fmt.Errorf("control plane not reachable (%v); start it with `menina serve`", err)
			}
			if !dryRun && !yes {
				return clierr.WithHint(fmt.Errorf("promotion changes a running environment"), "review with --dry-run, then pass --yes (and optionally --state-token)")
			}
			body := fmt.Sprintf(`{"project":%q,"fromEnvironment":%q,"toEnvironment":%q,"app":%q,"stateToken":%q,"dryRun":%v,"confirm":%v}`,
				project, from, to, args[0], stateToken, dryRun, yes)
			var resp map[string]any
			if err := client.PostJSONDecode(cmd.Context(), "/promote", []byte(body), &resp); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "conflict") {
					return clierr.Wrap(clierr.CodeConflict, err)
				}
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(resp)
			}
			plan, _ := resp["plan"].(map[string]any)
			if plan == nil {
				return fmt.Errorf("control plane returned no promotion plan")
			}
			app.Infof("Promotion plan %s/%s: %s -> %s", project, args[0], from, to)
			app.Infof("  digest:       %v", plan["digest"])
			app.Infof("  source:       %v", plan["sourceRelease"])
			app.Infof("  config:       version %v", plan["configVersion"])
			app.Infof("  state token:  %v", plan["stateToken"])
			if dryRun {
				app.Infof("Dry run: nothing queued. Re-run with --yes --state-token %v to promote.", plan["stateToken"])
				return nil
			}
			app.Infof("Queued promotion job %v", resp["jobId"])
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&from, "from", "", "source environment")
	cmd.Flags().StringVar(&to, "to", "", "target environment")
	cmd.Flags().StringVar(&stateToken, "state-token", "", "expected target state token from a reviewed dry run")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the promotion")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the target plan without queueing")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}
