package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/moureau-dev/menina/internal/core/config"
	"github.com/moureau-dev/menina/internal/core/secrets"
	"github.com/moureau-dev/menina/internal/core/state/contract"
)

type DigestResolver interface {
	ResolveTag(ctx context.Context, repository, tag string) (string, error)
}

type Engine struct {
	cluster  Cluster
	db       contract.Store
	resolver DigestResolver
	log      *slog.Logger
}

type Result struct {
	Project     string
	Environment string
	App         string
	Namespace   string
	Image       string
	Digest      string
	RolledOut   bool
	NoOp        bool
}

func NewEngine(cluster Cluster, db contract.Store, resolver DigestResolver, log *slog.Logger) *Engine {
	return &Engine{cluster: cluster, db: db, resolver: resolver, log: log}
}

func (e *Engine) ResolveDigest(ctx context.Context, app *config.ResolvedApp) (string, error) {
	if e.resolver == nil || app.Repository == "" || app.Tag == "" {
		return app.DesiredDigest, nil
	}
	return e.resolver.ResolveTag(ctx, app.Repository, app.Tag)
}

func (e *Engine) DeployResolved(ctx context.Context, app *config.ResolvedApp, desiredDigest string) (*Result, error) {
	return e.applyResolved(ctx, app, ImageForDigest(app, desiredDigest))
}

// Apply reconciles an app's Kubernetes resources to the given image. It is used
// by the configuration-driven reconciler and is idempotent (an unchanged pod
// template does not trigger a rollout). Release history is recorded by the
// caller only after a rollout succeeds.
func (e *Engine) Apply(ctx context.Context, app *config.ResolvedApp, image string) (*Result, error) {
	return e.applyResolved(ctx, app, image)
}

// ImageForDigest returns the image reference for an app: repository@digest when
// a digest is known, otherwise repository:tag.
func ImageForDigest(app *config.ResolvedApp, digest string) string {
	if digest != "" {
		return app.Repository + "@" + digest
	}
	if app.Tag != "" {
		return app.Repository + ":" + app.Tag
	}
	return app.Repository
}

func (e *Engine) applyResolved(ctx context.Context, app *config.ResolvedApp, image string) (*Result, error) {
	if !app.Enabled {
		return nil, fmt.Errorf("app %s/%s/%s is disabled", app.Project, app.Environment, app.Name)
	}
	w := e.buildWorkloadImage(app, image)
	if err := e.cluster.EnsureNamespace(ctx, app.Namespace); err != nil {
		return nil, fmt.Errorf("ensure namespace %q: %w", app.Namespace, err)
	}
	if len(app.Secrets) > 0 {
		secretName, secretHash, err := e.ensureAppSecret(ctx, app)
		if err != nil {
			return nil, err
		}
		w.SecretEnvName = secretName
		w.SecretEnv = map[string]string{}
		w.SecretHash = secretHash
		for name := range app.Secrets {
			w.SecretEnv[name] = name
		}
	}
	if app.Storage != nil && app.Storage.Enabled {
		size := app.Storage.Size
		if size == "" {
			size = "1Gi"
		}
		if err := e.cluster.EnsurePVC(ctx, app.Namespace, app.Name+"-data", size, ""); err != nil {
			return nil, fmt.Errorf("ensure pvc for %s/%s: %w", app.Namespace, app.Name, err)
		}
	}
	if app.Workload.Type == config.WorkloadService && app.Workload.Port > 0 {
		if err := e.cluster.ApplyService(ctx, w); err != nil {
			return nil, fmt.Errorf("apply service for %s/%s: %w", app.Namespace, app.Name, err)
		}
	}
	if err := e.cluster.ApplyWorkload(ctx, w); err != nil {
		return nil, fmt.Errorf("apply workload for %s/%s: %w", app.Namespace, app.Name, err)
	}
	e.log.Info("workload applied", "project", app.Project, "environment", app.Environment, "app", app.Name, "image", w.Image)
	return &Result{
		Project:     app.Project,
		Environment: app.Environment,
		App:         app.Name,
		Namespace:   app.Namespace,
		Image:       w.Image,
		RolledOut:   true,
	}, nil
}

