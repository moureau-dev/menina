package clientcli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/clierr"
	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/manifest"
)

const defaultManifestFile = ".menina.yml"

var varNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type manifestOptions struct {
	File        string
	Project     string
	Environment string
	Registry    string
	Tag         string
	Vars        []string
}

func (o *manifestOptions) load(cmd *cobra.Command) (*manifest.File, error) {
	data, baseDir, err := readManifestInput(cmd, o.File)
	if err != nil {
		return nil, err
	}
	overrides := map[string]string{}
	for _, item := range o.Vars {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid --var %q: use KEY=VALUE", item)
		}
		if !varNameRe.MatchString(key) {
			return nil, fmt.Errorf("invalid --var name %q", key)
		}
		overrides[key] = value
	}
	if o.Project != "" {
		overrides["PROJECT"] = o.Project
	}
	if o.Environment != "" {
		overrides["ENVIRONMENT"] = o.Environment
	}
	if len(overrides) == 0 {
		overrides = nil
	}
	f, err := manifest.LoadWithVars(data, baseDir, overrides)
	if err != nil {
		return nil, err
	}
	if o.Project != "" {
		f.Project = o.Project
	}
	if o.Environment != "" {
		f.Environment = o.Environment
	}
	return f, nil
}

func readManifestInput(cmd *cobra.Command, path string) ([]byte, string, error) {
	if path == "-" {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, "", err
		}
		wd, err := os.Getwd()
		if err != nil {
			return nil, "", err
		}
		return data, wd, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return data, filepath.Dir(path), nil
}

func newInitCommand(app *App) *cobra.Command {
	var file, environment, project, from string
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a .menina.yml manifest",
		Long: "Create a repository manifest. With --from, copies an existing manifest\n" +
			"and rewrites its environment (and vars.ENVIRONMENT).",
		Example: "  menina init\n" +
			"  menina init --environment staging --from .menina.yml --file .menina.staging.yml\n" +
			"  menina init --project my-app --environment production --file .menina.yml",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				file = defaultManifestFile
			}
			if environment == "" {
				environment = "production"
			}
			if project == "" {
				project = defaultProjectName()
			}
			if !force && fileExists(file) {
				return fmt.Errorf("%s already exists (use --force to overwrite)", file)
			}
			var data []byte
			if from != "" {
				raw, err := os.ReadFile(from)
				if err != nil {
					return err
				}
				data, err = manifest.WithEnvironment(raw, environment)
				if err != nil {
					return err
				}
			} else {
				data = []byte(fmt.Sprintf(manifestTemplate, project, environment, environment))
			}
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(file, data, 0o644); err != nil {
				return err
			}
			app.infof("Created %s (project %q, environment %q)", file, project, environment)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", defaultManifestFile, "manifest path")
	cmd.Flags().StringVarP(&environment, "environment", "e", "production", "environment name")
	cmd.Flags().StringVar(&project, "project", "", "project name (default: directory name)")
	cmd.Flags().StringVar(&from, "from", "", "copy an existing manifest")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing manifest")
	return cmd
}

const manifestTemplate = `# yaml-language-server: $schema=https://menina.moureau.dev/schema/menina.schema.json
version: 1
project: %s
environment: %s

vars:
  ENVIRONMENT: %s

defaults:
  context: .
  tag: ${ENVIRONMENT}

apps:
  web:
    dockerfile: Dockerfile
    service: { port: 8080 }
    # exposure:
    #   http:
    #     hosts: [app.example.com]
`

func defaultProjectName() string {
	wd, err := os.Getwd()
	if err != nil {
		return "app"
	}
	name := strings.ToLower(filepath.Base(wd))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '.':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}

