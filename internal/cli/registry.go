package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/registry"
	"github.com/moureau-dev/menina/internal/secrets"
)

func newRegistryCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "registry", Short: "Inspect and manage the image registry"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "status",
			Short: "Check Zot health and storage",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := app.LoadConfig()
				if err != nil {
					return err
				}
				client, err := registryClient(cfg)
				if err != nil {
					return err
				}
				if err := client.Ping(context.Background()); err != nil {
					return err
				}
				out := map[string]any{"domain": cfg.Registry.Domain, "status": "ok"}
				if app.Opts.JSON {
					return app.PrintJSON(out)
				}
				app.Infof("Registry %s: %s", cfg.Registry.Domain, out["status"])
				return nil
			},
		},
		registryCredentialCommand(app),
	)
	return cmd
}

func registryCredentialCommand(app *App) *cobra.Command {
	var regenerate bool
	var username, password string
	cmd := &cobra.Command{
		Use:   "credential",
		Short: "Show or regenerate the registry push credential",
		Long: "Prints the username and password used to push images to Zot.\n" +
			"With --regenerate, a new random password is generated, stored with 0600\n" +
			"permissions, written to config.yml as a secret reference, and applied to\n" +
			"Zot and the k3s containerd credentials (rolling managed workloads).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			if !regenerate {
				if !cfg.Registry.Auth.Enabled {
					return fmt.Errorf("registry push credentials are not enabled; run `menina registry credential --regenerate`")
				}
				user, pass, err := resolveRegistryCreds(cfg)
				if err != nil {
					return err
				}
				if app.Opts.JSON {
					return app.PrintJSON(map[string]string{"domain": cfg.Registry.Domain, "username": user, "password": pass})
				}
				app.Infof("Registry:  %s", cfg.Registry.Domain)
				app.Infof("Username:  %s", user)
				app.Infof("Password:  %s", pass)
				return nil
			}

			if username == "" {
				username = orDefault(cfg.Registry.Auth.Username, "menina")
			}
			if password == "" {
				password, err = secrets.GeneratePassword(32)
				if err != nil {
					return err
				}
			}
			path := registryPasswordPath(cfg)
			if err := secrets.WriteSecretFile(path, password); err != nil {
				return err
			}
			cfg.Registry.Auth = config.RegistryAuth{
				Enabled:     true,
				Username:    username,
				PasswordRef: "file:" + path,
			}
			if err := config.SavePlatform(cfg.Host.ConfigDir, cfg); err != nil {
				return err
			}
			app.Infof("Stored registry credential in %s and updated config.yml", path)
			if err := applyRegistryCredentials(cmd.Context(), app, cfg); err != nil {
				app.Log.Warn("could not fully apply the new credential; run `menina deploy` to reconcile", "error", err)
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]string{"domain": cfg.Registry.Domain, "username": username, "password": password})
			}
			app.Infof("Registry:  %s", cfg.Registry.Domain)
			app.Infof("Username:  %s", username)
			app.Infof("Password:  %s", password)
			app.Infof("Use with:  docker login %s -u %s", cfg.Registry.Domain, username)
			return nil
		},
	}
	cmd.Flags().BoolVar(&regenerate, "regenerate", false, "generate and apply a new random password")
	cmd.Flags().StringVar(&username, "username", "", "registry username (default: current or \"menina\")")
	cmd.Flags().StringVar(&password, "password", "", "explicit password instead of a generated one")
	return cmd
}

func registryClient(cfg *config.Config) (*registry.Client, error) {
	username, password, err := resolveRegistryCreds(cfg)
	if err != nil {
		return nil, err
	}
	scheme := "https"
	if !cfg.TLS.Terminate {
		scheme = "http"
	}
	base := scheme + "://" + cfg.Registry.Domain
	return registry.NewClient(base, username, password, !cfg.TLS.Terminate), nil
}
