package controller

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/moureau-dev/menina/internal/api"
	"github.com/moureau-dev/menina/internal/api/transport/unix"
	"github.com/moureau-dev/menina/internal/core/cluster"
	"github.com/moureau-dev/menina/internal/core/config"
	"github.com/moureau-dev/menina/internal/core/notify"
	"github.com/moureau-dev/menina/internal/core/registry"
	"github.com/moureau-dev/menina/internal/core/secrets"
	"github.com/moureau-dev/menina/internal/core/state/contract"
	"github.com/moureau-dev/menina/internal/core/state/sqlite"
	"github.com/moureau-dev/menina/internal/core/worker"
	"github.com/moureau-dev/menina/internal/edge"
	"github.com/moureau-dev/menina/internal/telemetry"
)

type broadcaster struct {
	mu     sync.Mutex
	subs   map[chan *edge.Snapshot]struct{}
	latest *edge.Snapshot
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subs: map[chan *edge.Snapshot]struct{}{}}
}

func (b *broadcaster) publish(s *edge.Snapshot) {
	b.mu.Lock()
	b.latest = s
	for ch := range b.subs {
		select {
		case ch <- s:
		default:
		}
	}
	b.mu.Unlock()
}

func (b *broadcaster) snapshot() *edge.Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.latest
}

