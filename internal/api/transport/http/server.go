// Package httpapi serves the API over TCP or TLS.
package httpapi

import (
	"context"
	"fmt"
	"net"
	stdhttp "net/http"
	"time"

	"github.com/moureau-dev/menina/internal/api/transport"
)

// Serve listens on addr and serves the API with bearer token authentication
// until the context is cancelled. Bind to loopback unless the listener is
// protected by another authenticated layer.
func Serve(ctx context.Context, addr, token string, handler stdhttp.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("api http listener: %w", err)
	}
	return ServeListener(ctx, ln, token, handler)
}

// ServeListener serves the API on an existing listener.
func ServeListener(ctx context.Context, ln net.Listener, token string, handler stdhttp.Handler) error {
	srv := &stdhttp.Server{
		Handler:           transport.Bearer(token, handler),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); err != nil && err != stdhttp.ErrServerClosed {
		return fmt.Errorf("api http listener: %w", err)
	}
	return nil
}
