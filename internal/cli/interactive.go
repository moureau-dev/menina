package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
)

// errExit signals that the user cancelled an interactive flow; Execute treats it
// as a clean exit.
var errExit = errors.New("cancelled")

// promptCtx is cancelled on SIGINT/SIGTERM so a prompt can abandon a blocking
// read. It is nil outside Execute (e.g. in tests), in which case prompts never
// self-cancel.
var promptCtx context.Context

func setPromptContext(ctx context.Context) { promptCtx = ctx }

func promptCancelled() <-chan struct{} {
	if promptCtx == nil {
		return nil
	}
	return promptCtx.Done()
}

// promptExit is called when a prompt is interrupted; it is a variable so tests
// can intercept it.
var promptExit = os.Exit

func runChild(ctx context.Context, c *cobra.Command, flags map[string]string, args ...string) error {
	c.SetContext(ctx)
	for k, v := range flags {
		if f := c.Flags().Lookup(k); f != nil {
			if err := c.Flags().Set(k, v); err != nil {
				return err
			}
		}
	}
	switch {
	case c.RunE != nil:
		return c.RunE(c, args)
	case c.Run != nil:
		c.Run(c, args)
		return nil
	}
	return nil
}

func createProject(app *App, name, namespace, description string) error {
	if err := config.EnsureProjectDirs(app.Opts.ConfigDir, name); err != nil {
		return err
	}
	pf := config.ProjectFile{Name: name, Namespace: namespace, Description: description}
	if err := writeYAML(filepath.Join(config.ProjectDir(app.Opts.ConfigDir, name), "project.yml"), pf); err != nil {
		return err
	}
	app.Infof("Created project %q", name)
	return nil
}

func createProjectInline(app *App, p *prompter) (string, error) {
	name := p.Ask("New project name", "")
	if name == "" {
		return "", fmt.Errorf("project name is required")
	}
	ns := p.Ask("Namespace (blank = name)", name)
	if err := createProject(app, name, ns, ""); err != nil {
		return "", err
	}
	if err := createEnvironments(app, name, chooseEnvironments(p)); err != nil {
		return "", err
	}
	return name, nil
}

func createAppInline(app *App, p *prompter, project string) (string, error) {
	name := p.Ask("New app name", "")
	if name == "" {
		return "", fmt.Errorf("app name is required")
	}
	repository := p.Ask("Image repository (e.g. registry.example.com/project/app)", "")
	workload := []string{config.WorkloadService, config.WorkloadWorker}[p.Choose("Workload type", []string{"service", "worker"}, 0)]
	af := config.AppFile{Name: name, Image: config.ImageSpec{Repository: repository}, Workload: config.WorkloadSpec{Type: workload}, Resources: config.ResourceSpec{CPU: "250m", Memory: "256Mi"}, Env: map[string]string{}}
	if workload == config.WorkloadService {
		af.Workload.Port = atoiOr(p.Ask("Service port", "8080"), 8080)
	}
	switch p.Choose("Scaling", []string{"Inherit platform default", "Fixed replicas", "Autoscaling range"}, 0) {
	case 1:
		n := atoiOr(p.Ask("Replicas", "1"), 1)
		af.Replicas = &config.ReplicasSpec{Min: n, Max: n}
	case 2:
		mn := atoiOr(p.Ask("Min replicas", "2"), 2)
		mx := atoiOr(p.Ask("Max replicas", "10"), 10)
		tc := atoiOr(p.Ask("Target CPU %", "70"), 70)
		af.Replicas = &config.ReplicasSpec{Min: mn, Max: mx, TargetCPU: tc}
	}
	if err := writeYAML(appPath(app, project, name), af); err != nil {
		return "", err
	}
	app.Infof("Created app %q in project %q", name, project)
	return name, nil
}

func writeEnvironment(app *App, project, name string) error {
	env := config.EnvironmentFile{Name: name, Apps: map[string]*config.AppOverride{}}
	return writeYAML(filepath.Join(config.ProjectDir(app.Opts.ConfigDir, project), "environments", name+".yml"), env)
}

func createEnvironments(app *App, project string, names []string) error {
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if err := writeEnvironment(app, project, n); err != nil {
			return err
		}
		app.Infof("Created environment %q in project %q", n, project)
	}
	return nil
}

