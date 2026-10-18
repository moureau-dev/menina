package config

import (
	"fmt"
	"sort"
	"time"

	"github.com/moureau-dev/menina/internal/release"
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
		Project:      p.Name,
		Environment:  env.Name,
		Name:         app.Name,
		Namespace:    namespace,
		Enabled:      true,
		Source:       app.Source,
		Repository:   app.Image.Repository,
		Tag:          app.Image.Tag,
		PullPolicy:   app.Image.PullPolicy,
		Workload:     app.Workload,
		Resources:    app.Resources,
		Exposure:     cloneExposure(app.Exposure),
		Storage:      cloneStorage(app.Storage),
		Replicas:     cloneReplicas(app.Replicas),
		Health:       cloneHealth(app.Health),
		Verification: cloneVerification(app.Verification),
		Hooks:        cloneHooks(app.Hooks),
		Recovery:     cloneRecovery(app.Recovery),
		DependsOn:    append([]string(nil), app.DependsOn...),
		Env:          cloneEnv(app.Env),
		Secrets:      cloneEnv(app.Secrets),
		Webhook:      webhook,
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
		if override.Verification != nil {
			ra.Verification = cloneVerification(override.Verification)
		}
		if override.Hooks != nil {
			ra.Hooks = cloneHooks(override.Hooks)
		}
		if override.Recovery != nil {
			ra.Recovery = cloneRecovery(override.Recovery)
		}
		if len(override.DependsOn) > 0 {
			ra.DependsOn = append([]string(nil), override.DependsOn...)
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
	if err := r.normalizeRelease(app.Name, env.Name, ra, p); err != nil {
		return nil, err
	}
	return ra, nil
}

func (r *Resolver) normalizeRelease(name, environment string, ra *ResolvedApp, p *Project) error {
	if ra.Verification != nil {
		if ra.Verification.Enabled == nil {
			enabled := true
			ra.Verification.Enabled = &enabled
		}
		if *ra.Verification.Enabled {
			if ra.Verification.Method == "" {
				ra.Verification.Method = "GET"
			}
			if ra.Verification.Timeout <= 0 {
				ra.Verification.Timeout = 15 * time.Second
			}
			switch ra.Verification.FailurePolicy {
			case "":
				ra.Verification.FailurePolicy = VerificationFailureRecover
			case VerificationFailureRecover, VerificationFailureKeep:
			default:
				return fmt.Errorf("app %q environment %q has invalid verification.failurePolicy %q (recover|keep)", name, environment, ra.Verification.FailurePolicy)
			}
			if ra.Verification.Path == "" && ra.Verification.SmokeTest == nil {
				return fmt.Errorf("app %q environment %q enables verification without a path or smokeTest", name, environment)
			}
			if ra.Verification.SmokeTest != nil {
				if err := normalizeHook(fmt.Sprintf("app %q environment %q verification.smokeTest", name, environment), ra.Verification.SmokeTest, 5*time.Minute); err != nil {
					return err
				}
			}
		}
	}
	if ra.Hooks != nil {
		for label, hook := range map[string]*HookSpec{
			"preDeploy":  ra.Hooks.PreDeploy,
			"migrate":    ra.Hooks.Migrate,
			"postDeploy": ra.Hooks.PostDeploy,
		} {
			if hook == nil {
				continue
			}
			timeout := 2 * time.Minute
			if label == "migrate" {
				timeout = 5 * time.Minute
			}
			if err := normalizeHook(fmt.Sprintf("app %q environment %q hooks.%s", name, environment, label), hook, timeout); err != nil {
				return err
			}
		}
		if (ra.Hooks.PreDeploy != nil || ra.Hooks.Migrate != nil || ra.Hooks.PostDeploy != nil) && !r.cfg.Security.AllowAppHooks {
			return fmt.Errorf("app %q environment %q declares hooks, but app hooks are disabled; set security.allowAppHooks on the host", name, environment)
		}
	}
	if ra.Recovery == nil {
		ra.Recovery = &RecoverySpec{}
	}
	if ra.Recovery.Policy == "" {
		if ra.Hooks != nil && ra.Hooks.Migrate != nil {
			ra.Recovery.Policy = string(release.RecoveryRequireApproval)
		} else {
			ra.Recovery.Policy = string(release.RecoveryNone)
		}
	}
	if !release.ValidRecoveryPolicy(release.RecoveryPolicy(ra.Recovery.Policy)) {
		return fmt.Errorf("app %q environment %q has invalid recovery.policy %q (previous|none|require-approval)", name, environment, ra.Recovery.Policy)
	}
	if len(ra.Recovery.Triggers) == 0 {
		ra.Recovery.Triggers = []string{
			string(release.TriggerDeployError),
			string(release.TriggerVerificationFailed),
			string(release.TriggerTimeout),
			string(release.TriggerInterrupted),
		}
	}
	for _, trigger := range ra.Recovery.Triggers {
		if !release.ValidTrigger(release.RecoveryTrigger(trigger)) {
			return fmt.Errorf("app %q environment %q has invalid recovery trigger %q", name, environment, trigger)
		}
	}
	for _, dep := range ra.DependsOn {
		if dep == ra.Name {
			return fmt.Errorf("app %q environment %q cannot depend on itself", name, environment)
		}
		if _, ok := p.Apps[dep]; !ok {
			return fmt.Errorf("app %q environment %q depends on unknown app %q", name, environment, dep)
		}
	}
	return nil
}

func normalizeHook(label string, hook *HookSpec, defaultTimeout time.Duration) error {
	if len(hook.Command) == 0 {
		return fmt.Errorf("%s requires a command", label)
	}
	if hook.Timeout <= 0 {
		hook.Timeout = defaultTimeout
	}
	return nil
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

func cloneVerification(in *VerificationSpec) *VerificationSpec {
	if in == nil {
		return nil
	}
	out := *in
	if in.Enabled != nil {
		enabled := *in.Enabled
		out.Enabled = &enabled
	}
	if in.ExpectStatus != nil {
		out.ExpectStatus = append([]int(nil), in.ExpectStatus...)
	}
	if in.ExpectHeader != nil {
		out.ExpectHeader = map[string]string{}
		for k, v := range in.ExpectHeader {
			out.ExpectHeader[k] = v
		}
	}
	out.SmokeTest = cloneHook(in.SmokeTest)
	return &out
}

func cloneHooks(in *HooksSpec) *HooksSpec {
	if in == nil {
		return nil
	}
	out := *in
	out.PreDeploy = cloneHook(in.PreDeploy)
	out.Migrate = cloneHook(in.Migrate)
	out.PostDeploy = cloneHook(in.PostDeploy)
	return &out
}

func cloneHook(in *HookSpec) *HookSpec {
	if in == nil {
		return nil
	}
	out := *in
	if in.Command != nil {
		out.Command = append([]string(nil), in.Command...)
	}
	return &out
}

func cloneRecovery(in *RecoverySpec) *RecoverySpec {
	if in == nil {
		return nil
	}
	out := *in
	if in.Triggers != nil {
		out.Triggers = append([]string(nil), in.Triggers...)
	}
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
