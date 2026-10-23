package host

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
)

func appCreate(app *App) *cobra.Command {
	var project, repository, workload, runAs, cpu, memory, storageSize, mountPath string
	var port, replicas, minReplicas, maxReplicas, targetCPU int
	var yes bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an app definition",
		Long: "Create an app definition. Interactive on a terminal (prompts for project,\n" +
			"repository, port and scaling); pass --yes with flags for scripts.",
		Example: "  menina app create api --project my-app --repository registry.example.com/my-app/api --port 8080\n" +
			"  menina app create worker --project my-app --repository registry.example.com/my-app/worker --type worker --yes",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			p := newPrompter()
			interactive := p.enabled && !yes

			if project == "" && interactive {
				project = p.Ask("Project", "")
			}
			if project == "" {
				return fmt.Errorf("--project is required")
			}
			if err := config.EnsureProjectDirs(app.Opts.ConfigDir, project); err != nil {
				return err
			}
			if repository == "" {
				repository = defaultImageRepository(app, project, name)
			}
			if repository == "" && interactive {
				repository = p.Ask("Image repository", "")
			}
			if repository == "" {
				return fmt.Errorf("--repository is required (or enable the bundled registry)")
			}
			if !cmd.Flags().Changed("type") && interactive {
				workload = []string{config.WorkloadService, config.WorkloadWorker}[p.Select("Workload type", []Option{
					{Label: "service", Description: "Listens on a port; gets a ClusterIP Service and can be exposed on a hostname."},
					{Label: "worker", Description: "No inbound port; runs continuously (queues, background jobs)."},
				}, 0)]
			}
			if workload == config.WorkloadService && !cmd.Flags().Changed("port") && interactive {
				port = atoiOr(p.Ask("Service port", strconv.Itoa(port)), port)
			}

			af := config.AppFile{
				Name:      name,
				Image:     config.ImageSpec{Repository: repository},
				Workload:  config.WorkloadSpec{Type: workload, Port: port},
				Resources: config.ResourceSpec{CPU: cpu, Memory: memory},
				Env:       map[string]string{},
			}
			if runAs != "" {
				af.Workload.Command = []string{runAs}
			}
			if storageSize != "" {
				af.Storage = &config.StorageSpec{Enabled: true, Size: storageSize, MountPath: mountPath}
			}
			switch {
			case cmd.Flags().Changed("min-replicas") || cmd.Flags().Changed("max-replicas"):
				af.Replicas = &config.ReplicasSpec{Min: minReplicas, Max: maxReplicas, TargetCPU: targetCPU}
			case cmd.Flags().Changed("replicas"):
				af.Replicas = &config.ReplicasSpec{Min: replicas, Max: replicas}
			case interactive:
				switch p.Choose("Scaling", []string{"Inherit platform default", "Fixed replicas", "Autoscaling range (min/max)"}, 0) {
				case 1:
					n := atoiOr(p.Ask("Replicas", "1"), 1)
					af.Replicas = &config.ReplicasSpec{Min: n, Max: n}
				case 2:
					mn := atoiOr(p.Ask("Min replicas", "2"), 2)
					mx := atoiOr(p.Ask("Max replicas", "10"), 10)
					tc := atoiOr(p.Ask("Target CPU %", "70"), 70)
					af.Replicas = &config.ReplicasSpec{Min: mn, Max: mx, TargetCPU: tc}
				}
			}

			if err := writeYAML(appPath(app, project, name), af); err != nil {
				return err
			}
			app.Infof("Created app %q in project %q", name, project)
			app.Infof("Next: `menina app deploy %s --project %s --environment <env>` (or push an image to the registry)", name, project)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&repository, "repository", "", "image repository")
	cmd.Flags().StringVar(&workload, "type", config.WorkloadService, "workload type (service|worker)")
	cmd.Flags().IntVar(&port, "port", 8080, "service port")
	cmd.Flags().StringVar(&cpu, "cpu", "250m", "CPU request/limit")
	cmd.Flags().StringVar(&memory, "memory", "256Mi", "memory request/limit")
	cmd.Flags().IntVar(&replicas, "replicas", 1, "fixed replica count")
	cmd.Flags().IntVar(&minReplicas, "min-replicas", 0, "minimum replicas (enables autoscaling)")
	cmd.Flags().IntVar(&maxReplicas, "max-replicas", 0, "maximum replicas (enables autoscaling)")
	cmd.Flags().IntVar(&targetCPU, "target-cpu", 70, "target CPU utilization for autoscaling")
	cmd.Flags().StringVar(&storageSize, "storage", "", "persistent volume size (e.g. 1Gi)")
	cmd.Flags().StringVar(&mountPath, "mount", "/data", "storage mount path")
	cmd.Flags().StringVar(&runAs, "command", "", "container command override")
	cmd.Flags().BoolVar(&yes, "yes", false, "non-interactive; use flags only")
	return cmd
}

