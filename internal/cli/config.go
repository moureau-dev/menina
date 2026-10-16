package cli

import (
	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
)

func newConfigCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Validate and inspect configuration",
		Long:  "Validate the platform configuration and every project/environment without applying anything.",
	}
	cmd.AddCommand(configValidate(app))
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
