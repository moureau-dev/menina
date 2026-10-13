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
