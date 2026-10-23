package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockAcquireCheckRelease(t *testing.T) {
	dir := t.TempDir()
	l, err := AcquireLock(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Check(); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(dir, 50*time.Millisecond); err == nil {
		t.Fatal("second acquire should fail while held")
	}
	// Simulate a fencing takeover.
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(`{"token":"other"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := l.Check(); err == nil {
		t.Fatal("Check should fail after takeover")
	}
	l.Release()
}
