package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadVerifiedAcceptsMatchingChecksum(t *testing.T) {
	payload := []byte("binary-contents")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	path, err := downloadVerified(context.Background(), srv.URL, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("downloadVerified: %v", err)
	}
	defer os.Remove(path)
	got, _ := os.ReadFile(path)
	if string(got) != string(payload) {
		t.Fatalf("downloaded content mismatch")
	}
}

func TestDownloadVerifiedRejectsMismatchedChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer srv.Close()

	if _, err := downloadVerified(context.Background(), srv.URL, "deadbeef"); err == nil {
		t.Fatal("downloadVerified must reject a checksum mismatch")
	}
}

func TestReplaceBinaryStagesAtomicallyAndBacksUp(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	src := filepath.Join(srcDir, "menina")
	if err := os.WriteFile(src, []byte("new-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dstDir, "menina")
	if err := os.WriteFile(dst, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(src, dst); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "new-binary" {
		t.Fatalf("destination = %q", got)
	}
	info, err := os.Stat(dst)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v err=%v", info.Mode().Perm(), err)
	}
	backup, _ := os.ReadFile(dst + ".bak")
	if string(backup) != "old-binary" {
		t.Fatalf("backup = %q", backup)
	}
	// No stray temp files left in the destination directory.
	entries, _ := os.ReadDir(dstDir)
	for _, e := range entries {
		if e.Name() != "menina" && e.Name() != "menina.bak" {
			t.Fatalf("unexpected leftover file: %s", e.Name())
		}
	}
}
