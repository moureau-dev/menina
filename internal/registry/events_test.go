package registry

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
)

const ociNotificationBody = `{
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

func TestParseOCINotification(t *testing.T) {
	events, err := ParseNotification(http.Header{}, strings.NewReader(ociNotificationBody))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Repository != "my-platform/api" || events[0].Tag != "production" || events[0].Digest != "sha256:abc" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].DeliveryID != "evt-1" || !events[0].IsPush() {
		t.Fatalf("event = %+v", events[0])
	}
}

func TestParseOCISingleEventFallback(t *testing.T) {
	body := `{"action":"push","target":{"repository":"p/api","tag":"dev","digest":"sha256:def"},"request":{"id":"r2"}}`
	events, err := ParseNotification(http.Header{}, strings.NewReader(body))
	if err != nil || len(events) != 1 || events[0].DeliveryID != "r2" {
		t.Fatalf("events = %+v err=%v", events, err)
	}
}

func TestParseNotificationRejectsGarbage(t *testing.T) {
	if _, err := ParseNotification(http.Header{}, strings.NewReader(`{"hello":"world"}`)); err == nil {
		t.Fatal("expected error for unrecognized payload")
	}
}

func TestParseZotCloudEventBinary(t *testing.T) {
	h := http.Header{}
	h.Set("Ce-Type", "zotregistry.image.updated")
	h.Set("Ce-Id", "ce-123")
	h.Set("Ce-Source", "zotregistry.dev")
	body := `{"name":"my-platform/api","reference":"production","digest":"sha256:abc","mediaType":"application/vnd.oci.image.manifest.v1+json"}`
	events, err := ParseNotification(h, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	e := events[0]
	if e.Repository != "my-platform/api" || e.Tag != "production" || e.Digest != "sha256:abc" || e.DeliveryID != "ce-123" {
		t.Fatalf("event = %+v", e)
	}
}

func TestParseZotCloudEventStructured(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/cloudevents+json")
	body := `{"id":"ce-9","type":"zotregistry.image.updated","source":"zotregistry.dev","data":{"name":"p/api","reference":"prod","digest":"sha256:9"}}`
	events, err := ParseNotification(h, strings.NewReader(body))
	if err != nil || len(events) != 1 || events[0].Digest != "sha256:9" || events[0].DeliveryID != "ce-9" {
		t.Fatalf("events = %+v err=%v", events, err)
	}
}

func TestParseZotIgnoresNonImageUpdatedEvents(t *testing.T) {
	h := http.Header{}
	h.Set("Ce-Type", "zotregistry.repository.created")
	h.Set("Ce-Id", "ce-1")
	events, err := ParseNotification(h, strings.NewReader(`{"name":"p/api"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no push events, got %+v", events)
	}
}

func TestDedupKeyStableAndDistinct(t *testing.T) {
	a := DedupKey("reg", "p/api", "sha256:1", "delivery-1")
	b := DedupKey("reg", "p/api", "sha256:1", "delivery-1")
	c := DedupKey("reg", "p/api", "sha256:2", "delivery-1")
	if a != b || a == c {
		t.Fatalf("dedup keys unstable: %s %s %s", a, b, c)
	}
}

func TestBuildZotConfigTranslation(t *testing.T) {
	data, err := BuildZotConfig(ZotParams{
		RootDirectory:    "/var/lib/registry",
		ContainerPort:    "5000",
		HtpasswdPath:     "/etc/zot-auth/htpasswd",
		EventAddress:     "http://10.0.0.5:9000/v2/events",
		EventToken:       "secret-token",
		RetentionEnabled: true,
		KeepRecentTags:   3,
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
	if cfg.Storage.Retention == nil || len(cfg.Storage.Retention.Policies) != 1 {
		t.Fatalf("retention = %+v (must be under storage for Zot v2)", cfg.Storage.Retention)
	}
	if got := cfg.Storage.Retention.Policies[0].KeepTags[0].MostRecentlyPushedCount; got != 3 {
		t.Fatalf("mostRecentlyPushedCount = %d", got)
	}
	if cfg.Storage.Retention.Policies[0].DeleteUntagged {
		t.Fatal("untagged manifests must be retained so digest-pinned rollbacks survive GC")
	}
	if cfg.Extensions.Events == nil || !cfg.Extensions.Events.Enable {
		t.Fatalf("events = %+v", cfg.Extensions.Events)
	}
	sink := cfg.Extensions.Events.Sinks[0]
	if sink.Type != "http" || sink.Address != "http://10.0.0.5:9000/v2/events" {
		t.Fatalf("sink = %+v", sink)
	}
	if sink.Headers["Authorization"] != "Bearer secret-token" {
		t.Fatalf("sink headers = %+v", sink.Headers)
	}
}

func TestBuildZotConfigRetentionDisabled(t *testing.T) {
	data, err := BuildZotConfig(ZotParams{RootDirectory: "/var/lib/registry", RetentionEnabled: false, KeepRecentTags: 3})
	if err != nil {
		t.Fatal(err)
	}
	var cfg ZotConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Retention != nil {
		t.Fatalf("retention must be omitted when disabled: %+v", cfg.Storage.Retention)
	}
}

func TestValidateAgainstZotVersion(t *testing.T) {
	if err := ValidateAgainstZotVersion("v2.1.22"); err != nil {
		t.Fatalf("v2.1.22 should be supported: %v", err)
	}
	if err := ValidateAgainstZotVersion("v2.1.2"); err == nil {
		t.Fatal("v2.1.2 lacks the events extension and must be rejected")
	}
	if err := ValidateAgainstZotVersion("v1.4.0"); err == nil {
		t.Fatal("v1 is unsupported")
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