func newValidateCommand(app *App) *cobra.Command {
	o := &manifestOptions{}
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a .menina.yml manifest",
		Long: "Parse, interpolate and render a repository manifest without applying it.\n" +
			"When config.yml is available, the rendered project is also resolved with\n" +
			"the rest of the host configuration.",
		Example: "  menina validate\n  menina validate -f .menina.yml --registry registry.example.com --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := o.load(cmd)
			if err != nil {
				return clierr.Wrap(clierr.CodeValidation, err)
			}
			r, err := f.Render(manifest.RenderOptions{
				ConfigDir: app.configDir(),
				Registry:  firstNonEmpty(o.Registry, f.Defaults.Registry),
				Tag:       o.Tag,
			})
			if err != nil {
				return clierr.Wrap(clierr.CodeValidation, err)
			}
			if err := validateRendered(app, r, false); err != nil {
				return clierr.Wrap(clierr.CodeValidation, err)
			}
			if app.jsonOutput() {
				return app.printJSON(manifestSummary(r))
			}
			printManifestSummary(app, r)
			return nil
		},
	}
	cmd.Flags().StringVarP(&o.File, "file", "f", defaultManifestFile, "manifest path (- for stdin)")
	cmd.Flags().StringVar(&o.Project, "project", "", "override the manifest project")
	cmd.Flags().StringArrayVar(&o.Vars, "var", nil, "override a manifest variable (KEY=VALUE); repeatable")
	cmd.Flags().StringVarP(&o.Environment, "environment", "e", "", "override the manifest environment")
	cmd.Flags().StringVar(&o.Registry, "registry", "", "registry host for built apps")
	cmd.Flags().StringVar(&o.Tag, "tag", "", "override the image tag")
	return cmd
}

func newApplyCommand(app *App) *cobra.Command {
	o := &manifestOptions{}
	var host string
	var local, check, diff, dryRun, verify bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a .menina.yml manifest to a host",
		Long: "Render a repository manifest into project/app/environment configuration.\n\n" +
			"Without --host, applies locally (the binary must run on the host). With\n" +
			"--host, sends the manifest over SSH to `menina apply --local -f -`.\n" +
			"Build/push are separate commands: push triggers the actual rollout.",
		Example: "  menina apply\n" +
			"  menina apply --check\n" +
			"  menina apply --host deploy@vps.example.com\n" +
			"  ssh -T host menina apply --local -f - < .menina.yml",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !local {
				if target := chooseClientHost(host); target != "" {
					data, _, err := readManifestInput(cmd, o.File)
					if err != nil {
						return err
					}
					return sshApply(cmd.Context(), app, target, data, o, check, diff, dryRun, verify)
				}
			}
			f, err := o.load(cmd)
			if err != nil {
				return clierr.Wrap(clierr.CodeValidation, err)
			}
			registry := o.Registry
			if registry == "" {
				registry = f.Defaults.Registry
			}
			if registry == "" {
				if cfg, err := app.loadConfig(); err == nil {
					registry = cfg.Registry.Domain
				}
			}
			r, err := f.Render(manifest.RenderOptions{
				ConfigDir: app.configDir(),
				Registry:  registry,
				Tag:       o.Tag,
			})
			if err != nil {
				return clierr.Wrap(clierr.CodeValidation, err)
			}
			if err := validateRendered(app, r, true); err != nil {
				return clierr.Wrap(clierr.CodeValidation, err)
			}
			if check {
				if app.jsonOutput() {
					summary := manifestSummary(r)
					summary["applied"] = false
					return app.printJSON(summary)
				}
				printManifestSummary(app, r)
				app.infof("Manifest OK (not written)")
				return nil
			}
			if diff || dryRun {
				changes, err := planApply(app.configDir(), r)
				if err != nil {
					return err
				}
				if app.jsonOutput() {
					summary := manifestSummary(r)
					summary["applied"] = false
					summary["dryRun"] = true
					summary["changes"] = changes
					return app.printJSON(summary)
				}
				if len(changes) == 0 {
					app.infof("No changes.")
					return nil
				}
				for _, change := range changes {
					app.infof("%s %s", strings.ToUpper(change.Action), change.Path)
					if change.Diff != "" {
						app.infof("%s", change.Diff)
					}
				}
				return nil
			}
			if err := r.Write(app.configDir()); err != nil {
				return err
			}
			if verify {
				if err := verifyApply(app.configDir(), r); err != nil {
					return err
				}
			}
			if app.jsonOutput() {
				summary := manifestSummary(r)
				summary["applied"] = true
				summary["verified"] = verify
				return app.printJSON(summary)
			}
			printManifestSummary(app, r)
			if verify {
				app.infof("Verified written config.")
			}
			app.infof("Applied. Push images to deploy, or run `menina app deploy <app> --project %s --environment %s`.", r.Project.Name, r.Environment.Name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&o.File, "file", "f", defaultManifestFile, "manifest path (- for stdin)")
	cmd.Flags().StringVar(&o.Project, "project", "", "override the manifest project")
	cmd.Flags().StringArrayVar(&o.Vars, "var", nil, "override a manifest variable (KEY=VALUE); repeatable")
	cmd.Flags().StringVarP(&o.Environment, "environment", "e", "", "override the manifest environment")
	cmd.Flags().StringVar(&o.Registry, "registry", "", "registry host for built apps")
	cmd.Flags().StringVar(&o.Tag, "tag", "", "override the image tag")
	cmd.Flags().StringVar(&host, "host", "", "SSH host (user@host); cached for later commands")
	cmd.Flags().BoolVar(&local, "local", false, "apply locally even if a host is cached")
	cmd.Flags().BoolVar(&check, "check", false, "validate and render without writing")
	cmd.Flags().BoolVar(&diff, "diff", false, "show config changes without writing")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "alias for --diff")
	cmd.Flags().BoolVar(&verify, "verify", false, "re-read the written config and verify apps/environment overrides")
	return cmd
}

