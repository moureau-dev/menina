package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testSender() *Sender {
	s := NewSender(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.backoff = time.Millisecond
	return s
}

func TestSendPostsGenericPayloadAndSignature(t *testing.T) {
	var body []byte
	var event, sig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		event = r.Header.Get("X-Menina-Event")
		sig = r.Header.Get("X-Menina-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ev := Event{Event: "deploy.succeeded", Action: "deploy", Project: "p", Environment: "production", App: "api", Repository: "r/api", Tag: "production", Digest: "sha256:x", Status: "succeeded", Actor: "manual", Message: "hi"}
	if err := testSender().Send(context.Background(), Webhook{URL: srv.URL, Secret: "topsecret"}, ev); err != nil {
		t.Fatal(err)
	}
	if event != "deploy.succeeded" {
		t.Fatalf("X-Menina-Event = %q", event)
	}
	if sig != Signature(body, "topsecret") {
		t.Fatalf("signature mismatch: %q", sig)
	}
	var got payload
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Event != "deploy.succeeded" || got.Project != "p" || got.Environment != "production" || got.App != "api" || got.Status != "succeeded" {
		t.Fatalf("payload = %+v", got)
	}
	if got.Timestamp == "" {
		t.Fatal("timestamp missing")
	}
}

func TestSendSkipsUnsubscribedEvent(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	w := Webhook{URL: srv.URL, Events: []string{"deploy.failed"}}
	if err := testSender().Send(context.Background(), w, Event{Event: "deploy.succeeded"}); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatal("unsubscribed event must not be delivered")
	}
}

func TestSendRetriesThenSucceeds(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := testSender().Send(context.Background(), Webhook{URL: srv.URL}, Event{Event: "deploy.succeeded"}); err != nil {
		t.Fatalf("should succeed on retry: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestSendFailsAfterAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := testSender().Send(context.Background(), Webhook{URL: srv.URL}, Event{Event: "deploy.succeeded"}); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
}
