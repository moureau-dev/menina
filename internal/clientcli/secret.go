package clientcli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newSecretCommand(app *App) *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage app secrets on the host over SSH",
		Long: "Set, list and remove app secret values on the host. Values are read from\n" +
			"stdin (or a hidden prompt) and piped to `menina secret ...` over SSH; they\n" +
			"never go through the manifest or the client configuration.",
		Example: "  printf '%s' \"$DATABASE_URL\" | menina secret set DATABASE_URL --host root@vps --project demo --environment production\n" +
			"  menina secret list --host root@vps --project demo --environment production\n" +
			"  menina secret remove DATABASE_URL --host root@vps --project demo --environment production --yes",
	}
	cmd.PersistentFlags().StringVar(&host, "host", "", "SSH host (user@host); defaults to MENINA_HOST or the cached host")
	cmd.AddCommand(
		clientSecretSet(app, &host),
		clientSecretList(app, &host),
		clientSecretRemove(app, &host),
		clientSecretVerify(app, &host),
	)
	return cmd
}

func clientSecretSet(app *App, hostFlag *string) *cobra.Command {
	var project, environment, fromFile string
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Store a secret value on the host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !varNameRe.MatchString(name) {
				return fmt.Errorf("invalid secret name %q: use letters, digits and underscore, starting with a letter or underscore", name)
			}
			if project == "" || environment == "" {
				return fmt.Errorf("--project and --environment are required")
			}
			value, err := readClientSecret(cmd, app, fromFile)
			if err != nil {
				return err
			}
			if value == "" {
				return fmt.Errorf("refusing to store an empty secret value")
			}
			host, err := requireSecretHost(*hostFlag)
			if err != nil {
				return err
			}
			remoteArgs := []string{"secret", "set", name, "--project", project, "--environment", environment}
			if app.jsonOutput() {
				remoteArgs = append(remoteArgs, "--json")
			}
			return runSSHSecret(cmd, host, []byte(value), remoteArgs...)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	cmd.Flags().StringVar(&fromFile, "file", "", "read the value from a local file instead of stdin")
	return cmd
}

func clientSecretList(app *App, hostFlag *string) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List secret names (values are never shown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" || environment == "" {
				return fmt.Errorf("--project and --environment are required")
			}
			host, err := requireSecretHost(*hostFlag)
			if err != nil {
				return err
			}
			remoteArgs := []string{"secret", "list", "--project", project, "--environment", environment}
			if app.jsonOutput() {
				remoteArgs = append(remoteArgs, "--json")
			}
			return runSSHSecret(cmd, host, nil, remoteArgs...)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	return cmd
}

func clientSecretRemove(app *App, hostFlag *string) *cobra.Command {
	var project, environment string
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Delete a stored secret value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !varNameRe.MatchString(name) {
				return fmt.Errorf("invalid secret name %q", name)
			}
			if project == "" || environment == "" {
				return fmt.Errorf("--project and --environment are required")
			}
			host, err := requireSecretHost(*hostFlag)
			if err != nil {
				return err
			}
			if !yes {
				if app.nonInteractive() || !term.IsTerminal(int(os.Stdin.Fd())) {
					return fmt.Errorf("refusing to delete in non-interactive mode; re-run with --yes")
				}
				fmt.Fprintf(os.Stderr, "Delete secret %q for %s/%s? [y/N]: ", name, project, environment)
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				answer := strings.ToLower(strings.TrimSpace(line))
				if answer != "y" && answer != "yes" {
					return fmt.Errorf("aborted")
				}
			}
			remoteArgs := []string{"secret", "remove", name, "--project", project, "--environment", environment, "--yes"}
			if app.jsonOutput() {
				remoteArgs = append(remoteArgs, "--json")
			}
			return runSSHSecret(cmd, host, nil, remoteArgs...)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without prompting")
	return cmd
}

func clientSecretVerify(app *App, hostFlag *string) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:   "verify [name]",
		Short: "Check that stored secret files exist and are readable",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" || environment == "" {
				return fmt.Errorf("--project and --environment are required")
			}
			host, err := requireSecretHost(*hostFlag)
			if err != nil {
				return err
			}
			remoteArgs := []string{"secret", "verify"}
			if len(args) == 1 {
				remoteArgs = append(remoteArgs, args[0])
			}
			remoteArgs = append(remoteArgs, "--project", project, "--environment", environment)
			if app.jsonOutput() {
				remoteArgs = append(remoteArgs, "--json")
			}
			return runSSHSecret(cmd, host, nil, remoteArgs...)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	return cmd
}

func requireSecretHost(flagHost string) (string, error) {
	host := chooseClientHost(flagHost)
	if host == "" {
		return "", fmt.Errorf("--host is required (or set MENINA_HOST / run `menina apply --host` first)")
	}
	if strings.HasPrefix(host, "-") {
		return "", fmt.Errorf("invalid SSH host %q", host)
	}
	return host, nil
}

func runSSHSecret(cmd *cobra.Command, host string, stdin []byte, args ...string) error {
	full := append([]string{"-T", host, "menina"}, args...)
	c := exec.CommandContext(cmd.Context(), "ssh", full...)
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	} else {
		c.Stdin = os.Stdin
	}
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("ssh %s: %w", host, err)
	}
	return nil
}

func readClientSecret(cmd *cobra.Command, app *App, fromFile string) (string, error) {
	if fromFile != "" {
		data, err := os.ReadFile(fromFile)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if app.nonInteractive() {
			return "", fmt.Errorf("secret value required; pipe it on stdin or use --file with --non-interactive")
		}
		fmt.Fprint(os.Stderr, "Secret value: ")
		data, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}
