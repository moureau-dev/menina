package host

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/core/config"
)

func executeCapture(args ...string) (string, error) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	root := NewRootCommand()
	root.SetArgs(args)
	root.SetErr(io.Discard)
	runErr := root.Execute()
	_ = w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	return string(data), runErr
}

func TestSetupGeneratesRegistryCredentialAndToken(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "setup", "--non-interactive"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	cfg, err := config.LoadPlatform(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Registry.Auth.Enabled {
		t.Fatal("expected registry auth to be enabled by default")
	}
	if cfg.Registry.Auth.Username != "menina" {
		t.Fatalf("username = %q", cfg.Registry.Auth.Username)
	}
	if !strings.HasPrefix(cfg.Registry.Auth.PasswordRef, "file:") {
		t.Fatalf("password should be a file reference, got %q", cfg.Registry.Auth.PasswordRef)
	}
	password := readSecret(t, strings.TrimPrefix(cfg.Registry.Auth.PasswordRef, "file:"))
	if len(password) < 24 {
		t.Fatalf("generated password too short: %q", password)
	}
	if cfg.Registry.Notifications.TokenRef == "" {
		t.Fatal("expected a notifications token reference")
	}
	token := readSecret(t, strings.TrimPrefix(cfg.Registry.Notifications.TokenRef, "file:"))
	if len(token) < 32 {
		t.Fatalf("generated token too short")
	}
}

func TestRegistryCredentialShowAndRegenerate(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "setup", "--non-interactive"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	out, err := executeCapture("--config-dir", dir, "registry", "credential")
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	if !strings.Contains(out, "Username:  menina") || !strings.Contains(out, "Password:") {
		t.Fatalf("unexpected credential output:\n%s", out)
	}

	cfg, _ := config.LoadPlatform(dir)
	path := strings.TrimPrefix(cfg.Registry.Auth.PasswordRef, "file:")
	before := readSecret(t, path)

	if _, err := executeCapture("--config-dir", dir, "registry", "credential", "--regenerate"); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	after := readSecret(t, path)
	if before == after {
		t.Fatal("regenerate should produce a new password")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("secret file mode = %o, want 0660", info.Mode().Perm())
	}
}

func TestBuildRegistriesYAMLIncludesAuth(t *testing.T) {
	cfg := config.Default()
	cfg.Registry.Domain = "registry.example.com"
	cfg.TLS.Terminate = true
	yaml := buildRegistriesYAML(&cfg, "menina", "s3cret")
	for _, want := range []string{"mirrors:", "registry.example.com", "configs:", "username:", "password:", "s3cret"} {
		if !strings.Contains(yaml, want) {
			t.Fatalf("registries.yaml missing %q:\n%s", want, yaml)
		}
	}
	noAuth := buildRegistriesYAML(&cfg, "", "")
	if strings.Contains(noAuth, "configs:") {
		t.Fatalf("expected no auth block when credentials are empty:\n%s", noAuth)
	}
}

func readSecret(t *testing.T, path string) string {
	t.Helper()
	if !filepath.IsAbs(path) {
		t.Fatalf("secret path %q is not absolute", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
