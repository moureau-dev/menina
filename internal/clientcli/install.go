package clientcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newInstallCommand(app *App) *cobra.Command {
	var dir string
	var noPath, force bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install this client and add it to PATH",
		Long: "Copy the running client to a user bin directory and add that directory to\n" +
			"your PATH. Use this when you downloaded the portable release binary\n" +
			"directly. The installers do the same thing.",
		Example: "  menina install\n" +
			"  menina install --dir /usr/local/bin --force\n" +
			"  menina install --no-path",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, pathNote, err := installSelf(dir, noPath, force)
			if err != nil {
				return err
			}
			if app.jsonOutput() {
				return app.printJSON(map[string]any{"installed": target, "path": pathNote})
			}
			app.infof("Installed %s", target)
			if pathNote != "" {
				app.infof("%s", pathNote)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "install directory (default: user bin directory)")
	cmd.Flags().BoolVar(&noPath, "no-path", false, "do not modify PATH")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing non-client binary")
	return cmd
}

func installSelf(dir string, noPath, force bool) (string, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	if dir == "" {
		dir = defaultInstallDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("create %s: %w", dir, err)
	}

	name := installBinaryName()
	target := filepath.Join(dir, name)
	same, err := sameFile(exe, target)
	if err != nil {
		return "", "", err
	}
	if same {
		note, _ := updatePath(dir, noPath)
		return target, note, nil
	}
	if fileExists(target) && !force && !isClientBinary(target) {
		return "", "", fmt.Errorf("%s already exists and is not a Menina client (use --force)", target)
	}
	if err := copySelf(exe, target); err != nil {
		return "", "", err
	}
	note, err := updatePath(dir, noPath)
	if err != nil {
		return target, "", err
	}
	return target, note, nil
}

func defaultInstallDir() string {
	if dir := strings.TrimSpace(os.Getenv("MENINA_CLIENT_DIR")); dir != "" {
		return dir
	}
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "Menina", "bin")
		}
	}
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		return "/usr/local/bin"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".menina", "bin")
	}
	return filepath.Join(home, ".local", "bin")
}

func installBinaryName() string {
	base := "menina"
	if runtime.GOOS == "windows" {
		base += ".exe"
	}
	if name := strings.TrimSpace(os.Getenv("MENINA_CLIENT_BINARY_NAME")); name != "" {
		return name
	}
	existing, err := exec.LookPath(base)
	if err != nil || isClientBinary(existing) {
		return base
	}
	if runtime.GOOS == "windows" {
		return "menina-client.exe"
	}
	return "menina-client"
}

func isClientBinary(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--help").CombinedOutput()
	return err == nil && strings.Contains(string(out), "Menina client")
}

func sameFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

func copySelf(src, target string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".menina-install-*")
	if err != nil {
		return fmt.Errorf("stage install in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}

	if runtime.GOOS == "windows" && fileExists(target) {
		backup := target + ".old"
		_ = os.Remove(backup)
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("move existing binary aside: %w", err)
		}
		if err := os.Rename(tmpName, target); err != nil {
			_ = os.Rename(backup, target)
			return fmt.Errorf("install binary: %w", err)
		}
		_ = os.Remove(backup)
		return nil
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	return nil
}

func updatePath(dir string, noPath bool) (string, error) {
	if noPath {
		return fmt.Sprintf("PATH not modified; add %s to PATH", dir), nil
	}
	if runtime.GOOS == "windows" {
		if err := ensureWindowsPath(dir); err != nil {
			return fmt.Sprintf("add %s to your PATH manually (%v)", dir, err), nil
		}
		return fmt.Sprintf("Added %s to the user PATH; restart your shell", dir), nil
	}
	rc, err := ensureUnixPath(dir)
	if err != nil {
		return fmt.Sprintf("add %s to your PATH manually (%v)", dir, err), nil
	}
	if rc == "" {
		return fmt.Sprintf("%s is already in PATH", dir), nil
	}
	return fmt.Sprintf("Added %s to PATH in %s; restart your shell", dir, rc), nil
}

func pathContains(path, dir string) bool {
	for _, entry := range filepath.SplitList(path) {
		if entry == dir {
			return true
		}
	}
	return false
}

func ensureUnixPath(dir string) (string, error) {
	if pathContains(os.Getenv("PATH"), dir) {
		return "", nil
	}
	shell := filepath.Base(os.Getenv("SHELL"))
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var rc string
	fish := false
	switch shell {
	case "zsh":
		rc = filepath.Join(home, ".zshrc")
	case "bash":
		if runtime.GOOS == "darwin" {
			rc = filepath.Join(home, ".bash_profile")
		} else {
			rc = filepath.Join(home, ".bashrc")
		}
	case "fish":
		rc = filepath.Join(home, ".config", "fish", "config.fish")
		fish = true
	default:
		return "", fmt.Errorf("unknown shell %q", shell)
	}
	if data, err := os.ReadFile(rc); err == nil && strings.Contains(string(data), "menina-client") {
		os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+dir)
		return rc, nil
	}
	if err := os.MkdirAll(filepath.Dir(rc), 0o755); err != nil {
		return "", err
	}
	var block string
	if fish {
		block = fmt.Sprintf("\n# >>> menina-client >>>\nfish_add_path %q\n# <<< menina-client <<<\n", dir)
	} else {
		block = fmt.Sprintf("\n# >>> menina-client >>>\nexport PATH=%q:$PATH\n# <<< menina-client <<<\n", dir)
	}
	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return "", err
	}
	os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+dir)
	return rc, nil
}

func ensureWindowsPath(dir string) error {
	quoted := "'" + strings.ReplaceAll(dir, "'", "''") + "'"
	script := fmt.Sprintf(
		`$dir = %s; $p = [Environment]::GetEnvironmentVariable("Path","User"); $parts = @(); if ($p) { $parts = $p -split ';' | Where-Object { $_ } }; if ($parts -notcontains $dir) { [Environment]::SetEnvironmentVariable("Path", ((@($parts) + $dir) -join ';'), "User") }`,
		quoted,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+dir)
	return nil
}

func clientInstalled() bool {
	for _, name := range []string{"menina", "menina-client"} {
		if path, err := exec.LookPath(name); err == nil && isClientBinary(path) {
			return true
		}
	}
	return false
}
