package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/config"
)

func execute(args ...string) error {
	root := NewRootCommand()
	root.SetArgs(args)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	return root.Execute()
}

func TestRootRegistersAllCommands(t *testing.T) {
	root := NewRootCommand()
	want := []string{"setup", "deploy", "status", "project", "environment", "app", "routes", "registry", "secret", "doctor", "serve", "proxy", "upgrade", "version", "install"}
	for _, name := range want {
		if _, _, err := root.Find([]string{name}); err != nil {
			t.Errorf("command %q not registered: %v", name, err)
		}
	}
}

func TestSetupNonInteractiveWritesValidConfig(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "setup", "--non-interactive"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yml")); err != nil {
		t.Fatalf("config.yml not written: %v", err)
	}
	cfg, err := config.LoadPlatform(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.ValidatePlatform(cfg); err != nil {
		t.Fatalf("written config invalid: %v", err)
	}
	if cfg.Registry.Domain == "" {
		t.Fatal("expected registry domain default")
	}
}

func TestProjectAndAppCreate(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "my-platform", "--namespace", "my-platform"); err != nil {
		t.Fatalf("project create: %v", err)
	}
	if err := execute("--config-dir", dir, "environment", "create", "production", "--project", "my-platform"); err != nil {
		t.Fatalf("environment create: %v", err)
	}
	if err := execute("--config-dir", dir, "app", "create", "api", "--project", "my-platform", "--repository", "registry.moureau.dev/my-platform/api"); err != nil {
		t.Fatalf("app create: %v", err)
	}
	p, err := config.LoadProject(config.ProjectsDir(dir), "my-platform")
	if err != nil {
		t.Fatal(err)
	}
	if p.Apps["api"] == nil {
		t.Fatal("api app not created")
	}
	if p.Envs["production"] == nil {
		t.Fatal("production environment not created")
	}
}

func TestDeleteCommandsRemoveConfigFiles(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--namespace", "p"); err != nil {
		t.Fatalf("project create: %v", err)
	}
	if err := execute("--config-dir", dir, "environment", "create", "production", "--project", "p"); err != nil {
		t.Fatalf("environment create: %v", err)
	}
	if err := execute("--config-dir", dir, "app", "create", "api", "--project", "p", "--repository", "registry.example.com/p/api"); err != nil {
		t.Fatalf("app create: %v", err)
	}
	if err := execute("--config-dir", dir, "app", "delete", "api", "--project", "p", "--yes"); err != nil {
		t.Fatalf("app delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "p", "apps", "api.yml")); !os.IsNotExist(err) {
		t.Fatal("app file should be removed")
	}
	if err := execute("--config-dir", dir, "environment", "delete", "production", "--project", "p", "--yes"); err != nil {
		t.Fatalf("environment delete: %v", err)
	}
	if err := execute("--config-dir", dir, "project", "delete", "p", "--yes"); err != nil {
		t.Fatalf("project delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "p")); !os.IsNotExist(err) {
		t.Fatal("project directory should be removed")
	}
}

func TestDeleteWithoutYesIsRefusedNonInteractively(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "project", "delete", "p"); err == nil {
		t.Fatal("non-interactive delete without --yes must be refused")
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "p")); err != nil {
		t.Fatal("project must still exist after a refused delete")
	}
}

func TestBackupCommandCreatesDatabaseCopy(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	cfg := "host:\n  configDir: " + dir + "\n  dataDir: " + dataDir + "\nplatform:\n  dbPath: " + filepath.Join(dataDir, "menina.db") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(cfg), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "backup"); err != nil {
		t.Fatalf("backup: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "backups"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a backup file: entries=%v err=%v", entries, err)
	}
}

func TestAppCreateAndScaleReplicas(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--namespace", "p"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "app", "create", "api", "--project", "p", "--repository", "r/api", "--yes"); err != nil {
		t.Fatal(err)
	}
	apps, _ := config.LoadApps(config.ProjectsDir(dir), "p")
	if apps["api"].Replicas != nil {
		t.Fatalf("creating without a replica flag must inherit the default, got %+v", apps["api"].Replicas)
	}

	if err := execute("--config-dir", dir, "app", "scale", "api", "--project", "p", "--replicas", "5"); err != nil {
		t.Fatal(err)
	}
	apps, _ = config.LoadApps(config.ProjectsDir(dir), "p")
	if r := apps["api"].Replicas; r == nil || r.Min != 5 || r.Max != 5 || r.Autoscaling() {
		t.Fatalf("fixed scale = %+v", r)
	}

	if err := execute("--config-dir", dir, "app", "scale", "api", "--project", "p", "--min", "2", "--max", "8"); err != nil {
		t.Fatal(err)
	}
	apps, _ = config.LoadApps(config.ProjectsDir(dir), "p")
	if r := apps["api"].Replicas; r == nil || r.Min != 2 || r.Max != 8 || !r.Autoscaling() {
		t.Fatalf("range scale = %+v", r)
	}

	if err := execute("--config-dir", dir, "app", "scale", "api", "--project", "p", "--reset"); err != nil {
		t.Fatal(err)
	}
	apps, _ = config.LoadApps(config.ProjectsDir(dir), "p")
	if apps["api"].Replicas != nil {
		t.Fatalf("reset must remove the replica override, got %+v", apps["api"].Replicas)
	}
}

