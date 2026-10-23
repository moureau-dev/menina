package host

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/core/identity"
)

var forcedAllowed = map[string]bool{
	"app": true, "release": true, "job": true, "secret": true, "config": true,
	"status": true, "routes": true, "notify": true, "registry": true,
	"apply": true, "api-ssh": true, "version": true, "capabilities": true,
}

func newSSHForcedCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:    "ssh-forced",
		Hidden: true,
		Short:  "Map an authenticated SSH key to an actor and run an allowlisted command",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fingerprint := strings.TrimSpace(os.Getenv("MENINA_KEY_FINGERPRINT"))
			if fingerprint == "" {
				return fmt.Errorf("missing authenticated key fingerprint")
			}
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			db, err := app.OpenDB(cfg)
			if err != nil {
				return err
			}
			profile, key, ok, err := db.ResolveKey(cmd.Context(), fingerprint)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("key is not authorized")
			}
			forced, err := parseForcedCommand(os.Getenv("SSH_ORIGINAL_COMMAND"))
			if err != nil {
				return err
			}
			if forced[0] != "api-ssh" {
				if err := identity.CheckCommand(profile, forced); err != nil {
					return err
				}
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			childArgs := append([]string{"--config-dir", app.Opts.ConfigDir}, forced...)
			child := exec.Command(exe, childArgs...)
			child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
			child.Env = append(os.Environ(),
				"MENINA_ACTOR_ID="+profile.ID,
				"MENINA_ACTOR_SOURCE=ssh",
				"MENINA_PROFILE_ID="+profile.ID,
				"MENINA_KEY_FINGERPRINT="+key.Fingerprint,
			)
			return child.Run()
		},
	}
}

func parseForcedCommand(original string) ([]string, error) {
	args := strings.Fields(original)
	if len(args) == 0 {
		return nil, fmt.Errorf("no command requested")
	}
	if args[0] == "menina" {
		args = args[1:]
	}
	if len(args) == 0 || !forcedAllowed[args[0]] {
		return nil, fmt.Errorf("command is not allowed")
	}
	return args, nil
}
