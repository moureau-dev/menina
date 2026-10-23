package host

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/core/identity"
	"github.com/moureau-dev/menina/internal/provision"
)

func identityBootstrapCommand(app *App) *cobra.Command {
	var ttl string
	cmd := &cobra.Command{
		Use:   "bootstrap <profile>",
		Args:  cobra.ExactArgs(1),
		Short: "Create a profile and print a one-paste client enrollment command",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			db, err := app.OpenDB(cfg)
			if err != nil {
				return err
			}
			profiles, err := db.Profiles(cmd.Context())
			if err != nil {
				return err
			}
			var profile identity.Profile
			found := false
			for _, p := range profiles {
				if p.Name == args[0] {
					profile, found = p, true
					break
				}
			}
			if !found {
				id, err := db.CreateProfile(cmd.Context(), identity.Profile{Name: args[0], Scopes: []string{string(identity.ScopeAdmin)}, Status: identity.StatusActive})
				if err != nil {
					return err
				}
				p, ok, err := db.ProfileByID(cmd.Context(), id)
				if err != nil || !ok {
					return fmt.Errorf("load created profile: %w", err)
				}
				profile = p
			}
			duration := identity.DefaultEnrollmentTTL
			if ttl != "" {
				duration, err = time.ParseDuration(ttl)
				if err != nil {
					return err
				}
			}
			token, stored, err := identity.NewEnrollmentToken(profile.ID, duration)
			if err != nil {
				return err
			}
			if _, err := db.CreateEnrollmentToken(cmd.Context(), stored); err != nil {
				return err
			}
			host := cfg.Networking.PublicIP
			if host == "" {
				host, _ = os.Hostname()
			}
			user := os.Getenv("SUDO_USER")
			if user == "" {
				user = "root"
			}
			sshHost := user + "@" + host
			hostKey, fingerprint, keyErr := provision.HostKey("/etc/ssh")
			command := fmt.Sprintf("menina enroll %s --host %s", token, sshHost)
			if keyErr == nil {
				command += fmt.Sprintf(" --host-key %q", hostKey)
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]any{
					"profileId": profile.ID, "profile": profile.Name, "token": token,
					"host": sshHost, "hostKey": hostKey, "hostKeyFingerprint": fingerprint,
					"expiresAt": stored.ExpiresAt, "command": command,
				})
			}
			app.Infof("Reachable host: %s", sshHost)
			if keyErr == nil {
				app.Infof("Host key: %s", fingerprint)
			}
			app.Infof("Enrollment token (shown once): %s", token)
			app.Infof("Expires: %s", stored.ExpiresAt.Format(time.RFC3339))
			app.Infof("Client command:")
			app.Infof("  %s", command)
			return nil
		},
	}
	cmd.Flags().StringVar(&ttl, "ttl", "", "token lifetime, default 15m")
	return cmd
}