func TestVersionFlags(t *testing.T) {
	for _, flag := range []string{"--version", "-v"} {
		out, err := executeCapture(flag)
		if err != nil {
			t.Fatalf("%s: %v", flag, err)
		}
		if !strings.Contains(out, "menina") {
			t.Fatalf("%s output = %q", flag, out)
		}
	}
}

func TestCreateCommandCreatesProject(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "create", "demo", "--environments", "development,staging"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "demo", "project.yml")); err != nil {
		t.Fatalf("project.yml not written: %v", err)
	}
	for _, env := range []string{"development", "staging"} {
		if _, err := os.Stat(filepath.Join(dir, "projects", "demo", "environments", env+".yml")); err != nil {
			t.Fatalf("environment %s not written: %v", env, err)
		}
	}
}

func TestProjectCreateWithEnvironments(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--environments", "production"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "p", "environments", "production.yml")); err != nil {
		t.Fatalf("production environment not written: %v", err)
	}
}

func TestProjectCreateDefaultsToProduction(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "p", "environments", "production.yml")); err != nil {
		t.Fatalf("default production environment not written: %v", err)
	}
}

func TestProjectCreateNoneSkipsEnvironments(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--environments", "none"); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(filepath.Join(dir, "projects", "p", "environments"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("expected no environments, found %d", len(ents))
	}
}

func TestBareCommandsAreNonInteractiveWithoutTTY(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"app"}, {"project"}, {"environment"}} {
		full := append([]string{"--config-dir", dir}, args...)
		if err := execute(full...); err != nil {
			t.Fatalf("%v should fall back to help without a TTY: %v", args, err)
		}
	}
}

func TestChooseEnvironmentsSingle(t *testing.T) {
	p := &prompter{reader: bufio.NewReader(strings.NewReader("\n")), enabled: true}
	got := chooseEnvironments(p)
	if len(got) != 1 || got[0] != "production" {
		t.Fatalf("single-environment answer = %v, want [production]", got)
	}
}

func TestChooseEnvironmentsAddAndRename(t *testing.T) {
	// multi=Yes; add (default development); add (default staging);
	// rename #2 -> prod; done
	in := "1\n1\n\n1\n\n2\n2\nprod\n3\n"
	p := &prompter{reader: bufio.NewReader(strings.NewReader(in)), enabled: true}
	got := chooseEnvironments(p)
	want := []string{"development", "prod"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("environments = %v, want %v", got, want)
	}
}

func TestAppScalePerEnvironment(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(execute("--config-dir", dir, "project", "create", "p", "--environments", "production,staging"))
	must(execute("--config-dir", dir, "app", "create", "api", "--project", "p", "--repository", "r", "--yes"))
	must(execute("--config-dir", dir, "app", "scale", "api", "--project", "p", "--environment", "production", "--min", "2", "--max", "10"))
	must(execute("--config-dir", dir, "app", "scale", "api", "--project", "p", "--replicas", "4"))

	env, err := config.LoadEnvironment(dir, "p", "production")
	must(err)
	if r := env.Apps["api"].Replicas; r == nil || r.Min != 2 || r.Max != 10 {
		t.Fatalf("production override = %+v", r)
	}
	apps, err := config.LoadApps(config.ProjectsDir(dir), "p")
	must(err)
	if r := apps["api"].Replicas; r == nil || r.Min != 4 {
		t.Fatalf("baseline = %+v", r)
	}
	staging, err := config.LoadEnvironment(dir, "p", "staging")
	must(err)
	if staging.Apps["api"] != nil {
		t.Fatalf("staging should be untouched, got %+v", staging.Apps["api"])
	}
}

func TestAppScaleUnknownEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--environments", "production"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "app", "create", "api", "--project", "p", "--repository", "r", "--yes"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "app", "scale", "api", "--project", "p", "--environment", "nope", "--replicas", "2"); err == nil {
		t.Fatal("expected error for unknown environment")
	}
}

