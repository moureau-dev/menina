package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

func atoiOr(s string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return fallback
}

func writeYAML(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o770|os.ModeSetgid); err != nil {
		return err
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return err
	}
	return os.Chmod(path, 0o640)
}

// confirmDestructive gates destructive operations. Non-interactive callers must
// pass --yes explicitly; otherwise the operation is refused.
func confirmDestructive(app *App, prompt string, yes bool) bool {
	if yes {
		return true
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		app.Infof("%s\nrefusing in non-interactive mode; re-run with --yes to confirm", prompt)
		return false
	}
	fmt.Fprintf(os.Stdout, "%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}
