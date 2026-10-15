package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/secrets"
)

// secretNameRe matches names usable as environment variables (Menina injects
// app secrets as container env vars).
var secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func newSecretCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage app secrets (per project/environment)",
		Long: "Store secret values on the host, outside the configuration tree.\n\n" +
			"App definitions reference a secret by name via `secrets: {ENV_VAR: <ref>}`;\n" +
			"`secret set` writes the value to secrets/<project>/<environment>/<name>\n" +
			"so it can be referenced as file:<path>. Values are read from stdin (hidden\n" +
			"on a terminal) and are never printed or written into configuration.",
	}
	cmd.AddCommand(secretSet(app), secretList(app), secretRemove(app))
	return cmd
}

// appSecretsDir is where a project/environment's secret files live. Under the
// platform secrets directory so `menina backup --with-secrets` includes them.
func appSecretsDir(app *App, project, environment string) string {
	return filepath.Join(app.Opts.ConfigDir, "secrets", project, environment)
}

func secretSet(app *App) *cobra.Command {
	var project, environment, fromFile string
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Store a secret value for a project/environment",
		Example: "  printf '%s' \"$DATABASE_URL\" | menina secret set DATABASE_URL --project my-app --environment production\n" +
			"  menina secret set API_KEY --project my-app --environment production --file ./api-key.txt",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !secretNameRe.MatchString(name) {
				return fmt.Errorf("invalid secret name %q: use letters, digits and underscore, starting with a letter or underscore", name)
			}
			if project == "" {
				return fmt.Errorf("--project is required")
			}
			if environment == "" {
				return fmt.Errorf("--environment is required")
			}
			if _, err := config.LoadProject(config.ProjectsDir(app.Opts.ConfigDir), project); err != nil {
				return err
			}
			if _, err := config.LoadEnvironment(app.Opts.ConfigDir, project, environment); err != nil {
				return err
			}
			value, err := readSecretValue(cmd, fromFile)
			if err != nil {
				return err
			}
			if value == "" {
				return fmt.Errorf("refusing to store an empty secret value")
			}
			path := filepath.Join(appSecretsDir(app, project, environment), name)
			if err := secrets.WriteSecretFile(path, value); err != nil {
				return err
			}
			ref := "file:" + path
			if app.Opts.JSON {
				return app.PrintJSON(map[string]string{
					"project": project, "environment": environment, "name": name, "ref": ref,
				})
			}
			app.Infof("Stored secret %q for %s/%s", name, project, environment)
			app.Infof("Reference it in an app: secrets: { %s: \"%s\" }", name, ref)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	cmd.Flags().StringVar(&fromFile, "file", "", "read the value from a file instead of stdin")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func secretList(app *App) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List secret names for a project/environment (values are never shown)",
		Example: "  menina secret list --project my-app --environment production",
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				return fmt.Errorf("--project is required")
			}
			if environment == "" {
				return fmt.Errorf("--environment is required")
			}
			names, err := listSecretNames(appSecretsDir(app, project, environment))
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(names)
			}
			for _, n := range names {
				app.Infof("%s", n)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func secretRemove(app *App) *cobra.Command {
	var project, environment string
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove <name>",
		Short:   "Delete a stored secret value",
		Example: "  menina secret remove API_KEY --project my-app --environment production --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if project == "" {
				return fmt.Errorf("--project is required")
			}
			if environment == "" {
				return fmt.Errorf("--environment is required")
			}
			path := filepath.Join(appSecretsDir(app, project, environment), name)
			if !fileExists(path) {
				return fmt.Errorf("secret %q not found for %s/%s", name, project, environment)
			}
			if !confirmDestructive(app, fmt.Sprintf("Delete secret %q for %s/%s?", name, project, environment), yes) {
				return fmt.Errorf("aborted")
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			app.Infof("Deleted secret %q for %s/%s", name, project, environment)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without prompting")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

// readSecretValue reads the secret value from a file, a hidden terminal prompt,
// or stdin (a pipe). Values are never taken from arguments, so they cannot leak
// via the process command line.
func readSecretValue(cmd *cobra.Command, fromFile string) (string, error) {
	if fromFile != "" {
		data, err := os.ReadFile(fromFile)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(os.Stderr, "Secret value: ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func listSecretNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}
