package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestFingerprintAuthorizedKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " laptop\n"
	fp, comment, err := FingerprintAuthorizedKey(line)
	if err != nil {
		t.Fatal(err)
	}
	if fp != ssh.FingerprintSHA256(signer.PublicKey()) || comment != "laptop" {
		t.Fatalf("fingerprint=%q comment=%q", fp, comment)
	}
}