func newBuildCommand(app *App) *cobra.Command {
	o := &manifestOptions{}
	var filter string
	var buildArgs []string
	var push, dryRun bool
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build (and optionally push) a manifest's images",
		Long: "Build every app/worker with a dockerfile using the manifest's repository,\n" +
			"tag and context. External images are skipped. With --push, pushes each\n" +
			"image after it builds; the push triggers the deploy for applied apps.",
		Example: "  menina build\n" +
			"  menina build --push\n" +
			"  menina build --push --filter api,web\n" +
			"  menina build --build-arg VITE_API_URL=\"$VITE_API_URL\"",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runManifestBuild(cmd, app, o, filter, buildArgs, true, push, dryRun)
		},
	}
	cmd.Flags().StringVarP(&o.File, "file", "f", defaultManifestFile, "manifest path (- for stdin)")
	cmd.Flags().StringVar(&o.Project, "project", "", "override the manifest project")
	cmd.Flags().StringArrayVar(&o.Vars, "var", nil, "override a manifest variable (KEY=VALUE); repeatable")
	cmd.Flags().StringVarP(&o.Environment, "environment", "e", "", "override the manifest environment")
	cmd.Flags().StringVar(&o.Registry, "registry", "", "registry host for built apps")
	cmd.Flags().StringVar(&o.Tag, "tag", "", "override the image tag")
	cmd.Flags().StringVar(&filter, "filter", "", "comma-separated app/worker names or globs")
	cmd.Flags().StringArrayVar(&buildArgs, "build-arg", nil, "docker build arg (KEY=VALUE); repeatable, overrides manifest buildArgs")
	cmd.Flags().BoolVar(&push, "push", false, "push each image after building")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print docker commands without running them")
	return cmd
}

