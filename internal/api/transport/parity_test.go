package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/moureau-dev/menina/internal/api"
	"github.com/moureau-dev/menina/internal/api/transport/stdio"
	"github.com/moureau-dev/menina/internal/api/transport/unix"
)

func TestTransportParity(t *testing.T) {
	dispatcher := api.NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	actor := api.Actor{ID: "local", Source: "test"}
	req := api.Request{Version: 1, Method: http.MethodGet, Path: "/status"}
	want := api.Response{Version: 1, Status: http.StatusOK, Body: json.RawMessage(`{"ok":true}`)}

	direct := dispatcher.Do(context.Background(), actor, req)
	assertResponse(t, "direct", direct, want)

	httpServer := httptest.NewServer(dispatcher)
	defer httpServer.Close()
	body, _ := json.Marshal(req)
	resp, err := http.Post(httpServer.URL+"/v1/commands", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var overHTTP api.Response
	if err := json.NewDecoder(resp.Body).Decode(&overHTTP); err != nil {
		t.Fatal(err)
	}
	assertResponse(t, "http", overHTTP, want)

	var overStdio bytes.Buffer
	in := bytes.NewBuffer(append(body, '\n'))
	if err := stdio.Serve(context.Background(), dispatcher, actor, in, &overStdio); err != nil {
		t.Fatal(err)
	}
	var stdioResp api.Response
	if err := json.Unmarshal(overStdio.Bytes(), &stdioResp); err != nil {
		t.Fatal(err)
	}
	assertResponse(t, "stdio", stdioResp, want)

	dir := t.TempDir()
	socket := filepath.Join(dir, "api.sock")
	tokenPath := filepath.Join(dir, "api.token")
	token, err := api.EnsureToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	server, err := unix.NewServer(socket, token, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	client, err := api.Dial(socket, tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		overUnix, err := client.Command(ctx, req)
		if err == nil {
			assertResponse(t, "unix", overUnix, want)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertResponse(t *testing.T, name string, got, want api.Response) {
	t.Helper()
	if got.Status != want.Status || !bytes.Equal(got.Body, want.Body) || got.Version != want.Version {
		t.Fatalf("%s response = %+v, want %+v", name, got, want)
	}
}
