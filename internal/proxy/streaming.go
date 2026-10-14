package proxy

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

type transportPair struct {
	dialTimeout time.Duration
	idleTimeout time.Duration
	mu          sync.Mutex
	cache       map[string]http.RoundTripper
}

func newTransportPair(dialTimeout, idleTimeout time.Duration) *transportPair {
	if dialTimeout <= 0 {
		dialTimeout = 15 * time.Second
	}
	if idleTimeout <= 0 {
		idleTimeout = 120 * time.Second
	}
	return &transportPair{
		dialTimeout: dialTimeout,
		idleTimeout: idleTimeout,
		cache:       map[string]http.RoundTripper{},
	}
}

// pick returns a transport for the route, honoring per-route read (response
// header) and idle timeouts. Transports are cached by their configuration so
// connections are reused.
func (t *transportPair) pick(r Route) http.RoundTripper {
	read, _ := parseDuration(r.Timeouts.Read)
	idle, _ := parseDuration(r.Timeouts.Idle)
	if idle <= 0 {
		idle = t.idleTimeout
	}
	key := fmt.Sprintf("h2c=%v;read=%s;idle=%s", r.H2C, read, idle)
	t.mu.Lock()
	defer t.mu.Unlock()
	if rt, ok := t.cache[key]; ok {
		return rt
	}
	dialer := &net.Dialer{Timeout: t.dialTimeout, KeepAlive: 30 * time.Second}
	var rt http.RoundTripper
	if r.H2C {
		rt = &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
			ReadIdleTimeout: idle,
		}
	} else {
		rt = &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          256,
			MaxIdleConnsPerHost:   64,
			IdleConnTimeout:       idle,
			TLSHandshakeTimeout:   t.dialTimeout,
			ExpectContinueTimeout: time.Second,
			ResponseHeaderTimeout: read,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		}
	}
	t.cache[key] = rt
	return rt
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	compress    bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	switch {
	case code == http.StatusNoContent, code == http.StatusNotModified, code < 200:
		g.compress = false
	default:
		g.compress = true
		g.Header().Del("Content-Length")
		g.Header().Set("Content-Encoding", "gzip")
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if !g.compress {
		return g.ResponseWriter.Write(b)
	}
	return g.gz.Write(b)
}

func (g *gzipResponseWriter) Flush() {
	if g.compress {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func gzipNewWriter(w io.Writer) *gzip.Writer {
	return gzip.NewWriter(w)
}

func buildProxy(route Route, transports *transportPair, trusted bool, log Logger) *httputil.ReverseProxy {
	target := &url.URL{Scheme: "http", Host: route.Backend}
	proxy := &httputil.ReverseProxy{}
	// Flush immediately so SSE, chunked and streaming responses are forwarded
	// as they are produced (the architecture promises no response buffering).
	proxy.FlushInterval = -1
	proxy.Transport = transports.pick(route)
	proxy.ErrorLog = nil
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.Host = pr.In.Host
		applyForwarded(pr, trusted)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if route.Compression && strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
			resp.Header.Del("Content-Encoding")
		}
		for k, v := range route.Headers {
			resp.Header.Set(k, v)
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if log != nil {
			log.Error("proxy upstream error", "host", route.Host, "backend", route.Backend, "error", err)
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "bad gateway\n")
	}
	return proxy
}

func applyForwarded(pr *httputil.ProxyRequest, trusted bool) {
	in := pr.In
	out := pr.Out
	clientIP, _, err := net.SplitHostPort(in.RemoteAddr)
	if err != nil {
		clientIP = in.RemoteAddr
	}
	if trusted {
		prior := in.Header.Get("X-Forwarded-For")
		if prior != "" {
			out.Header.Set("X-Forwarded-For", prior+", "+clientIP)
		} else {
			out.Header.Set("X-Forwarded-For", clientIP)
		}
		if proto := in.Header.Get("X-Forwarded-Proto"); proto != "" {
			out.Header.Set("X-Forwarded-Proto", proto)
		} else if in.TLS != nil {
			out.Header.Set("X-Forwarded-Proto", "https")
		} else {
			out.Header.Set("X-Forwarded-Proto", "http")
		}
		if host := in.Header.Get("X-Forwarded-Host"); host != "" {
			out.Header.Set("X-Forwarded-Host", host)
		}
	} else {
		out.Header.Set("X-Forwarded-For", clientIP)
		// Do not let an untrusted client inject forwarded metadata.
		out.Header.Del("X-Forwarded-Host")
		out.Header.Del("Forwarded")
		if in.TLS != nil {
			out.Header.Set("X-Forwarded-Proto", "https")
		} else {
			out.Header.Set("X-Forwarded-Proto", "http")
		}
	}
	out.Header.Set("X-Real-IP", clientIP)
}

type Logger interface {
	Error(msg string, args ...any)
}
