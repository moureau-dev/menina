package cli

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/infrastructure"
)

func newDoctorCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration and infrastructure problems",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := app.LoadConfig()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}
			app.Opts.ConfigDir = cfg.Host.ConfigDir
			results := []checkResult{}
			results = append(results, check("configuration", config.ValidatePlatform(cfg)))

			var resolved *config.ResolvedConfig
			projects, err := config.LoadAllProjects(config.ProjectsDir(cfg.Host.ConfigDir))
			if err != nil {
				results = append(results, check("project tree", err))
			} else {
				rc, err := config.NewResolver(cfg).Resolve(projects, 0)
				if err != nil {
					results = append(results, check("resolved configuration", err))
				} else {
					resolved = rc
					results = append(results, check("resolved configuration", config.ValidateResolved(resolved)))
				}
			}

			results = append(results, check("data directory", infrastructure.NewStorage(cfg.Host.DataDir).EnsureDirs()))
			if _, err := infraStorage(cfg); err != nil {
				results = append(results, check("disk space", err))
			} else {
				results = append(results, check("disk space", nil))
			}

			if client, err := app.Control(cfg); err != nil {
				results = append(results, check("control plane", fmt.Errorf("not running: %w", err)))
			} else {
				var status map[string]any
				if err := client.GetJSON(ctx, "/status", &status); err != nil {
					results = append(results, check("control plane", err))
				} else {
					results = append(results, check("control plane", nil))
					if ok, _ := status["kubernetesOK"].(bool); !ok {
						results = append(results, check("kubernetes API", fmt.Errorf("%v", status["kubernetes"])))
					} else {
						results = append(results, check("kubernetes API", nil))
					}
				}
			}

			if cfg.Registry.Enabled {
				if rc, err := registryClient(cfg); err != nil {
					results = append(results, check("registry", err))
				} else if err := rc.Ping(ctx); err != nil {
					results = append(results, check("registry", err))
				} else {
					results = append(results, check("registry", nil))
				}
				results = append(results, checkRegistryRoute(ctx, app, cfg))
			}

			results = append(results, checkProxyTunnel(ctx, cfg))
			results = append(results, checkTLS(ctx, cfg, resolved)...)
			results = append(results, checkBackends(ctx, app, cfg)...)

			failed := 0
			for _, r := range results {
				mark := "ok"
				if r.err != nil {
					mark = "FAIL"
					failed++
				}
				app.Infof("[%s] %s", mark, r.name)
				if r.err != nil {
					app.Infof("       %v", r.err)
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d check(s) failed", failed)
			}
			app.Infof("\nAll checks passed.")
			return nil
		},
	}
}

type checkResult struct {
	name string
	err  error
}

func check(name string, err error) checkResult { return checkResult{name: name, err: err} }

func infraStorage(cfg *config.Config) (infrastructure.DiskStats, error) {
	st := infrastructure.NewStorage(cfg.Host.DataDir)
	return st.DiskUsage(cfg.Host.DataDir)
}

func checkProxyTunnel(ctx context.Context, cfg *config.Config) checkResult {
	if cfg.Networking.Mode != config.NetworkingCloudflareTunnel {
		return check("proxy/tunnel", nil)
	}
	if cfg.Networking.Cloudflare.AccountID == "" || cfg.Networking.Cloudflare.TunnelID == "" {
		return check("proxy/tunnel", fmt.Errorf("cloudflare tunnel configured but accountId/tunnelId missing"))
	}
	return check("proxy/tunnel", nil)
}

func checkRegistryRoute(ctx context.Context, app *App, cfg *config.Config) checkResult {
	client, err := app.Control(cfg)
	if err != nil {
		return check("registry route", fmt.Errorf("control plane not running: %w", err))
	}
	var routes []map[string]any
	if err := client.GetJSON(ctx, "/routes", &routes); err != nil {
		return check("registry route", err)
	}
	for _, r := range routes {
		if host, _ := r["host"].(string); host == cfg.Registry.Domain {
			return check("registry route", nil)
		}
	}
	return check("registry route", fmt.Errorf("no proxy route for %s yet; run `menina deploy` (Zot must have a ClusterIP)", cfg.Registry.Domain))
}

