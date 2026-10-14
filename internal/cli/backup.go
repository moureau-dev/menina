package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

func newBackupCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "backup",
		Short: "Back up the Menina SQLite database",
		Long:  "Write a consistent VACUUM INTO copy of the SQLite database to <data-dir>/backups/. Image blobs are not included.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			db, err := app.OpenDB(cfg)
			if err != nil {
				return err
			}
			defer db.Close()
			dir := filepath.Join(cfg.Host.DataDir, "backups")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return err
			}
			dest := filepath.Join(dir, fmt.Sprintf("menina-%s.db", time.Now().UTC().Format("20060102T150405Z")))
			if err := db.Backup(cmd.Context(), dest); err != nil {
				return fmt.Errorf("backup failed: %w", err)
			}
			if info, err := os.Stat(dest); err == nil {
				app.Infof("Backed up to %s (%d bytes)", dest, info.Size())
			} else {
				app.Infof("Backed up to %s", dest)
			}
			return nil
		},
	}
}
