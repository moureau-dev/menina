package clientcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/manifest"
)

func writeTestManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".menina.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestManifestOptionsOverrideProjectEnvironmentAndVars(t *testing.T) {
	path := writeTestManifest(t, `project: ${PROJECT}
environment: ${ENVIRONMENT}
vars:
  PROJECT: demo
  ENVIRONMENT: production
  DOMAIN: example.com
apps:
  web:
    dockerfile: Dockerfile
    port: 8080
    host: web.${DOMAIN}
`)
	o := &manifestOptions{
		File:        path,
		Project:     "demo-staging",
		Environment: "staging",
		Vars:        []string{"DOMAIN=staging.example.com"},
	}
	f, err := o.load(&cobra.Command{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Project != "demo-staging" || f.Environment != "staging" {
		t.Fatalf("project/environment = %q/%q", f.Project, f.Environment)
	}
	if got := f.Apps["web"].Host; got != "web.staging.example.com" {
		t.Fatalf("host = %q", got)
	}
}

func TestManifestOptionsRejectsInvalidVar(t *testing.T) {
	path := writeTestManifest(t, "project: demo\nenvironment: production\n")
	o := &manifestOptions{File: path, Vars: []string{"NOPE"}}
	if _, err := o.load(&cobra.Command{}); err == nil {
		t.Fatal("expected invalid --var to fail")
	}
}

func TestParseBuildArgs(t *testing.T) {
	got, err := parseBuildArgs([]string{"A=1", "B=two=three", "EMPTY="})
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "1" || got["B"] != "two=three" || got["EMPTY"] != "" {
		t.Fatalf("build args = %#v", got)
	}
	if _, err := parseBuildArgs([]string{"NOPE"}); err == nil {
		t.Fatal("expected missing = to fail")
	}
	flags := buildArgFlags(map[string]string{"B": "2", "A": "1"}, map[string]string{"B": "override"})
	if strings.Join(flags, ",") != "A=1,B=override" {
		t.Fatalf("flags = %#v", flags)
	}
}

func TestApplyPlanDiffAndVerify(t *testing.T) {
	dir := t.TempDir()
	doc := `project: demo
environment: production
apps:
  web:
    dockerfile: Dockerfile
    service: { port: 8080 }
`
	f, err := manifest.Load([]byte(doc), ".")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Render(manifest.RenderOptions{ConfigDir: dir, Registry: "registry.test"})
	if err != nil {
		t.Fatal(err)
	}
	changes, err := planApply(dir, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 || changes[0].Action != "create" {
		t.Fatalf("initial plan = %+v", changes)
	}
	if err := r.Write(dir); err != nil {
		t.Fatal(err)
	}
	changes, err = planApply(dir, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected no changes after write, got %+v", changes)
	}
	if err := verifyApply(dir, r); err != nil {
		t.Fatalf("verify: %v", err)
	}
}
