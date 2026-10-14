package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	t.Setenv("MENINA_TEST_SECRET", "s3cr3t")
	r := NewResolver()

	v, err := r.Resolve("env:MENINA_TEST_SECRET")
	if err != nil || v != "s3cr3t" {
		t.Fatalf("env resolve = %q err=%v", v, err)
	}

	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err = r.Resolve("file:" + path)
	if err != nil || v != "file-secret" {
		t.Fatalf("file resolve = %q err=%v", v, err)
	}

	v, err = r.Resolve("literal:plain")
	if err != nil || v != "plain" {
		t.Fatalf("literal resolve = %q err=%v", v, err)
	}

	if _, err := r.Resolve("env:MENINA_DOES_NOT_EXIST"); err == nil {
		t.Fatal("expected missing env error")
	}
	if _, err := r.Resolve("keyring:foo"); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

func TestRedact(t *testing.T) {
	if got := Redact("supersecretvalue"); got == "supersecretvalue" || len(got) != len("supersecretvalue") {
		t.Fatalf("redact = %q", got)
	}
	if got := Redact(""); got != "" {
		t.Fatalf("redact empty = %q", got)
	}
}

func TestWriteSecretFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "token")
	if err := WriteSecretFile(path, "value"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("mode = %o, want 0660", info.Mode().Perm())
	}
}
