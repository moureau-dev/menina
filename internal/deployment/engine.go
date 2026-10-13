package deployment

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
)

type DigestResolver interface {
	ResolveTag(ctx context.Context, repository, tag string) (string, error)
}

type Engine struct {
	cluster  Cluster
	db       *database.DB
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

func NewEngine(cluster Cluster, db *database.DB, resolver DigestResolver, log *slog.Logger) *Engine {
	return &Engine{cluster: cluster, db: db, resolver: resolver, log: log}
}

func (e *Engine) ResolveDigest(ctx context.Context, app *config.ResolvedApp) (string, error) {
	if e.resolver == nil || app.Repository == "" || app.Tag == "" {
		return app.DesiredDigest, nil
	}
	return e.resolver.ResolveTag(ctx, app.Repository, app.Tag)
}

func (e *Engine) DeployResolved(ctx context.Context, app *config.ResolvedApp, desiredDigest string) (*Result, error) {
	if !app.Enabled {
		return nil, fmt.Errorf("app %s/%s/%s is disabled", app.Project, app.Environment, app.Name)
	}
	w := e.buildWorkload(app, desiredDigest)
	if err := e.cluster.EnsureNamespace(ctx, app.Namespace); err != nil {
		return nil, fmt.Errorf("ensure namespace %q: %w", app.Namespace, err)
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
	if e.db != nil {
		_ = e.db.RecordRelease(ctx, database.Release{
			Project:     app.Project,
			Environment: app.Environment,
			App:         app.Name,
			Repository:  app.Repository,
			Tag:         app.Tag,
			Digest:      desiredDigest,
			Status:      "deployed",
		})
	}
	e.log.Info("workload applied", "project", app.Project, "environment", app.Environment, "app", app.Name, "image", w.Image)
	return &Result{
		Project:     app.Project,
		Environment: app.Environment,
		App:         app.Name,
		Namespace:   app.Namespace,
		Image:       w.Image,
		Digest:      desiredDigest,
		RolledOut:   true,
	}, nil
}

func (e *Engine) Deploy(ctx context.Context, app *config.ResolvedApp) (*Result, error) {
	digest, err := e.ResolveDigest(ctx, app)
	if err != nil {
		return nil, err
	}
	return e.DeployResolved(ctx, app, digest)
}

func (e *Engine) Rollback(ctx context.Context, project, environment, app, digest string) (*Result, error) {
	if digest == "" {
		return nil, fmt.Errorf("rollback requires a recorded image digest")
	}
	releases, err := e.db.Releases(ctx, project, environment, app)
	if err != nil {
		return nil, err
	}
	found := false
	for _, r := range releases {
		if r.Digest == digest {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("digest %s has no recorded release for %s/%s/%s", digest, project, environment, app)
	}
	res := &Result{Project: project, Environment: environment, App: app, Digest: digest, RolledOut: true, NoOp: false}
	return res, nil
}

func (e *Engine) buildWorkload(app *config.ResolvedApp, digest string) Workload {
	image := app.Repository
	if digest != "" {
		image = app.Repository + "@" + digest
	} else if app.Tag != "" {
		image = app.Repository + ":" + app.Tag
	}
	w := Workload{
		Project:     app.Project,
		Environment: app.Environment,
		App:         app.Name,
		Namespace:   app.Namespace,
		Name:        app.Name,
		Image:       image,
		Type:        app.Workload.Type,
		Replicas:    app.Replicas,
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
	return out
}
