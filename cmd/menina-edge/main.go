package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/moureau-dev/menina/internal/api"
	"github.com/moureau-dev/menina/internal/core/config"
	"github.com/moureau-dev/menina/internal/edge"
)

func main() {
	configDir := flag.String("config-dir", envOr("MENINA_CONFIG_DIR", "/etc/menina"), "configuration directory")
	verbose := flag.Bool("verbose", false, "verbose logging")
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
	opts := edge.Options{
		TrustedProxies: cfg.Security.TrustedProxies,
		TLS: edge.TLSOptions{
			Terminate: cfg.TLS.Terminate,
			Mode:      cfg.TLS.Mode,
			CertDir:   cfg.TLS.CertDir,
			ACME: edge.ACMEOptions{
				Email:        cfg.TLS.ACME.Email,
				DirectoryURL: cfg.TLS.ACME.DirectoryURL,
				StorageDir:   cfg.TLS.ACME.StorageDir,
				BaseDomains:  cfg.Networking.BaseDomains,
			},
		},
		Log:          log,
		HTTPListen:   cfg.Platform.HTTPListen,
		HTTPSListen:  cfg.Platform.HTTPSListen,
		HTTPRedirect: cfg.TLS.HTTPRedirect,
	}
	server, err := edge.NewServer(opts)
	if err != nil {
		log.Error("start edge", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go api.SubscribeRouting(ctx, cfg.Platform.ControlSocket, cfg.Security.ControlTokenFile, log, server)
	if err := server.Serve(ctx, cfg.Platform.HTTPListen, cfg.Platform.HTTPSListen); err != nil {
		log.Error("edge stopped", "error", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
