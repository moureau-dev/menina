package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apierrors "github.com/moureau-dev/menina/internal/api/errors"
	"github.com/moureau-dev/menina/internal/core/audit"
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

func TestDispatcherTrustedActorHeaderOnlyLocally(t *testing.T) {
	d := NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"actor": ActorFromContext(r.Context()).ID})
	}))
	body, _ := json.Marshal(Request{Version: 1, Method: http.MethodGet, Path: "/status"})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/commands", bytes.NewReader(body))
	req.RemoteAddr = ""
	req.Header.Set("X-Menina-Actor-ID", "profile-1")
	req.Header.Set("X-Menina-Actor-Source", "ssh")
	d.ServeHTTP(rr, req)
	var local struct {
		Body struct {
			Actor string `json:"actor"`
		} `json:"body"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &local); err != nil {
		t.Fatal(err)
	}
	if local.Body.Actor != "profile-1" {
		t.Fatalf("local actor = %q", local.Body.Actor)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/commands", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Menina-Actor-ID", "profile-1")
	d.ServeHTTP(rr, req)
	var remote struct {
		Body struct {
			Actor string `json:"actor"`
		} `json:"body"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &remote); err != nil {
		t.Fatal(err)
	}
	if remote.Body.Actor != "local" {
		t.Fatalf("remote actor = %q", remote.Body.Actor)
	}
}

type fakeAudit struct{ events []audit.Event }

func (f *fakeAudit) RecordAudit(ctx context.Context, e audit.Event) (string, error) {
	f.events = append(f.events, e)
	return "audit-1", nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(context.Context, Actor, Request) error {
	return apierrors.New(apierrors.CodeAuth, "denied", "")
}

func TestDispatcherRecordsAudit(t *testing.T) {
	auditor := &fakeAudit{}
	d := NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	d.SetAuditor(auditor)
	d.Do(context.Background(), Actor{ID: "profile-1", Source: "ssh", KeyFingerprint: "SHA256:x"}, Request{Method: http.MethodGet, Path: "/status"})
	if len(auditor.events) != 1 || auditor.events[0].Status != audit.StatusOK || auditor.events[0].KeyFingerprint != "SHA256:x" {
		t.Fatalf("events = %+v", auditor.events)
	}

	denied := NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	denied.SetAuditor(auditor)
	denied.SetAuthorizer(denyAuthorizer{})
	denied.Do(context.Background(), Actor{ID: "profile-1", Source: "ssh"}, Request{Method: http.MethodPost, Path: "/deploy"})
	if len(auditor.events) != 2 || auditor.events[1].Status != audit.StatusDenied {
		t.Fatalf("events = %+v", auditor.events)
	}
}
