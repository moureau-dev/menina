package provision

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestHostKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	line := ssh.MarshalAuthorizedKey(signer.PublicKey())
	if err := os.WriteFile(filepath.Join(dir, "ssh_host_ed25519_key.pub"), line, 0o600); err != nil {
		t.Fatal(err)
	}
	gotLine, fingerprint, err := HostKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gotLine != string(line) || fingerprint != ssh.FingerprintSHA256(signer.PublicKey()) {
		t.Fatalf("line=%q fingerprint=%q", gotLine, fingerprint)
	}
}
