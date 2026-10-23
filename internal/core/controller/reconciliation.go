package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/core/worker"
	"github.com/moureau-dev/menina/internal/database"
	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/edge"
	"github.com/moureau-dev/menina/internal/release"
)

type Reconciler struct {
	db          *database.DB
	cluster     deployment.Cluster
	engine      *deployment.Engine
	snapshot    func() *config.ResolvedConfig
	cfg         *config.Config
	log         *slog.Logger
	onPublish   func(*edge.Snapshot)
	mu          sync.Mutex
	lastSig     string
	reconcileMu sync.Mutex
}

const (
	registryZotService = "zot"
	registryZotPort    = 5000
)

func NewReconciler(db *database.DB, cluster deployment.Cluster, engine *deployment.Engine, snapshot func() *config.ResolvedConfig, cfg *config.Config, log *slog.Logger) *Reconciler {
	return &Reconciler{db: db, cluster: cluster, engine: engine, snapshot: snapshot, cfg: cfg, log: log}
}

func (r *Reconciler) OnPublish(fn func(*edge.Snapshot)) { r.onPublish = fn }

func (r *Reconciler) BuildSnapshot(rc *config.ResolvedConfig) (*edge.Snapshot, error) {
	snap := &edge.Snapshot{Version: rc.Version, GeneratedAt: time.Now()}
	var dbroutes []database.Route
	for _, rp := range rc.Projects {
		for _, re := range rp.Environments {
			for _, app := range re.Apps {
				if !app.Enabled || !app.Exposure.Enabled {
					continue
				}
				if app.Workload.Type != config.WorkloadService || app.Workload.Port == 0 {
					r.log.Warn("skipping exposure for non-service app", "app", app.Name)
					continue
				}
				ip, err := r.cluster.ServiceClusterIP(context.Background(), app.Namespace, app.Name)
				if err != nil || ip == "" || ip == "None" {
					r.log.Warn("service has no ClusterIP; route degraded", "app", app.Name, "namespace", app.Namespace, "error", err)
					continue
				}
				backend := fmt.Sprintf("%s:%d", ip, app.Workload.Port)
				headers, _ := json.Marshal(app.Exposure.Headers)
				timeouts, _ := json.Marshal(app.Exposure.Timeouts)
				for _, host := range app.Exposure.Hosts {
					snap.Routes = append(snap.Routes, edge.Route{
						Host:         host,
						PathPrefix:   app.Exposure.PathPrefix,
						Backend:      backend,
						Service:      app.Name,
						Namespace:    app.Namespace,
						Project:      app.Project,
						Environment:  app.Environment,
						App:          app.Name,
						TLS:          r.cfg.TLS.Terminate,
						RedirectHTTP: app.Exposure.RedirectHTTP || r.cfg.TLS.HTTPRedirect,
						Compression:  app.Exposure.Compression,
						H2C:          app.Exposure.H2C,
						Streaming:    app.Exposure.Streaming,
						MaxBodyBytes: app.Exposure.MaxBodyBytes,
						Headers:      app.Exposure.Headers,
						Timeouts: edge.RouteTimeouts{
							Read:  app.Exposure.Timeouts.Read,
							Write: app.Exposure.Timeouts.Write,
							Idle:  app.Exposure.Timeouts.Idle,
						},
					})
					dbroutes = append(dbroutes, database.Route{
						Host:         host,
						PathPrefix:   app.Exposure.PathPrefix,
						Project:      app.Project,
						Environment:  app.Environment,
						App:          app.Name,
						Namespace:    app.Namespace,
						Service:      app.Name,
						Port:         app.Workload.Port,
						TLS:          r.cfg.TLS.Terminate,
						RedirectHTTP: app.Exposure.RedirectHTTP || r.cfg.TLS.HTTPRedirect,
						Headers:      string(headers),
						Timeouts:     string(timeouts),
					})
				}
			}
		}
	}
	if r.cfg.Registry.Enabled && r.cfg.Registry.Domain != "" {
		ns := r.cfg.Registry.Namespace
		if ns == "" {
			ns = "menina-registry"
		}
		if ip, err := r.cluster.ServiceClusterIP(context.Background(), ns, registryZotService); err == nil && ip != "" && ip != "None" {
			backend := fmt.Sprintf("%s:%d", ip, registryZotPort)
			snap.Routes = append(snap.Routes, edge.Route{
				Host:         r.cfg.Registry.Domain,
				PathPrefix:   "/",
				Backend:      backend,
				Service:      registryZotService,
				Namespace:    ns,
				Project:      "_registry",
				Environment:  "_platform",
				App:          registryZotService,
				TLS:          r.cfg.TLS.Terminate,
				RedirectHTTP: r.cfg.TLS.HTTPRedirect,
			})
			dbroutes = append(dbroutes, database.Route{
				Host:         r.cfg.Registry.Domain,
				PathPrefix:   "/",
				Project:      "_registry",
				Environment:  "_platform",
				App:          registryZotService,
				Namespace:    ns,
				Service:      registryZotService,
				Port:         registryZotPort,
				TLS:          r.cfg.TLS.Terminate,
				RedirectHTTP: r.cfg.TLS.HTTPRedirect,
			})
		} else {
			r.log.Warn("registry service has no ClusterIP; registry route not published", "error", err)
		}
	}
	if err := r.db.ReplaceRoutes(context.Background(), dbroutes); err != nil {
		return nil, err
	}
	return snap, nil
}

