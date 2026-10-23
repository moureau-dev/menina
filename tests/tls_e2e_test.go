package tests

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moureau-dev/menina/internal/edge"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Menina Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(100 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

func (ca *testCA) issue(t *testing.T, host string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		DNSNames:     []string{host},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// seedACME writes a certificate into autocert's DirCache in the format it
// expects (private key first, then the chain), keyed by hostname.
func (ca *testCA) seedACME(t *testing.T, dir, host string) {
	t.Helper()
	certPEM, keyPEM := ca.issue(t, host)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
	content := append(append(append([]byte{}, keyPEM...), certPEM...), caPEM...)
	if err := os.WriteFile(filepath.Join(dir, host), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTLSEndToEndServesCachedACMECertificate(t *testing.T) {
	host := "api.hello.com"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "secure-ok")
	}))
	defer backend.Close()

	cacheDir := t.TempDir()
	ca := newTestCA(t)
	ca.seedACME(t, cacheDir, host)

	server, err := edge.NewServer(edge.Options{
		Log: testLogger(),
		TLS: edge.TLSOptions{
			Terminate: true,
			Mode:      "acme",
			ACME:      edge.ACMEOptions{StorageDir: cacheDir, BaseDomains: []string{"hello.com"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.UpdateSnapshot(&edge.Snapshot{Version: 1, Routes: []edge.Route{{
		Host: host, PathPrefix: "/", Backend: strings.TrimPrefix(backend.URL, "http://"), RedirectHTTP: false,
	}}}); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{Handler: server.Handler(), TLSConfig: server.TLSConfig()}
	go func() { _ = httpSrv.ServeTLS(ln, "", "") }()
	defer httpSrv.Close()

	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		ServerName: host,
		RootCAs:    ca.pool,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("TLS handshake with the proxy failed: %v", err)
	}
	defer conn.Close()

	req, _ := http.NewRequest(http.MethodGet, "https://"+host+"/", nil)
	req.Host = host
	if err := req.Write(conn); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "secure-ok" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
}

func TestTLSHandshakeFailsForUnroutedHost(t *testing.T) {
	cacheDir := t.TempDir()
	server, err := edge.NewServer(edge.Options{
		Log: testLogger(),
		TLS: edge.TLSOptions{Terminate: true, Mode: "acme", ACME: edge.ACMEOptions{StorageDir: cacheDir}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.UpdateSnapshot(&edge.Snapshot{Version: 1, Routes: []edge.Route{{
		Host: "known.hello.com", PathPrefix: "/", Backend: "1.1.1.1:80",
	}}}); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{Handler: server.Handler(), TLSConfig: server.TLSConfig()}
	go func() { _ = httpSrv.ServeTLS(ln, "", "") }()
	defer httpSrv.Close()

	_, err = tls.Dial("tcp", ln.Addr().String(), &tls.Config{ServerName: "unknown.example.org", InsecureSkipVerify: true})
	if err == nil {
		t.Fatal("expected TLS handshake to fail for a host with no route and no cached cert")
	}
}
