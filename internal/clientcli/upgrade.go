package clientcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/version"
)

const defaultReleaseRepo = "moureau-dev/menina"

type clientRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []clientAsset `json:"assets"`
}

type clientAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func releaseRepo() string {
	if repo := strings.TrimSpace(os.Getenv("MENINA_REPO")); repo != "" {
		return repo
	}
	return defaultReleaseRepo
}

func newVersionCommand(app *App) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long:  "Print the installed client version. With --check, ask GitHub for the latest release.",
		Example: "  menina version\n" +
			"  menina version --check\n" +
			"  menina version --check --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Get()
			if !check {
				if app.jsonOutput() {
					return app.printJSON(info)
				}
				app.infof("menina %s (commit %s, built %s, %s)", info.Version, info.Commit, info.Date, info.GoVersion)
				return nil
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			rel, err := latestClientRelease(ctx)
			if err != nil {
				return err
			}
			result := map[string]any{
				"current":         info.Version,
				"latest":          rel.TagName,
				"updateAvailable": info.Version != rel.TagName,
			}
			if app.jsonOutput() {
				return app.printJSON(result)
			}
			app.infof("Installed: %s", info.Version)
			app.infof("Latest:    %s", rel.TagName)
			if info.Version == rel.TagName {
				app.infof("Already up to date.")
			} else {
				app.infof("Update available: run `menina upgrade`.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "check the latest release")
	return cmd
}

func newUpgradeCommand(app *App) *cobra.Command {
	var check bool
	var target string
	var force bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Download and install a newer client binary",
		Long: "Download a client release, verify its checksum, and replace the running\n" +
			"binary. On Windows the previous executable is renamed and removed on the\n" +
			"next upgrade. Use --check to only compare versions.",
		Example: "  menina upgrade --check\n" +
			"  menina upgrade\n" +
			"  menina upgrade --version v0.2.0\n" +
			"  menina upgrade --force",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Minute)
			defer cancel()

			current := version.Get().Version
			tag := strings.TrimSpace(target)
			var assetURL, sumsURL string
			if tag == "" {
				rel, err := latestClientRelease(ctx)
				if err != nil {
					return err
				}
				tag = rel.TagName
				asset := clientAssetName()
				for _, a := range rel.Assets {
					switch a.Name {
					case asset:
						assetURL = a.BrowserDownloadURL
					case "checksums.txt":
						sumsURL = a.BrowserDownloadURL
					}
				}
				if assetURL == "" {
					return fmt.Errorf("release %s has no asset %s", tag, asset)
				}
			} else {
				base := fmt.Sprintf("https://github.com/%s/releases/download/%s", releaseRepo(), tag)
				assetURL = base + "/" + clientAssetName()
				sumsURL = base + "/checksums.txt"
			}
			if sumsURL == "" {
				sumsURL = fmt.Sprintf("https://github.com/%s/releases/download/%s/checksums.txt", releaseRepo(), tag)
			}

			updateAvailable := current != tag
			result := map[string]any{
				"current":         current,
				"latest":          tag,
				"updateAvailable": updateAvailable,
			}
			if !updateAvailable && !force {
				if app.jsonOutput() {
					result["upgraded"] = false
					return app.printJSON(result)
				}
				app.infof("Installed: %s", current)
				app.infof("Latest:    %s", tag)
				app.infof("Already up to date.")
				return nil
			}
			if check {
				if app.jsonOutput() {
					result["upgraded"] = false
					return app.printJSON(result)
				}
				app.infof("Installed: %s", current)
				app.infof("Latest:    %s", tag)
				app.infof("Update available.")
				return nil
			}

			expected, err := fetchClientChecksum(ctx, sumsURL, clientAssetName())
			if err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			tmp, err := downloadClientBinary(ctx, assetURL, expected, filepath.Dir(exe))
			if err != nil {
				return err
			}
			defer os.Remove(tmp)
			if err := verifyClientBinary(ctx, tmp); err != nil {
				return fmt.Errorf("downloaded binary failed to execute; refusing to install: %w", err)
			}
			if err := replaceClientBinary(tmp, exe); err != nil {
				return err
			}
			if app.jsonOutput() {
				result["upgraded"] = true
				return app.printJSON(result)
			}
			app.infof("Installed %s at %s", tag, exe)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only check for a newer release")
	cmd.Flags().StringVar(&target, "version", "", "install a specific release tag")
	cmd.Flags().BoolVar(&force, "force", false, "reinstall even when already up to date")
	return cmd
}

func clientAssetName() string {
	name := fmt.Sprintf("menina-client-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func latestClientRelease(ctx context.Context) (*clientRelease, error) {
	url := "https://api.github.com/repos/" + releaseRepo() + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "menina-client")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: %s", resp.Status)
	}
	var rel clientRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func fetchClientChecksum(ctx context.Context, sumsURL, assetName string) (string, error) {
	if sumsURL == "" {
		return "", fmt.Errorf("release has no checksums.txt; refusing to upgrade without verification")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "menina-client")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum for %s not found", assetName)
}

func downloadClientBinary(ctx context.Context, url, expected, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "menina-client")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(dir, ".menina-client-*")
	if err != nil {
		return "", fmt.Errorf("stage download in %s: %w", dir, err)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("checksum mismatch: expected %s got %s", expected, got)
	}
	return tmp.Name(), nil
}

func verifyClientBinary(ctx context.Context, path string) error {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func replaceClientBinary(src, dst string) error {
	if runtime.GOOS == "windows" {
		backup := dst + ".old"
		_ = os.Remove(backup)
		if err := os.Rename(dst, backup); err != nil {
			return fmt.Errorf("move current binary aside: %w", err)
		}
		if err := os.Rename(src, dst); err != nil {
			_ = os.Rename(backup, dst)
			return fmt.Errorf("install new binary: %w", err)
		}
		// The old process still holds the file open; removal may fail and is
		// retried at the start of the next upgrade.
		_ = os.Remove(backup)
		return nil
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}
