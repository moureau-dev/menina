package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/version"
)

const releaseRepo = "moureau-dev/menina"

func newUpgradeCommand(app *App) *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Download and install a new verified Menina binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Minute)
			defer cancel()
			latest, err := latestRelease(ctx)
			if err != nil {
				return err
			}
			app.Infof("Installed: %s", version.Version)
			app.Infof("Latest:    %s", latest.TagName)
			if version.Version == latest.TagName {
				app.Infof("Already up to date.")
				return nil
			}
			if checkOnly {
				return nil
			}
			assetName := fmt.Sprintf("menina-linux-%s", runtime.GOARCH)
			assetURL, sumsURL := "", ""
			for _, a := range latest.Assets {
				switch a.Name {
				case assetName:
					assetURL = a.BrowserDownloadURL
				case "checksums.txt":
					sumsURL = a.BrowserDownloadURL
				}
			}
			if assetURL == "" {
				return fmt.Errorf("release %s has no asset %s", latest.TagName, assetName)
			}
			expected, err := fetchChecksum(ctx, sumsURL, assetName)
			if err != nil {
				return err
			}
			tmp, err := downloadVerified(ctx, assetURL, expected)
			if err != nil {
				return err
			}
			defer os.Remove(tmp)
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if err := replaceBinary(tmp, exe); err != nil {
				return err
			}
			app.Infof("Upgraded to %s. Restart services with: systemctl restart menina-serve menina-proxy", latest.TagName)
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only check for a newer release")
	return cmd
}

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func latestRelease(ctx context.Context) (*release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+releaseRepo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: %s", resp.Status)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func fetchChecksum(ctx context.Context, sumsURL, assetName string) (string, error) {
	if sumsURL == "" {
		return "", fmt.Errorf("release has no checksums.txt; refusing to upgrade without verification")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
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

func downloadVerified(ctx context.Context, url, expected string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp("", "menina-upgrade-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("checksum mismatch: expected %s got %s", expected, got)
	}
	return tmp.Name(), nil
}

func replaceBinary(src, dst string) error {
	if err := os.Chmod(src, 0o755); err != nil {
		return err
	}
	backup := dst + ".bak"
	if err := copyFile(dst, backup); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
