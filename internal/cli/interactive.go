package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
)

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

func createEnvironmentInline(app *App, p *prompter, project string) (string, error) {
	name := p.Ask("New environment name", "")
	if name == "" {
		return "", fmt.Errorf("environment name is required")
	}
	env := config.EnvironmentFile{Name: name, Apps: map[string]*config.AppOverride{}}
	if err := writeYAML(filepath.Join(config.ProjectDir(app.Opts.ConfigDir, project), "environments", name+".yml"), env); err != nil {
		return "", err
	}
	app.Infof("Created environment %q in project %q", name, project)
	return name, nil
}

func selectProject(app *App, p *prompter) (string, error) {
	names, err := config.ListProjects(config.ProjectsDir(app.Opts.ConfigDir))
	if err != nil {
		return "", err
	}
	options := append([]string{"+ create a new project"}, names...)
	idx := p.Choose("Select a project", options, 0)
	if idx == 0 {
		return createProjectInline(app, p)
	}
	return names[idx-1], nil
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
	idx := p.Choose(fmt.Sprintf("Apps in project %q", project), options, 0)
	if idx == 0 {
		return createAppInline(app, p, project)
	}
	return names[idx-1], nil
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
	return order[p.Choose("Select an environment", order, 0)], nil
}

func selectEnvironmentInteractive(app *App, p *prompter, project string) (string, error) {
	_, order, err := config.LoadEnvironments(config.ProjectsDir(app.Opts.ConfigDir), project)
	if err != nil {
		return "", err
	}
	options := append([]string{"+ create a new environment"}, order...)
	idx := p.Choose(fmt.Sprintf("Environments in project %q", project), options, 0)
	if idx == 0 {
		return createEnvironmentInline(app, p, project)
	}
	return order[idx-1], nil
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
			err = scaleInline(ctx, app, p, project, name)
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
			if p.Confirm(fmt.Sprintf("Delete app %q?", name), false) {
				err = runChild(ctx, appDelete(app), map[string]string{"project": project, "yes": "true"}, name)
			}
		default:
			return nil
		}
		if err != nil {
			app.Infof("error: %v", err)
		}
	}
}

func scaleInline(ctx context.Context, app *App, p *prompter, project, name string) error {
	switch p.Choose("Scaling", []string{"Fixed replicas", "Autoscaling range", "Reset (inherit default)"}, 0) {
	case 0:
		n := atoiOr(p.Ask("Replicas", "1"), 1)
		return runChild(ctx, appScale(app), map[string]string{"project": project, "replicas": fmt.Sprintf("%d", n)}, name)
	case 1:
		mn := atoiOr(p.Ask("Min replicas", "2"), 2)
		mx := atoiOr(p.Ask("Max replicas", "10"), 10)
		tc := atoiOr(p.Ask("Target CPU %", "70"), 70)
		return runChild(ctx, appScale(app), map[string]string{"project": project, "min": fmt.Sprintf("%d", mn), "max": fmt.Sprintf("%d", mx), "target-cpu": fmt.Sprintf("%d", tc)}, name)
	default:
		return runChild(ctx, appScale(app), map[string]string{"project": project, "reset": "true"}, name)
	}
}