func appScale(app *App) *cobra.Command {
	var project, environment string
	var replicas, minReplicas, maxReplicas, targetCPU int
	var reset, baseline bool
	cmd := &cobra.Command{
		Use:   "scale <name>",
		Short: "Change an app's replica count or autoscaling range",
		Long: "Change an app's scaling.\n\n" +
			"Without -e/--baseline it writes the shared baseline; on a terminal with\n" +
			"more than one environment it asks which target to apply to. Use\n" +
			"-e/--environment to write a single environment's override, or --baseline\n" +
			"to be explicit. --reset removes the override (inherit the baseline/default).",
		Example: "  menina app scale api --project my-app --replicas 4\n" +
			"  menina app scale api --project my-app --min 2 --max 10\n" +
			"  menina app scale api --project my-app -e production --min 3 --max 20\n" +
			"  menina app scale api --project my-app --reset",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if project == "" {
				return fmt.Errorf("--project is required")
			}
			apps, err := config.LoadApps(config.ProjectsDir(app.Opts.ConfigDir), project)
			if err != nil {
				return err
			}
			if _, ok := apps[name]; !ok {
				return fmt.Errorf("app %q not found in project %q", name, project)
			}

			spec, err := scaleSpec(cmd, reset, replicas, minReplicas, maxReplicas, targetCPU)
			if err != nil {
				return err
			}

			target, err := resolveScaleTarget(newPrompter(), app.Opts.ConfigDir, project, environment, baseline)
			if err != nil {
				return err
			}
			if target != "" {
				return applyEnvironmentScale(app, project, target, name, spec)
			}

			af := apps[name]
			af.Replicas = spec
			if err := writeYAML(appPath(app, project, name), af); err != nil {
				return err
			}
			app.Infof("Scaled %q baseline: %s", name, describeReplicas(spec))
			app.Infof("Apply with `menina app deploy %s --project %s --environment <env>`, or let reconciliation pick it up.", name, project)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVarP(&environment, "environment", "e", "", "write a per-environment override instead of the baseline")
	cmd.Flags().BoolVar(&baseline, "baseline", false, "apply to the shared baseline without prompting")
	cmd.Flags().IntVar(&replicas, "replicas", 0, "fixed replica count")
	cmd.Flags().IntVar(&minReplicas, "min", 0, "minimum replicas (enables autoscaling)")
	cmd.Flags().IntVar(&maxReplicas, "max", 0, "maximum replicas (enables autoscaling)")
	cmd.Flags().IntVar(&targetCPU, "target-cpu", 70, "target CPU utilization for autoscaling")
	cmd.Flags().BoolVar(&reset, "reset", false, "remove the override and inherit the baseline/default")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

// scaleSpec turns the scale flags into a replica spec, or nil to reset.
func scaleSpec(cmd *cobra.Command, reset bool, replicas, minReplicas, maxReplicas, targetCPU int) (*config.ReplicasSpec, error) {
	switch {
	case reset:
		return nil, nil
	case cmd.Flags().Changed("replicas"):
		return &config.ReplicasSpec{Min: replicas, Max: replicas}, nil
	case cmd.Flags().Changed("min") || cmd.Flags().Changed("max"):
		return &config.ReplicasSpec{Min: minReplicas, Max: maxReplicas, TargetCPU: targetCPU}, nil
	default:
		return nil, fmt.Errorf("specify --replicas N, --min/--max, or --reset")
	}
}

func describeReplicas(spec *config.ReplicasSpec) string {
	switch {
	case spec == nil:
		return "inherit the baseline/platform default"
	case spec.Autoscaling():
		return fmt.Sprintf("%d–%d replicas (target CPU %d%%)", spec.Min, spec.Max, spec.TargetCPU)
	default:
		return fmt.Sprintf("%d replica(s)", spec.Min)
	}
}

// applyEnvironmentScale writes a per-environment replica override, leaving the
// app baseline untouched.
func applyEnvironmentScale(app *App, project, environment, name string, spec *config.ReplicasSpec) error {
	env, err := config.LoadEnvironment(app.Opts.ConfigDir, project, environment)
	if err != nil {
		return err
	}
	ov := env.Apps[name]
	if ov == nil {
		ov = &config.AppOverride{}
	}
	ov.Replicas = spec
	if err := config.SaveAppOverride(app.Opts.ConfigDir, project, environment, name, ov); err != nil {
		return err
	}
	app.Infof("Scaled %q in environment %q: %s", name, environment, describeReplicas(spec))
	app.Infof("Apply with `menina app deploy %s --project %s --environment %s`, or let reconciliation pick it up.", name, project, environment)
	return nil
}
