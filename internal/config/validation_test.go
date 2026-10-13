package config

import "testing"

func TestValidatePlatformRejectsCertManagerTermination(t *testing.T) {
	cfg := Default()
	cfg.Host.ConfigDir = "/etc/menina"
	cfg.Host.DataDir = "/var/lib/menina"
	cfg.TLS.Terminate = true
	cfg.TLS.Mode = TLSModeCertManager
	if err := ValidatePlatform(&cfg); err == nil {
		t.Fatal("expected cert-manager + host termination to be rejected")
	}
}

func TestValidateResolvedRejectsDuplicateHosts(t *testing.T) {
	rc := &ResolvedConfig{
		Projects: map[string]*ResolvedProject{
			"a": {
				Environments: map[string]*ResolvedEnvironment{
					"prod": {
						Apps: map[string]*ResolvedApp{
							"one": {Project: "a", Environment: "prod", Name: "one", Enabled: true, Repository: "r/one", Tag: "prod", Workload: WorkloadSpec{Type: WorkloadService, Port: 80}, Exposure: ExposureSpec{Enabled: true, Hosts: []string{"hello.com"}}},
							"two": {Project: "a", Environment: "prod", Name: "two", Enabled: true, Repository: "r/two", Tag: "prod", Workload: WorkloadSpec{Type: WorkloadService, Port: 80}, Exposure: ExposureSpec{Enabled: true, Hosts: []string{"hello.com"}}},
						},
					},
				},
			},
		},
	}
	if err := ValidateResolved(rc); err == nil {
		t.Fatal("expected duplicate host validation error")
	}
}

func TestValidateResolvedRejectsInvalidHostname(t *testing.T) {
	rc := &ResolvedConfig{
		Projects: map[string]*ResolvedProject{
			"a": {
				Environments: map[string]*ResolvedEnvironment{
					"prod": {
						Apps: map[string]*ResolvedApp{
							"one": {Project: "a", Environment: "prod", Name: "one", Enabled: true, Repository: "r/one", Tag: "prod", Workload: WorkloadSpec{Type: WorkloadService, Port: 80}, Exposure: ExposureSpec{Enabled: true, Hosts: []string{"not a host"}}},
						},
					},
				},
			},
		},
	}
	if err := ValidateResolved(rc); err == nil {
		t.Fatal("expected invalid hostname error")
	}
}

func TestValidateResolvedRejectsWildcardHost(t *testing.T) {
	rc := &ResolvedConfig{
		Projects: map[string]*ResolvedProject{
			"a": {
				Environments: map[string]*ResolvedEnvironment{
					"prod": {
						Apps: map[string]*ResolvedApp{
							"one": {Project: "a", Environment: "prod", Name: "one", Enabled: true, Repository: "r/one", Tag: "prod", Workload: WorkloadSpec{Type: WorkloadService, Port: 80}, Exposure: ExposureSpec{Enabled: true, Hosts: []string{"*.example.com"}}},
						},
					},
				},
			},
		},
	}
	if err := ValidateResolved(rc); err == nil {
		t.Fatal("expected wildcard host to be rejected with a clear error")
	}
}

func TestValidateResolvedServiceRequiresPort(t *testing.T) {
	rc := &ResolvedConfig{
		Projects: map[string]*ResolvedProject{
			"a": {
				Environments: map[string]*ResolvedEnvironment{
					"prod": {Apps: map[string]*ResolvedApp{
						"one": {Project: "a", Environment: "prod", Name: "one", Enabled: true, Repository: "r/one", Tag: "prod", Workload: WorkloadSpec{Type: WorkloadService}},
					}},
				},
			},
		},
	}
	if err := ValidateResolved(rc); err == nil {
		t.Fatal("expected service-port validation error")
	}
}
