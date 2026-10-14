package config

import "testing"

func testConfig() *Config {
	cfg := Default()
	cfg.Defaults.ImageTagStrategy = "environment"
	cfg.Defaults.Replicas = 1
	cfg.Defaults.CPU = "250m"
	cfg.Defaults.Memory = "256Mi"
	return &cfg
}

func testProject() *Project {
	return &Project{
		Name:      "my-platform",
		Namespace: "my-platform",
		Apps: map[string]*AppFile{
			"api": {
				Name:      "api",
				Image:     ImageSpec{Repository: "registry.moureau.dev/my-platform/api"},
				Workload:  WorkloadSpec{Type: WorkloadService, Port: 8080},
				Resources: ResourceSpec{CPU: "500m", Memory: "512Mi"},
				Exposure:  ExposureSpec{Enabled: false},
			},
			"worker": {
				Name:     "worker",
				Image:    ImageSpec{Repository: "registry.moureau.dev/my-platform/worker"},
				Workload: WorkloadSpec{Type: WorkloadWorker},
			},
		},
		EnvOrder: []string{"development", "production"},
		Envs: map[string]*EnvironmentFile{
			"development": {Name: "development", Apps: map[string]*AppOverride{}},
			"production": {
				Name: "production",
				Apps: map[string]*AppOverride{
					"api": {
						Enabled:  boolPtr(true),
						Exposure: &ExposureSpec{Enabled: true, Hosts: []string{"api.hello.com"}},
					},
				},
			},
		},
	}
}

func boolPtr(b bool) *bool { return &b }

