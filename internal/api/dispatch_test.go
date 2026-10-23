package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDispatcherActorVersionAndIdempotency(t *testing.T) {
	var calls int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		actor := ActorFromContext(r.Context())
		_ = json.NewEncoder(w).Encode(map[string]string{
			"actor":   actor.ID,
			"version": r.Header.Get("X-Menina-API-Version"),
			"path":    r.URL.Path,
		})
	})
	d := NewDispatcher(handler)
	req := Request{Method: http.MethodPost, Path: "/deploy", IdempotencyKey: "same", Body: json.RawMessage(`{"app":"api"}`)}

	first := d.Do(context.Background(), Actor{ID: "local", Source: "unix"}, req)
	if first.Status != http.StatusOK || calls != 1 {
		t.Fatalf("first status=%d calls=%d body=%s", first.Status, calls, first.Body)
	}
	var body map[string]string
	if err := json.Unmarshal(first.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["actor"] != "local" || body["version"] != "1" || body["path"] != "/deploy" {
		t.Fatalf("body = %+v", body)
	}

	second := d.Do(context.Background(), Actor{ID: "local", Source: "ssh"}, req)
	if calls != 1 || !bytes.Equal(first.Body, second.Body) {
		t.Fatalf("idempotent replay calls=%d first=%s second=%s", calls, first.Body, second.Body)
	}

	req.IdempotencyKey = "other"
	_ = d.Do(context.Background(), Actor{ID: "local"}, req)
	if calls != 2 {
		t.Fatalf("different idempotency key calls=%d", calls)
	}

	unsupported := d.Do(context.Background(), Actor{ID: "local"}, Request{Version: 99, Path: "/deploy"})
	if unsupported.Status != http.StatusBadRequest || unsupported.Error == nil || unsupported.Error.Code != "unsupported" {
		t.Fatalf("unsupported = %+v", unsupported)
	}
}

func TestDispatcherServeHTTPEnvelope(t *testing.T) {
	d := NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	body, _ := json.Marshal(Request{Version: 1, Method: http.MethodGet, Path: "/status"})
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/commands", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Menina-API-Version") != "1" {
		t.Fatalf("version header = %q", rr.Header().Get("X-Menina-API-Version"))
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusOK || string(resp.Body) != `{"ok":true}` {
		t.Fatalf("response = %+v", resp)
	}
}