func (b *broadcaster) subscribe() chan *edge.Snapshot {
	ch := make(chan *edge.Snapshot, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	if b.latest != nil {
		ch <- b.latest
	}
	b.mu.Unlock()
	return ch
}

func (b *broadcaster) unsubscribe(ch chan *edge.Snapshot) {
	b.mu.Lock()
	delete(b.subs, ch)
	close(ch)
	b.mu.Unlock()
}

type Service struct {
	cfg       *config.Config
	configDir string
	log       *slog.Logger

	db          contract.Store
	watcher     *config.Watcher
	cluster     k8s.Cluster
	engine      *k8s.Engine
	jobs        *worker.Jobs
	reconciler  *Reconciler
	control     *unix.Server
	dispatcher  *api.Dispatcher
	registry    *registry.Client
	eventsSrv   *http.Server
	bus         *broadcaster
	token       string
	notifyToken string
	sender      *notify.Sender
}

func (s *Service) DB() contract.Store { return s.db }

func NewService(cfg *config.Config, configDir string, log *slog.Logger) (*Service, error) {
	db, err := sqlite.Open(cfg.Platform.DBPath)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	cluster := newDeferredCluster(cfg.Kubernetes.Kubeconfig, log)
	engine := k8s.NewEngine(cluster, db, nil, log)
	watcher := config.NewWatcher(configDir, cfg, config.NewResolver(cfg), 750*time.Millisecond, log)

	svc := &Service{
		cfg:       cfg,
		configDir: configDir,
		log:       log,
		db:        db,
		cluster:   cluster,
		engine:    engine,
		watcher:   watcher,
		bus:       newBroadcaster(),
		sender:    notify.NewSender(log),
	}
	snapFn := func() *config.ResolvedConfig {
		if s := watcher.Snapshot(); s != nil {
			return s.Resolved
		}
		return nil
	}
	svc.jobs = worker.NewJobs(db, engine, cluster, snapFn, cfg.Registry.Domain, log)
	svc.jobs.SetSender(svc.sender)
	svc.jobs.SetTLS(cfg.TLS.Terminate)
	svc.jobs.SetAllowHooks(cfg.Security.AllowAppHooks)
	svc.reconciler = NewReconciler(db, cluster, engine, snapFn, cfg, log)
	svc.reconciler.OnPublish(svc.bus.publish)
	svc.dispatcher = api.NewDispatcher(svc.controlMux())
	svc.dispatcher.SetAuthorizer(api.NewStoreAuthorizer(db))
	svc.dispatcher.SetAuditor(db)
	if cfg.Registry.Enabled {
		svc.registry = registry.NewClient(registryBaseURL(cfg), "", "", !cfg.TLS.Terminate)
	}
	if cfg.Registry.Notifications.TokenRef != "" {
		token, err := secrets.NewResolver().Resolve(cfg.Registry.Notifications.TokenRef)
		if err != nil {
			return nil, fmt.Errorf("resolve registry notification token: %w", err)
		}
		svc.notifyToken = token
	}
	if cfg.Registry.Notifications.Enabled && svc.notifyToken == "" {
		return nil, fmt.Errorf("registry notifications are enabled but no valid token is configured (registry.notifications.tokenRef); refusing to start an unauthenticated webhook")
	}
	return svc, nil
}

func (s *Service) SetDigestResolver(r k8s.DigestResolver) {
	s.engine = k8s.NewEngine(s.cluster, s.db, r, s.log)
	s.jobs = worker.NewJobs(s.db, s.engine, s.cluster, func() *config.ResolvedConfig {
		if snap := s.watcher.Snapshot(); snap != nil {
			return snap.Resolved
		}
		return nil
	}, s.cfg.Registry.Domain, s.log)
	s.jobs.SetSender(s.sender)
	s.jobs.SetTLS(s.cfg.TLS.Terminate)
	s.jobs.SetAllowHooks(s.cfg.Security.AllowAppHooks)
	s.reconciler = NewReconciler(s.db, s.cluster, s.engine, func() *config.ResolvedConfig {
		if snap := s.watcher.Snapshot(); snap != nil {
			return snap.Resolved
		}
		return nil
	}, s.cfg, s.log)
	s.reconciler.OnPublish(s.bus.publish)
}

func (s *Service) Run(ctx context.Context) error {
	if requeued, err := s.db.RequeueProcessingJobs(ctx); err != nil {
		s.log.Warn("could not requeue in-progress jobs", "error", err)
	} else if requeued > 0 {
		s.log.Info("requeued in-progress jobs after restart", "count", requeued)
	}

	token, err := api.EnsureToken(s.cfg.Security.ControlTokenFile)
	if err != nil {
		return fmt.Errorf("control token: %w", err)
	}
	s.token = token
	control, err := unix.NewServer(s.cfg.Platform.ControlSocket, token, s.Handler())
	if err != nil {
		return err
	}
	if err := control.Start(ctx); err != nil {
		return err
	}
	s.control = control
	s.log.Info("control plane listening", "socket", s.cfg.Platform.ControlSocket)

	s.watcher.OnChange(func(snap *config.Snapshot) {
		if snap.Resolved == nil {
			return
		}
		if err := s.reconciler.Publish(ctx, snap.Resolved); err != nil {
			s.log.Warn("publish routing snapshot", "error", err)
		}
		s.reconciler.ReconcileWorkloads(ctx, snap.Resolved)
	})
	go func() {
		if err := s.watcher.Run(ctx); err != nil {
			s.log.Error("config watcher stopped", "error", err)
		}
	}()
	time.Sleep(300 * time.Millisecond)
	if snap := s.watcher.Snapshot(); snap != nil && snap.Resolved != nil {
		if err := s.reconciler.Publish(ctx, snap.Resolved); err != nil {
			s.log.Warn("initial routing snapshot publish", "error", err)
		}
		s.reconciler.ReconcileWorkloads(ctx, snap.Resolved)
	}
	s.sweepInterrupted(ctx)
	go s.runInterruptedSweep(ctx)

	if err := s.startEventsServer(ctx); err != nil {
		return err
	}
	if !s.cfg.Registry.Notifications.Enabled {
		s.log.Info("registry notifications are disabled; /v2/events will return 503")
	}
	s.jobs.RunWorkers(ctx, s.cfg.Platform.Workers)
	go s.reconciler.Run(ctx, s.cfg.Platform.ReconcileInterval)

	<-ctx.Done()
	s.log.Info("control plane shutting down")
	return nil
}

func (s *Service) startEventsServer(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/events", s.handleRegistryEvent)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/metrics", s.handleMetrics)
	s.eventsSrv = &http.Server{Addr: s.cfg.Platform.EventsListen, Handler: mux, ReadHeaderTimeout: 15 * time.Second}
	go func() {
		s.log.Info("registry event listener started", "addr", s.cfg.Platform.EventsListen)
		if err := s.eventsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("registry event listener stopped", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.eventsSrv.Shutdown(shutdownCtx)
	}()
	return nil
}

func (s *Service) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", telemetry.ContentType)
	fmt.Fprintf(w, "# HELP menina_up Control plane liveness\n# TYPE menina_up gauge\nmenina_up 1\n")
	fmt.Fprintf(w, "# HELP menina_snapshot_version Current routing snapshot version\n# TYPE menina_snapshot_version gauge\nmenina_snapshot_version %d\n", s.bus.snapshotVersion())
	fmt.Fprintf(w, "# HELP menina_routes Number of configured proxy routes\n# TYPE menina_routes gauge\nmenina_routes %d\n", s.bus.snapshotRoutes())
	snapshotOK := 0
	if s.bus.snapshot() != nil {
		snapshotOK = 1
	}
	fmt.Fprintf(w, "# HELP menina_proxy_snapshot_ready Whether a routing snapshot has been published\n# TYPE menina_proxy_snapshot_ready gauge\nmenina_proxy_snapshot_ready %d\n", snapshotOK)

	if counts, err := s.db.CountJobsByStatus(r.Context()); err == nil {
		fmt.Fprintf(w, "# HELP menina_jobs Deployment jobs by status\n# TYPE menina_jobs gauge\n")
		statuses := make([]string, 0, len(counts))
		queue := 0
		for status := range counts {
			statuses = append(statuses, status)
			if status == sqlite.JobStatusPending || status == sqlite.JobStatusProcessing {
				queue += counts[status]
			}
		}
		sort.Strings(statuses)
		for _, status := range statuses {
			fmt.Fprintf(w, "menina_jobs{status=%q} %d\n", status, counts[status])
		}
		fmt.Fprintf(w, "# HELP menina_queue_depth Pending plus processing deployment jobs\n# TYPE menina_queue_depth gauge\nmenina_queue_depth %d\n", queue)
	}

	if stats, err := s.db.ReleaseStats(r.Context()); err == nil {
		fmt.Fprintf(w, "# HELP menina_releases Releases by state\n# TYPE menina_releases gauge\n")
		success, failure := 0, 0
		sum, count := 0.0, 0
		for _, stat := range stats {
			fmt.Fprintf(w, "menina_releases{state=%q} %d\n", stat.State, stat.Count)
			switch stat.State {
			case "active", "rolled_back":
				success += stat.Count
			case "failed":
				failure += stat.Count
			}
			sum += stat.SumSeconds
			count += stat.FinishedCount
		}
		fmt.Fprintf(w, "# HELP menina_deploy_success_total Releases that reached active or rolled_back\n# TYPE menina_deploy_success_total counter\nmenina_deploy_success_total %d\n", success)
		fmt.Fprintf(w, "# HELP menina_deploy_failure_total Releases that failed\n# TYPE menina_deploy_failure_total counter\nmenina_deploy_failure_total %d\n", failure)
		fmt.Fprintf(w, "# HELP menina_deploy_duration_seconds_sum Sum of release durations\n# TYPE menina_deploy_duration_seconds_sum counter\nmenina_deploy_duration_seconds_sum %f\n", sum)
		fmt.Fprintf(w, "# HELP menina_deploy_duration_seconds_count Number of releases with a duration\n# TYPE menina_deploy_duration_seconds_count counter\nmenina_deploy_duration_seconds_count %d\n", count)
	}

	if observed, err := s.db.Observed(r.Context()); err == nil {
		fmt.Fprintf(w, "# HELP menina_app_ready_replicas Ready replicas per app\n# TYPE menina_app_ready_replicas gauge\n")
		fmt.Fprintf(w, "# HELP menina_app_desired_replicas Desired replicas per app\n# TYPE menina_app_desired_replicas gauge\n")
		for _, o := range observed {
			labels := fmt.Sprintf("project=%q,environment=%q,app=%q", o.Project, o.Environment, o.App)
			fmt.Fprintf(w, "menina_app_ready_replicas{%s} %d\n", labels, o.ReadyReplicas)
			fmt.Fprintf(w, "menina_app_desired_replicas{%s} %d\n", labels, o.Replicas)
		}
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.cfg.Host.DataDir, &stat); err == nil {
		total := stat.Blocks * uint64(stat.Bsize)
		avail := stat.Bavail * uint64(stat.Bsize)
		fmt.Fprintf(w, "# HELP menina_node_disk_total_bytes Node data-dir filesystem size (also hosts the bundled registry)\n# TYPE menina_node_disk_total_bytes gauge\nmenina_node_disk_total_bytes %d\n", total)
		fmt.Fprintf(w, "# HELP menina_node_disk_available_bytes Node data-dir filesystem free space\n# TYPE menina_node_disk_available_bytes gauge\nmenina_node_disk_available_bytes %d\n", avail)
	}
}

