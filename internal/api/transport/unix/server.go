// Package unix serves the API over a Unix domain socket.
package unix

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/moureau-dev/menina/internal/api/transport"
)

// Server serves an API handler over a Unix socket.
type Server struct {
	socketPath string
	token      string
	handler    http.Handler
	srv        *http.Server
}

// NewServer builds a Unix socket API server.
func NewServer(socketPath, token string, handler http.Handler) (*Server, error) {
	return &Server{socketPath: socketPath, token: token, handler: handler}, nil
}

// Start listens on the socket and serves until the context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(s.socketPath)
	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on control socket: %w", err)
	}
	if err := os.Chmod(s.socketPath, 0o660); err != nil {
		return err
	}
	s.srv = &http.Server{Handler: transport.Bearer(s.token, s.handler)}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "control server: %v\n", err)
		}
	}()
	return nil
}
