package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
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

func TestMetricsEndpoint(t *testing.T) {
	svc := serviceWithNotifyToken(t, "tok")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	svc.handleMetrics(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "menina_snapshot_version") || !strings.Contains(body, "menina_routes") {
		t.Fatalf("metrics body = %q", body)
	}
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

func TestRequireProductionConfirmation(t *testing.T) {
	svc := &Service{cfg: &config.Config{}}
	svc.cfg.Security.EnforceProdPolicy = true
	if err := svc.requireProductionConfirmation("production", false); err == nil {
		t.Fatal("production deploy without confirmation must be rejected")
	}
	if err := svc.requireProductionConfirmation("production", true); err != nil {
		t.Fatalf("confirmed production deploy should pass: %v", err)
	}
	if err := svc.requireProductionConfirmation("development", false); err != nil {
		t.Fatalf("non-production deploy should not require confirmation: %v", err)
	}
	svc.cfg.Security.EnforceProdPolicy = false
	if err := svc.requireProductionConfirmation("production", false); err != nil {
		t.Fatalf("policy disabled should allow: %v", err)
	}
}

func TestNewServiceFailsWhenNotificationsEnabledWithoutToken(t *testing.T) {
	cfg := config.Default()
	cfg.Registry.Enabled = true
	cfg.Registry.Notifications.Enabled = true
	cfg.Registry.Notifications.TokenRef = ""
	cfg.Platform.DBPath = filepath.Join(t.TempDir(), "menina.db")
	if _, err := NewService(&cfg, t.TempDir(), testLogger()); err == nil {
		t.Fatal("NewService must fail closed when notifications are enabled without a token")
	}
}

func TestNewServiceSucceedsWhenNotificationsDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Registry.Enabled = false
	cfg.Registry.Notifications.Enabled = false
	cfg.Registry.Notifications.TokenRef = ""
	cfg.Platform.DBPath = filepath.Join(t.TempDir(), "menina.db")
	if _, err := NewService(&cfg, t.TempDir(), testLogger()); err != nil {
		t.Fatalf("disabled notifications must not require a token: %v", err)
	}
}

func TestRegistryWebhookRefusesWithoutToken(t *testing.T) {
	svc := &Service{}
	req := httptest.NewRequest(http.MethodPost, "/v2/events", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	svc.handleRegistryEvent(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when no token is configured", rr.Code)
	}
}

func TestReleaseStatusAggregate(t *testing.T) {
	svc := serviceWithNotifyToken(t, "tok")
	ctx := context.Background()
	if _, err := svc.db.CreateRelease(ctx, database.Release{Project: "p", Environment: "prod", App: "api", Repository: "r/api", Digest: "sha256:a", State: "active", ConfigVersion: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.CreateRelease(ctx, database.Release{Project: "p", Environment: "prod", App: "admin", Repository: "r/admin", Digest: "sha256:b", State: "failed", Message: "verification failed", Migration: `{"command":["migrate"]}`}); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/release?project=p&environment=prod", nil)
	svc.handleRelease(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"status":"partially_failed"`) {
		t.Fatalf("aggregate must report partial failure: %s", body)
	}
	if !strings.Contains(body, `"data":"migration_not_reversed"`) {
		t.Fatalf("aggregate must report migration state separately: %s", body)
	}
}

func TestPromoteDryRunAndStateToken(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Registry.Enabled = false
	cfg.Registry.Notifications.Enabled = false
	cfg.Security.EnforceProdPolicy = false
	cfg.Platform.DBPath = filepath.Join(dir, "menina.db")
	if err := config.SavePlatform(dir, &cfg); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(dir, "projects", "p")
	for _, sub := range []string{"apps", "environments"} {
		if err := os.MkdirAll(filepath.Join(projectDir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(filepath.Join(projectDir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("project.yml", "name: p\n")
	write("apps/api.yml", "name: api\nimage:\n  repository: registry.example.com/p/api\nworkload:\n  type: service\n  port: 8080\n")
	write("environments/production.yml", "name: production\n")
	write("environments/staging.yml", "name: staging\n")

	svc, err := NewService(&cfg, dir, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.watcher.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.CreateRelease(context.Background(), database.Release{Project: "p", Environment: "production", App: "api", Repository: "registry.example.com/p/api", Digest: "sha256:abc", Image: "registry.example.com/p/api@sha256:abc", State: "active"}); err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promote", strings.NewReader(body))
		svc.handlePromote(rr, req)
		return rr
	}
	rr := post(`{"project":"p","fromEnvironment":"production","toEnvironment":"staging","app":"api","dryRun":true}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "sha256:abc") {
		t.Fatalf("dry run = %d %s", rr.Code, rr.Body.String())
	}
	var plan struct {
		Plan struct {
			StateToken string `json:"stateToken"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Plan.StateToken == "" {
		t.Fatal("dry run must return a state token")
	}
	bad := post(`{"project":"p","fromEnvironment":"production","toEnvironment":"staging","app":"api","stateToken":"stale"}`)
	if bad.Code != http.StatusConflict {
		t.Fatalf("stale token = %d %s", bad.Code, bad.Body.String())
	}
	ok := post(fmt.Sprintf(`{"project":"p","fromEnvironment":"production","toEnvironment":"staging","app":"api","stateToken":%q}`, plan.Plan.StateToken))
	if ok.Code != http.StatusAccepted {
		t.Fatalf("promote = %d %s", ok.Code, ok.Body.String())
	}
}
