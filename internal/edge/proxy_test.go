package edge

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func backendHost(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	return strings.TrimPrefix(ts.URL, "http://")
}

func startProxy(t *testing.T, opts Options, routes ...Route) (*Server, *httptest.Server) {
	t.Helper()
	if opts.Log == nil {
		opts.Log = testLogger()
	}
	s, err := NewServer(opts)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if err := s.UpdateSnapshot(&Snapshot{Version: 1, Routes: routes}); err != nil {
		t.Fatalf("swap snapshot: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func TestProxyForwardsRequestAndHeaders(t *testing.T) {
	var seen struct {
		host, xff, proto, realIP string
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.host = r.Host
		seen.xff = r.Header.Get("X-Forwarded-For")
		seen.proto = r.Header.Get("X-Forwarded-Proto")
		seen.realIP = r.Header.Get("X-Real-IP")
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend)})
	req, _ := http.NewRequest(http.MethodGet, proxySrv.URL+"/hello", nil)
	req.Host = "example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if seen.host != "example.com" {
		t.Errorf("host = %q, want example.com", seen.host)
	}
	if !strings.Contains(seen.xff, "127.0.0.1") {
		t.Errorf("xff = %q", seen.xff)
	}
	if seen.proto != "http" {
		t.Errorf("proto = %q", seen.proto)
	}
	if seen.realIP == "" {
		t.Errorf("real ip empty")
	}
}

func TestProxyTrustedProxyPreservesForwarded(t *testing.T) {
	var xff string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xff = r.Header.Get("X-Forwarded-For")
	}))
	defer backend.Close()

	_, proxySrv := startProxy(t, Options{TrustedProxies: []string{"127.0.0.1"}}, Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend)})
	req, _ := http.NewRequest(http.MethodGet, proxySrv.URL+"/", nil)
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if xff != "203.0.113.9, 127.0.0.1" {
		t.Fatalf("xff = %q, want trusted chain", xff)
	}
}

func TestProxyStreamingSSEFlushesIncrementally(t *testing.T) {
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("backend response writer is not a flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: one\n\n")
		fl.Flush()
		<-release
		fmt.Fprint(w, "data: two\n\n")
		fl.Flush()
	}))
	defer backend.Close()
	defer close(release)

	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend), Streaming: true})
	req, _ := http.NewRequest(http.MethodGet, proxySrv.URL+"/", nil)
	req.Host = "example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read first event: %v", err)
	}
	if !strings.Contains(line, "one") {
		t.Fatalf("first event = %q", line)
	}
	release <- struct{}{}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read second event: %v", err)
	}
}

func TestProxyWebSocketUpgradeFullDuplex(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected websocket", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		fmt.Fprint(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_, _ = io.Copy(conn, conn)
	}))
	defer backend.Close()

	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend)})
	conn, err := net.Dial("tcp", proxySrv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: example.com\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("status line = %q", status)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(reader, echo); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(echo) != "ping" {
		t.Fatalf("echo = %q", echo)
	}
}

func TestProxyH2CWithTrailers(t *testing.T) {
	backendHandler := h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "X-Trail")
		_, _ = io.WriteString(w, "grpc-body")
		w.Header().Set("X-Trail", "done")
	}), &http2.Server{})
	backend := httptest.NewServer(backendHandler)
	defer backend.Close()

	s, err := NewServer(Options{Log: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSnapshot(&Snapshot{Version: 1, Routes: []Route{{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend), H2C: true}}}); err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(h2c.NewHandler(s.Handler(), &http2.Server{}))
	defer proxySrv.Close()

	client := &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			return net.Dial(network, proxySrv.Listener.Addr().String())
		},
	}}
	req, _ := http.NewRequest(http.MethodPost, "http://example.com/grpc", strings.NewReader(""))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("h2c request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "grpc-body" {
		t.Fatalf("body = %q", body)
	}
	if resp.Trailer.Get("X-Trail") != "done" {
		t.Fatalf("trailer = %q", resp.Trailer.Get("X-Trail"))
	}
}

func TestProxyLargeTransferStreams(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusRequestEntityTooLarge)
			return
		}
		fmt.Fprintf(w, "%d", n)
	}))
	defer backend.Close()

	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend)})
	payload := bytes.Repeat([]byte("x"), 8*1024*1024)
	req, _ := http.NewRequest(http.MethodPost, proxySrv.URL+"/upload", bytes.NewReader(payload))
	req.Host = "example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != fmt.Sprintf("%d", len(payload)) {
		t.Fatalf("backend received %s bytes, want %d", body, len(payload))
	}
}

func TestProxyMaxBodyBytesEnforced(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
	}))
	defer backend.Close()

	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend), MaxBodyBytes: 1024})
	req, _ := http.NewRequest(http.MethodPost, proxySrv.URL+"/upload", bytes.NewReader(bytes.Repeat([]byte("x"), 64*1024)))
	req.Host = "example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestProxyCompression(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "please compress me please compress me")
	}))
	defer backend.Close()

	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend), Compression: true})
	req, _ := http.NewRequest(http.MethodGet, proxySrv.URL+"/", nil)
	req.Host = "example.com"
	req.Header.Set("Accept-Encoding", "gzip")
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("content-encoding = %q", resp.Header.Get("Content-Encoding"))
	}
	gr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(gr)
	if !strings.Contains(string(body), "compress me") {
		t.Fatalf("body = %q", body)
	}
}

