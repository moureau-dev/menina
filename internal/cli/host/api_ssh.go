package host

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/api"
	sshbridge "github.com/moureau-dev/menina/internal/api/transport/ssh"
)

func newAPISSHCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:    "api-ssh",
		Hidden: true,
		Short:  "Bridge stdin JSON API requests over SSH",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			actor := api.Actor{ID: "local", Source: "ssh"}
			if fingerprint := os.Getenv("MENINA_KEY_FINGERPRINT"); fingerprint != "" {
				db, err := app.OpenDB(cfg)
				if err != nil {
					return err
				}
				profile, _, ok, err := db.ResolveKey(cmd.Context(), fingerprint)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("key is not authorized")
				}
				actor = api.Actor{ID: profile.ID, Source: "ssh", KeyFingerprint: fingerprint}
			} else if os.Getenv("SSH_ORIGINAL_COMMAND") != "" {
				return fmt.Errorf("missing authenticated key fingerprint")
			}
			client, err := app.Control(cfg)
			if err != nil {
				return err
			}
			return sshbridge.Serve(cmd.Context(), client, actor, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}
