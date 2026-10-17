package manifest

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/moureau-dev/menina/internal/config"
)

// RenderOptions controls how a manifest maps onto Menina's config model.
type RenderOptions struct {
	// ConfigDir is the target configuration directory; it is used to build
	// secret references (secrets/<project>/<environment>/<name>).
	ConfigDir string
	// Registry is the registry host used for built apps when the manifest does
	// not set defaults.registry.
	Registry string
	// Tag overrides defaults.tag and the environment name.
	Tag string
}

// Target is a buildable image declared by the manifest.
type Target struct {
	Name       string
	Repository string
	Tag        string
	Dockerfile string
	Context    string
	BuildArgs  map[string]string
	Worker     bool
}

// Rendered is the Menina configuration derived from a manifest.
type Rendered struct {
	File        *File
	Project     *config.ProjectFile
	Apps        map[string]*config.AppFile
	Environment *config.EnvironmentFile
	Overrides   map[string]*config.AppOverride
	Targets     []Target
	Warnings    []string
}

// Render validates the manifest and maps it onto project/app/environment
// configuration. It does not write anything.
func (f *File) Render(opts RenderOptions) (*Rendered, error) {
	tag := strings.TrimSpace(opts.Tag)
	if tag == "" {
		tag = strings.TrimSpace(f.Defaults.Tag)
	}
	if tag == "" {
		tag = f.Environment
	}
	registry := strings.TrimRight(strings.TrimSpace(opts.Registry), "/")
	if registry == "" {
		registry = strings.TrimRight(strings.TrimSpace(f.Defaults.Registry), "/")
	}

	out := &Rendered{
		File:        f,
		Project:     &config.ProjectFile{Name: f.Project},
		Apps:        map[string]*config.AppFile{},
		Environment: &config.EnvironmentFile{Name: f.Environment, Apps: map[string]*config.AppOverride{}},
		Overrides:   map[string]*config.AppOverride{},
	}

	names := map[string]string{}
	for name := range f.Apps {
		names[name] = "app"
	}
	for name := range f.Workers {
		if kind, ok := names[name]; ok {
			return nil, fmt.Errorf("%q is declared as both %s and worker", name, kind)
		}
		names[name] = "worker"
	}

	ordered := make([]string, 0, len(names))
	var appNames, workerNames []string
	for name, kind := range names {
		if kind == "worker" {
			workerNames = append(workerNames, name)
		} else {
			appNames = append(appNames, name)
		}
	}
	sort.Strings(appNames)
	sort.Strings(workerNames)
	ordered = append(ordered, appNames...)
	ordered = append(ordered, workerNames...)

	usedStorages := map[string]string{}
	var builtTargets []Target

	for _, name := range ordered {
		if !workloadNameRe.MatchString(name) {
			return nil, fmt.Errorf("invalid name %q: use lowercase letters, digits and hyphens", name)
		}
		worker := names[name] == "worker"
		w, ok := f.Apps[name]
		if !ok {
			w = f.Workers[name]
		}
		app, override, targets, used, err := f.renderWorkload(name, w, worker, tag, registry, opts.ConfigDir, out.Apps)
		if err != nil {
			return nil, err
		}
		for storage, owner := range used {
			if other, ok := usedStorages[storage]; ok {
				return nil, fmt.Errorf("storage %q is referenced by both %q and %q; named volumes cannot be shared between workloads yet", storage, other, owner)
			}
			usedStorages[storage] = owner
		}
		out.Apps[name] = app
		out.Environment.Apps[name] = override
		out.Overrides[name] = override
		builtTargets = append(builtTargets, targets...)
	}

	deduped, err := dedupeTargets(builtTargets)
	if err != nil {
		return nil, err
	}
	out.Targets = deduped

	for name := range f.Storages {
		if _, ok := usedStorages[name]; !ok {
			out.Warnings = append(out.Warnings, fmt.Sprintf("storage %q is declared but not referenced", name))
		}
	}
	sort.Strings(out.Warnings)
	return out, nil
}