func TestProxyRedirectsHTTPToHTTPS(t *testing.T) {
	s, err := NewServer(Options{Log: testLogger(), TLS: TLSOptions{Terminate: true, Mode: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSnapshot(&Snapshot{Version: 1, Routes: []Route{{Host: "example.com", PathPrefix: "/", Backend: "1.1.1.1:80", RedirectHTTP: true}}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != "https://example.com/path" {
		t.Fatalf("location = %q", got)
	}
}

func TestRedirectHonorsRouteAndGlobalPolicy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	_, noRedirect := startProxy(t,
		Options{TLS: TLSOptions{Terminate: true, Mode: "none"}, HTTPRedirect: false},
		Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend), RedirectHTTP: false},
	)
	req, _ := http.NewRequest(http.MethodGet, noRedirect.URL+"/", nil)
	req.Host = "example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("redirect disabled: status = %d, want 200", resp.StatusCode)
	}

	_, redirect := startProxy(t,
		Options{TLS: TLSOptions{Terminate: true, Mode: "none"}},
		Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend), RedirectHTTP: true},
	)
	req2, _ := http.NewRequest(http.MethodGet, redirect.URL+"/", nil)
	req2.Host = "example.com"
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("redirect enabled: status = %d, want 301", resp2.StatusCode)
	}
}

func TestRedirectIgnoresUntrustedForwardedProto(t *testing.T) {
	s, err := NewServer(Options{Log: testLogger(), TLS: TLSOptions{Terminate: true, Mode: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSnapshot(&Snapshot{Version: 1, Routes: []Route{{Host: "example.com", PathPrefix: "/", Backend: "1.1.1.1:80", RedirectHTTP: true}}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Host = "example.com"
	req.RemoteAddr = "203.0.113.7:5000"
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("spoofed X-Forwarded-Proto must not bypass redirect, got %d", rr.Code)
	}
}

func TestRedirectHonorsTrustedForwardedProto(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()
	s, err := NewServer(Options{
		Log:            testLogger(),
		TLS:            TLSOptions{Terminate: true, Mode: "none"},
		TrustedProxies: []string{"192.0.2.10"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSnapshot(&Snapshot{Version: 1, Routes: []Route{{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend), RedirectHTTP: true}}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Host = "example.com"
	req.RemoteAddr = "192.0.2.10:5000"
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusMovedPermanently {
		t.Fatalf("trusted X-Forwarded-Proto https should not redirect")
	}
}

func TestProxyUnknownHostReturns404(t *testing.T) {
	_, proxySrv := startProxy(t, Options{})
	resp, err := http.Get(proxySrv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestProxyHealthEndpointReportsSnapshot(t *testing.T) {
	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: "1.1.1.1:80"})
	resp, err := http.Get(proxySrv.URL + "/_menina/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"snapshotVersion":1`) {
		t.Fatalf("health body = %q", body)
	}
}

func TestProxyBadGatewayOnDeadBackend(t *testing.T) {
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := dead.Addr().String()
	_ = dead.Close()
	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: addr})
	req, _ := http.NewRequest(http.MethodGet, proxySrv.URL+"/", nil)
	req.Host = "example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func TestProxyStripsForwardedHostFromUntrustedClient(t *testing.T) {
	var xfh string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xfh = r.Header.Get("X-Forwarded-Host")
	}))
	defer backend.Close()
	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend)})
	req, _ := http.NewRequest(http.MethodGet, proxySrv.URL+"/", nil)
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-Host", "evil.example.org")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if xfh != "" {
		t.Fatalf("untrusted X-Forwarded-Host must be stripped, got %q", xfh)
	}
}

func TestProxyMaliciousHostHeaderRejected(t *testing.T) {
	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: "1.1.1.1:80"})
	for _, host := range []string{"", "evil.com", "../../etc/passwd", "example.com.evil.com"} {
		req, _ := http.NewRequest(http.MethodGet, proxySrv.URL+"/", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("host %q: status = %d, want 404", host, resp.StatusCode)
		}
	}
}

func TestProxySSEFlushesByDefaultWithoutStreamingFlag(t *testing.T) {
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: one\n\n")
		fl.Flush()
		<-release
		fmt.Fprint(w, "data: two\n\n")
		fl.Flush()
	}))
	defer backend.Close()
	defer close(release)

	// Note: no Streaming flag on the route; flushing must still be immediate.
	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend)})
	req, _ := http.NewRequest(http.MethodGet, proxySrv.URL+"/", nil)
	req.Host = "example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("first SSE event was not flushed before the backend finished: %v", err)
	}
	if !strings.Contains(line, "one") {
		t.Fatalf("first event = %q", line)
	}
}

func TestProxyPerRouteResponseHeaderTimeout(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		_, _ = io.WriteString(w, "late")
	}))
	defer backend.Close()

	_, proxySrv := startProxy(t, Options{}, Route{
		Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend),
		Timeouts: RouteTimeouts{Read: "100ms"},
	})
	req, _ := http.NewRequest(http.MethodGet, proxySrv.URL+"/", nil)
	req.Host = "example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 from the response-header timeout", resp.StatusCode)
	}
}

func TestProxyCompressionSkippedForNoContent(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	_, proxySrv := startProxy(t, Options{}, Route{Host: "example.com", PathPrefix: "/", Backend: backendHost(t, backend), Compression: true})
	req, _ := http.NewRequest(http.MethodGet, proxySrv.URL+"/", nil)
	req.Host = "example.com"
	req.Header.Set("Accept-Encoding", "gzip")
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Encoding") == "gzip" {
		t.Fatal("204 responses must not be gzip-encoded")
	}
}