func TestResolveIsolatesEnvironmentsByNamespace(t *testing.T) {
	r := NewResolver(testConfig())
	rc, err := r.Resolve([]*Project{testProject()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := rc.FindApp("my-platform", "development", "api")
	prod, _ := rc.FindApp("my-platform", "production", "api")
	if dev.Namespace != "my-platform-development" || prod.Namespace != "my-platform-production" {
		t.Fatalf("namespaces: dev=%q prod=%q", dev.Namespace, prod.Namespace)
	}
	if dev.Namespace == prod.Namespace {
		t.Fatal("environments must not share a namespace (deleting one would affect the other)")
	}
}

func TestResolveEnvironmentNamespaceOverride(t *testing.T) {
	p := testProject()
	p.Envs["production"].Namespace = "custom-prod"
	r := NewResolver(testConfig())
	rc, err := r.Resolve([]*Project{p}, 1)
	if err != nil {
		t.Fatal(err)
	}
	prod, _ := rc.FindApp("my-platform", "production", "api")
	if prod.Namespace != "custom-prod" {
		t.Fatalf("namespace = %q, want custom-prod", prod.Namespace)
	}
}

func TestResolveDeployWebhookInheritanceAndOverride(t *testing.T) {
	cfg := testConfig()
	cfg.Webhooks.Deploy = DeployWebhook{Enabled: true, URLRef: "env:GLOBAL", Events: []string{"deploy.succeeded"}}
	p := testProject()
	rc, err := NewResolver(cfg).Resolve([]*Project{p}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if wh := rc.Projects["my-platform"].DeployWebhook; wh == nil || wh.URLRef != "env:GLOBAL" {
		t.Fatalf("expected the platform webhook to apply: %+v", wh)
	}

	p.Webhooks.Deploy = &DeployWebhook{URLRef: "file:/x", SecretRef: "file:/s"}
	rc, err = NewResolver(cfg).Resolve([]*Project{p}, 2)
	if err != nil {
		t.Fatal(err)
	}
	wh := rc.Projects["my-platform"].DeployWebhook
	if wh == nil || wh.URLRef != "file:/x" || !wh.Enabled || wh.SecretRef != "file:/s" {
		t.Fatalf("project override not applied: %+v", wh)
	}
	if len(wh.Events) == 0 {
		t.Fatalf("expected events to be populated: %+v", wh)
	}
	app, err := rc.FindApp("my-platform", "production", "api")
	if err != nil {
		t.Fatal(err)
	}
	if app.Webhook == nil || app.Webhook.URLRef != "file:/x" {
		t.Fatalf("app did not inherit the project webhook: %+v", app.Webhook)
	}
}

func TestResolveDeployWebhookDisabledWithoutURL(t *testing.T) {
	cfg := testConfig()
	cfg.Webhooks.Deploy = DeployWebhook{Enabled: true}
	rc, err := NewResolver(cfg).Resolve([]*Project{testProject()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if wh := rc.Projects["my-platform"].DeployWebhook; wh == nil || wh.Enabled {
		t.Fatalf("webhook without a URL must be disabled: %+v", wh)
	}
}

func TestResolveMergesEnvironmentOverrides(t *testing.T) {
	r := NewResolver(testConfig())
	rc, err := r.Resolve([]*Project{testProject()}, 7)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rc.Version != 7 {
		t.Fatalf("version = %d, want 7", rc.Version)
	}
	prod, err := rc.FindApp("my-platform", "production", "api")
	if err != nil {
		t.Fatal(err)
	}
	if prod.Tag != "production" {
		t.Errorf("tag = %q, want production", prod.Tag)
	}
	if !prod.Exposure.Enabled || len(prod.Exposure.Hosts) != 1 || prod.Exposure.Hosts[0] != "api.hello.com" {
		t.Errorf("unexpected exposure: %+v", prod.Exposure)
	}
	if prod.Resources.CPU != "500m" || prod.Resources.Memory != "512Mi" {
		t.Errorf("resources = %+v", prod.Resources)
	}
	if prod.Image() != "registry.moureau.dev/my-platform/api:production" {
		t.Errorf("image = %q", prod.Image())
	}
}

func TestExposureRequiresEnvironmentOptIn(t *testing.T) {
	r := NewResolver(testConfig())
	rc, err := r.Resolve([]*Project{testProject()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := rc.FindApp("my-platform", "development", "api")
	if err != nil {
		t.Fatal(err)
	}
	if dev.Exposure.Enabled {
		t.Fatalf("development exposure should stay disabled without explicit opt-in")
	}
	if dev.Tag != "development" {
		t.Fatalf("tag = %q, want development", dev.Tag)
	}
}

func TestRepositoryOverrideRejected(t *testing.T) {
	p := testProject()
	p.Envs["production"].Apps["api"].Image = &ImageSpec{Repository: "registry.other/evil"}
	r := NewResolver(testConfig())
	if _, err := r.Resolve([]*Project{p}, 1); err == nil {
		t.Fatal("expected repository override to be rejected")
	}
}

func TestResourcesFieldWiseMerge(t *testing.T) {
	p := testProject()
	p.Envs["production"].Apps["api"].Resources = &ResourceSpec{Memory: "1Gi"}
	r := NewResolver(testConfig())
	rc, err := r.Resolve([]*Project{p}, 1)
	if err != nil {
		t.Fatal(err)
	}
	app, _ := rc.FindApp("my-platform", "production", "api")
	if app.Resources.CPU != "500m" || app.Resources.Memory != "1Gi" {
		t.Fatalf("resources = %+v, want cpu=500m memory=1Gi", app.Resources)
	}
}

func TestUnknownAppInEnvironmentRejected(t *testing.T) {
	p := testProject()
	p.Envs["production"].Apps["ghost"] = &AppOverride{}
	r := NewResolver(testConfig())
	if _, err := r.Resolve([]*Project{p}, 1); err == nil {
		t.Fatal("expected unknown app reference to be rejected")
	}
}

func TestWorkerHasNoExposure(t *testing.T) {
	r := NewResolver(testConfig())
	rc, err := r.Resolve([]*Project{testProject()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := rc.FindApp("my-platform", "production", "worker")
	if w.Workload.Type != WorkloadWorker || w.Exposure.Enabled {
		t.Fatalf("unexpected worker: %+v", w)
	}
}
