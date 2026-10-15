package config

import (
	"fmt"
	"sort"
)

type ResolvedConfig struct {
	Version  int64
	Projects map[string]*ResolvedProject
}

type Resolver struct {
	cfg *Config
}

func NewResolver(cfg *Config) *Resolver {
	return &Resolver{cfg: cfg}
}

func (r *Resolver) Resolve(projects []*Project, version int64) (*ResolvedConfig, error) {
	out := &ResolvedConfig{Version: version, Projects: map[string]*ResolvedProject{}}
	for _, p := range projects {
		rp, err := r.resolveProject(p)
		if err != nil {
			return nil, err
		}
		out.Projects[p.Name] = rp
	}
	return out, nil
}

func (r *Resolver) resolveProject(p *Project) (*ResolvedProject, error) {
	ns := p.Namespace
	if ns == "" {
		ns = r.cfg.Defaults.Namespace
	}
	rp := &ResolvedProject{
		Name:         p.Name,
		Namespace:    ns,
		Description:  p.Description,
		Environments: map[string]*ResolvedEnvironment{},
	}
	rp.DeployWebhook = r.effectiveDeployWebhook(p)
	for _, envName := range p.EnvOrder {
		envFile := p.Envs[envName]
		envNS := envFile.Namespace
		if envNS == "" {
			base := p.Namespace
			if base == "" {
				base = r.cfg.Defaults.Namespace
			}
			envNS = base + "-" + envName
		}
		re := &ResolvedEnvironment{Project: p.Name, Name: envName, Apps: map[string]*ResolvedApp{}}
		names := make([]string, 0, len(p.Apps))
		for n := range p.Apps {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			app := p.Apps[name]
			override := envFile.Apps[name]
			ra, err := r.resolveApp(p, envFile, app, override, envNS, rp.DeployWebhook)
			if err != nil {
				return nil, err
			}
			re.Apps[name] = ra
		}
		for name := range envFile.Apps {
			if _, ok := p.Apps[name]; !ok {
				return nil, fmt.Errorf("project %q environment %q references unknown app %q", p.Name, envName, name)
			}
		}
		rp.Environments[envName] = re
	}
	return rp, nil
}

func (r *Resolver) resolveApp(p *Project, env *EnvironmentFile, app *AppFile, override *AppOverride, namespace string, webhook *DeployWebhook) (*ResolvedApp, error) {
	ra := &ResolvedApp{
		Project:     p.Name,
		Environment: env.Name,
		Name:        app.Name,
		Namespace:   namespace,
		Enabled:     true,
		Source:      app.Source,
		Repository:  app.Image.Repository,
		Tag:         app.Image.Tag,
		PullPolicy:  app.Image.PullPolicy,
		Workload:    app.Workload,
		Resources:   app.Resources,
		Exposure:    cloneExposure(app.Exposure),
		Storage:     cloneStorage(app.Storage),
		Replicas:    cloneReplicas(app.Replicas),
		Health:      cloneHealth(app.Health),
		Env:         cloneEnv(app.Env),
		Secrets:     cloneEnv(app.Secrets),
		Webhook:     webhook,
	}
	if ra.Workload.Type == "" {
		ra.Workload.Type = WorkloadService
	}
	if override != nil {
		if override.Enabled != nil {
			ra.Enabled = *override.Enabled
		}
		if override.Image != nil {
			img := override.Image
			if img.Repository != "" && app.Image.Repository != "" && img.Repository != app.Image.Repository {
				return nil, fmt.Errorf("app %q environment %q cannot override image repository", app.Name, env.Name)
			}
			if img.Repository != "" {
				ra.Repository = img.Repository
			}
			if img.Tag != "" {
				ra.Tag = img.Tag
			}
			if img.PullPolicy != "" {
				ra.PullPolicy = img.PullPolicy
			}
		}
		if override.Workload != nil {
			ra.Workload = mergeWorkload(ra.Workload, *override.Workload)
		}
		if override.Resources != nil {
			ra.Resources = mergeResources(ra.Resources, *override.Resources)
		}
		if override.Exposure != nil {
			ra.Exposure = mergeExposure(ra.Exposure, *override.Exposure)
		}
		if override.Storage != nil {
			ra.Storage = cloneStorage(override.Storage)
		}
		if override.Health != nil {
			ra.Health = cloneHealth(override.Health)
		}
		if override.Replicas != nil {
			ra.Replicas = *override.Replicas
		}
		for k, v := range override.Env {
			ra.Env[k] = v
		}
		for k, v := range override.Secrets {
			ra.Secrets[k] = v
		}
	}
	if ra.Exposure.Enabled && len(ra.Exposure.Hosts) == 0 {
		return nil, fmt.Errorf("app %q environment %q enables exposure without hosts", app.Name, env.Name)
	}
	if ra.Tag == "" {
		switch r.cfg.Defaults.ImageTagStrategy {
		case "environment", "":
			ra.Tag = env.Name
		case "latest":
			ra.Tag = "latest"
		default:
			ra.Tag = env.Name
		}
	}
	if ra.Replicas.Min == 0 && ra.Replicas.Max == 0 {
		ra.Replicas = ReplicasSpec{Min: r.cfg.Defaults.Replicas, Max: r.cfg.Defaults.Replicas}
	}
	if ra.Replicas.Min == 0 {
		ra.Replicas.Min = r.cfg.Defaults.Replicas
	}
	if ra.Replicas.Max == 0 {
		ra.Replicas.Max = ra.Replicas.Min
	}
	if ra.Replicas.Max < ra.Replicas.Min {
		ra.Replicas.Max = ra.Replicas.Min
	}
	if ra.Replicas.Autoscaling() && ra.Replicas.TargetCPU == 0 {
		ra.Replicas.TargetCPU = 70
	}
	if ra.Resources.CPU == "" {
		ra.Resources.CPU = r.cfg.Defaults.CPU
	}
	if ra.Resources.Memory == "" {
		ra.Resources.Memory = r.cfg.Defaults.Memory
	}
	return ra, nil
}

