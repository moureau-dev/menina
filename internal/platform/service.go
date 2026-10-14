package platform

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/proxy"
	"github.com/moureau-dev/menina/internal/registry"
	"github.com/moureau-dev/menina/internal/secrets"
)

type broadcaster struct {
	mu     sync.Mutex
	subs   map[chan *proxy.Snapshot]struct{}
	latest *proxy.Snapshot
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subs: map[chan *proxy.Snapshot]struct{}{}}
}

func (b *broadcaster) publish(s *proxy.Snapshot) {
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

func (b *broadcaster) snapshot() *proxy.Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.latest
}

func (b *broadcaster) subscribe() chan *proxy.Snapshot {
	ch := make(chan *proxy.Snapshot, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	if b.latest != nil {
		ch <- b.latest
	}
	b.mu.Unlock()
	return ch
}

func (b *broadcaster) unsubscribe(ch chan *proxy.Snapshot) {
	b.mu.Lock()
	delete(b.subs, ch)
	close(ch)
	b.mu.Unlock()
}

type Service struct {
	cfg       *config.Config
	configDir string
	log       *slog.Logger

	db          *database.DB
	watcher     *config.Watcher
	cluster     deployment.Cluster
	engine      *deployment.Engine
	jobs        *Jobs
	reconciler  *Reconciler
	control     *ControlServer
	registry    *registry.Client
	eventsSrv   *http.Server
	bus         *broadcaster
	token       string
	notifyToken string
}

func (s *Service) DB() *database.DB { return s.db }

func NewService(cfg *config.Config, configDir string, log *slog.Logger) (*Service, error) {
	db, err := database.Open(cfg.Platform.DBPath)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	cluster := newDeferredCluster(cfg.Kubernetes.Kubeconfig, log)
	engine := deployment.NewEngine(cluster, db, nil, log)
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
	}
	snapFn := func() *config.ResolvedConfig {
		if s := watcher.Snapshot(); s != nil {
			return s.Resolved
		}
		return nil
	}
	svc.jobs = NewJobs(db, engine, cluster, snapFn, cfg.Registry.Domain, log)
	svc.reconciler = NewReconciler(db, cluster, engine, snapFn, cfg, log)
	svc.reconciler.OnPublish(svc.bus.publish)
	if cfg.Registry.Enabled {
		svc.registry = registry.NewClient(registryBaseURL(cfg), "", "", !cfg.TLS.Terminate)
	}
	if cfg.Registry.Notifications.TokenRef != "" {
		token, err := secrets.NewResolver().Resolve(cfg.Registry.Notifications.TokenRef)
		if err != nil {
			log.Warn("registry notification token could not be resolved; webhook authentication disabled", "error", err)
		} else {
			svc.notifyToken = token
		}
	}
	return svc, nil
}

func (s *Service) SetDigestResolver(r deployment.DigestResolver) {
	s.engine = deployment.NewEngine(s.cluster, s.db, r, s.log)
	s.jobs = NewJobs(s.db, s.engine, s.cluster, func() *config.ResolvedConfig {
		if snap := s.watcher.Snapshot(); snap != nil {
			return snap.Resolved
		}
		return nil
	}, s.cfg.Registry.Domain, s.log)
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

	token, err := EnsureToken(s.cfg.Security.ControlTokenFile)
	if err != nil {
		return fmt.Errorf("control token: %w", err)
	}
	s.token = token
	control, err := NewControlServer(s.cfg.Platform.ControlSocket, token, s.controlMux())
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
	}

	if err := s.startEventsServer(ctx); err != nil {
		return err
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

func (s *Service) handleRegistryEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.notifyToken != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.notifyToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
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
			snap = &proxy.Snapshot{}
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
	mux.HandleFunc("/deploy", s.handleDeploy)
	mux.HandleFunc("/rollback", s.handleRollback)
	return mux
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
	_, err = s.db.IngestRegistryEvent(r.Context(), &database.Event{
		DedupKey:   dedup,
		Registry:   s.cfg.Registry.Domain,
		Repository: app.Repository,
		Tag:        app.Tag,
	}, []database.Job{{
		DedupKey:    dedup,
		Kind:        "manual",
		Project:     req.Project,
		Environment: req.Environment,
		App:         req.App,
		Repository:  app.Repository,
		Tag:         app.Tag,
	}})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

type rollbackRequest struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	App         string `json:"app"`
	Digest      string `json:"digest"`
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
	_, err = s.db.IngestRegistryEvent(r.Context(), &database.Event{
		DedupKey:   dedup,
		Registry:   s.cfg.Registry.Domain,
		Repository: app.Repository,
		Digest:     req.Digest,
	}, []database.Job{{
		DedupKey:      dedup,
		Kind:          "rollback",
		Project:       req.Project,
		Environment:   req.Environment,
		App:           req.App,
		Repository:    app.Repository,
		Tag:           app.Tag,
		DesiredDigest: req.Digest,
	}})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "digest": req.Digest})
}

func (s *Service) watcherLastError() string {
	if snap := s.watcher.Snapshot(); snap != nil {
		return snap.LastError
	}
	return ""
}

func (s *Service) Token() string { return s.token }

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