func createEnvironmentInline(app *App, p *prompter, project string) (string, error) {
	name := p.Ask("New environment name", "")
	if name == "" {
		return "", fmt.Errorf("environment name is required")
	}
	if err := writeEnvironment(app, project, name); err != nil {
		return "", err
	}
	app.Infof("Created environment %q in project %q", name, project)
	return name, nil
}

func containsString(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

func suggestEnvironmentName(envs []string) string {
	for _, c := range []string{"development", "staging", "production"} {
		if !containsString(envs, c) {
			return c
		}
	}
	return ""
}

// chooseEnvironments builds the environment list interactively. It first asks
// whether the project has more than one environment; if not, only "production"
// is created. Otherwise it runs an editable list, showing the current
// environments on every step, from which names can be added or renamed.
func chooseEnvironments(p *prompter) []string {
	if !p.yesNo("Do you account for multiple environments?",
		"Create separate deployment targets (e.g. development, staging, production) you can add and rename.",
		"Create only production.",
		false) {
		return []string{"production"}
	}
	var envs []string
	for {
		if len(envs) > 0 {
			fmt.Printf("  Current environments: %s\n", strings.Join(envs, ", "))
		}
		actions := []string{"+ add an environment", "done"}
		if len(envs) > 0 {
			actions = []string{"+ add an environment", "rename an environment", "done"}
		}
		switch actions[p.Choose("Environments", actions, len(actions)-1)] {
		case "+ add an environment":
			name := p.Ask("  Environment name", suggestEnvironmentName(envs))
			if name == "" {
				continue
			}
			if containsString(envs, name) {
				fmt.Printf("  %q is already in the list\n", name)
				continue
			}
			envs = append(envs, name)
		case "rename an environment":
			i := p.Choose("  Rename which?", envs, 0)
			name := p.Ask("  New name", envs[i])
			if name == "" || (name != envs[i] && containsString(envs, name)) {
				continue
			}
			envs[i] = name
		default:
			if len(envs) == 0 {
				return []string{"production"}
			}
			return envs
		}
	}
}

// resolveEnvironmentNames decides which environments to create: an explicit
// flag (or "none" for none), the interactive chooser, or — non-interactively —
// just "production".
func resolveEnvironmentNames(p *prompter, flag string, interactive bool) []string {
	if flag != "" {
		if strings.EqualFold(strings.TrimSpace(flag), "none") {
			return nil
		}
		return splitCSV(flag)
	}
	if interactive && p.enabled {
		return chooseEnvironments(p)
	}
	return []string{"production"}
}

func selectProject(app *App, p *prompter) (string, error) {
	names, err := config.ListProjects(config.ProjectsDir(app.Opts.ConfigDir))
	if err != nil {
		return "", err
	}
	options := append([]string{"+ create a new project"}, names...)
	options = append(options, "Exit")
	idx := p.Choose("Select a project", options, 0)
	switch {
	case idx == 0:
		return createProjectInline(app, p)
	case idx == len(options)-1:
		return "", errExit
	default:
		return names[idx-1], nil
	}
}

func selectApp(app *App, p *prompter, project string) (string, error) {
	apps, err := config.LoadApps(config.ProjectsDir(app.Opts.ConfigDir), project)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(apps))
	for n := range apps {
		names = append(names, n)
	}
	sort.Strings(names)
	options := append([]string{"+ create a new app"}, names...)
	options = append(options, "Exit")
	idx := p.Choose(fmt.Sprintf("Apps in project %q", project), options, 0)
	switch {
	case idx == 0:
		return createAppInline(app, p, project)
	case idx == len(options)-1:
		return "", errExit
	default:
		return names[idx-1], nil
	}
}

func selectEnvironment(app *App, p *prompter, project string) (string, error) {
	_, order, err := config.LoadEnvironments(config.ProjectsDir(app.Opts.ConfigDir), project)
	if err != nil {
		return "", err
	}
	if len(order) == 0 {
		return "", fmt.Errorf("project %q has no environments; create one with `menina environment create <name> --project %s`", project, project)
	}
	if len(order) == 1 {
		return order[0], nil
	}
	options := append(append([]string{}, order...), "Exit")
	idx := p.Choose("Select an environment", options, 0)
	if idx == len(options)-1 {
		return "", errExit
	}
	return order[idx], nil
}

