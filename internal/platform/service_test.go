package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
)

func serviceWithNotifyToken(t *testing.T, token string) *Service {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "notify.token")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Registry.Enabled = false
	cfg.Registry.Notifications.TokenRef = "file:" + tokenPath
	cfg.Platform.DBPath = filepath.Join(dir, "menina.db")
	svc, err := NewService(&cfg, dir, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestRegistryWebhookRequiresToken(t *testing.T) {
	svc := serviceWithNotifyToken(t, "super-secret-token")
	body := `{"events":[{"id":"e1","action":"push","target":{"repository":"p/api","tag":"dev","digest":"sha256:x"}}]}`

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "Bearer nope", http.StatusUnauthorized},
		{"correct", "Bearer super-secret-token", http.StatusAccepted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v2/events", strings.NewReader(body))
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rr := httptest.NewRecorder()
			svc.handleRegistryEvent(rr, req)
			if rr.Code != c.want {
				t.Fatalf("status = %d, want %d (body %q)", rr.Code, c.want, rr.Body.String())
			}
		})
	}
}

func TestRegistryWebhookAcceptsZotCloudEvent(t *testing.T) {
	svc := serviceWithNotifyToken(t, "tok")
	// Shape captured from a real Zot v2.1.22 push event (binary CloudEvents).
	body := `{"digest":"sha256:365bc2546d43809f951b08e1370dd1a150366273dd9bfb168ee3ccd4593c0adb","mediaType":"application/vnd.docker.distribution.manifest.v2+json","name":"my-platform/api","reference":"production"}`
	req := httptest.NewRequest(http.MethodPost, "/v2/events", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Ce-Type", "zotregistry.image.updated")
	req.Header.Set("Ce-Id", "ce-1")
	req.Header.Set("Ce-Source", "zotregistry.dev")
	rr := httptest.NewRecorder()
	svc.handleRegistryEvent(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %q)", rr.Code, rr.Body.String())
	}
	events, err := svc.db.PendingEvents(context.Background(), 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("event was not durably persisted: %+v err=%v", events, err)
	}
	if events[0].Repository != "my-platform/api" || events[0].Tag != "production" {
		t.Fatalf("persisted event = %+v", events[0])
	}
}

func TestRegistryWebhookOpenWhenNoTokenConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Registry.Enabled = false
	cfg.Platform.DBPath = filepath.Join(t.TempDir(), "menina.db")
	svc, err := NewService(&cfg, t.TempDir(), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"events":[{"id":"e1","action":"push","target":{"repository":"p/api","tag":"dev","digest":"sha256:x"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/v2/events", strings.NewReader(body))
	rr := httptest.NewRecorder()
	svc.handleRegistryEvent(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rr.Code)
	}
}
