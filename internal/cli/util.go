package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func writeYAML(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o770|os.ModeSetgid); err != nil {
		return err
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o640)
}

// confirmDestructive gates destructive operations. Non-interactive callers must
// pass --yes explicitly; otherwise the operation is refused.
func confirmDestructive(app *App, prompt string, yes bool) bool {
	if yes {
		return true
	}
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
		app.Infof("%s\nrefusing in non-interactive mode; re-run with --yes to confirm", prompt)
		return false
	}
	fmt.Fprintf(os.Stdout, "%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}