func (s *Service) handleRegistryEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.notifyToken == "" {
		http.Error(w, "registry notifications are not configured", http.StatusServiceUnavailable)
		return
	}
	if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.notifyToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	events, err := registry.ParseNotification(r.Header, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	total := 0
	for _, e := range events {
		if !e.IsPush() {
			continue
		}
		created, n, err := s.jobs.Ingest(r.Context(), e)
		if err != nil {
			s.log.Error("persist registry event", "error", err)
			http.Error(w, "could not persist event", http.StatusInternalServerError)
			return
		}
		if created {
			total += n
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"accepted": true, "jobs": total})
}

func (s *Service) controlMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/snapshot", func(w http.ResponseWriter, r *http.Request) {
		snap := s.bus.snapshot()
		if snap == nil {
			snap = &edge.Snapshot{}
		}
		writeJSON(w, http.StatusOK, snap)
	})
	mux.HandleFunc("/stream", s.handleStream)
	mux.HandleFunc("/routes", func(w http.ResponseWriter, r *http.Request) {
		routes, err := s.db.Routes(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, routes)
	})
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		releases, err := s.db.Releases(r.Context(), q.Get("project"), q.Get("environment"), q.Get("app"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, releases)
	})
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/job-status", s.handleJobStatus)
	mux.HandleFunc("/jobs", s.handleJobs)
	mux.HandleFunc("/jobs/cancel", s.handleJobCancel)
	mux.HandleFunc("/jobs/watch", s.handleJobWatch)
	mux.HandleFunc("/deploy", s.handleDeploy)
	mux.HandleFunc("/rollback", s.handleRollback)
	mux.HandleFunc("/release", s.handleRelease)
	mux.HandleFunc("/promote", s.handlePromote)
	mux.HandleFunc("/webhook-deliveries", s.handleWebhookDeliveries)
	mux.HandleFunc("/webhook-replay", s.handleWebhookReplay)
	mux.HandleFunc("/config/apply", s.handleConfigApply)
	mux.HandleFunc("/config/revisions", s.handleConfigRevisions)
	mux.HandleFunc("/config/rollback", s.handleConfigRollback)
	mux.HandleFunc("/audit", s.handleAudit)
	return mux
}

