package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanSoftDeletes(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureProjectDirs(dir, "p"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ProjectDir(dir, "p"), "project.yml"), "name: p\n")
	writeFile(t, filepath.Join(ProjectDir(dir, "p"), "apps", "api.yml"), "name: api\n")
	writeFile(t, filepath.Join(ProjectDir(dir, "p"), "environments", "production.yml"), "name: production\n")

	if err := EnsureProjectDirs(dir, "gone"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ProjectDir(dir, "gone"), "project.yml"), "name: gone\n")

	move := func(src, dst string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(src, dst); err != nil {
			t.Fatal(err)
		}
	}
	move(filepath.Join(ProjectDir(dir, "p"), "apps", "api.yml"), SoftDeleteAppPath(dir, "p", "api"))
	move(filepath.Join(ProjectDir(dir, "p"), "environments", "production.yml"), SoftDeleteEnvironmentPath(dir, "p", "production"))
	move(ProjectDir(dir, "gone"), SoftDeleteProjectPath(dir, "gone"))

	found, err := ScanSoftDeletes(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range found {
		got[s.Kind+"|"+s.Project+"|"+s.Environment+"|"+s.App] = true
	}
	for _, want := range []string{
		SoftDeleteApp + "|p||api",
		SoftDeleteEnvironment + "|p|production|",
		SoftDeleteProject + "|gone||",
	} {
		if !got[want] {
			t.Fatalf("missing soft delete %q in %+v", want, found)
		}
	}

	// A live app must not be reported.
	writeFile(t, filepath.Join(ProjectDir(dir, "p"), "apps", "web.yml"), "name: web\n")
	found, _ = ScanSoftDeletes(dir)
	for _, s := range found {
		if s.Kind == SoftDeleteApp && s.App == "web" {
			t.Fatalf("live app web reported as soft-deleted: %+v", s)
		}
	}
}
