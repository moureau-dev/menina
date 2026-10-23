package tests

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moureau-dev/menina/internal/api"
	"github.com/moureau-dev/menina/internal/api/transport/unix"
	"github.com/moureau-dev/menina/internal/edge"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitDial(t *testing.T, socket, tokenPath string) *api.Client {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		client, err := api.Dial(socket, tokenPath)
		if err == nil {
			return client
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("control socket did not become available")
	return nil
}

func TestControlIPCDeliversRoutingSnapshotToProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello from backend")
	}))
	defer backend.Close()

	socket := filepath.Join(t.TempDir(), "control.sock")
	tokenPath := filepath.Join(t.TempDir(), "control.token")
	token, err := api.EnsureToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := &edge.Snapshot{
		Version: 42,
		Routes: []edge.Route{{
			Host: "example.com", PathPrefix: "/",
			Backend: strings.TrimPrefix(backend.URL, "http://"),
			Service: "web", Namespace: "menina-apps",
		}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = jsonEncode(w, snapshot)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := unix.NewServer(socket, token, mux)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}

	client := waitDial(t, socket, tokenPath)
	got, err := client.Snapshot(ctx)
	if err != nil {
		t.Fatalf("fetch snapshot: %v", err)
	}
	if got.Version != 42 || len(got.Routes) != 1 {
		t.Fatalf("snapshot = %+v", got)
	}

	proxyServer, err := edge.NewServer(edge.Options{Log: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if err := proxyServer.UpdateSnapshot(got); err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(proxyServer.Handler())
	defer front.Close()

	req, _ := http.NewRequest(http.MethodGet, front.URL+"/", nil)
	req.Host = "example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "hello from backend" {
		t.Fatalf("proxy response %d %q", resp.StatusCode, body)
	}
	if proxyServer.SnapshotVersion() != 42 {
		t.Fatalf("proxy snapshot version = %d", proxyServer.SnapshotVersion())
	}
}

func TestControlRejectsUnauthorizedClient(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "control.sock")
	tokenPath := filepath.Join(t.TempDir(), "control.token")
	token, err := api.EnsureToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, _ := unix.NewServer(socket, token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "secret")
	}))
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}

	badTokenPath := filepath.Join(t.TempDir(), "bad.token")
	if err := os.WriteFile(badTokenPath, []byte("wrong"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := waitDial(t, socket, badTokenPath)
	var out map[string]any
	if err := client.GetJSON(ctx, "/snapshot", &out); err == nil {
		t.Fatal("expected unauthorized request to fail")
	}
}

func jsonEncode(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}