func TestResolveScaleTarget(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--environments", "production,staging"); err != nil {
		t.Fatal(err)
	}
	// Options are [baseline, production, staging]; pick staging.
	p := &prompter{reader: bufio.NewReader(strings.NewReader("3\n")), enabled: true}
	got, err := resolveScaleTarget(p, dir, "p", "", false)
	if err != nil || got != "staging" {
		t.Fatalf("target = %q, %v; want staging", got, err)
	}
	// Pick the baseline.
	p = &prompter{reader: bufio.NewReader(strings.NewReader("1\n")), enabled: true}
	if got, err := resolveScaleTarget(p, dir, "p", "", false); err != nil || got != "" {
		t.Fatalf("baseline target = %q, %v; want empty", got, err)
	}
	// Explicit environment and --baseline never prompt.
	if got, _ := resolveScaleTarget(&prompter{enabled: true}, dir, "p", "production", false); got != "production" {
		t.Fatalf("explicit target = %q", got)
	}
	if got, _ := resolveScaleTarget(&prompter{enabled: true}, dir, "p", "", true); got != "" {
		t.Fatalf("--baseline target = %q", got)
	}
}

func TestAppScaleBaselineFlag(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(execute("--config-dir", dir, "project", "create", "p", "--environments", "production,staging"))
	must(execute("--config-dir", dir, "app", "create", "api", "--project", "p", "--repository", "r", "--yes"))
	must(execute("--config-dir", dir, "app", "scale", "api", "--project", "p", "--baseline", "--replicas", "3"))
	apps, err := config.LoadApps(config.ProjectsDir(dir), "p")
	must(err)
	if r := apps["api"].Replicas; r == nil || r.Min != 3 {
		t.Fatalf("baseline = %+v", r)
	}
	for _, env := range []string{"production", "staging"} {
		e, err := config.LoadEnvironment(dir, "p", env)
		must(err)
		if e.Apps["api"] != nil {
			t.Fatalf("%s should be untouched, got %+v", env, e.Apps["api"])
		}
	}
}

func TestHelpIsDescriptive(t *testing.T) {
	root, err := executeCapture("--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(root, "Examples:") || !strings.Contains(root, "menina setup") {
		t.Fatalf("root help lacks examples:\n%s", root)
	}
	app, err := executeCapture("app", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(app, "Deploy / Scale / Edit / Logs / Rollback / Delete") {
		t.Fatalf("app help does not describe the interactive menu:\n%s", app)
	}
}

func TestAppDeleteSoftAndRestore(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--environments", "production"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "app", "create", "api", "--project", "p", "--repository", "r", "--yes"); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dir, "projects", "p", "apps", "api.yml")
	trash := filepath.Join(dir, "projects", "p", "apps", ".trash", "api.yml")

	if err := execute("--config-dir", dir, "app", "delete", "api", "--project", "p", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(live); err == nil {
		t.Fatal("soft delete must remove the live app file")
	}
	if _, err := os.Stat(trash); err != nil {
		t.Fatalf("soft delete must keep the config in the trash: %v", err)
	}
	if err := execute("--config-dir", dir, "app", "restore", "api", "--project", "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("restore must move the config back: %v", err)
	}
	if _, err := os.Stat(trash); err == nil {
		t.Fatal("restore must empty the trash")
	}
}

func TestAppDeleteHardRemovesConfig(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--environments", "production"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "app", "create", "api", "--project", "p", "--repository", "r", "--yes"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "app", "delete", "api", "--project", "p", "--hard", "--yes"); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dir, "projects", "p", "apps", "api.yml")
	trash := filepath.Join(dir, "projects", "p", "apps", ".trash", "api.yml")
	if _, err := os.Stat(live); err == nil {
		t.Fatal("hard delete must remove the config")
	}
	if _, err := os.Stat(trash); err == nil {
		t.Fatal("hard delete must not leave a trash entry")
	}
}

func TestProjectDeleteSoftAndRestore(t *testing.T) {
	dir := t.TempDir()
	if err := execute("--config-dir", dir, "project", "create", "p", "--environments", "production"); err != nil {
		t.Fatal(err)
	}
	if err := execute("--config-dir", dir, "project", "delete", "p", "--yes"); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dir, "projects", "p", "project.yml")
	trash := filepath.Join(dir, "projects", ".trash", "p")
	if _, err := os.Stat(live); err == nil {
		t.Fatal("soft delete must remove the live project")
	}
	if _, err := os.Stat(filepath.Join(trash, "project.yml")); err != nil {
		t.Fatalf("soft delete must keep the project in the trash: %v", err)
	}
	// The trashed project must not be listed.
	out, err := executeCapture("--config-dir", dir, "project", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("soft-deleted project must not be listed: %q", out)
	}
	if err := execute("--config-dir", dir, "project", "restore", "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("restore must move the project back: %v", err)
	}
}

func TestSelectorsOfferExit(t *testing.T) {
	dir := t.TempDir()
	app := &App{Opts: RootOptions{ConfigDir: dir}}
	// With no projects the menu is ["+ create a new project", "Exit"]; pick Exit.
	p := &prompter{reader: bufio.NewReader(strings.NewReader("2\n")), enabled: true}
	_, err := selectProject(app, p)
	if !errors.Is(err, errExit) {
		t.Fatalf("selecting Exit = %v, want errExit", err)
	}
}

func TestPromptInterruptExits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	setPromptContext(ctx)
	defer setPromptContext(nil)

	exited := 0
	promptExit = func(int) { exited++ }
	defer func() { promptExit = os.Exit }()

	// readLine must not block: the context is already cancelled (Ctrl+C).
	(&prompter{reader: bufio.NewReader(strings.NewReader("ignored\n")), enabled: true}).readLine()
	if exited != 1 {
		t.Fatalf("interrupted prompt exited=%d, want 1", exited)
	}
}