// EnsureAppSecrets projects an app's secret references into a Kubernetes
// Secret and returns the Secret name and a fingerprint of its values.
func (e *Engine) EnsureAppSecrets(ctx context.Context, app *config.ResolvedApp) (string, string, error) {
	if len(app.Secrets) == 0 {
		return "", "", nil
	}
	return e.ensureAppSecret(ctx, app)
}

// ensureAppSecret resolves an app's secret references to values, projects them
// into a Kubernetes Secret, and returns the Secret's name. Only references are
// ever stored in configuration; resolved values stay out of the config tree.
func (e *Engine) ensureAppSecret(ctx context.Context, app *config.ResolvedApp) (string, string, error) {
	name := app.Name + "-secrets"
	data, hash, err := resolveSecrets(app)
	if err != nil {
		return "", "", err
	}
	if err := e.cluster.EnsureSecret(ctx, app.Namespace, name, data); err != nil {
		return "", "", fmt.Errorf("ensure secret for %s/%s: %w", app.Namespace, app.Name, err)
	}
	return name, hash, nil
}

// SecretFingerprint returns a stable hash of the resolved secret values that
// are injected into an app. It never returns the values themselves.
func SecretFingerprint(app *config.ResolvedApp) (string, error) {
	_, hash, err := resolveSecrets(app)
	return hash, err
}

func resolveSecrets(app *config.ResolvedApp) (map[string][]byte, string, error) {
	resolver := secrets.NewResolver()
	data := make(map[string][]byte, len(app.Secrets))
	names := make([]string, 0, len(app.Secrets))
	for envName := range app.Secrets {
		names = append(names, envName)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, envName := range names {
		value, err := resolver.Resolve(app.Secrets[envName])
		if err != nil {
			return nil, "", fmt.Errorf("resolve secret %q for %s/%s: %w", envName, app.Namespace, app.Name, err)
		}
		data[envName] = []byte(value)
		h.Write([]byte(envName))
		h.Write([]byte{0})
		h.Write([]byte(value))
		h.Write([]byte{0})
	}
	if len(names) == 0 {
		return data, "", nil
	}
	return data, "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (e *Engine) buildWorkload(app *config.ResolvedApp, digest string) Workload {
	return e.buildWorkloadImage(app, ImageForDigest(app, digest))
}

func (e *Engine) buildWorkloadImage(app *config.ResolvedApp, image string) Workload {
	maxReplicas := 0
	targetCPU := 0
	if app.Replicas.Autoscaling() {
		maxReplicas = app.Replicas.Max
		targetCPU = app.Replicas.TargetCPU
	}
	w := Workload{
		Project:     app.Project,
		Environment: app.Environment,
		App:         app.Name,
		Namespace:   app.Namespace,
		Name:        app.Name,
		Image:       image,
		Type:        app.Workload.Type,
		Replicas:    app.Replicas.Min,
		MaxReplicas: maxReplicas,
		TargetCPU:   targetCPU,
		Port:        app.Workload.Port,
		Command:     app.Workload.Command,
		Args:        app.Workload.Args,
		Env:         app.Env,
		CPU:         app.Resources.CPU,
		Memory:      app.Resources.Memory,
		PullPolicy:  app.PullPolicy,
	}
	if app.Health != nil {
		if app.Health.Readiness != "" {
			w.HealthPath = app.Health.Readiness
		} else if app.Health.Liveness != "" {
			w.HealthPath = app.Health.Liveness
		}
	}
	if app.Storage != nil && app.Storage.Enabled {
		w.StorageSize = app.Storage.Size
		w.MountPath = app.Storage.MountPath
	}
	for _, p := range app.Exposure.Ports.TCP {
		w.Ports = append(w.Ports, PortMapping{Port: p, Protocol: "TCP"})
	}
	for _, p := range app.Exposure.Ports.UDP {
		w.Ports = append(w.Ports, PortMapping{Port: p, Protocol: "UDP"})
	}
	return w
}

func SanitizeName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_' || r == '.' || r == ' ':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	return out
}