func selectEnvironmentInteractive(app *App, p *prompter, project string) (string, error) {
	_, order, err := config.LoadEnvironments(config.ProjectsDir(app.Opts.ConfigDir), project)
	if err != nil {
		return "", err
	}
	options := append([]string{"+ create a new environment"}, order...)
	options = append(options, "Exit")
	idx := p.Choose(fmt.Sprintf("Environments in project %q", project), options, 0)
	switch {
	case idx == 0:
		return createEnvironmentInline(app, p, project)
	case idx == len(options)-1:
		return "", errExit
	default:
		return order[idx-1], nil
	}
}

func appMenu(ctx context.Context, app *App, p *prompter, project, name, defaultEnv string) error {
	pickEnv := func() (string, error) {
		if defaultEnv != "" {
			return defaultEnv, nil
		}
		return selectEnvironment(app, p, project)
	}
	for {
		actions := []string{"Deploy", "Scale", "Edit", "Logs", "Rollback", "Delete", "Back"}
		idx := p.Choose(fmt.Sprintf("%s / %s", project, name), actions, 0)
		var err error
		switch idx {
		case 0:
			if env, e := pickEnv(); e != nil {
				err = e
			} else {
				err = runChild(ctx, appDeploy(app), map[string]string{"project": project, "environment": env}, name)
			}
		case 1:
			err = scaleInline(ctx, app, p, project, name, defaultEnv)
		case 2:
			err = runChild(ctx, appEdit(app), map[string]string{"project": project}, name)
		case 3:
			if env, e := pickEnv(); e != nil {
				err = e
			} else {
				err = runChild(ctx, appLogs(app), map[string]string{"project": project, "environment": env}, name)
			}
		case 4:
			if env, e := pickEnv(); e != nil {
				err = e
			} else {
				err = runChild(ctx, appRollback(app), map[string]string{"project": project, "environment": env}, name)
			}
		case 5:
			if p.Confirm(fmt.Sprintf("Soft-delete app %q? (stops it; config kept for restore)", name), false) {
				err = runChild(ctx, appDelete(app), map[string]string{"project": project, "yes": "true"}, name)
			}
		default:
			return nil
		}
		if err != nil {
			if errors.Is(err, errExit) {
				return errExit
			}
			app.Infof("error: %v", err)
		}
	}
}

// resolveScaleTarget picks where a scaling change lands: an explicit
// environment, the shared baseline (""), or — when run on a terminal with no
// explicit choice and more than one environment — a prompt.
func resolveScaleTarget(p *prompter, configDir, project, environment string, baseline bool) (string, error) {
	if environment != "" || baseline || !p.enabled {
		return environment, nil
	}
	_, order, err := config.LoadEnvironments(config.ProjectsDir(configDir), project)
	if err != nil {
		return "", err
	}
	if len(order) <= 1 {
		return "", nil
	}
	options := append([]string{"All environments (baseline)"}, order...)
	if idx := p.Choose("Apply scaling to", options, 0); idx > 0 {
		return order[idx-1], nil
	}
	return "", nil
}

func scaleInline(ctx context.Context, app *App, p *prompter, project, name, defaultEnv string) error {
	target, err := resolveScaleTarget(p, app.Opts.ConfigDir, project, defaultEnv, false)
	if err != nil {
		return err
	}
	flags := map[string]string{"project": project}
	if target != "" {
		flags["environment"] = target
	} else {
		flags["baseline"] = "true"
	}
	switch p.Choose("Scaling", []string{"Fixed replicas", "Autoscaling range", "Reset (inherit default)"}, 0) {
	case 0:
		flags["replicas"] = fmt.Sprintf("%d", atoiOr(p.Ask("Replicas", "1"), 1))
	case 1:
		flags["min"] = fmt.Sprintf("%d", atoiOr(p.Ask("Min replicas", "2"), 2))
		flags["max"] = fmt.Sprintf("%d", atoiOr(p.Ask("Max replicas", "10"), 10))
		flags["target-cpu"] = fmt.Sprintf("%d", atoiOr(p.Ask("Target CPU %", "70"), 70))
	default:
		flags["reset"] = "true"
	}
	return runChild(ctx, appScale(app), flags, name)
}