func TestSelectAndYesNo(t *testing.T) {
	// Choose the second option.
	p := &prompter{reader: bufio.NewReader(strings.NewReader("2\n")), enabled: true}
	if got := p.Select("pick", []Option{{Label: "a", Description: "first"}, {Label: "b"}}, 0); got != 1 {
		t.Fatalf("Select = %d, want 1", got)
	}
	// Blank takes the default.
	p = &prompter{reader: bufio.NewReader(strings.NewReader("\n")), enabled: true}
	if got := p.Select("pick", []Option{{Label: "a"}, {Label: "b"}}, 1); got != 1 {
		t.Fatalf("Select default = %d, want 1", got)
	}
	// yesNo: choosing "No" overrides a yes default.
	p = &prompter{reader: bufio.NewReader(strings.NewReader("2\n")), enabled: true}
	if p.yesNo("q", "yes desc", "no desc", true) {
		t.Fatal("yesNo chose No but returned true")
	}
	// Blank takes the default.
	p = &prompter{reader: bufio.NewReader(strings.NewReader("\n")), enabled: true}
	if !p.yesNo("q", "yes desc", "no desc", true) {
		t.Fatal("yesNo blank should default to Yes")
	}
	// y/n are accepted as aliases on a Yes/No menu.
	p = &prompter{reader: bufio.NewReader(strings.NewReader("n\n")), enabled: true}
	if p.yesNo("q", "yes desc", "no desc", true) {
		t.Fatal("yesNo 'n' should choose No")
	}
	p = &prompter{reader: bufio.NewReader(strings.NewReader("y\n")), enabled: true}
	if !p.yesNo("q", "yes desc", "no desc", false) {
		t.Fatal("yesNo 'y' should choose Yes")
	}
}

func TestDefaultImageRepository(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Host.ConfigDir = dir
	cfg.Registry.Enabled = true
	cfg.Registry.Domain = "registry.test"
	if err := config.SavePlatform(dir, &cfg); err != nil {
		t.Fatal(err)
	}
	app := &App{Opts: RootOptions{ConfigDir: dir}}
	if got := defaultImageRepository(app, "my-app", "api"); got != "registry.test/my-app/api" {
		t.Fatalf("defaultImageRepository = %q", got)
	}
	cfg.Registry.Enabled = false
	_ = config.SavePlatform(dir, &cfg)
	if got := defaultImageRepository(app, "my-app", "api"); got != "" {
		t.Fatalf("registry disabled should default to empty, got %q", got)
	}
}

func TestSelectRendersDescriptions(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	p := &prompter{reader: bufio.NewReader(strings.NewReader("1\n")), enabled: true}
	p.Select("  Choose", []Option{
		{Label: "Yes", Description: "does the thing"},
		{Label: "No", Description: "skips the thing"},
	}, 0)
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "does the thing") || !strings.Contains(string(out), "2. No") {
		t.Fatalf("select output missing labels/descriptions:\n%s", out)
	}
}

func TestCompletionGenerate(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		if err := execute("completion", shell); err != nil {
			t.Errorf("completion %s: %v", shell, err)
		}
	}
}
