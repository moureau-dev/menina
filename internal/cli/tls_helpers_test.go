package cli

import (
	"testing"

	"github.com/moureau-dev/menina/internal/config"
)

func TestBaseZone(t *testing.T) {
	cases := map[string]string{
		"api.hello.com":      "hello.com",
		"hello.com":          "hello.com",
		"a.b.hello.co.uk":    "hello.co.uk",
		"api.example.com.br": "example.com.br",
	}
	for in, want := range cases {
		if got := baseZone(in); got != want {
			t.Errorf("baseZone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDNSTargetsIncludesRegistryBaseDomainsAndExposureHosts(t *testing.T) {
	cfg := config.Default()
	cfg.Networking.BaseDomains = []string{"hello.com", "hello.com"}
	cfg.Registry.Enabled = true
	cfg.Registry.Domain = "registry.hello.com"
	got := dnsTargets(&cfg, []string{"api.hello.com", "hello.com"})
	want := map[string]bool{"hello.com": true, "registry.hello.com": true, "api.hello.com": true}
	if len(got) != len(want) {
		t.Fatalf("dnsTargets = %v, want %v", got, want)
	}
	for _, d := range got {
		if !want[d] {
			t.Fatalf("unexpected target %q in %v", d, got)
		}
	}
}

func TestHTTPSPort(t *testing.T) {
	if got := httpsPort(":8443"); got != "8443" {
		t.Fatalf("httpsPort(:8443) = %q", got)
	}
	if got := httpsPort(""); got != "443" {
		t.Fatalf("httpsPort(\"\") = %q", got)
	}
}

func TestCheckCertDirWritable(t *testing.T) {
	dir := t.TempDir() + "/certs"
	if err := checkCertDirWritable(dir); err != nil {
		t.Fatalf("expected writable cert dir: %v", err)
	}
	if err := checkCertDirWritable(""); err == nil {
		t.Fatal("expected error for empty cert dir")
	}
}
