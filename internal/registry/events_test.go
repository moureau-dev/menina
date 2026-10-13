package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
)

const notificationBody = `{
  "events": [
    {
      "id": "evt-1",
      "timestamp": "2024-01-01T00:00:00Z",
      "action": "push",
      "target": {"repository": "my-platform/api", "tag": "production", "digest": "sha256:abc", "size": 123},
      "request": {"id": "req-1", "host": "registry.moureau.dev"}
    }
  ]
}`

func TestParseNotification(t *testing.T) {
	events, err := ParseNotification(strings.NewReader(notificationBody))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	e := events[0]
	if e.Repository != "my-platform/api" || e.Tag != "production" || e.Digest != "sha256:abc" {
		t.Fatalf("event = %+v", e)
	}
	if e.DeliveryID != "evt-1" {
		t.Fatalf("delivery = %q", e.DeliveryID)
	}
	if !e.IsPush() {
		t.Fatal("expected push event")
	}
}

func TestParseSingleEventFallback(t *testing.T) {
	body := `{"action":"push","target":{"repository":"p/api","tag":"dev","digest":"sha256:def"},"request":{"id":"r2"}}`
	events, err := ParseNotification(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].DeliveryID != "r2" {
		t.Fatalf("events = %+v", events)
	}
}

func TestParseNotificationRejectsGarbage(t *testing.T) {
	if _, err := ParseNotification(strings.NewReader(`{"hello":"world"}`)); err == nil {
		t.Fatal("expected error for unrecognized payload")
	}
}

func TestDedupKeyStableAndDistinct(t *testing.T) {
	a := DedupKey("reg", "p/api", "sha256:1", "delivery-1")
	b := DedupKey("reg", "p/api", "sha256:1", "delivery-1")
	c := DedupKey("reg", "p/api", "sha256:2", "delivery-1")
	d := DedupKey("reg", "p/api", "sha256:1", "delivery-2")
	if a != b {
		t.Fatal("dedup key should be stable")
	}
	if a == c || a == d {
		t.Fatal("dedup key should vary with digest and delivery")
	}
}

func TestBuildZotConfigTranslation(t *testing.T) {
	data, err := BuildZotConfig(ZotParams{
		RootDirectory:  "/var/lib/registry",
		ContainerPort:  "5000",
		HtpasswdPath:   "/etc/zot-auth/htpasswd",
		EventAddress:   "http://menina-serve.menina-system.svc:9000/v2/events",
		EventToken:     "secret-token",
		KeepRecentTags: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg ZotConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.RootDirectory != "/var/lib/registry" || !cfg.Storage.GC {
		t.Fatalf("storage = %+v", cfg.Storage)
	}
	if cfg.HTTP.Auth.Htpasswd.Path != "/etc/zot-auth/htpasswd" {
		t.Fatalf("htpasswd path = %q", cfg.HTTP.Auth.Htpasswd.Path)
	}
	if len(cfg.Extensions.Retention.Policy.Repositories) != 1 {
		t.Fatalf("retention = %+v", cfg.Extensions.Retention)
	}
	retain := cfg.Extensions.Retention.Policy.Repositories[0].Policies[0].Retain
	if retain != 3 {
		t.Fatalf("retain = %d", retain)
	}
	if len(cfg.Extensions.Events.Sinks) != 1 || cfg.Extensions.Events.Sinks[0].Headers["Authorization"] != "Bearer secret-token" {
		t.Fatalf("events = %+v", cfg.Extensions.Events)
	}
}

func TestTranslateRetentionRejectsBadDigest(t *testing.T) {
	reg := config.Registry{Retention: config.Retention{KeepRecentTags: 3}}
	if _, _, err := TranslateRetention(reg, []string{"not-a-digest"}); err == nil {
		t.Fatal("expected invalid digest error")
	}
	keep, protected, err := TranslateRetention(reg, []string{"sha256:abc"})
	if err != nil || keep != 3 || len(protected) != 1 {
		t.Fatalf("keep=%d protected=%v err=%v", keep, protected, err)
	}
}
