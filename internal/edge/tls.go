package edge

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

type ACMEOptions struct {
	Email        string
	DirectoryURL string
	StorageDir   string
	BaseDomains  []string
}

type TLSOptions struct {
	Terminate bool
	Mode      string
	CertDir   string
	ACME      ACMEOptions
}

type TLSManager struct {
	opts        TLSOptions
	acme        *autocert.Manager
	mu          sync.RWMutex
	manual      map[string]*tls.Certificate
	allowedHost func(string) bool
}

func NewTLSManager(opts TLSOptions) (*TLSManager, error) {
	m := &TLSManager{opts: opts, manual: map[string]*tls.Certificate{}}
	if !opts.Terminate {
		return m, nil
	}
	switch opts.Mode {
	case "acme":
		dir := opts.ACME.StorageDir
		if dir == "" {
			dir = filepath.Join(opts.CertDir, "acme")
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		man := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(dir),
			HostPolicy: func(ctx context.Context, host string) error { return m.checkHostPolicy(host) },
		}
		if opts.ACME.Email != "" {
			man.Email = opts.ACME.Email
		}
		if opts.ACME.DirectoryURL != "" {
			man.Client = &acme.Client{DirectoryURL: opts.ACME.DirectoryURL}
		}
		m.acme = man
	case "manual":
		if err := m.loadManual(); err != nil {
			return nil, err
		}
	case "none", "":
	default:
		return nil, fmt.Errorf("unsupported tls mode %q", opts.Mode)
	}
	return m, nil
}

func (m *TLSManager) Terminating() bool { return m.opts.Terminate }

// SetAllowedHost registers an additional predicate consulted by the ACME host
// policy. The proxy uses it to permit certificate issuance for every hostname
// that currently has a route, so exposing an app is enough to get it TLS.
func (m *TLSManager) SetAllowedHost(fn func(string) bool) {
	m.mu.Lock()
	m.allowedHost = fn
	m.mu.Unlock()
}

func (m *TLSManager) checkHostPolicy(host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	m.mu.RLock()
	allowed := m.allowedHost
	m.mu.RUnlock()
	if allowed != nil && allowed(host) {
		return nil
	}
	for _, d := range m.opts.ACME.BaseDomains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return nil
		}
	}
	return fmt.Errorf("acme: host %q is not permitted (add it to networking.baseDomains or expose it as a route)", host)
}

func (m *TLSManager) TLSServerConfig() *tls.Config {
	if m.acme != nil {
		cfg := m.acme.TLSConfig()
		cfg.MinVersion = tls.VersionTLS12
		return cfg
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1"},
		GetCertificate: m.GetCertificate,
	}
}

func (m *TLSManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := strings.ToLower(hello.ServerName)
	m.mu.RLock()
	cert, ok := m.manual[host]
	m.mu.RUnlock()
	if ok {
		return cert, nil
	}
	if m.acme != nil {
		return m.acme.GetCertificate(hello)
	}
	return nil, fmt.Errorf("no certificate available for %q", host)
}

func (m *TLSManager) ACMEHTTPHandler(fallback http.Handler) http.Handler {
	if m.acme != nil {
		return m.acme.HTTPHandler(fallback)
	}
	return fallback
}

// Warmup proactively obtains or refreshes the certificate for host so that
// newly exposed apps do not have to wait for (and fail) their first TLS
// handshake, and idle hosts still get renewed.
func (m *TLSManager) Warmup(host string) error {
	if m.acme == nil {
		return nil
	}
	// autocert ignores all ClientHelloInfo fields except ServerName.
	_, err := m.acme.GetCertificate(&tls.ClientHelloInfo{ServerName: host})
	return err
}

// ReloadManual re-reads certificate files from disk. ACME certificates are
// managed by autocert and reloaded automatically; this is only meaningful for
// manual mode.
func (m *TLSManager) ReloadManual() error {
	if m.opts.Mode != "manual" {
		return nil
	}
	next := map[string]*tls.Certificate{}
	if err := m.loadManualInto(next); err != nil {
		return err
	}
	m.mu.Lock()
	m.manual = next
	m.mu.Unlock()
	return nil
}

func (m *TLSManager) loadManual() error {
	return m.loadManualInto(m.manual)
}

func (m *TLSManager) loadManualInto(target map[string]*tls.Certificate) error {
	entries, err := os.ReadDir(m.opts.CertDir)
	if err != nil {
		return fmt.Errorf("read cert dir %q: %w", m.opts.CertDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".crt") && !strings.HasSuffix(e.Name(), ".pem")) {
			continue
		}
		base := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".crt"), ".pem")
		certPath := filepath.Join(m.opts.CertDir, e.Name())
		keyPath := filepath.Join(m.opts.CertDir, base+".key")
		if _, err := os.Stat(keyPath); err != nil {
			continue
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return fmt.Errorf("load keypair %s: %w", certPath, err)
		}
		host := strings.ReplaceAll(base, "_", ".")
		target[strings.ToLower(host)] = &cert
	}
	return nil
}
