package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPublicKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id.pub")
	if err := os.WriteFile(path, []byte("ssh-ed25519 AAAA test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := readPublicKey(path)
	if err != nil || key != "ssh-ed25519 AAAA test" {
		t.Fatalf("key=%q err=%v", key, err)
	}
}

func TestSanitizeContextName(t *testing.T) {
	if got := sanitizeContextName("deploy@vps.example.com:2222"); got != "deploy-vps-example-com-2222" {
		t.Fatalf("name = %q", got)
	}
}