// checkBackends verifies the host can actually reach every resolved route
// backend (ClusterIP:port) over TCP. ICMP is deliberately not used: kube-proxy
// programs DNAT for TCP/UDP, so ping to a ClusterIP fails even when routing works.
func checkBackends(ctx context.Context, app *App, cfg *config.Config) []checkResult {
	client, err := app.Control(cfg)
	if err != nil {
		return nil
	}
	snap, err := client.Snapshot(ctx)
	if err != nil {
		return nil
	}
	var out []checkResult
	for _, route := range snap.Routes {
		out = append(out, check("backend "+route.Host, tcpReachable(ctx, route.Backend)))
	}
	if len(snap.Routes) == 0 {
		out = append(out, check("route backends", nil))
	}
	return out
}

func tcpReachable(ctx context.Context, addr string) error {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot reach backend %s from the host (kube-proxy ClusterIP routing): %w", addr, err)
	}
	_ = conn.Close()
	return nil
}

func checkTLS(ctx context.Context, cfg *config.Config, resolved *config.ResolvedConfig) []checkResult {
	if !cfg.TLS.Terminate {
		return []checkResult{check("tls", nil)}
	}
	var out []checkResult
	out = append(out, check("tls certificate directory", checkCertDirWritable(cfg.TLS.CertDir)))

	if cfg.TLS.Mode == config.TLSModeACME {
		dirURL := cfg.TLS.ACME.DirectoryURL
		if dirURL == "" {
			dirURL = "https://acme-v02.api.letsencrypt.org/directory"
		}
		out = append(out, check("acme directory", httpReachable(ctx, dirURL)))
	} else if cfg.TLS.Mode == config.TLSModeManual {
		out = append(out, check("manual certificates", checkManualCerts(cfg.TLS.CertDir, exposedHosts(resolved))))
	}
	if cfg.TLS.Mode == config.TLSModeACME || cfg.TLS.Mode == config.TLSModeManual {
		port := httpsPort(cfg.Platform.HTTPSListen)
		for _, host := range exposedHosts(resolved) {
			out = append(out, check("tls "+host, verifyHostCert(ctx, host, port)))
		}
	}
	return out
}

func exposedHosts(resolved *config.ResolvedConfig) []string {
	if resolved == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var hosts []string
	for _, rp := range resolved.Projects {
		for _, re := range rp.Environments {
			for _, app := range re.Apps {
				if !app.Enabled || !app.Exposure.Enabled {
					continue
				}
				for _, h := range app.Exposure.Hosts {
					if _, ok := seen[h]; ok {
						continue
					}
					seen[h] = struct{}{}
					hosts = append(hosts, h)
				}
			}
		}
	}
	return hosts
}

func checkCertDirWritable(dir string) error {
	if dir == "" {
		return fmt.Errorf("tls.certDir is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".menina-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func checkManualCerts(dir string, hosts []string) error {
	for _, h := range hosts {
		base := filepath.Join(dir, h)
		if _, err := os.Stat(base + ".crt"); err != nil {
			if _, err := os.Stat(base + ".pem"); err != nil {
				return fmt.Errorf("no certificate file for %s in %s", h, dir)
			}
		}
		if _, err := os.Stat(base + ".key"); err != nil {
			return fmt.Errorf("no private key for %s in %s", h, dir)
		}
	}
	return nil
}

func httpsPort(listen string) string {
	if _, port, err := net.SplitHostPort(listen); err == nil && port != "" {
		return port
	}
	return "443"
}

func verifyHostCert(ctx context.Context, host, port string) error {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	defer raw.Close()
	conn := tls.Client(raw, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	defer conn.Close()
	if err := conn.HandshakeContext(ctx); err != nil {
		return err
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fmt.Errorf("no certificate presented for %s", host)
	}
	leaf := certs[0]
	if time.Until(leaf.NotAfter) < 14*24*time.Hour {
		return fmt.Errorf("certificate for %s expires soon (%s)", host, leaf.NotAfter.Format(time.RFC3339))
	}
	return nil
}

func httpReachable(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s returned status %d", url, resp.StatusCode)
	}
	return nil
}
