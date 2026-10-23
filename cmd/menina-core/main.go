package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/moureau-dev/menina/internal/api"
	httpapi "github.com/moureau-dev/menina/internal/api/transport/http"
	"github.com/moureau-dev/menina/internal/core/config"
	"github.com/moureau-dev/menina/internal/core/controller"
	"github.com/moureau-dev/menina/internal/core/registry"
	"github.com/moureau-dev/menina/internal/core/secrets"
)

func main() {
	configDir := flag.String("config-dir", envOr("MENINA_CONFIG_DIR", "/etc/menina"), "configuration directory")
	verbose := flag.Bool("verbose", false, "verbose logging")
	apiListen := flag.String("api-listen", "", "optional host:port for the HTTP API; bind to loopback")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := config.LoadPlatform(*configDir)
	if err != nil {
		log.Error("load config", "error", err)
		os.Exit(1)
	}
	if err := config.ValidatePlatform(cfg); err != nil {
		log.Error("validate config", "error", err)
		os.Exit(1)
	}
	svc, err := controller.NewService(cfg, cfg.Host.ConfigDir, log)
	if err != nil {
		log.Error("start controller", "error", err)
		os.Exit(1)
	}
	if cfg.Registry.Enabled {
		rc, err := registryClient(cfg)
		if err != nil {
			log.Warn("registry digest resolver unavailable", "error", err)
		} else {
			svc.SetDigestResolver(registry.BundledResolver{Client: rc})
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *apiListen != "" {
		token, err := api.EnsureToken(cfg.Security.ControlTokenFile)
		if err != nil {
			log.Error("control token", "error", err)
			os.Exit(1)
		}
		go func() {
			if err := httpapi.Serve(ctx, *apiListen, token, svc.Handler()); err != nil {
				log.Error("api http listener stopped", "error", err)
			}
		}()
	}
	if err := svc.Run(ctx); err != nil {
		log.Error("controller stopped", "error", err)
		os.Exit(1)
	}
}

func registryClient(cfg *config.Config) (*registry.Client, error) {
	username, password := "", ""
	if cfg.Registry.Auth.Enabled {
		username = cfg.Registry.Auth.Username
		if username == "" {
			username = "menina"
		}
		var err error
		password, err = secrets.NewResolver().Resolve(cfg.Registry.Auth.PasswordRef)
		if err != nil {
			return nil, err
		}
	}
	scheme := "https"
	if !cfg.TLS.Terminate {
		scheme = "http"
	}
	return registry.NewClient(scheme+"://"+cfg.Registry.Domain, username, password, !cfg.TLS.Terminate), nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
