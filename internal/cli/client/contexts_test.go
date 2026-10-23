package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContextResolutionPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("MENINA_PROFILE", "staging")
	if err := (&App{}).addContext("production", Context{Host: "deploy@prod", HostKey: "ssh-ed25519 KEY"}); err != nil {
		t.Fatal(err)
	}
	file, err := loadContexts()
	if err != nil {
		t.Fatal(err)
	}
	file.Current = "production"
	if err := saveContexts(file); err != nil {
		t.Fatal(err)
	}

	profile := "production"
	app := &App{opts: Options{Profile: &profile}}
	host, name, key := app.resolveHost("", "")
	if host != "deploy@prod" || name != "production" || key != "ssh-ed25519 KEY" {
		t.Fatalf("resolved host=%q name=%q key=%q", host, name, key)
	}
	if host, _, _ := app.resolveHost("flag@host", ""); host != "flag@host" {
		t.Fatalf("flag host = %q", host)
	}
	t.Setenv("MENINA_HOST", "env@host")
	if host, _, _ := app.resolveHost("", ""); host != "env@host" {
		t.Fatalf("env host = %q", host)
	}
	t.Setenv("MENINA_PROFILE", "")
	t.Setenv("MENINA_HOST", "")
	empty := ""
	app = &App{opts: Options{Profile: &empty}}
	if host, name, _ := app.resolveHost("", ""); host != "deploy@prod" || name != "production" {
		t.Fatalf("current context host=%q name=%q", host, name)
	}
}

func TestSSHPinArgs(t *testing.T) {
	args, cleanup := sshPinArgs("deploy@example.com:2222", "ssh-ed25519 KEY")
	defer cleanup()
	if len(args) != 4 {
		t.Fatalf("args = %v", args)
	}
	path := ""
	for _, arg := range args {
		if len(arg) > len("UserKnownHostsFile=") && arg[:len("UserKnownHostsFile=")] == "UserKnownHostsFile=" {
			path = arg[len("UserKnownHostsFile="):]
		}
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[example.com]:2222 ssh-ed25519 KEY\n" {
		t.Fatalf("known_hosts = %q", data)
	}
}
