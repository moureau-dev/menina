package host

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/cli/backup"
	"github.com/moureau-dev/menina/internal/core/config"
	"github.com/moureau-dev/menina/internal/core/cluster"
	"github.com/moureau-dev/menina/internal/version"
)

func newBackupCommand(app *App) *cobra.Command {
	var out string
	var withVolumes, withImages, withSecrets, consistent bool
	var passphrase string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create a portable backup archive (tar.gz)",
		Long: "Create a tar.gz bundle: the SQLite database, config.yml and projects/,\n" +
			"and (optionally) secrets, TLS material, application volumes and the\n" +
			"registry images. Restore it on another host with `menina restore`.\n\n" +
			"--with-volumes/--with-images read the node's local-path storage directly\n" +
			"and must run as root.",
		Example: "  menina backup --out /root/menina.tgz\n" +
			"  sudo menina backup --out /root/menina.tgz --with-volumes --with-images\n" +
			"  sudo MENINA_BACKUP_PASSPHRASE=... menina backup --with-secrets --with-volumes",
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

			needVols := withVolumes || withImages
			vols, err := volumeManager(app, cfg, needVols)
			if err != nil {
				return err
			}
			if passphrase == "" {
				passphrase = os.Getenv("MENINA_BACKUP_PASSPHRASE")
			}
			path, manifest, err := backup.Create(cmd.Context(), backup.CreateOptions{
				ConfigDir:     cfg.Host.ConfigDir,
				DataDir:       cfg.Host.DataDir,
				DBPath:        cfg.Platform.DBPath,
				Out:           out,
				WithVolumes:   withVolumes,
				WithImages:    withImages,
				WithSecrets:   withSecrets,
				Consistent:    consistent,
				Passphrase:    passphrase,
				MeninaVersion: version.Get().Version,
				DB:            db,
				Config:        cfg,
				Volumes:       vols,
				Log:           app.Log,
			})
			if err != nil {
				return err
			}
			info, _ := os.Stat(path)
			app.Infof("Backup written to %s (%s; components: %s)", path, humanBytes(info), strings.Join(manifest.Components, ", "))
			if withSecrets && passphrase == "" {
				app.Infof("warning: secrets included in plaintext; use MENINA_BACKUP_PASSPHRASE to encrypt the archive")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "archive path (default <data-dir>/backups/menina-<time>.tar.gz)")
	cmd.Flags().BoolVar(&withVolumes, "with-volumes", false, "include application volume data (requires root)")
	cmd.Flags().BoolVar(&withImages, "with-images", false, "include registry image data (requires root)")
	cmd.Flags().BoolVar(&withSecrets, "with-secrets", false, "include secrets/ (registry password, tokens)")
	cmd.Flags().BoolVar(&consistent, "consistent", false, "scale workloads to 0 while copying volumes/images (quiesced snapshot)")
	cmd.Flags().StringVar(&passphrase, "passphrase", "", "encrypt the archive (or set MENINA_BACKUP_PASSPHRASE)")
	return cmd
}

func volumeManager(app *App, cfg *config.Config, need bool) (backup.VolumeManager, error) {
	if !need {
		return nil, nil
	}
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("--with-volumes/--with-images read the node's local-path storage and must run as root (try: sudo menina backup ...)")
	}
	cluster, err := k8s.NewKubernetesCluster(cfg.Kubernetes.Kubeconfig)
	if err != nil {
		return nil, err
	}
	return &backup.LocalPathVolumeManager{Cluster: cluster, Log: app.Log}, nil
}

func humanBytes(info os.FileInfo) string {
	if info == nil {
		return "unknown size"
	}
	n := float64(info.Size())
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", int(n), units[i])
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}
