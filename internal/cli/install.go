package cli

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newInstallCommand(app *App) *cobra.Command {
	var system bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install shell completion for bash, zsh and fish",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			targets := map[string]string{
				"bash": "/usr/share/bash-completion/completions/menina",
				"zsh":  "/usr/share/zsh/site-functions/_menina",
				"fish": "/usr/share/fish/vendor_completions.d/menina.fish",
			}
			if !system {
				targets = map[string]string{
					"bash": filepath.Join(home, ".local/share/bash-completion/completions/menina"),
					"zsh":  filepath.Join(home, ".zsh/completions/_menina"),
					"fish": filepath.Join(home, ".config/fish/completions/menina.fish"),
				}
			}
			gen := map[string]func(*bytes.Buffer) error{
				"bash": func(b *bytes.Buffer) error { return root.GenBashCompletion(b) },
				"zsh":  func(b *bytes.Buffer) error { return root.GenZshCompletion(b) },
				"fish": func(b *bytes.Buffer) error { return root.GenFishCompletion(b, true) },
			}
			for shell, path := range targets {
				var buf bytes.Buffer
				if err := gen[shell](&buf); err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					return err
				}
				app.Infof("Installed %s completion to %s", shell, path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&system, "system", false, "install for all users (requires root)")
	return cmd
}
