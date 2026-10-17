package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/clierr"
	"github.com/moureau-dev/menina/internal/config"
)

func newConfigCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Validate and inspect configuration",
		Long:  "Validate the platform configuration and every project/environment without applying anything.",
	}
	cmd.AddCommand(configValidate(app), configRevisions(app), configRollback(app))
	return cmd
}

func configValidate(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate config.yml and every project/environment",
		Long: "Load and resolve the whole configuration tree and report whether it is valid,\n" +
			"without touching the cluster. Exits non-zero when the configuration is invalid.\n" +
			"Useful after editing YAML, since the running control plane keeps its last\n" +
			"valid snapshot and rejects errors silently otherwise.",
		Example: "  menina config validate\n  menina config validate --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			fail := func(err error, result map[string]any) error {
				result["valid"] = false
				result["error"] = err.Error()
				if app.Opts.JSON {
					_ = app.PrintJSON(result)
				} else {
					app.Infof("invalid: %v", err)
				}
				return err
			}
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			result := map[string]any{"configDir": cfg.Host.ConfigDir}
			if err := config.ValidatePlatform(cfg); err != nil {
				return fail(err, result)
			}
			projects, err := config.LoadAllProjects(config.ProjectsDir(app.Opts.ConfigDir))
			if err != nil {
				return fail(err, result)
			}
			rc, err := config.NewResolver(cfg).Resolve(projects, 0)
			if err != nil {
				return fail(err, result)
			}
			if err := config.ValidateResolved(rc); err != nil {
				return fail(err, result)
			}
			envCount := 0
			for _, p := range rc.Projects {
				envCount += len(p.Environments)
			}
			result["valid"] = true
			result["projects"] = len(rc.Projects)
			result["environments"] = envCount
			if app.Opts.JSON {
				return app.PrintJSON(result)
			}
			app.Infof("Configuration OK: %d project(s), %d environment(s)", len(rc.Projects), envCount)
			return nil
		},
	}
}

func configRevisions(app *App) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:     "revisions",
		Short:   "List apply revisions (pre-apply snapshots)",
		Example: "  menina config revisions\n  menina config revisions --project my-app --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			revisions, err := config.ListRevisions(app.Opts.ConfigDir)
			if err != nil {
				return err
			}
			filtered := revisions[:0]
			for _, rev := range revisions {
				if project != "" && rev.Project != project {
					continue
				}
				if environment != "" && rev.Environment != environment {
					continue
				}
				filtered = append(filtered, rev)
			}
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].CreatedAt.After(filtered[j].CreatedAt) })
			if app.Opts.JSON {
				return app.PrintJSON(filtered)
			}
			if len(filtered) == 0 {
				app.Infof("No revisions.")
				return nil
			}
			app.Infof("%-30s %-24s %-14s %s", "ID", "CREATED", "PROJECT/ENV", "FILES")
			for _, rev := range filtered {
				app.Infof("%-30s %-24s %-14s %d", rev.ID, rev.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), rev.Project+"/"+rev.Environment, rev.Files)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "filter by project")
	cmd.Flags().StringVar(&environment, "environment", "", "filter by environment")
	return cmd
}

func configRollback(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rollback <revision-id>",
		Short: "Restore configuration files to a previous apply revision",
		Long: "Restore the files changed by an apply (including files created or deleted)\n" +
			"to their state before that revision. This writes desired-state YAML; it does\n" +
			"not roll back deployed images.",
		Example: "  menina config rollback 20261017T211500.000000000 --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirmDestructive(app, fmt.Sprintf("Restore configuration revision %q?", args[0]), yes) {
				return fmt.Errorf("aborted")
			}
			if err := config.RestoreRevision(app.Opts.ConfigDir, args[0]); err != nil {
				return clierr.Wrap(clierr.CodeNotFound, err)
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]any{"restored": args[0]})
			}
			app.Infof("Restored configuration revision %s", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the restore")
	return cmd
}
