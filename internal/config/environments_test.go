package config

import (
	"os"
	"path/filepath"
	"testing"
)

func seedEnvDirProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "projects", "my-platform", "project.yml"), "name: my-platform\n")
	writeFile(t, filepath.Join(dir, "projects", "my-platform", "apps", "api.yml"), `name: api
image:
  repository: registry.moureau.dev/my-platform/api
workload:
  type: service
  port: 8080
`)
	writeFile(t, filepath.Join(dir, "projects", "my-platform", "environments", "production.yml"), "name: production\n")
	return dir
}

func TestLoadEnvironmentOverridesFromDirectory(t *testing.T) {
	dir := seedEnvDirProject(t)
	writeFile(t, filepath.Join(dir, "projects", "my-platform", "environments", "production", "api.yml"), `enabled: true
exposure:
  enabled: true
  hosts:
    - api.hello.com
`)
	projects, err := LoadAllProjects(ProjectsDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := NewResolver(&Config{Defaults: Default().Defaults}).Resolve(projects, 0)
	if err != nil {
		t.Fatal(err)
	}
	app, err := rc.FindApp("my-platform", "production", "api")
	if err != nil {
		t.Fatal(err)
	}
	if !app.Enabled || !app.Exposure.Enabled || len(app.Exposure.Hosts) != 1 || app.Exposure.Hosts[0] != "api.hello.com" {
		t.Fatalf("app = %+v", app)
	}
}

func TestLoadEnvironmentFromDirectoryWithoutMetaFile(t *testing.T) {
	dir := seedEnvDirProject(t)
	if err := os.Remove(filepath.Join(dir, "projects", "my-platform", "environments", "production.yml")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "projects", "my-platform", "environments", "staging", "api.yml"), "enabled: true\n")
	envs, order, err := LoadEnvironments(ProjectsDir(dir), "my-platform")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := envs["staging"]; !ok {
		t.Fatalf("staging environment missing: order=%v", order)
	}
	if envs["staging"].Apps["api"] == nil {
		t.Fatalf("api override missing: %+v", envs["staging"].Apps)
	}
}

func TestSaveAppOverrideWritesPerAppFile(t *testing.T) {
	dir := seedEnvDirProject(t)
	ov := &AppOverride{Enabled: boolPtr(true), Replicas: &ReplicasSpec{Min: 4, Max: 4}}
	if err := SaveAppOverride(dir, "my-platform", "production", "api", ov); err != nil {
		t.Fatal(err)
	}
	// The environment metadata file must not be rewritten.
	data, err := os.ReadFile(filepath.Join(dir, "projects", "my-platform", "environments", "production.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "name: production\n" {
		t.Fatalf("metadata file was modified: %q", data)
	}
	env, err := LoadEnvironment(dir, "my-platform", "production")
	if err != nil {
		t.Fatal(err)
	}
	if env.Apps["api"] == nil || env.Apps["api"].Replicas == nil || env.Apps["api"].Replicas.Min != 4 {
		t.Fatalf("override = %+v", env.Apps["api"])
	}
}

func TestDirectoryOverrideWinsOverInline(t *testing.T) {
	dir := seedEnvDirProject(t)
	writeFile(t, filepath.Join(dir, "projects", "my-platform", "environments", "production.yml"), `name: production
apps:
  api:
    enabled: false
`)
	writeFile(t, filepath.Join(dir, "projects", "my-platform", "environments", "production", "api.yml"), "enabled: true\n")
	env, err := LoadEnvironment(dir, "my-platform", "production")
	if err != nil {
		t.Fatal(err)
	}
	if env.Apps["api"] == nil || env.Apps["api"].Enabled == nil || !*env.Apps["api"].Enabled {
		t.Fatalf("override = %+v", env.Apps["api"])
	}
}

func TestLoadEnvironmentsSkipsTrashDir(t *testing.T) {
	dir := seedEnvDirProject(t)
	writeFile(t, filepath.Join(dir, "projects", "my-platform", "environments", ".trash", "old.yml"), "name: old\n")
	envs, _, err := LoadEnvironments(ProjectsDir(dir), "my-platform")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := envs[".trash"]; ok {
		t.Fatal(".trash must not be loaded as an environment")
	}
}
