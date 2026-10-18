package clientcli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newClientJobCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Inspect and watch deployment jobs over SSH",
	}
	cmd.AddCommand(clientJobList(app), clientJobWatch(app))
	return cmd
}

func requireClientHost(flagHost string) (string, error) {
	host := chooseClientHost(flagHost)
	if host == "" {
		return "", fmt.Errorf("--host is required (or set MENINA_HOST / run `menina apply --host` first)")
	}
	if strings.HasPrefix(host, "-") {
		return "", fmt.Errorf("invalid SSH host %q", host)
	}
	return host, nil
}

func clientJobList(app *App) *cobra.Command {
	var host, status string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List deployment jobs on the host",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := requireClientHost(host)
			if err != nil {
				return err
			}
			remote := []string{"job", "list"}
			if status != "" {
				remote = append(remote, "--status", status)
			}
			if limit > 0 {
				remote = append(remote, "--limit", fmt.Sprintf("%d", limit))
			}
			if app.jsonOutput() {
				remote = append(remote, "--json")
			}
			return runSSHInherit(cmd, target, remote...)
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "SSH host (user@host)")
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum number of jobs")
	return cmd
}

func clientJobWatch(app *App) *cobra.Command {
	var host string
	var timeout time.Duration
	var cursor int64
	cmd := &cobra.Command{
		Use:   "watch <id>",
		Short: "Stream a deployment job from the host (NDJSON with --json)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := requireClientHost(host)
			if err != nil {
				return err
			}
			remote := []string{"job", "watch", args[0], "--timeout", timeout.String()}
			if cursor > 0 {
				remote = append(remote, "--cursor", fmt.Sprintf("%d", cursor))
			}
			if app.jsonOutput() {
				remote = append(remote, "--json")
			}
			return runSSHInherit(cmd, target, remote...)
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "SSH host (user@host)")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to watch before failing")
	cmd.Flags().Int64Var(&cursor, "cursor", 0, "resume after this event ID")
	return cmd
}

func runSSHInherit(cmd *cobra.Command, host string, args ...string) error {
	full := append([]string{"-T", host, "menina"}, args...)
	c := exec.CommandContext(cmd.Context(), "ssh", full...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("ssh %s: %w", host, err)
	}
	return nil
}
