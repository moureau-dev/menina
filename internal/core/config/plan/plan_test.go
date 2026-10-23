package plan

import (
	"path/filepath"
	"testing"
)

func TestValidatePaths(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "projects", "p", "apps", "api.yml")
	bad := filepath.Join(dir, "..", "outside.yml")
	if err := ValidatePaths(dir, &Plan{Writes: []Write{{Path: good}}}); err != nil {
		t.Fatalf("good path rejected: %v", err)
	}
	if err := ValidatePaths(dir, &Plan{Writes: []Write{{Path: bad}}}); err == nil {
		t.Fatal("outside path accepted")
	}
	if err := ValidatePaths(dir, &Plan{Writes: []Write{{Path: filepath.Join(dir, "secrets", "x")}}}); err == nil {
		t.Fatal("non-desired-state path accepted")
	}
}

func TestHashDetectsTampering(t *testing.T) {
	p := &Plan{Project: "p", Environment: "production", Writes: []Write{{Path: "/tmp/a", Content: []byte("a")}}}
	p.ID = Hash(p)
	if err := VerifyBase(t.TempDir(), p); err == nil {
		t.Fatal("expected verify to fail on missing config dir")
	}
	p.Writes[0].Content = []byte("b")
	if Hash(p) == p.ID {
		t.Fatal("hash did not change after tampering")
	}
}