func (r *Reconciler) Publish(ctx context.Context, rc *config.ResolvedConfig) error {
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	return r.publishLocked(ctx, rc)
}

func (r *Reconciler) publishLocked(ctx context.Context, rc *config.ResolvedConfig) error {
	snap, err := r.BuildSnapshot(rc)
	if err != nil {
		return err
	}
	if data, err := json.Marshal(rc); err == nil {
		_ = r.db.SetMeta(ctx, "resolved_config", string(data))
	}
	r.publishSnapshot(snap)
	return nil
}

func (r *Reconciler) publishSnapshot(snap *edge.Snapshot) {
	r.mu.Lock()
	sig := routeSignature(snap.Routes)
	changed := sig != r.lastSig
	r.lastSig = sig
	r.mu.Unlock()
	if changed && r.onPublish != nil {
		r.onPublish(snap)
	}
}

func routeSignature(routes []edge.Route) string {
	parts := make([]string, 0, len(routes))
	for _, r := range routes {
		parts = append(parts, fmt.Sprintf("%s|%s|%s", r.Host, r.PathPrefix, r.Backend))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

// ReconcileAll rebuilds the routing snapshot (refreshing ClusterIPs and
// recovering routes that were degraded when a Service did not yet exist),
// reconciles Kubernetes workloads to the desired configuration, and then
// reconciles image tags. It runs on the periodic ticker so routing does not
// silently drift while only configuration-triggered reloads would fix it.
func (r *Reconciler) ReconcileAll(ctx context.Context) {
	rc := r.snapshot()
	if rc == nil {
		return
	}
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	if err := r.publishLocked(ctx, rc); err != nil {
		r.log.Warn("route reconciliation failed", "error", err)
	}
	r.ReconcileWorkloads(ctx, rc)
	r.ReconcileTags(ctx)
}

// ReconcileWorkloads applies the desired Kubernetes resources for every enabled
// app and removes workloads that are no longer part of the desired state
// (disabled, deleted or renamed apps). Applying an unchanged workload is a
// no-op: the pod template hash is identical so Kubernetes does not roll out.
func (r *Reconciler) ReconcileWorkloads(ctx context.Context, rc *config.ResolvedConfig) {
	if rc == nil {
		return
	}
	desired := map[string]bool{}
	for _, rp := range rc.Projects {
		for _, re := range rp.Environments {
			for _, name := range orderedApps(re) {
				app := re.Apps[name]
				if app == nil || !app.Enabled {
					continue
				}
				if r.releaseHold(ctx, app) {
					r.log.Info("release is rolled back; skipping reconciliation until a new deploy", "app", app.Name)
					desired[app.Namespace+"/"+app.Name] = true
					continue
				}
				image := r.desiredImage(ctx, app)
				if _, err := r.engine.Apply(ctx, app, image); err != nil {
					r.log.Warn("config reconcile: apply failed", "app", app.Name, "namespace", app.Namespace, "error", err)
				}
				// Mark desired even if the apply failed, so a configured app is
				// never pruned just because the cluster was briefly unreachable.
				desired[app.Namespace+"/"+app.Name] = true
			}
		}
	}
	r.syncSoftDeletes(ctx)
	r.pruneWorkloads(ctx, desired)
}

// syncSoftDeletes records the current soft-deleted definitions (derived from the
// config tree's trash directories) as soft-deleted observed rows. The control
// plane is the single SQLite writer, so this is where filesystem state becomes
// rows; the reconciler then stops (scales to zero) rather than deletes them.
func (r *Reconciler) syncSoftDeletes(ctx context.Context) {
	found, err := config.ScanSoftDeletes(r.cfg.Host.ConfigDir)
	if err != nil {
		r.log.Warn("config reconcile: could not scan soft deletes", "error", err)
		return
	}
	targets := make([]database.SoftDeleteTarget, 0, len(found))
	for _, s := range found {
		targets = append(targets, database.SoftDeleteTarget{Project: s.Project, Environment: s.Environment, App: s.App})
	}
	if err := r.db.SyncObservedSoftDeletes(ctx, targets); err != nil {
		r.log.Warn("config reconcile: could not record soft deletes", "error", err)
	}
}

func (r *Reconciler) desiredImage(ctx context.Context, app *config.ResolvedApp) string {
	digest, err := r.engine.ResolveDigest(ctx, app)
	if err == nil && digest != "" {
		return deployment.ImageForDigest(app, digest)
	}
	if err != nil {
		// Preserve the currently pinned digest instead of falling back to a
		// mutable tag when the registry is temporarily unreachable.
		if st, serr := r.cluster.WorkloadStatus(ctx, app.Namespace, app.Name); serr == nil && st.Image != "" {
			return st.Image
		}
		r.log.Warn("config reconcile: could not resolve digest; using configured tag", "app", app.Name, "error", err)
	}
	return deployment.ImageForDigest(app, "")
}

func (r *Reconciler) pruneWorkloads(ctx context.Context, desired map[string]bool) {
	workloads, err := r.cluster.ListWorkloads(ctx)
	if err != nil {
		r.log.Warn("config reconcile: could not list workloads for pruning", "error", err)
		return
	}
	soft, err := r.db.SoftDeletedTargets(ctx)
	if err != nil {
		r.log.Warn("config reconcile: could not read soft deletes", "error", err)
	}
	registryNS := r.cfg.Registry.Namespace
	if registryNS == "" {
		registryNS = "menina-registry"
	}
	for _, w := range workloads {
		if w.Namespace == registryNS {
			continue
		}
		if desired[w.Namespace+"/"+w.Name] {
			continue
		}
		if w.App != "" && softDeleted(soft, w.Project, w.Environment, w.App) {
			// Soft-deleted: stop (scale to zero) and keep the objects, PVC and
			// history so it can be restored.
			r.log.Info("config reconcile: stopping soft-deleted workload", "namespace", w.Namespace, "name", w.Name)
			if err := r.cluster.ScaleWorkload(ctx, w.Namespace, w.Name, 0); err != nil {
				r.log.Warn("config reconcile: scale to zero failed", "name", w.Name, "error", err)
			}
			continue
		}
		r.log.Info("config reconcile: removing workload no longer in desired state", "namespace", w.Namespace, "name", w.Name)
		if err := r.cluster.DeleteWorkload(ctx, w.Namespace, w.Name); err != nil {
			r.log.Warn("config reconcile: delete workload failed", "name", w.Name, "error", err)
		}
		if err := r.cluster.DeleteService(ctx, w.Namespace, w.Name); err != nil {
			r.log.Warn("config reconcile: delete service failed", "name", w.Name, "error", err)
		}
		if err := r.cluster.DeleteAutoscaler(ctx, w.Namespace, w.Name); err != nil {
			r.log.Warn("config reconcile: delete autoscaler failed", "name", w.Name, "error", err)
		}
		if w.App != "" {
			if err := r.db.DeleteObserved(ctx, w.Project, w.Environment, w.App); err != nil {
				r.log.Warn("config reconcile: delete observed row failed", "name", w.Name, "error", err)
			}
		}
	}
}

func softDeleted(list []database.SoftDeleteTarget, project, environment, app string) bool {
	for _, t := range list {
		if t.Project == project && t.Environment == environment && t.App == app {
			return true
		}
	}
	return false
}

func (r *Reconciler) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	r.ReconcileAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.ReconcileAll(ctx)
		}
	}
}