func (f *File) renderWorkload(name string, w Workload, worker bool, tag, registry, configDir string, rendered map[string]*config.AppFile) (*config.AppFile, *config.AppOverride, []Target, map[string]string, error) {
	base := w
	if worker && w.From != "" {
		src, ok := f.Apps[w.From]
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("worker %q: from %q is not an app", name, w.From)
		}
		if w.Image != "" || w.Dockerfile != "" {
			return nil, nil, nil, nil, fmt.Errorf("worker %q: set either from or image/dockerfile, not both", name)
		}
		srcApp, ok := rendered[w.From]
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("worker %q: app %q has not been rendered", name, w.From)
		}
		base.Dockerfile = src.Dockerfile
		base.Context = src.Context
		base.Image = srcApp.Image.Repository
		if srcApp.Image.Tag != "" {
			base.Image += ":" + srcApp.Image.Tag
		}
		base.Env = mergeEnv(src.Env, w.Env)
		base.Secrets = mergeUnique(src.Secrets, w.Secrets)
		base.BuildArgs = mergeBuildArgs(src.BuildArgs, w.BuildArgs)
		if w.Resources == nil {
			base.Resources = src.Resources
		}
		// Storage is deliberately not inherited from the source app: a worker
		// mounting its parent app's SQLite volume would be a second writer.
		// An explicit worker storage block is kept.
	}

	kind := config.WorkloadService
	if worker {
		kind = config.WorkloadWorker
	}
	if base.Type != "" {
		if base.Type != config.WorkloadService && base.Type != config.WorkloadWorker {
			return nil, nil, nil, nil, fmt.Errorf("%q: invalid type %q (service|worker)", name, base.Type)
		}
		if worker && base.Type != config.WorkloadWorker {
			return nil, nil, nil, nil, fmt.Errorf("%q is in workers but declares type %q", name, base.Type)
		}
		if !worker && base.Type != config.WorkloadService {
			return nil, nil, nil, nil, fmt.Errorf("%q is in apps but declares type %q; move it to workers", name, base.Type)
		}
	}
	servicePort := 0
	if base.Service != nil {
		servicePort = base.Service.Port
	}
	if base.Port != 0 {
		if servicePort != 0 && servicePort != base.Port {
			return nil, nil, nil, nil, fmt.Errorf("%q: service.port and the legacy port must match", name)
		}
		servicePort = base.Port
	}
	if worker {
		if base.Service != nil {
			return nil, nil, nil, nil, fmt.Errorf("worker %q cannot declare service", name)
		}
		if servicePort != 0 {
			return nil, nil, nil, nil, fmt.Errorf("worker %q cannot declare port", name)
		}
		if base.Host != "" || base.Exposure != nil {
			return nil, nil, nil, nil, fmt.Errorf("worker %q cannot declare exposure", name)
		}
		if base.Health != nil {
			return nil, nil, nil, nil, fmt.Errorf("worker %q cannot declare health probes", name)
		}
	} else {
		if servicePort <= 0 || servicePort > 65535 {
			return nil, nil, nil, nil, fmt.Errorf("app %q must declare a valid service.port", name)
		}
	}

	exposure, err := resolveExposure(name, base)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	repository, imageTag, built, err := f.resolveImage(name, base, tag, registry)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	replicas := cloneReplicas(base.Replicas)
	storage, used, err := f.resolveStorage(name, base.Storage, &replicas)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	secrets := map[string]string{}
	for _, secret := range base.Secrets {
		if !secretNameRe.MatchString(secret) {
			return nil, nil, nil, nil, fmt.Errorf("%q: invalid secret name %q", name, secret)
		}
		secrets[secret] = "file:" + path.Join(configDir, "secrets", f.Project, f.Environment, secret)
	}

	app := &config.AppFile{
		Name:      name,
		Image:     config.ImageSpec{Repository: repository, Tag: imageTag},
		Workload:  config.WorkloadSpec{Type: kind, Port: servicePort, Command: base.Command, Args: base.Args},
		Resources: config.ResourceSpec{},
		Env:       mergeEnv(nil, base.Env),
		Secrets:   secrets,
		Storage:   storage,
		Replicas:  replicas,
		Health:    cloneHealth(base.Health),
	}
	if base.Resources != nil {
		app.Resources = *base.Resources
	}
	if app.Env == nil {
		app.Env = map[string]string{}
	}

	override := &config.AppOverride{Enabled: boolPtr(true)}
	if built {
		override.Image = &config.ImageSpec{Tag: imageTag}
	}
	if exposure != nil {
		override.Exposure = exposure
	}

	var targets []Target
	if built {
		context := strings.TrimSpace(base.Context)
		if context == "" {
			context = f.Defaults.Context
		}
		targets = append(targets, Target{
			Name:       name,
			Repository: repository,
			Tag:        imageTag,
			Dockerfile: resolvePath(f.BaseDir, base.Dockerfile),
			Context:    resolvePath(f.BaseDir, context),
			BuildArgs:  mergeBuildArgs(f.Defaults.BuildArgs, base.BuildArgs),
			Worker:     worker,
		})
	}
	return app, override, targets, used, nil
}

