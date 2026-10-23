package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newEnrollCommand(app *App) *cobra.Command {
	var host, hostKey, publicKey string
	cmd := &cobra.Command{
		Use:   "enroll <token>",
		Args:  cobra.ExactArgs(1),
		Short: "Consume an enrollment token and attach this client's public key",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := app.requireClientHost(host)
			if err != nil {
				return err
			}
			key, err := readPublicKey(publicKey)
			if err != nil {
				return err
			}
			name := app.contextName(app.profile())
			if name == "" {
				name = sanitizeContextName(target)
			}
			if err := app.addContext(name, Context{Host: target, HostKey: hostKey}); err != nil {
				return err
			}
			if err := app.runSSHWithInput(cmd, target, []byte(args[0]), "identity", "enroll", "consume", "-", "--public-key", key, "--comment", "client"); err != nil {
				return err
			}
			return app.runSSHInherit(cmd, target, "whoami")
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "SSH host (user@host)")
	cmd.Flags().StringVar(&hostKey, "host-key", "", "pinned host public key")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "public key file (default: first ~/.ssh/id_*.pub)")
	return cmd
}

func readPublicKey(path string) (string, error) {
	if path == "" {
		home, _ := os.UserHomeDir()
		for _, name := range []string{"id_ed25519.pub", "id_rsa.pub", "id_ecdsa.pub"} {
			candidate := filepath.Join(home, ".ssh", name)
			if _, err := os.ReadFile(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		return "", fmt.Errorf("no public key found; pass --public-key")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("public key %s is empty", path)
	}
	return key, nil
}

func sanitizeContextName(host string) string {
	name := strings.ReplaceAll(host, "@", "-")
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, ".", "-")
	if name == "" {
		return "default"
	}
	return name
}