func newPushCommand(app *App) *cobra.Command {
	o := &manifestOptions{}
	var filter string
	var buildArgs []string
	var build, dryRun bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push a manifest's images (optionally building first)",
		Long: "Push images to the registry. With --build, builds each image first.\n" +
			"Pushing an image deploys matching apps that were already applied.",
		Example: "  menina push\n  menina push --build\n  menina push --build --filter api\n" +
			"  menina push --build --build-arg NODE_ENV=production",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runManifestBuild(cmd, app, o, filter, buildArgs, build, true, dryRun)
		},
	}
	cmd.Flags().StringVarP(&o.File, "file", "f", defaultManifestFile, "manifest path (- for stdin)")
	cmd.Flags().StringVar(&o.Project, "project", "", "override the manifest project")
	cmd.Flags().StringArrayVar(&o.Vars, "var", nil, "override a manifest variable (KEY=VALUE); repeatable")
	cmd.Flags().StringVarP(&o.Environment, "environment", "e", "", "override the manifest environment")
	cmd.Flags().StringVar(&o.Registry, "registry", "", "registry host for built apps")
	cmd.Flags().StringVar(&o.Tag, "tag", "", "override the image tag")
	cmd.Flags().StringVar(&filter, "filter", "", "comma-separated app/worker names or globs")
	cmd.Flags().StringArrayVar(&buildArgs, "build-arg", nil, "docker build arg (KEY=VALUE); repeatable, overrides manifest buildArgs")
	cmd.Flags().BoolVar(&build, "build", false, "build before pushing")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print docker commands without running them")
	return cmd
}

func runManifestBuild(cmd *cobra.Command, app *App, o *manifestOptions, filter string, buildArgs []string, build, push, dryRun bool) error {
	f, err := o.load(cmd)
	if err != nil {
		return clierr.Wrap(clierr.CodeValidation, err)
	}
	overrides, err := parseBuildArgs(buildArgs)
	if err != nil {
		return clierr.Wrap(clierr.CodeValidation, err)
	}
	registry := o.Registry
	if registry == "" {
		registry = f.Defaults.Registry
	}
	if registry == "" {
		if cfg, err := app.loadConfig(); err == nil {
			registry = cfg.Registry.Domain
		}
	}
	r, err := f.Render(manifest.RenderOptions{
		ConfigDir: app.configDir(),
		Registry:  registry,
		Tag:       o.Tag,
	})
	if err != nil {
		return clierr.Wrap(clierr.CodeValidation, err)
	}
	targets, err := manifest.FilterTargets(r.Targets, filter)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		if app.jsonOutput() {
			return app.printJSON(map[string]any{"images": []string{}})
		}
		app.infof("No buildable apps selected")
		return nil
	}

	doBuild := build
	for _, t := range targets {
		if doBuild {
			args := []string{"build", "-t", t.Image()}
			for _, kv := range buildArgFlags(t.BuildArgs, overrides) {
				args = append(args, "--build-arg", kv)
			}
			if t.Dockerfile != "" {
				args = append(args, "-f", t.Dockerfile)
			}
			args = append(args, t.Context)
			if err := runDocker(cmd, dryRun, app.jsonOutput(), args...); err != nil {
				return err
			}
		}
		if push {
			if err := runDocker(cmd, dryRun, app.jsonOutput(), "push", t.Image()); err != nil {
				return err
			}
		}
	}
	if app.jsonOutput() {
		images := make([]string, 0, len(targets))
		for _, t := range targets {
			images = append(images, t.Image())
		}
		return app.printJSON(map[string]any{"images": images})
	}
	return nil
}

func parseBuildArgs(values []string) (map[string]string, error) {
	out := map[string]string{}
	for _, item := range values {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid --build-arg %q: use KEY=VALUE", item)
		}
		out[key] = value
	}
	return out, nil
}

func buildArgFlags(base, overrides map[string]string) []string {
	merged := map[string]string{}
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+merged[k])
	}
	return out
}

