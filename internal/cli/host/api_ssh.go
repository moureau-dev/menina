package host

import (
	"github.com/spf13/cobra"

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
			client, err := app.Control(cfg)
			if err != nil {
				return err
			}
			return sshbridge.Serve(cmd.Context(), client, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}
