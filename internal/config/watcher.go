package config

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Snapshot struct {
	Config    *Config
	Resolved  *ResolvedConfig
	Projects  []*Project
	Version   int64
	LoadedAt  time.Time
	LastError string
}

type Watcher struct {
	configDir string
	cfg       *Config
	resolver  *Resolver
	debounce  time.Duration
	log       *slog.Logger

	mu       sync.RWMutex
	snapshot *Snapshot
	version  int64

	onChange func(*Snapshot)
}

func NewWatcher(configDir string, cfg *Config, resolver *Resolver, debounce time.Duration, log *slog.Logger) *Watcher {
	if debounce <= 0 {
		debounce = 750 * time.Millisecond
	}
	return &Watcher{
		configDir: configDir,
		cfg:       cfg,
		resolver:  resolver,
		debounce:  debounce,
		log:       log,
	}
}

func (w *Watcher) OnChange(fn func(*Snapshot)) { w.onChange = fn }

func (w *Watcher) Snapshot() *Snapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.snapshot
}

func (w *Watcher) Load() (*Snapshot, error) {
	projects, err := LoadAllProjects(ProjectsDir(w.configDir))
	if err != nil {
		return nil, err
	}
	rc, err := w.resolver.Resolve(projects, 0)
	if err != nil {
		return nil, err
	}
	if err := ValidateResolved(rc); err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.version++
	rc.Version = w.version
	w.snapshot = &Snapshot{
		Config:   w.cfg,
		Resolved: rc,
		Projects: projects,
		Version:  w.version,
		LoadedAt: time.Now(),
	}
	return w.snapshot, nil
}

func (w *Watcher) Run(ctx context.Context) error {
	if _, err := w.Load(); err != nil {
		w.recordError(err)
		w.log.Error("initial configuration load failed", "error", err)
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fw.Close()
	if err := w.addRecursive(fw, w.configDir); err != nil {
		return err
	}
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-fw.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
				if event.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						_ = w.addRecursive(fw, event.Name)
					}
				}
				if timer == nil {
					timer = time.NewTimer(w.debounce)
					timerC = timer.C
				} else {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(w.debounce)
				}
			}
		case err, ok := <-fw.Errors:
			if !ok {
				return nil
			}
			w.log.Warn("config watcher error", "error", err)
		case <-timerC:
			timer = nil
			timerC = nil
			w.reload()
		}
	}
}

func (w *Watcher) reload() {
	snap, err := w.Load()
	if err != nil {
		w.recordError(err)
		w.log.Error("configuration update rejected; retaining last valid snapshot", "error", err)
		return
	}
	w.log.Info("configuration snapshot promoted", "version", snap.Version, "projects", len(snap.Projects))
	if w.onChange != nil {
		w.onChange(snap)
	}
}

func (w *Watcher) recordError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.snapshot != nil {
		w.snapshot.LastError = err.Error()
		return
	}
	w.snapshot = &Snapshot{Config: w.cfg, LastError: err.Error(), LoadedAt: time.Now()}
}

func (w *Watcher) addRecursive(fw *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if err := fw.Add(path); err != nil {
				w.log.Warn("could not watch directory", "path", path, "error", err)
			}
		}
		return nil
	})
}
