package host

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/cli/backup"
)

func newRestoreCommand(app *App) *cobra.Command {
	var withVolumes, withImages, force, consistent bool
	var passphrase string
	cmd := &cobra.Command{
		Use:   "restore <archive>",
		Short: "Restore a backup archive created by `menina backup`",
		Long: "Extract a backup archive and apply it: the SQLite database, config.yml and\n" +
			"projects/, plus secrets, TLS material, application volumes and registry\n" +
			"images when present and requested. Migrations run on the next `menina\n" +
			"serve` start.\n\n" +
			"Stop menina-serve before restoring (it is the database writer), then start\n" +
			"it again afterwards. --with-volumes/--with-images require root.",
		Example: "  sudo systemctl stop menina-serve\n" +
			"  sudo menina restore /root/menina.tgz --force --with-volumes --with-images\n" +
			"  sudo systemctl start menina-serve",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			if passphrase == "" {
				passphrase = os.Getenv("MENINA_BACKUP_PASSPHRASE")
			}
			vols, err := volumeManager(app, cfg, withVolumes || withImages)
			if err != nil {
				return err
			}
			m, err := backup.Restore(cmd.Context(), backup.RestoreOptions{
				Archive:     args[0],
				ConfigDir:   cfg.Host.ConfigDir,
				DataDir:     cfg.Host.DataDir,
				DBPath:      cfg.Platform.DBPath,
				WithVolumes: withVolumes,
				WithImages:  withImages,
				Consistent:  consistent,
				Passphrase:  passphrase,
				Force:       force,
				Volumes:     vols,
				Config:      cfg,
				Log:         app.Log,
			})
			if err != nil {
				return err
			}
			app.Infof("Restored archive (created %s, components: %s)", m.CreatedAt.Format("2006-01-02T15:04:05Z"), strings.Join(m.Components, ", "))
			app.Infof("Next: start the control plane so migrations run and state reconciles:")
			app.Infof("  systemctl start menina-serve menina-proxy")
			return nil
		},
	}
	cmd.Flags().BoolVar(&withVolumes, "with-volumes", false, "restore application volume data (requires root)")
	cmd.Flags().BoolVar(&withImages, "with-images", false, "restore registry image data (requires root)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing database/config")
	cmd.Flags().BoolVar(&consistent, "consistent", false, "scale workloads down while restoring volumes/images")
	cmd.Flags().StringVar(&passphrase, "passphrase", "", "passphrase for an encrypted archive (or MENINA_BACKUP_PASSPHRASE)")
	return cmd
}
