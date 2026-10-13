package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Options struct {
	TrustedProxies []string
	TLS            TLSOptions
	Log            *slog.Logger
	DialTimeout    time.Duration
	IdleTimeout    time.Duration
	HTTPListen     string
	HTTPSListen    string
	HTTPRedirect   bool
}

type Server struct {
	table        *RoutingTable
	tls          *TLSManager
	transports   *transportPair
	trusted      []*net.IPNet
	log          *slog.Logger
	httpSrv      *http.Server
	httpsSrv     *http.Server
	httpRedirect bool
	tlsMode      string
	warmCh       chan struct{}
	warmMu       sync.Mutex
	warmNext     map[string]time.Time
}

func NewServer(opts Options) (*Server, error) {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	tm, err := NewTLSManager(opts.TLS)
	if err != nil {
		return nil, err
	}
	s := &Server{
		table:        NewRoutingTable(),
		tls:          tm,
		transports:   newTransportPair(opts.DialTimeout, opts.IdleTimeout),
		log:          opts.Log,
		httpRedirect: opts.HTTPRedirect,
		tlsMode:      opts.TLS.Mode,
		warmCh:       make(chan struct{}, 1),
		warmNext:     map[string]time.Time{},
	}
	for _, cidr := range opts.TrustedProxies {
		netw, err := parseTrusted(cidr)
		if err != nil {
			return nil, err
		}
		s.trusted = append(s.trusted, netw)
	}
	if opts.TLS.Terminate && opts.TLS.Mode == "acme" {
		tm.SetAllowedHost(func(host string) bool { return s.table.HasHost(host) })
	}
	return s, nil
}

func parseTrusted(cidr string) (*net.IPNet, error) {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return nil, fmt.Errorf("empty trusted proxy entry")
	}
	if ip := net.ParseIP(cidr); ip != nil {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
	}
	_, netw, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid trusted proxy %q: %w", cidr, err)
	}
	return netw, nil
}

func (s *Server) Table() *RoutingTable { return s.table }
func (s *Server) UpdateSnapshot(sn *Snapshot) error {
	if err := s.table.Swap(sn); err != nil {
		return err
	}
	select {
	case s.warmCh <- struct{}{}:
	default:
	}
	return nil
}
func (s *Server) SnapshotVersion() int64 { return s.table.Version() }
func (s *Server) Terminating() bool      { return s.tls.Terminating() }

// TLSConfig exposes the server TLS configuration (with the ACME/manual
// certificate resolver) for callers that run their own listener.
func (s *Server) TLSConfig() *tls.Config { return s.tls.TLSServerConfig() }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/_menina/health", s.handleHealth)
	mux.HandleFunc("/_menina/ready", s.handleHealth)
	mux.HandleFunc("/", s.handle)
	return mux
}

func (s *Server) handlerWithACME() http.Handler {
	h := s.Handler()
	if s.tls.acme != nil {
		return s.tls.ACMEHTTPHandler(h)
	}
	return h
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","snapshotVersion":%d,"routes":%d}`, s.table.Version(), s.table.Count())
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	route, ok := s.table.Lookup(r.Host, r.URL.Path)
	if !ok {
		s.log.Info("no route for host", "host", r.Host, "path", r.URL.Path)
		http.Error(w, "no route configured for this host", http.StatusNotFound)
		return
	}
	if s.shouldRedirectHTTPS(r, route) {
		target := "https://" + r.Host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}
	trusted := s.isTrusted(r.RemoteAddr)
	for k, v := range route.Headers {
		r.Header.Set(k, v)
	}
	if route.MaxBodyBytes > 0 {
		if r.ContentLength > route.MaxBodyBytes {
			http.Error(w, "request entity too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, route.MaxBodyBytes)
	}
	proxy := buildProxy(route, s.transports, trusted, s.log)
	if route.Compression && !isWebSocket(r) && acceptsGzip(r) {
		gw := newGzipWriter(w)
		proxy.ServeHTTP(gw, r)
		_ = gw.gz.Close()
		return
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) shouldRedirectHTTPS(r *http.Request, route Route) bool {
	if !s.tls.Terminating() || r.TLS != nil {
		return false
	}
	if !route.RedirectHTTP && !s.httpRedirect {
		return false
	}
	proto := "http"
	if s.isTrusted(r.RemoteAddr) {
		if xfp := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); xfp != "" {
			proto = xfp
		}
	}
	return proto != "https"
}

func (s *Server) isTrusted(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range s.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) Serve(ctx context.Context, httpAddr, httpsAddr string) error {
	errCh := make(chan error, 2)
	if s.tls.acme != nil {
		go s.warmLoop(ctx)
	}
	if s.tls.Terminating() && s.tlsMode == "manual" {
		go s.reloadLoop(ctx)
	}
	if httpAddr != "" {
		s.httpSrv = &http.Server{
			Addr:              httpAddr,
			Handler:           s.handlerWithACME(),
			ReadHeaderTimeout: 15 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			s.log.Info("proxy listening (http)", "addr", httpAddr)
			if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}()
	}
	if httpsAddr != "" && s.tls.Terminating() {
		s.httpsSrv = &http.Server{
			Addr:              httpsAddr,
			Handler:           s.Handler(),
			TLSConfig:         s.tls.TLSServerConfig(),
			ReadHeaderTimeout: 15 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			s.log.Info("proxy listening (https)", "addr", httpsAddr)
			if err := s.httpsSrv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}()
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if s.httpSrv != nil {
			_ = s.httpSrv.Shutdown(shutdownCtx)
		}
		if s.httpsSrv != nil {
			_ = s.httpsSrv.Shutdown(shutdownCtx)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

func newGzipWriter(w http.ResponseWriter) *gzipResponseWriter {
	return &gzipResponseWriter{ResponseWriter: w, gz: gzipNewWriter(w)}
}

func (s *Server) warmLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	s.warmRoutes(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.warmCh:
			s.warmRoutes(ctx)
		case <-ticker.C:
			s.warmRoutes(ctx)
		}
	}
}

func (s *Server) warmRoutes(ctx context.Context) {
	now := time.Now()
	for _, host := range s.table.Hosts() {
		s.warmMu.Lock()
		next := s.warmNext[host]
		s.warmMu.Unlock()
		if now.Before(next) {
			continue
		}
		err := s.tls.Warmup(host)
		s.warmMu.Lock()
		if err != nil {
			s.warmNext[host] = now.Add(1 * time.Hour)
		} else {
			s.warmNext[host] = now.Add(30 * time.Minute)
		}
		s.warmMu.Unlock()
		if err != nil {
			s.log.Warn("certificate warmup failed", "host", host, "error", err)
		} else {
			s.log.Info("certificate ready", "host", host)
		}
	}
}

func (s *Server) reloadLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.tls.ReloadManual(); err != nil {
				s.log.Warn("manual certificate reload failed", "error", err)
			}
		}
	}
}

func TLSConfigInsecureForTest() *tls.Config { return &tls.Config{InsecureSkipVerify: true} }