func (r *Reconciler) ReconcileTags(ctx context.Context) {
	rc := r.snapshot()
	if rc == nil {
		return
	}
	for _, rp := range rc.Projects {
		for _, re := range rp.Environments {
			for _, app := range re.Apps {
				if !app.Enabled {
					continue
				}
				if r.releaseHold(ctx, app) {
					continue
				}
				digest, err := r.engine.ResolveDigest(ctx, app)
				if err != nil {
					r.log.Warn("tag reconciliation resolve failed", "app", app.Name, "error", err)
					continue
				}
				st, err := r.cluster.WorkloadStatus(ctx, app.Namespace, app.Name)
				if err != nil {
					continue
				}
				desiredImage := deployment.ImageForDigest(app, digest)
				status := "ready"
				if st.ReadyReplicas < st.Replicas {
					status = "progressing"
				}
				_ = r.db.UpsertObserved(ctx, database.Observed{
					Project:        app.Project,
					Environment:    app.Environment,
					App:            app.Name,
					DesiredDigest:  digest,
					ObservedDigest: digestFromImage(st.Image),
					Image:          st.Image,
					Replicas:       st.Replicas,
					ReadyReplicas:  st.ReadyReplicas,
					Status:         status,
				})
				if st.Image == desiredImage {
					continue
				}
				r.log.Info("drift detected; enqueuing reconciliation deploy", "app", app.Name, "desired", desiredImage, "current", st.Image)
				_, err = r.db.IngestRegistryEvent(ctx, &database.Event{
					DedupKey:   fmt.Sprintf("reconcile|%s|%s|%s|%d", app.Project, app.Environment, app.Name, time.Now().UnixNano()),
					Registry:   r.cfg.Registry.Domain,
					Repository: app.Repository,
					Tag:        app.Tag,
					Digest:     digest,
				}, []database.Job{worker.PinJob(database.Job{
					DedupKey:    fmt.Sprintf("reconcile|%s|%s|%s|%d", app.Project, app.Environment, app.Name, time.Now().UnixNano()),
					Kind:        "reconcile",
					Project:     app.Project,
					Environment: app.Environment,
					App:         app.Name,
					Repository:  app.Repository,
					Tag:         app.Tag,
				}, app, rc.Version, "reconcile", "config-reconcile")})
				if err != nil {
					r.log.Warn("enqueue reconciliation job failed", "app", app.Name, "error", err)
				}
			}
		}
	}
}