func (s *Service) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	jobs, err := s.db.ListJobs(r.Context(), q.Get("status"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Service) handleJobWatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if _, ok, err := s.db.JobByID(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")

	writeEvent := func(eventID int64, terminal bool) bool {
		job, ok, err := s.db.JobByID(r.Context(), id)
		if err != nil || !ok {
			return false
		}
		data, err := json.Marshal(map[string]any{
			"eventId":  eventID,
			"terminal": terminal,
			"job":      job,
		})
		if err != nil {
			return false
		}
		if _, err := w.Write(append(data, '\n')); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	sentLegacy := false
	for {
		events, err := s.db.JobEventsAfter(r.Context(), id, cursor)
		if err != nil {
			return
		}
		if len(events) == 0 {
			job, ok, err := s.db.JobByID(r.Context(), id)
			if err != nil || !ok {
				return
			}
			terminal := isTerminalJobStatus(job.Status)
			if cursor == 0 && !sentLegacy {
				// Jobs created before job events existed still stream their
				// current state once, so a first watch is never silently empty.
				sentLegacy = true
				if !writeEvent(0, terminal) {
					return
				}
			} else if terminal {
				// Resuming at or past the last event of a finished job must not
				// wait forever for another event.
				if !writeEvent(cursor, true) {
					return
				}
			}
			if terminal && len(events) == 0 && (cursor > 0 || sentLegacy) {
				return
			}
		}
		for _, event := range events {
			cursor = event.ID
			terminal := isTerminalJobStatus(event.Status)
			if !writeEvent(event.ID, terminal) {
				return
			}
			if terminal {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func isTerminalJobStatus(status string) bool {
	switch status {
	case "done", "failed", "cancelled", "superseded":
		return true
	}
	return false
}

func (s *Service) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	cancelled, err := s.db.CancelJob(r.Context(), req.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": req.ID, "cancelled": cancelled})
}

func (s *Service) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	ch := s.bus.subscribe()
	defer s.bus.unsubscribe(ch)
	for {
		select {
		case <-r.Context().Done():
			return
		case snap, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(snap)
			if err != nil {
				continue
			}
			_, _ = w.Write(append(data, '\n'))
			flusher.Flush()
		}
	}
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"pid":             os.Getpid(),
		"snapshotVersion": s.bus.snapshotVersion(),
		"routes":          s.bus.snapshotRoutes(),
		"kubernetes":      "unknown",
		"registry":        "unknown",
		"configLastError": s.watcherLastError(),
	}
	if err := s.cluster.Ping(r.Context()); err != nil {
		status["kubernetes"] = "unavailable: " + err.Error()
		status["kubernetesOK"] = false
	} else {
		status["kubernetes"] = "ok"
		status["kubernetesOK"] = true
	}
	if s.registry != nil {
		if err := s.registry.Ping(r.Context()); err != nil {
			status["registry"] = "unavailable: " + err.Error()
		} else {
			status["registry"] = "ok"
		}
	}
	if counts, err := s.db.CountJobsByStatus(r.Context()); err == nil {
		status["jobs"] = counts
	}
	writeJSON(w, http.StatusOK, status)
}

type deployRequest struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	App         string `json:"app"`
	Digest      string `json:"digest"`
	Confirm     bool   `json:"confirm"`
}

func (s *Service) requireProductionConfirmation(environment string, confirmed bool) error {
	if !s.cfg.Security.EnforceProdPolicy {
		return nil
	}
	if strings.EqualFold(environment, "production") && !confirmed {
		return fmt.Errorf("deploying to the %q environment requires explicit confirmation (security.enforceProdPolicy)", environment)
	}
	return nil
}

func (s *Service) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req deployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.requireProductionConfirmation(req.Environment, req.Confirm); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	snap := s.watcher.Snapshot()
	if snap == nil || snap.Resolved == nil {
		http.Error(w, "no resolved configuration", http.StatusServiceUnavailable)
		return
	}
	app, err := snap.Resolved.FindApp(req.Project, req.Environment, req.App)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	dedup := fmt.Sprintf("manual|%s|%s|%s|%d", req.Project, req.Environment, req.App, time.Now().UnixNano())
	_, err = s.db.IngestRegistryEvent(r.Context(), &sqlite.Event{
		DedupKey:   dedup,
		Registry:   s.cfg.Registry.Domain,
		Repository: app.Repository,
		Tag:        app.Tag,
	}, []sqlite.Job{worker.PinJob(sqlite.Job{
		DedupKey:    dedup,
		Kind:        "manual",
		Project:     req.Project,
		Environment: req.Environment,
		App:         req.App,
		Repository:  app.Repository,
		Tag:         app.Tag,
	}, app, snap.Version, "manual", "cli")})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, _, _ := s.db.JobIDByDedupKey(r.Context(), dedup)
	s.jobs.EmitDeploy(app, notify.Event{Event: "deploy.queued", Action: "deploy", Status: "queued", Actor: "manual", Message: fmt.Sprintf("%s/%s/%s deploy queued", req.Project, req.Environment, req.App)})
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "jobId": id, "snapshotVersion": snap.Version})
}

