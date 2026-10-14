package cli

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
		Args:  cobra.ExactArgs(1),
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
			if repository == "" && interactive {
				repository = p.Ask("Image repository (e.g. registry.example.com/project/app)", "")
			}
			if repository == "" {
				return fmt.Errorf("--repository is required")
			}
			if !cmd.Flags().Changed("type") && interactive {
				workload = []string{config.WorkloadService, config.WorkloadWorker}[p.Choose("Workload type", []string{"service", "worker"}, 0)]
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
	var project string
	var replicas, minReplicas, maxReplicas, targetCPU int
	var reset bool
	cmd := &cobra.Command{
		Use:   "scale <name>",
		Short: "Change an app's replica count or autoscaling range",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if project == "" {
				return fmt.Errorf("--project is required")
			}
			apps, err := config.LoadApps(config.ProjectsDir(app.Opts.ConfigDir), project)
			if err != nil {
				return err
			}
			af, ok := apps[name]
			if !ok {
				return fmt.Errorf("app %q not found in project %q", name, project)
			}
			switch {
			case reset:
				af.Replicas = nil
			case cmd.Flags().Changed("replicas"):
				af.Replicas = &config.ReplicasSpec{Min: replicas, Max: replicas}
			case cmd.Flags().Changed("min") || cmd.Flags().Changed("max"):
				af.Replicas = &config.ReplicasSpec{Min: minReplicas, Max: maxReplicas, TargetCPU: targetCPU}
			default:
				return fmt.Errorf("specify --replicas N, --min/--max, or --reset")
			}
			if err := writeYAML(appPath(app, project, name), af); err != nil {
				return err
			}
			switch {
			case af.Replicas == nil:
				app.Infof("Scaled %q to inherit the platform default", name)
			case af.Replicas.Autoscaling():
				app.Infof("Scaled %q to %d–%d replicas (target CPU %d%%)", name, af.Replicas.Min, af.Replicas.Max, af.Replicas.TargetCPU)
			default:
				app.Infof("Scaled %q to %d replica(s)", name, af.Replicas.Min)
			}
			app.Infof("Apply with `menina app deploy %s --project %s --environment <env>`, or let reconciliation pick it up.", name, project)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().IntVar(&replicas, "replicas", 0, "fixed replica count")
	cmd.Flags().IntVar(&minReplicas, "min", 0, "minimum replicas (enables autoscaling)")
	cmd.Flags().IntVar(&maxReplicas, "max", 0, "maximum replicas (enables autoscaling)")
	cmd.Flags().IntVar(&targetCPU, "target-cpu", 70, "target CPU utilization for autoscaling")
	cmd.Flags().BoolVar(&reset, "reset", false, "remove the override and inherit the platform default")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}