func digestFromImage(image string) string {
	if i := indexByte(image, '@'); i >= 0 {
		return image[i+1:]
	}
	return ""
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// orderedApps returns app names with explicit dependencies first. The order is
// stable (alphabetical within independent groups) and does not imply a general
// orchestration DAG.
func orderedApps(re *config.ResolvedEnvironment) []string {
	names := make([]string, 0, len(re.Apps))
	for name := range re.Apps {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	var out []string
	var visit func(string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		if app := re.Apps[name]; app != nil {
			deps := append([]string(nil), app.DependsOn...)
			sort.Strings(deps)
			for _, dep := range deps {
				if _, ok := re.Apps[dep]; ok {
					visit(dep)
				}
			}
		}
		out = append(out, name)
	}
	for _, name := range names {
		visit(name)
	}
	return out
}

// releaseHold reports whether the newest release is rolled back (manual or
// policy-driven). Reconciliation must not immediately redeploy the failed
// target and undo the restore; a new push or explicit deploy creates a newer
// release and resumes normal reconciliation.
func (r *Reconciler) releaseHold(ctx context.Context, app *config.ResolvedApp) bool {
	rel, ok, err := r.db.LatestRelease(ctx, app.Project, app.Environment, app.Name)
	if err != nil || !ok {
		return false
	}
	switch release.State(rel.State) {
	case release.RolledBack, release.Recovering:
		return true
	}
	return false
}
