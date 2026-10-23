package host

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/moureau-dev/menina/internal/api/errors"
	"github.com/moureau-dev/menina/internal/core/config"
)

// verifyDeployed checks the app's public HTTP routes (when exposed) and TCP L4
// ports (when configured). It runs on the host, so it exercises the real proxy
// and node ports rather than an in-cluster shortcut.
func verifyDeployed(ctx context.Context, cfg *config.Config, app *App, project, environment, name string) error {
	projects, err := config.LoadAllProjects(config.ProjectsDir(app.Opts.ConfigDir))
	if err != nil {
		return err
	}
	resolved, err := config.NewResolver(cfg).Resolve(projects, 0)
	if err != nil {
		return err
	}
	ra, err := resolved.FindApp(project, environment, name)
	if err != nil {
		return apierrors.Wrap(apierrors.CodeNotFound, err)
	}

	checked := 0
	if ra.Exposure.Enabled && len(ra.Exposure.Hosts) > 0 {
		scheme := "http"
		if cfg.TLS.Terminate {
			scheme = "https"
		}
		path := "/"
		if ra.Health != nil && ra.Health.Readiness != "" {
			path = ra.Health.Readiness
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		client := &http.Client{Timeout: 15 * time.Second}
		for _, host := range ra.Exposure.Hosts {
			url := scheme + "://" + host + path
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return apierrors.WithHint(err, fmt.Sprintf("could not reach %s; check `menina routes` and `menina doctor`", url))
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 500 {
				return apierrors.WithHint(fmt.Errorf("%s returned HTTP %d", url, resp.StatusCode), "check `menina app logs` and `menina doctor`")
			}
			app.Infof("Verified %s (HTTP %d)", url, resp.StatusCode)
			checked++
		}
	}
	for _, port := range ra.Exposure.Ports.TCP {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return apierrors.WithHint(fmt.Errorf("could not connect to %s: %w", addr, err), "check the app's L4 ports and firewall")
		}
		_ = conn.Close()
		app.Infof("Verified TCP %s", addr)
		checked++
	}
	if checked == 0 {
		app.Infof("No HTTP or TCP exposure configured; rollout wait was the verification.")
	}
	return nil
}
