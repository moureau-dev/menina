package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/secrets"
)

type prompter struct {
	reader  *bufio.Reader
	enabled bool
}

func newPrompter() *prompter {
	enabled := term.IsTerminal(int(os.Stdin.Fd()))
	return &prompter{reader: bufio.NewReader(os.Stdin), enabled: enabled}
}

// readLine reads a line, abandoning the read (and exiting) if the process is
// interrupted (SIGINT/SIGTERM) while waiting, so Ctrl+C always works even mid
// prompt.
func (p *prompter) readLine() string {
	ch := make(chan string, 1)
	go func() {
		line, _ := p.reader.ReadString('\n')
		ch <- strings.TrimSpace(line)
	}()
	select {
	case line := <-ch:
		return line
	case <-promptCancelled():
		fmt.Println()
		promptExit(130)
		return ""
	}
}

func (p *prompter) Ask(label, def string) string {
	if !p.enabled {
		return def
	}
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line := p.readLine()
	if line == "" {
		return def
	}
	return line
}

func (p *prompter) Confirm(label string, def bool) bool {
	if !p.enabled {
		return def
	}
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	fmt.Printf("%s %s ", label, hint)
	line := strings.ToLower(p.readLine())
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}

func (p *prompter) Choose(label string, options []string, def int) int {
	if !p.enabled {
		return def
	}
	fmt.Println(label)
	for i, o := range options {
		fmt.Printf("  %d. %s\n", i+1, o)
	}
	fmt.Printf("Select [%d]: ", def+1)
	line := p.readLine()
	if line == "" {
		return def
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(options) {
		return def
	}
	return n - 1
}

func newSetupCommand(app *App) *cobra.Command {
	var nonInteractive bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure the host and platform",
		Long: "Interactively write config.yml: host, Kubernetes/ingress, registry (Zot),\n" +
			"networking, TLS (ACME/manual/none) and defaults (namespace, replicas, CPU,\n" +
			"memory). It also generates the registry push credential and the webhook\n" +
			"token. Must run as root. Follow with `menina deploy`.",
		Example: "  sudo menina setup\n  sudo menina setup --non-interactive   # accept all defaults",
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() != 0 && strings.HasPrefix(app.Opts.ConfigDir, "/etc") {
				return fmt.Errorf("menina setup writes to %s and must run as root (try: sudo menina setup, or pass --config-dir)", app.Opts.ConfigDir)
			}
			p := newPrompter()
			if nonInteractive {
				p.enabled = false
			}
			cfg := config.Default()
			existing, err := config.LoadPlatform(app.Opts.ConfigDir)
			if err == nil {
				cfg = *existing
				app.Infof("Loaded existing configuration from %s", app.Opts.ConfigDir)
			}
			var cloudflareToken string

			app.Infof("Welcome to Menina.\n")
			app.Infof("Kubernetes")
			cfg.Kubernetes.IngressOwnership = config.IngressOwnershipMenina
			cfg.Kubernetes.DisableTraefik = p.Confirm("  Install k3s with Traefik disabled (Menina owns :80/:443)?", cfg.Kubernetes.DisableTraefik)
			if !cfg.Kubernetes.DisableTraefik {
				cfg.Kubernetes.IngressOwnership = config.IngressOwnershipExternal
			}

			app.Infof("\nRegistry")
			cfg.Registry.Enabled = p.Confirm("  Install Zot on this cluster?", cfg.Registry.Enabled)
			cfg.Registry.Domain = p.Ask("  Registry domain", cfg.Registry.Domain)
			keep := p.Ask("  Retention: keep recent tags per repository", strconv.Itoa(cfg.Registry.Retention.KeepRecentTags))
			if n, err := strconv.Atoi(keep); err == nil {
				cfg.Registry.Retention.KeepRecentTags = n
			}

			app.Infof("\nNetworking")
			mode := p.Choose("  Networking mode", []string{"Public VPS ingress", "Cloudflare Tunnel"}, 0)
			if mode == 0 {
				cfg.Networking.Mode = config.NetworkingPublic
				if p.Confirm("  Automate public DNS via Cloudflare? (requires an API token; \"n\" = you manage DNS yourself)", false) {
					cloudflareToken = p.Ask("  Cloudflare API token", "")
					cfg.Networking.Cloudflare.Zone = p.Ask("  Cloudflare zone", cfg.Networking.Cloudflare.Zone)
					cfg.Networking.Cloudflare.DNSProxied = p.Confirm("  Proxy through Cloudflare (orange cloud)?", false)
					cfg.Networking.PublicIP = p.Ask("  VPS public IP (blank = auto-detect)", cfg.Networking.PublicIP)
				}
			} else {
				cfg.Networking.Mode = config.NetworkingCloudflareTunnel
				cfg.Networking.Cloudflare.AccountID = p.Ask("  Cloudflare account ID", cfg.Networking.Cloudflare.AccountID)
				cfg.Networking.Cloudflare.TunnelID = p.Ask("  Cloudflare tunnel ID", cfg.Networking.Cloudflare.TunnelID)
				cfg.Networking.Cloudflare.Zone = p.Ask("  Cloudflare zone", cfg.Networking.Cloudflare.Zone)
				cfg.Networking.Cloudflare.OriginURL = p.Ask("  Local origin URL", orDefault(cfg.Networking.Cloudflare.OriginURL, "http://127.0.0.1:80"))
			}
			domains := p.Ask("  Base domains (comma-separated)", strings.Join(cfg.Networking.BaseDomains, ","))
			cfg.Networking.BaseDomains = splitCSV(domains)
			trusted := p.Ask("  Trusted reverse proxies (CIDR/IP, comma-separated; they may set X-Forwarded-*)", strings.Join(cfg.Security.TrustedProxies, ","))
			cfg.Security.TrustedProxies = splitCSV(trusted)

			app.Infof("\nTLS")
			cfg.TLS.Terminate = p.Confirm("  Terminate TLS in Menina?", cfg.TLS.Terminate)
			if cfg.TLS.Terminate {
				strat := p.Choose("  Certificate strategy", []string{"ACME / Let's Encrypt", "cert-manager on k3s (consume manually)", "Provide certificates manually"}, 0)
				switch strat {
				case 0:
					cfg.TLS.Mode = config.TLSModeACME
					cfg.TLS.ACME.Email = p.Ask("  ACME account email", cfg.TLS.ACME.Email)
				case 1:
					app.Infof("    cert-manager certificates are not consumed automatically by the host proxy yet.")
					app.Infof("    Menina will serve certificates you place in %s (manual mode).", cfg.TLS.CertDir)
					cfg.TLS.Mode = config.TLSModeManual
				case 2:
					cfg.TLS.Mode = config.TLSModeManual
				}
			} else {
				cfg.TLS.Mode = config.TLSModeNone
			}
			if cfg.Networking.Mode == config.NetworkingCloudflareTunnel {
				if cfg.TLS.Terminate {
					app.Infof("    Cloudflare Tunnel terminates public TLS; Menina will not terminate TLS at the origin.")
					cfg.TLS.Terminate = false
					cfg.TLS.Mode = config.TLSModeNone
				}
				if cfg.Networking.Cloudflare.OriginProto == "" {
					cfg.Networking.Cloudflare.OriginProto = "http"
				}
			}

			app.Infof("")
			cfg.Host.ConfigDir = p.Ask("  Configuration directory", app.Opts.ConfigDir)
			app.Opts.ConfigDir = cfg.Host.ConfigDir
			cfg.Host.DataDir = p.Ask("  Data directory", cfg.Host.DataDir)
			cfg.Platform.DBPath = p.Ask("  SQLite database path", orDefault(cfg.Platform.DBPath, cfg.Host.DataDir+"/menina.db"))

			if cfg.Registry.Enabled {
				app.Infof("\nRegistry credentials")
				if p.Confirm("  Generate a random push credential for Zot?", true) {
					password, err := secrets.GeneratePassword(32)
					if err != nil {
						return err
					}
					path := filepath.Join(cfg.Host.ConfigDir, "secrets", "registry-password")
					if err := secrets.WriteSecretFile(path, password); err != nil {
						return err
					}
					cfg.Registry.Auth = config.RegistryAuth{
						Enabled:     true,
						Username:    "menina",
						PasswordRef: "file:" + path,
					}
					app.Infof("  Push password stored at %s (retrieve with `menina registry credential`)", path)
				}
				if cfg.Registry.Notifications.Enabled {
					token, err := secrets.GeneratePassword(48)
					if err != nil {
						return err
					}
					path := filepath.Join(cfg.Host.ConfigDir, "secrets", "notifications-token")
					if err := secrets.WriteSecretFile(path, token); err != nil {
						return err
					}
					cfg.Registry.Notifications.TokenRef = "file:" + path
					app.Infof("  Webhook authentication token stored at %s", path)
				}
			}

			if cloudflareToken != "" {
				path := filepath.Join(cfg.Host.ConfigDir, "secrets", "cloudflare-token")
				if err := secrets.WriteSecretFile(path, cloudflareToken); err != nil {
					return err
				}
				cfg.Networking.Cloudflare.APITokenSecretRef = "file:" + path
				app.Infof("  Cloudflare API token stored at %s", path)
			}

			app.Infof("\nDeploy notifications")
			if p.Confirm("  Configure an outbound deploy webhook?", false) {
				if url := p.Ask("  Webhook URL", ""); url != "" {
					path := filepath.Join(cfg.Host.ConfigDir, "secrets", "deploy-webhook")
					if err := secrets.WriteSecretFile(path, url); err != nil {
						return err
					}
					cfg.Webhooks.Deploy = config.DeployWebhook{Enabled: true, URLRef: "file:" + path}
					if p.Confirm("  Sign payloads with an HMAC secret?", false) {
						if s := p.Ask("  Signing secret", ""); s != "" {
							sp := filepath.Join(cfg.Host.ConfigDir, "secrets", "deploy-webhook-signing")
							if err := secrets.WriteSecretFile(sp, s); err != nil {
								return err
							}
							cfg.Webhooks.Deploy.SecretRef = "file:" + sp
						}
					}
					app.Infof("  Deploy webhook configured")
				}
			}

			app.Infof("\nDefaults")
			cfg.Defaults.Namespace = p.Ask("  Default namespace for apps", cfg.Defaults.Namespace)
			cfg.Defaults.Replicas = atoiOr(p.Ask("  Default replicas", strconv.Itoa(cfg.Defaults.Replicas)), cfg.Defaults.Replicas)
			cfg.Defaults.CPU = p.Ask("  Default CPU request/limit", cfg.Defaults.CPU)
			cfg.Defaults.Memory = p.Ask("  Default memory request/limit", cfg.Defaults.Memory)

			if p.Confirm("\nReview configuration?", true) {
				data, _ := yaml.Marshal(cfg)
				app.Infof("\n%s", string(data))
			}
			if err := config.ValidatePlatform(&cfg); err != nil {
				return err
			}
			if err := config.SavePlatform(cfg.Host.ConfigDir, &cfg); err != nil {
				return err
			}
			app.Infof("Configuration written to %s/config.yml", cfg.Host.ConfigDir)
			app.Infof("Next: run `menina deploy`")
			return nil
		},
	}
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "accept all defaults without prompting")
	return cmd
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