func resolveExposure(name string, w Workload) (*config.ExposureSpec, error) {
	hosts := []string{}
	if w.Host != "" {
		hosts = append(hosts, w.Host)
	}
	var http *HTTPExposure
	if w.Exposure != nil {
		http = w.Exposure.HTTP
	}
	if http != nil {
		hosts = append(hosts, http.Hosts...)
	}
	hosts = uniqueStrings(hosts)
	if http != nil && len(hosts) == 0 {
		return nil, fmt.Errorf("%q: exposure.http requires at least one host", name)
	}

	exp := &config.ExposureSpec{}
	if w.Exposure != nil {
		exp.Ports = w.Exposure.Ports
	}
	if len(hosts) > 0 {
		exp.Enabled = true
		exp.Hosts = hosts
		if http != nil {
			exp.PathPrefix = http.PathPrefix
			exp.RedirectHTTP = http.RedirectHTTP
			exp.Headers = http.Headers
			exp.Compression = http.Compression
			exp.H2C = http.H2C
			exp.Streaming = http.Streaming
			exp.MaxBodyBytes = http.MaxBodyBytes
			exp.Timeouts = http.Timeouts
		}
	}
	if !exp.Enabled && exp.Ports.IsZero() {
		return nil, nil
	}
	return exp, nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func (f *File) resolveImage(name string, w Workload, tag, registry string) (repository, imageTag string, built bool, err error) {
	if w.Dockerfile == "" && w.Image == "" {
		return "", "", false, fmt.Errorf("%q must declare dockerfile or image", name)
	}
	if w.Dockerfile != "" {
		built = true
		repo, explicitTag, err := splitImage(w.Image)
		if err != nil && w.Image != "" {
			return "", "", false, fmt.Errorf("%q: %w", name, err)
		}
		if w.Image == "" {
			if registry == "" {
				return "", "", false, fmt.Errorf("%q needs an image registry: set defaults.registry, pass --registry, or run where config.yml defines registry.domain", name)
			}
			repository = registry + "/" + f.Project + "/" + name
		} else {
			repository = repo
		}
		imageTag = tag
		if explicitTag != "" {
			imageTag = explicitTag
		}
		return repository, imageTag, true, nil
	}
	repository, imageTag, err = splitImage(w.Image)
	if err != nil {
		return "", "", false, fmt.Errorf("%q: %w", name, err)
	}
	return repository, imageTag, false, nil
}

func (f *File) resolveStorage(name string, refs StorageRefs, replicas **config.ReplicasSpec) (*config.StorageSpec, map[string]string, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	if len(refs) > 1 {
		return nil, nil, fmt.Errorf("%q: Menina currently supports one storage per workload", name)
	}
	ref := refs[0]
	used := map[string]string{}
	size, path, access := ref.Size, ref.Path, AccessSingleWriter
	if ref.Use != "" {
		storage, ok := f.Storages[ref.Use]
		if !ok {
			return nil, nil, fmt.Errorf("%q references unknown storage %q", name, ref.Use)
		}
		size = storage.Size
		if ref.Path != "" {
			path = ref.Path
		} else {
			path = storage.Path
		}
		access = storage.Access
		used[ref.Use] = name
	}
	if size == "" {
		return nil, nil, fmt.Errorf("%q: storage size is required", name)
	}
	if path == "" {
		return nil, nil, fmt.Errorf("%q: storage path is required", name)
	}
	if access == "" {
		access = AccessSingleWriter
	}
	switch access {
	case AccessSingleWriter:
		// Force one replica: SQLite and similar embedded stores are
		// single-writer, and a fixed count also avoids rollout overlap.
		if *replicas == nil {
			*replicas = &config.ReplicasSpec{Min: 1, Max: 1}
		} else if (*replicas).Autoscaling() || (*replicas).Min != 1 || (*replicas).Max != 1 {
			return nil, nil, fmt.Errorf("%q: storage is single-writer, so replicas must be 1", name)
		}
	case AccessMultiWriter:
		return nil, nil, fmt.Errorf("%q: multi-writer storage needs a ReadWriteMany storage class, which Menina does not configure yet", name)
	default:
		return nil, nil, fmt.Errorf("%q: invalid storage access %q (single-writer|multi-writer)", name, access)
	}
	return &config.StorageSpec{Enabled: true, Size: size, MountPath: path}, used, nil
}

func resolvePath(baseDir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}

func dedupeTargets(in []Target) ([]Target, error) {
	seen := map[string]Target{}
	out := make([]Target, 0, len(in))
	for _, t := range in {
		key := t.Repository + ":" + t.Tag + "|" + t.Dockerfile + "|" + t.Context
		if existing, ok := seen[key]; ok {
			if !equalBuildArgs(existing.BuildArgs, t.BuildArgs) {
				return nil, fmt.Errorf("%s is built by multiple workloads with different buildArgs", t.Image())
			}
			continue
		}
		seen[key] = t
		out = append(out, t)
	}
	return out, nil
}

func equalBuildArgs(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// ProjectModel returns the project tree for the resolver. It lets apply
// validate the rendered configuration before writing it.
func (r *Rendered) ProjectModel() *config.Project {
	return &config.Project{
		Name:      r.Project.Name,
		Namespace: r.Project.Namespace,
		Apps:      r.Apps,
		Envs:      map[string]*config.EnvironmentFile{r.Environment.Name: r.Environment},
		EnvOrder:  []string{r.Environment.Name},
	}
}

// Write persists the rendered configuration. It creates missing project and
// environment files but never rewrites an existing environment definition
// (other repositories may contribute to it). Apps and per-app overrides are
// written.
// PlannedFile is a config file apply would write.
type PlannedFile struct {
	Path    string
	Content []byte
}

// Plan returns the files apply would write, without touching disk. Existing
// project.yml and environment definition files are preserved, matching Write.
func (r *Rendered) Plan(configDir string) ([]PlannedFile, error) {
	var files []PlannedFile
	add := func(path string, v any) error {
		data, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		files = append(files, PlannedFile{Path: path, Content: data})
		return nil
	}

	projectPath := filepath.Join(config.ProjectDir(configDir, r.Project.Name), "project.yml")
	if !fileExists(projectPath) {
		if err := add(projectPath, r.Project); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(r.Apps))
	for name := range r.Apps {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(config.ProjectDir(configDir, r.Project.Name), "apps", name+".yml")
		if err := add(path, r.Apps[name]); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(config.EnvironmentPath(configDir, r.Project.Name, r.Environment.Name)); os.IsNotExist(err) {
		path := config.EnvironmentPath(configDir, r.Project.Name, r.Environment.Name)
		if err := add(path, &config.EnvironmentFile{Name: r.Environment.Name, Apps: map[string]*config.AppOverride{}}); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	names = names[:0]
	for name := range r.Overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := config.AppOverridePath(configDir, r.Project.Name, r.Environment.Name, name)
		if err := add(path, r.Overrides[name]); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// Write persists the rendered configuration.
func (r *Rendered) Write(configDir string) error {
	if err := config.EnsureProjectDirs(configDir, r.Project.Name); err != nil {
		return err
	}
	files, err := r.Plan(configDir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := writeFileBytes(file.Path, file.Content); err != nil {
			return err
		}
	}
	return nil
}

func writeFileBytes(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return err
	}
	return os.Chmod(path, 0o640)
}

func mergeEnv(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func mergeBuildArgs(base, override map[string]string) map[string]string {
	return mergeEnv(base, override)
}

func mergeUnique(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, v := range list {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

func cloneReplicas(in *config.ReplicasSpec) *config.ReplicasSpec {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneHealth(in *config.HealthSpec) *config.HealthSpec {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func boolPtr(v bool) *bool { return &v }

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