func runDocker(cmd *cobra.Command, dryRun, jsonMode bool, args ...string) error {
	out := os.Stdout
	if jsonMode {
		out = os.Stderr
	}
	if dryRun {
		fmt.Fprintf(out, "docker %s\n", strings.Join(args, " "))
		return nil
	}
	c := exec.CommandContext(cmd.Context(), "docker", args...)
	c.Stdin = os.Stdin
	c.Stdout = out
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func manifestSummary(r *manifest.Rendered) map[string]any {
	var apps, workers []string
	for name := range r.Apps {
		if r.Apps[name].Workload.Type == config.WorkloadWorker {
			workers = append(workers, name)
		} else {
			apps = append(apps, name)
		}
	}
	sort.Strings(apps)
	sort.Strings(workers)
	targets := make([]string, 0, len(r.Targets))
	for _, t := range r.Targets {
		targets = append(targets, t.Image())
	}
	sort.Strings(targets)
	return map[string]any{
		"project":     r.Project.Name,
		"environment": r.Environment.Name,
		"apps":        apps,
		"workers":     workers,
		"images":      targets,
		"warnings":    r.Warnings,
	}
}

func printManifestSummary(app *App, r *manifest.Rendered) {
	apps, workers := 0, 0
	for _, a := range r.Apps {
		if a.Workload.Type == config.WorkloadWorker {
			workers++
		} else {
			apps++
		}
	}
	app.infof("Manifest: project %q, environment %q (%d app(s), %d worker(s), %d image(s))",
		r.Project.Name, r.Environment.Name, apps, workers, len(r.Targets))
	for _, warning := range r.Warnings {
		app.infof("warning: %s", warning)
	}
}

// validateRendered resolves the rendered project with the host configuration
// when available. When required is true, a missing config.yml is an error.
func validateRendered(app *App, r *manifest.Rendered, required bool) error {
	cfg, err := app.loadConfig()
	if err != nil {
		if required {
			return fmt.Errorf("load platform config: %w", err)
		}
		return nil
	}
	projects, err := config.LoadAllProjects(config.ProjectsDir(app.configDir()))
	if err != nil {
		return err
	}
	model := r.ProjectModel()
	found := false
	for i, p := range projects {
		if p.Name != model.Name {
			continue
		}
		mergeProject(p, model)
		projects[i] = p
		found = true
	}
	if !found {
		projects = append(projects, model)
	}
	rc, err := config.NewResolver(cfg).Resolve(projects, 0)
	if err != nil {
		return err
	}
	return config.ValidateResolved(rc)
}

func mergeProject(dst, src *config.Project) {
	if dst.Apps == nil {
		dst.Apps = map[string]*config.AppFile{}
	}
	for name, app := range src.Apps {
		dst.Apps[name] = app
	}
	if dst.Envs == nil {
		dst.Envs = map[string]*config.EnvironmentFile{}
	}
	for name, env := range src.Envs {
		dst.Envs[name] = env
		found := false
		for _, existing := range dst.EnvOrder {
			if existing == name {
				found = true
				break
			}
		}
		if !found {
			dst.EnvOrder = append(dst.EnvOrder, name)
		}
	}
}

func chooseClientHost(flagHost string) string {
	if flagHost != "" {
		_ = writeCachedHost(flagHost)
		return flagHost
	}
	if host := os.Getenv("MENINA_HOST"); host != "" {
		return host
	}
	host, _ := readCachedHost()
	return host
}

func clientHostPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "menina", "host"), nil
}

func readCachedHost() (string, error) {
	path, err := clientHostPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func writeCachedHost(host string) error {
	path, err := clientHostPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(host+"\n"), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func sshApply(ctx context.Context, app *App, host string, data []byte, o *manifestOptions, check, diff, dryRun, verify bool) error {
	if strings.HasPrefix(host, "-") {
		return fmt.Errorf("invalid SSH host %q", host)
	}
	args := []string{"-T", host, "menina", "apply", "--local", "-f", "-"}
	if o.Project != "" {
		args = append(args, "--project", o.Project)
	}
	for _, v := range o.Vars {
		args = append(args, "--var", v)
	}
	if o.Environment != "" {
		args = append(args, "-e", o.Environment)
	}
	if o.Registry != "" {
		args = append(args, "--registry", o.Registry)
	}
	if o.Tag != "" {
		args = append(args, "--tag", o.Tag)
	}
	if check {
		args = append(args, "--check")
	}
	if diff {
		args = append(args, "--diff")
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if verify {
		args = append(args, "--verify")
	}
	if app.jsonOutput() {
		args = append(args, "--json")
	}
	c := exec.CommandContext(ctx, "ssh", args...)
	c.Stdin = bytes.NewReader(data)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("ssh %s: %w", host, err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
