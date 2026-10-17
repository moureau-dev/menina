package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveListRestoreRevision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "p", "apps", "api.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(dir, "projects", "p", "apps", "new.yml")
	id, err := SaveRevision(dir, "p", "production", []string{path, created})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("empty revision id")
	}
	if err := os.WriteFile(path, []byte("after\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("new\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	list, err := ListRevisions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Files != 2 {
		t.Fatalf("revisions = %+v", list)
	}
	if err := RestoreRevision(dir, id); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before\n" {
		t.Fatalf("restored content = %q", data)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created file should have been removed: %v", err)
	}
}
