package host

import (
	"testing"

	"github.com/moureau-dev/menina/internal/core/config"
)

func TestRequireProdConfirmation(t *testing.T) {
	cfg := config.Default()
	if err := requireProdConfirmation(&cfg, "production", false); err == nil {
		t.Fatal("production without --yes must be rejected when enforced")
	}
	if err := requireProdConfirmation(&cfg, "production", true); err != nil {
		t.Fatalf("production with --yes should pass: %v", err)
	}
	if err := requireProdConfirmation(&cfg, "development", false); err != nil {
		t.Fatalf("non-production should pass: %v", err)
	}
	cfg.Security.EnforceProdPolicy = false
	if err := requireProdConfirmation(&cfg, "production", false); err != nil {
		t.Fatalf("disabled policy should allow: %v", err)
	}
}
