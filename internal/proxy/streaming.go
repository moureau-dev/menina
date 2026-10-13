package proxy

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

type transportPair struct {
	http1 *http.Transport
	h2c   *http2.Transport
}

func newTransportPair(dialTimeout, idleTimeout time.Duration) *transportPair {
	if dialTimeout <= 0 {
		dialTimeout = 15 * time.Second
	}
	if idleTimeout <= 0 {
		idleTimeout = 120 * time.Second
	}
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	http1 := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   dialTimeout,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: 0,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	h2c := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &transportPair{http1: http1, h2c: h2c}
}

func (t *transportPair) pick(r Route) http.RoundTripper {
	if r.H2C {
		return t.h2c
	}
	return t.http1
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	g.Header().Del("Content-Length")
	g.Header().Set("Content-Encoding", "gzip")
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	return g.gz.Write(b)
}

func (g *gzipResponseWriter) Flush() {
	_ = g.gz.Flush()
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
	flush := time.Duration(0)
	if route.Streaming {
		flush = -1
	}
	proxy.FlushInterval = flush
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