type rollbackRequest struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	App         string `json:"app"`
	Digest      string `json:"digest"`
	Confirm     bool   `json:"confirm"`
}

func (s *Service) handleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.requireProductionConfirmation(req.Environment, req.Confirm); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if req.Digest == "" || !strings.Contains(req.Digest, ":") {
		http.Error(w, "rollback requires an immutable image digest", http.StatusBadRequest)
		return
	}
	snap := s.watcher.Snapshot()
	if snap == nil || snap.Resolved == nil {
		http.Error(w, "no resolved configuration", http.StatusServiceUnavailable)
		return
	}
	app, err := snap.Resolved.FindApp(req.Project, req.Environment, req.App)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	dedup := fmt.Sprintf("rollback|%s|%s|%s|%d", req.Project, req.Environment, req.App, time.Now().UnixNano())
	_, err = s.db.IngestRegistryEvent(r.Context(), &sqlite.Event{
		DedupKey:   dedup,
		Registry:   s.cfg.Registry.Domain,
		Repository: app.Repository,
		Digest:     req.Digest,
	}, []sqlite.Job{worker.PinJob(sqlite.Job{
		DedupKey:      dedup,
		Kind:          "rollback",
		Project:       req.Project,
		Environment:   req.Environment,
		App:           req.App,
		Repository:    app.Repository,
		Tag:           app.Tag,
		DesiredDigest: req.Digest,
	}, app, snap.Version, "rollback", "cli")})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, _, _ := s.db.JobIDByDedupKey(r.Context(), dedup)
	s.jobs.EmitDeploy(app, notify.Event{Event: "deploy.queued", Action: "rollback", Status: "queued", Actor: "rollback", Digest: req.Digest, Message: fmt.Sprintf("%s/%s/%s rollback to %s queued", req.Project, req.Environment, req.App, req.Digest)})
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "digest": req.Digest, "jobId": id})
}

