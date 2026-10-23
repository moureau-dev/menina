package client

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func (a *App) profile() string {
	if a.opts.Profile != nil {
		return *a.opts.Profile
	}
	return ""
}

func (a *App) requireClientHost(flagHost string) (string, error) {
	host := a.chooseClientHost(flagHost, a.profile())
	if host == "" {
		return "", fmt.Errorf("--host or --profile is required (or set MENINA_HOST / run `menina apply --host` first)")
	}
	if strings.HasPrefix(host, "-") {
		return "", fmt.Errorf("invalid SSH host %q", host)
	}
	return host, nil
}

func (a *App) runSSHInherit(cmd *cobra.Command, host string, args ...string) error {
	if strings.HasPrefix(host, "-") {
		return fmt.Errorf("invalid SSH host %q", host)
	}
	pin, cleanup := a.sshArgs(host, a.profile())
	defer cleanup()
	full := append([]string{}, pin...)
	full = append(full, "-T", host, "menina")
	full = append(full, args...)
	c := exec.CommandContext(cmd.Context(), "ssh", full...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("ssh %s: %w", host, err)
	}
	return nil
}
