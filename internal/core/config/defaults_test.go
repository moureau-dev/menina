package config

import "testing"

func TestDefaultHasNoHardcodedInClusterAssumptions(t *testing.T) {
	cfg := Default()
	if cfg.Platform.Namespace != "menina-system" {
		t.Fatalf("platform namespace = %q", cfg.Platform.Namespace)
	}
	if cfg.Registry.Namespace != "menina-registry" {
		t.Fatalf("registry namespace = %q", cfg.Registry.Namespace)
	}
	if cfg.Registry.PVCSize == "" {
		t.Fatal("registry PVC size should have a default")
	}
	if cfg.Registry.Notifications.Endpoint != "" {
		t.Fatalf("event endpoint should be empty by default (derived at deploy time), got %q", cfg.Registry.Notifications.Endpoint)
	}
}