func (s *Service) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resp := map[string]any{"found": false}
	if id := q.Get("id"); id != "" {
		if job, ok, err := s.db.JobByID(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if ok {
			resp["found"] = true
			resp["job"] = job
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	project, environment, app := q.Get("project"), q.Get("environment"), q.Get("app")
	if job, ok, err := s.db.LatestJobByTarget(r.Context(), project, environment, app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if ok {
		resp["found"] = true
		resp["job"] = job
	}
	if releases, err := s.db.Releases(r.Context(), project, environment, app); err == nil && len(releases) > 0 {
		resp["latestRelease"] = releases[0]
		resp["release"] = releases[0]
		if snap := s.watcher.Snapshot(); snap != nil && snap.Resolved != nil {
			if ra, err := snap.Resolved.FindApp(project, environment, app); err == nil {
				if stale, hint := s.secretStale(r, ra); stale {
					resp["secretStale"] = true
					resp["secretHint"] = hint
				}
			}
		}
	}
	if observed, err := s.db.Observed(r.Context()); err == nil {
		for _, o := range observed {
			if o.Project == project && o.Environment == environment && o.App == app {
				resp["observed"] = o
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) watcherLastError() string {
	if snap := s.watcher.Snapshot(); snap != nil {
		return snap.LastError
	}
	return ""
}

func (s *Service) Token() string { return s.token }

// Handler returns the versioned API dispatcher used by every transport.
func (s *Service) Handler() http.Handler {
	if s.dispatcher == nil {
		s.dispatcher = api.NewDispatcher(s.controlMux())
	}
	return s.dispatcher
}

func (b *broadcaster) snapshotVersion() int64 {
	if s := b.snapshot(); s != nil {
		return s.Version
	}
	return 0
}

func (b *broadcaster) snapshotRoutes() int {
	if s := b.snapshot(); s != nil {
		return len(s.Routes)
	}
	return 0
}

func registryBaseURL(cfg *config.Config) string {
	scheme := "http"
	if cfg.TLS.Terminate {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, cfg.Registry.Domain)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Service) handleWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	deliveries, err := s.db.WebhookDeliveries(r.Context(), q.Get("status"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
}

func (s *Service) handleWebhookReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	id, err := s.jobs.ReplayWebhook(r.Context(), req.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"replayed": true, "id": id})
}

// sweepInterrupted marks releases left by a crashed control plane as failed and
// applies the interrupted recovery trigger.
func (s *Service) sweepInterrupted(ctx context.Context) {
	interrupted, err := s.db.InterruptedReleases(ctx, 30*time.Minute)
	if err != nil {
		s.log.Warn("could not read interrupted releases", "error", err)
		return
	}
	for _, rel := range interrupted {
		rel := rel
		go s.jobs.HandleInterruptedRelease(ctx, rel)
	}
}

func (s *Service) runInterruptedSweep(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepInterrupted(ctx)
		}
	}
}
