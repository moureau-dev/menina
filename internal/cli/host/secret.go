package host

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

	"github.com/moureau-dev/menina/internal/clierr"
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
	cmd.AddCommand(secretSet(app), secretRotate(app), secretList(app), secretRemove(app), secretVerify(app))
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

func secretRotate(app *App) *cobra.Command {
	var project, environment, fromFile, output string
	var generate, restart, yes bool
	var length int
	cmd := &cobra.Command{
		Use:   "rotate <name>",
		Short: "Replace a stored secret value",
		Long: "Write a new value for an existing secret (or create it if missing). Reads\n" +
			"from stdin/--file, or generates a random value with --generate.",
		Example: "  printf '%s' \"$NEW_VALUE\" | menina secret rotate DATABASE_URL --project my-app --environment production\n" +
			"  menina secret rotate API_KEY --project my-app --environment production --generate",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !secretNameRe.MatchString(name) {
				return fmt.Errorf("invalid secret name %q: use letters, digits and underscore, starting with a letter or underscore", name)
			}
			if project == "" || environment == "" {
				return fmt.Errorf("--project and --environment are required")
			}
			if _, err := config.LoadProject(config.ProjectsDir(app.Opts.ConfigDir), project); err != nil {
				return clierr.Wrap(clierr.CodeNotFound, err)
			}
			if _, err := config.LoadEnvironment(app.Opts.ConfigDir, project, environment); err != nil {
				return clierr.Wrap(clierr.CodeNotFound, err)
			}
			var value string
			var err error
			if generate {
				value, err = secrets.GeneratePassword(length)
			} else {
				value, err = readSecretValue(cmd, fromFile)
			}
			if err != nil {
				return err
			}
			if value == "" {
				return fmt.Errorf("refusing to store an empty secret value")
			}
			if output != "" {
				if err := os.WriteFile(output, []byte(value), 0o600); err != nil {
					return err
				}
				if err := os.Chmod(output, 0o600); err != nil {
					return err
				}
			}
			path := filepath.Join(appSecretsDir(app, project, environment), name)
			if err := secrets.WriteSecretFile(path, value); err != nil {
				return err
			}
			referencing, err := secretReferencingApps(app, project, environment, name)
			if err != nil {
				return err
			}
			restarted := false
			if restart && len(referencing) > 0 {
				cfg, err := app.LoadConfig()
				if err != nil {
					return err
				}
				if err := requireProdConfirmation(cfg, environment, yes); err != nil {
					return err
				}
				client, err := app.Control(cfg)
				if err != nil {
					return fmt.Errorf("control plane not reachable (%v); secret rotated but apps were not restarted", err)
				}
				for _, target := range referencing {
					body := fmt.Sprintf(`{"project":%q,"environment":%q,"app":%q,"confirm":true}`, project, environment, target)
					if err := client.PostJSON(cmd.Context(), "/deploy", []byte(body)); err != nil {
						return fmt.Errorf("secret rotated, but restarting %s failed: %w", target, err)
					}
					app.Infof("Queued redeploy of %s to pick up the rotated secret", target)
				}
				restarted = true
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]any{
					"project": project, "environment": environment, "name": name, "rotated": true,
					"referencingApps": referencing, "restarted": restarted,
				})
			}
			app.Infof("Rotated secret %q for %s/%s", name, project, environment)
			if len(referencing) > 0 && !restarted {
				app.Infof("Apps referencing this secret keep the old value until redeployed: %s", strings.Join(referencing, ", "))
				app.Infof("Re-run with --restart to queue those deployments.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&environment, "environment", "", "environment name")
	cmd.Flags().StringVar(&fromFile, "file", "", "read the value from a file instead of stdin")
	cmd.Flags().StringVar(&output, "output", "", "also write the new value to this file (mode 0600)")
	cmd.Flags().BoolVar(&generate, "generate", false, "generate a random value")
	cmd.Flags().IntVar(&length, "length", 32, "generated value length")
	cmd.Flags().BoolVar(&restart, "restart", false, "queue deployments for apps that reference this secret")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm restarting production apps")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func secretList(app *App) *cobra.Command {
	var project, environment string
	var references bool
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
			if references {
				out := map[string][]string{}
				for _, n := range names {
					apps, err := secretReferencingApps(app, project, environment, n)
					if err != nil {
						return err
					}
					out[n] = apps
				}
				if app.Opts.JSON {
					return app.PrintJSON(out)
				}
				for _, n := range names {
					app.Infof("%s: %s", n, strings.Join(out[n], ", "))
				}
				return nil
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
	cmd.Flags().BoolVar(&references, "references", false, "show which apps reference each secret")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func secretReferencingApps(app *App, project, environment, name string) ([]string, error) {
	projectsDir := config.ProjectsDir(app.Opts.ConfigDir)
	p, err := config.LoadProject(projectsDir, project)
	if err != nil {
		return nil, err
	}
	env, err := config.LoadEnvironment(app.Opts.ConfigDir, project, environment)
	if err != nil {
		return nil, err
	}
	want := "file:" + filepath.Join(appSecretsDir(app, project, environment), name)
	var out []string
	for appName, base := range p.Apps {
		refs := map[string]string{}
		for k, v := range base.Secrets {
			refs[k] = v
		}
		if ov := env.Apps[appName]; ov != nil {
			for k, v := range ov.Secrets {
				refs[k] = v
			}
		}
		for _, ref := range refs {
			if ref == want {
				out = append(out, appName)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
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
	if nonInteractive {
		if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			return "", fmt.Errorf("secret value required; pipe it on stdin or use --file with --non-interactive")
		}
	}
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

func secretVerify(app *App) *cobra.Command {
	var project, environment string
	cmd := &cobra.Command{
		Use:   "verify [name]",
		Short: "Check that stored secret files exist, are readable and are not empty",
		Long: "Verify one secret by name, or every secret file in a project/environment.\n" +
			"Never prints secret values.",
		Example: "  menina secret verify DATABASE_URL --project my-app --environment production\n" +
			"  menina secret verify --project my-app --environment production --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				return fmt.Errorf("--project is required")
			}
			if environment == "" {
				return fmt.Errorf("--environment is required")
			}
			dir := appSecretsDir(app, project, environment)
			var names []string
			if len(args) == 1 {
				if !secretNameRe.MatchString(args[0]) {
					return fmt.Errorf("invalid secret name %q", args[0])
				}
				names = []string{args[0]}
			} else {
				var err error
				names, err = listSecretNames(dir)
				if err != nil {
					return err
				}
			}
			type result struct {
				Name  string `json:"name"`
				Path  string `json:"path"`
				Mode  string `json:"mode,omitempty"`
				OK    bool   `json:"ok"`
				Error string `json:"error,omitempty"`
			}
			results := make([]result, 0, len(names))
			failed := false
			for _, name := range names {
				path := filepath.Join(dir, name)
				r := result{Name: name, Path: path}
				info, err := os.Stat(path)
				switch {
				case err != nil:
					r.Error = err.Error()
				case info.IsDir():
					r.Error = "is a directory"
				case info.Size() == 0:
					r.Error = "file is empty"
				default:
					f, err := os.Open(path)
					if err != nil {
						r.Error = err.Error()
					} else {
						_ = f.Close()
						r.OK = true
						r.Mode = info.Mode().Perm().String()
					}
				}
				if !r.OK {
					failed = true
				}
				results = append(results, r)
			}
			if app.Opts.JSON {
				if err := app.PrintJSON(results); err != nil {
					return err
				}
			} else {
				for _, r := range results {
					if r.OK {
						app.Infof("ok   %s (%s)", r.Name, r.Mode)
					} else {
						app.Infof("FAIL %s: %s", r.Name, r.Error)
					}
				}
			}
			if failed {
				return fmt.Errorf("one or more secrets are missing or unreadable")
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