func mergeWorkload(base, override WorkloadSpec) WorkloadSpec {
	if override.Type != "" {
		base.Type = override.Type
	}
	if override.Port != 0 {
		base.Port = override.Port
	}
	if len(override.Command) > 0 {
		base.Command = override.Command
	}
	if len(override.Args) > 0 {
		base.Args = override.Args
	}
	return base
}

func mergeResources(base, override ResourceSpec) ResourceSpec {
	if override.CPU != "" {
		base.CPU = override.CPU
	}
	if override.Memory != "" {
		base.Memory = override.Memory
	}
	return base
}

func mergeExposure(base, override ExposureSpec) ExposureSpec {
	base.Enabled = override.Enabled
	if len(override.Hosts) > 0 {
		base.Hosts = override.Hosts
	}
	if override.PathPrefix != "" {
		base.PathPrefix = override.PathPrefix
	}
	base.RedirectHTTP = override.RedirectHTTP
	base.Compression = override.Compression
	base.H2C = override.H2C
	base.Streaming = override.Streaming
	if override.MaxBodyBytes > 0 {
		base.MaxBodyBytes = override.MaxBodyBytes
	}
	if override.Timeouts != (RouteTimeouts{}) {
		base.Timeouts = override.Timeouts
	}
	if len(override.Headers) > 0 {
		if base.Headers == nil {
			base.Headers = map[string]string{}
		}
		for k, v := range override.Headers {
			base.Headers[k] = v
		}
	}
	if !override.Ports.IsZero() {
		base.Ports = override.Ports
	}
	return base
}

func cloneExposure(in ExposureSpec) ExposureSpec {
	out := in
	if in.Hosts != nil {
		out.Hosts = append([]string(nil), in.Hosts...)
	}
	if in.Headers != nil {
		out.Headers = map[string]string{}
		for k, v := range in.Headers {
			out.Headers[k] = v
		}
	}
	if in.Ports.TCP != nil {
		out.Ports.TCP = append([]int(nil), in.Ports.TCP...)
	}
	if in.Ports.UDP != nil {
		out.Ports.UDP = append([]int(nil), in.Ports.UDP...)
	}
	return out
}

func cloneStorage(in *StorageSpec) *StorageSpec {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneReplicas(in *ReplicasSpec) ReplicasSpec {
	if in == nil {
		return ReplicasSpec{}
	}
	return *in
}

func cloneHealth(in *HealthSpec) *HealthSpec {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneEnv(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (r *Resolver) effectiveDeployWebhook(p *Project) *DeployWebhook {
	wh := r.cfg.Webhooks.Deploy
	if p.Webhooks.Deploy != nil {
		pw := *p.Webhooks.Deploy
		if pw.URLRef != "" {
			wh.URLRef = pw.URLRef
			wh.Enabled = true
		}
		if pw.SecretRef != "" {
			wh.SecretRef = pw.SecretRef
		}
		if len(pw.Events) > 0 {
			wh.Events = pw.Events
		}
		if pw.Enabled {
			wh.Enabled = true
		}
	}
	if wh.Enabled && len(wh.Events) == 0 {
		wh.Events = DefaultDeployEvents
	}
	if wh.URLRef == "" {
		wh.Enabled = false
	}
	return &wh
}

func (rc *ResolvedConfig) FindApp(project, environment, app string) (*ResolvedApp, error) {
	rp, ok := rc.Projects[project]
	if !ok {
		return nil, fmt.Errorf("project %q not found", project)
	}
	re, ok := rp.Environments[environment]
	if !ok {
		return nil, fmt.Errorf("environment %q not found in project %q", environment, project)
	}
	ra, ok := re.Apps[app]
	if !ok {
		return nil, fmt.Errorf("app %q not found in %s/%s", app, project, environment)
	}
	return ra, nil
}
