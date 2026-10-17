package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func executeIn(input string, args ...string) error {
	root := NewRootCommand()
	root.SetArgs(args)
	root.SetIn(strings.NewReader(input))
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	return root.Execute()
}

func seedSecretProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--environments", "production"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSecretSetWritesFileAndPrintsRef(t *testing.T) {
	dir := seedSecretProject(t)
	err := executeIn("s3cr3t-value\n", "--config-dir", dir, "secret", "set", "DATABASE_URL", "--project", "p", "--environment", "production")
	if err != nil {
		t.Fatalf("secret set: %v", err)
	}
	path := filepath.Join(dir, "secrets", "p", "production", "DATABASE_URL")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("secret file not written: %v", err)
	}
	if string(data) != "s3cr3t-value" {
		t.Fatalf("secret value = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("secret file mode = %o, want 660", info.Mode().Perm())
	}
}

func TestSecretSetFromFile(t *testing.T) {
	dir := seedSecretProject(t)
	src := filepath.Join(t.TempDir(), "value")
	if err := os.WriteFile(src, []byte("file-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := executeIn("ignored", "--config-dir", dir, "secret", "set", "API_KEY", "--project", "p", "--environment", "production", "--file", src); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "secrets", "p", "production", "API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "file-value" {
		t.Fatalf("value = %q, want file-value", data)
	}
}

func TestSecretSetRejectsInvalidName(t *testing.T) {
	dir := seedSecretProject(t)
	if err := executeIn("v\n", "--config-dir", dir, "secret", "set", "bad name", "--project", "p", "--environment", "production"); err == nil {
		t.Fatal("expected an error for an invalid secret name")
	}
}

func TestSecretSetRejectsEmptyValue(t *testing.T) {
	dir := seedSecretProject(t)
	if err := executeIn("\n", "--config-dir", dir, "secret", "set", "EMPTY", "--project", "p", "--environment", "production"); err == nil {
		t.Fatal("expected an error for an empty secret value")
	}
}

func TestSecretSetRejectsUnknownProject(t *testing.T) {
	dir := t.TempDir()
	if err := executeIn("v\n", "--config-dir", dir, "secret", "set", "K", "--project", "nope", "--environment", "production"); err == nil {
		t.Fatal("expected an error for an unknown project")
	}
}

func TestSecretListAndRemove(t *testing.T) {
	dir := seedSecretProject(t)
	for _, in := range []struct{ name, val string }{{"A", "1"}, {"B", "2"}} {
		if err := executeIn(in.val+"\n", "--config-dir", dir, "secret", "set", in.name, "--project", "p", "--environment", "production"); err != nil {
			t.Fatal(err)
		}
	}
	if err := execute("--config-dir", dir, "secret", "remove", "A", "--project", "p", "--environment", "production", "--yes"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "secrets", "p", "production", "A")); !os.IsNotExist(err) {
		t.Fatalf("secret A should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "secrets", "p", "production", "B")); err != nil {
		t.Fatalf("secret B should remain: %v", err)
	}
}

func TestSecretRemoveRefusesWithoutYes(t *testing.T) {
	dir := seedSecretProject(t)
	if err := executeIn("1\n", "--config-dir", dir, "secret", "set", "A", "--project", "p", "--environment", "production"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "secret", "remove", "A", "--project", "p", "--environment", "production"); err == nil {
		t.Fatal("expected removal to be refused without --yes in non-interactive mode")
	}
}

func TestSecretVerify(t *testing.T) {
	dir := seedSecretProject(t)
	if err := executeIn("value\n", "--config-dir", dir, "secret", "set", "DATABASE_URL", "--project", "p", "--environment", "production"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "secret", "verify", "DATABASE_URL", "--project", "p", "--environment", "production"); err != nil {
		t.Fatalf("verify existing secret: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "secrets", "p", "production", "DATABASE_URL")); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "secret", "verify", "DATABASE_URL", "--project", "p", "--environment", "production"); err == nil {
		t.Fatal("verify should fail for a missing secret")
	}
}

func TestSecretRotate(t *testing.T) {
	dir := seedSecretProject(t)
	if err := executeIn("first\n", "--config-dir", dir, "secret", "set", "DATABASE_URL", "--project", "p", "--environment", "production"); err != nil {
		t.Fatal(err)
	}
	if err := executeIn("", "--config-dir", dir, "secret", "rotate", "DATABASE_URL", "--project", "p", "--environment", "production", "--generate"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "secrets", "p", "production", "DATABASE_URL")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "first" || len(data) == 0 {
		t.Fatalf("secret was not rotated: %q", data)
	}
}
