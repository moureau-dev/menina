package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSelfSigned(t *testing.T, dir, host string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{host},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certOut, _ := os.Create(filepath.Join(dir, host+".crt"))
	_ = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	certOut.Close()
	keyBytes, _ := x509.MarshalECPrivateKey(key)
	keyOut, _ := os.Create(filepath.Join(dir, host+".key"))
	_ = pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	keyOut.Close()
}

func TestTLSManagerLoadsManualCertificates(t *testing.T) {
	dir := t.TempDir()
	writeSelfSigned(t, dir, "example.com")
	m, err := NewTLSManager(TLSOptions{Terminate: true, Mode: "manual", CertDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Terminating() {
		t.Fatal("expected terminating mode")
	}
	cert, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"})
	if err != nil {
		t.Fatalf("get certificate: %v", err)
	}
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatal("empty certificate")
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "unknown.com"}); err == nil {
		t.Fatal("expected error for unknown host with no ACME")
	}
}

func TestTLSManagerNonTerminating(t *testing.T) {
	m, err := NewTLSManager(TLSOptions{Terminate: false})
	if err != nil {
		t.Fatal(err)
	}
	if m.Terminating() {
		t.Fatal("should not terminate")
	}
}

func TestTLSManagerRejectsUnknownMode(t *testing.T) {
	if _, err := NewTLSManager(TLSOptions{Terminate: true, Mode: "bogus"}); err == nil {
		t.Fatal("expected unknown mode error")
	}
}

func TestACMEHostPolicyAllowsBaseDomainsAndRoutes(t *testing.T) {
	m, err := NewTLSManager(TLSOptions{
		Terminate: true,
		Mode:      "acme",
		CertDir:   t.TempDir(),
		ACME:      ACMEOptions{BaseDomains: []string{"hello.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.checkHostPolicy("hello.com"); err != nil {
		t.Fatalf("base domain should be permitted: %v", err)
	}
	if err := m.checkHostPolicy("api.hello.com"); err != nil {
		t.Fatalf("base subdomain should be permitted: %v", err)
	}
	if err := m.checkHostPolicy("evil.example.org"); err == nil {
		t.Fatal("unrelated host should be rejected when no route exists")
	}
	m.SetAllowedHost(func(host string) bool { return host == "app.example.org" })
	if err := m.checkHostPolicy("app.example.org"); err != nil {
		t.Fatalf("route host should be permitted: %v", err)
	}
	if cfg := m.TLSServerConfig(); !contains(cfg.NextProtos, "acme-tls/1") {
		t.Fatalf("acme TLS config should offer acme-tls/1, got %v", cfg.NextProtos)
	}
}

func TestServerWiresRouteHostsIntoACMEPolicy(t *testing.T) {
	s, err := NewServer(Options{
		Log: testLogger(),
		TLS: TLSOptions{Terminate: true, Mode: "acme", CertDir: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSnapshot(&Snapshot{Version: 1, Routes: []Route{{Host: "api.hello.com", PathPrefix: "/", Backend: "1.1.1.1:80"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.tls.checkHostPolicy("api.hello.com"); err != nil {
		t.Fatalf("exposed route host should be permitted: %v", err)
	}
	if err := s.tls.checkHostPolicy("not-exposed.example.com"); err == nil {
		t.Fatal("host without a route should not be permitted")
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestACMEPolicyDeniesAfterRouteRemoval(t *testing.T) {
	s, err := NewServer(Options{
		Log: testLogger(),
		TLS: TLSOptions{Terminate: true, Mode: "acme", CertDir: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSnapshot(&Snapshot{Version: 1, Routes: []Route{{Host: "api.hello.com", PathPrefix: "/", Backend: "1.1.1.1:80"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.tls.checkHostPolicy("api.hello.com"); err != nil {
		t.Fatalf("route host should be permitted: %v", err)
	}
	if err := s.UpdateSnapshot(&Snapshot{Version: 2, Routes: nil}); err != nil {
		t.Fatal(err)
	}
	if err := s.tls.checkHostPolicy("api.hello.com"); err == nil {
		t.Fatal("removed host should no longer be permitted (no cert served for deleted routes)")
	}
}

func TestWarmupIsNoopWithoutACME(t *testing.T) {
	m, err := NewTLSManager(TLSOptions{Terminate: true, Mode: "manual", CertDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Warmup("example.com"); err != nil {
		t.Fatalf("warmup without acme should be a no-op: %v", err)
	}
}

func TestManualCertificateReload(t *testing.T) {
	dir := t.TempDir()
	writeSelfSigned(t, dir, "a.example.com")
	m, err := NewTLSManager(TLSOptions{Terminate: true, Mode: "manual", CertDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "a.example.com"}); err != nil {
		t.Fatalf("a.example.com should be loaded: %v", err)
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "b.example.com"}); err == nil {
		t.Fatal("b.example.com should not be known yet")
	}
	writeSelfSigned(t, dir, "b.example.com")
	if err := m.ReloadManual(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "b.example.com"}); err != nil {
		t.Fatalf("b.example.com should be served after reload: %v", err)
	}
}
