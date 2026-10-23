package host

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

	"github.com/moureau-dev/menina/internal/cli/tui"
	"github.com/moureau-dev/menina/internal/core/config"
	"github.com/moureau-dev/menina/internal/core/secrets"
)

type prompter struct {
	reader  *bufio.Reader
	enabled bool
	tui     bool
}

var nonInteractive bool

func newPrompter() *prompter {
	enabled := term.IsTerminal(int(os.Stdin.Fd())) && !nonInteractive
	return &prompter{reader: bufio.NewReader(os.Stdin), enabled: enabled, tui: enabled}
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
	if p.tui {
		v, action := tui.Input(label, def)
		switch action {
		case tui.ActionBack:
			panic(backSignal{})
		case tui.ActionSelected:
			return v
		default:
			promptExit(130)
		}
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
	if p.tui {
		v, action := tui.Confirm(label, def)
		switch action {
		case tui.ActionBack:
			panic(backSignal{})
		case tui.ActionSelected:
			return v
		default:
			promptExit(130)
		}
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

// Option is a selectable choice with an optional one-line description.
type Option struct {
	Label       string
	Description string
}

func (p *prompter) Choose(label string, options []string, def int) int {
	opts := make([]Option, len(options))
	for i, o := range options {
		opts[i] = Option{Label: o}
	}
	return p.Select(label, opts, def)
}

func (p *prompter) Select(label string, options []Option, def int) int {
	if !p.enabled {
		return def
	}
	if p.tui {
		idx, action := tui.Select(label, tuiOptions(options), def, false)
		switch action {
		case tui.ActionSelected:
			return idx
		case tui.ActionBack:
			panic(backSignal{})
		default:
			promptExit(130)
		}
	}
	fmt.Println(label)
	for i, o := range options {
		fmt.Printf("  %d. %s\n", i+1, o.Label)
		if o.Description != "" {
			fmt.Printf("     %s\n", o.Description)
		}
	}
	fmt.Printf("Select [%d]: ", def+1)
	line := p.readLine()
	if line == "" {
		return def
	}
	if n, err := strconv.Atoi(line); err == nil {
		if n < 1 || n > len(options) {
			return def
		}
		return n - 1
	}
	// Accept y/n as aliases for a Yes/No menu.
	switch strings.ToLower(line) {
	case "y", "yes":
		if len(options) >= 1 {
			return 0
		}
	case "n", "no":
		if len(options) >= 2 {
			return 1
		}
	}
	return def
}

// Menu is like Select but exposes "back" (Esc / Shift+Tab) so a menu can
// return to its parent screen instead of exiting. Ctrl+C still exits.
func (p *prompter) Menu(label string, options []Option, def int) (int, bool) {
	if !p.enabled {
		return def, true
	}
	if p.tui {
		idx, action := tui.Select(label, tuiOptions(options), def, true)
		switch action {
		case tui.ActionSelected:
			return idx, true
		case tui.ActionBack:
			return def, false
		default:
			promptExit(130)
			return def, false
		}
	}
	return p.Select(label, options, def), true
}

// MenuChoose is Menu for a list of plain strings.
func (p *prompter) MenuChoose(label string, options []string, def int) (int, bool) {
	opts := make([]Option, len(options))
	for i, o := range options {
		opts[i] = Option{Label: o}
	}
	return p.Menu(label, opts, def)
}

func tuiOptions(options []Option) []tui.Option {
	opts := make([]tui.Option, len(options))
	for i, o := range options {
		opts[i] = tui.Option{Label: o.Label, Description: o.Description}
	}
	return opts
}

// yesNo is a Select with Yes/No options and descriptions.
func (p *prompter) yesNo(label, yesDesc, noDesc string, def bool) bool {
	d := 1
	if def {
		d = 0
	}
	return p.Select(label, []Option{
		{Label: "Yes", Description: yesDesc},
		{Label: "No", Description: noDesc},
	}, d) == 0
}

func newSetupCommand(app *App) *cobra.Command {
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
			if app.Opts.NonInteractive {
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
			cfg.Kubernetes.DisableTraefik = p.yesNo("  Install k3s with Traefik disabled (Menina owns :80/:443)?",
				"Menina installs k3s and owns :80/:443; Traefik and ServiceLB are disabled.",
				"Use an existing ingress controller; Menina integrates instead of owning the ports.",
				cfg.Kubernetes.DisableTraefik)
			if !cfg.Kubernetes.DisableTraefik {
				cfg.Kubernetes.IngressOwnership = config.IngressOwnershipExternal
			}

			app.Infof("\nRegistry")
			cfg.Registry.Enabled = p.yesNo("  Install Zot on this cluster?",
				"Run a private OCI registry (Zot) in-cluster and route its domain.",
				"Skip the registry; configure apps to pull from an external registry.",
				cfg.Registry.Enabled)
			cfg.Registry.Domain = p.Ask("  Registry domain", cfg.Registry.Domain)
			keep := p.Ask("  Retention: keep recent tags per repository", strconv.Itoa(cfg.Registry.Retention.KeepRecentTags))
			if n, err := strconv.Atoi(keep); err == nil {
				cfg.Registry.Retention.KeepRecentTags = n
			}

			app.Infof("\nNetworking")
			mode := p.Select("  Networking mode", []Option{
				{Label: "Public VPS ingress", Description: "The host is reachable by IP; Menina owns :80/:443 or serves HTTP behind a proxy (e.g. Cloudflare orange cloud)."},
				{Label: "Cloudflare Tunnel", Description: "cloudflared fronts the host; no inbound ports are opened (Menina runs the local origin only)."},
			}, 0)
			if mode == 0 {
				cfg.Networking.Mode = config.NetworkingPublic
				if p.yesNo("  Automate public DNS via Cloudflare?",
					"Menina creates/updates DNS records through the Cloudflare API (requires an API token).",
					"You manage DNS records yourself (no token needed).",
					false) {
					cloudflareToken = p.Ask("  Cloudflare API token", "")
					cfg.Networking.Cloudflare.Zone = p.Ask("  Cloudflare zone", cfg.Networking.Cloudflare.Zone)
					cfg.Networking.Cloudflare.DNSProxied = p.yesNo("  Proxy through Cloudflare (orange cloud)?",
						"Records are proxied; Cloudflare terminates public TLS (set Cloudflare SSL mode to Flexible for an HTTP origin).",
						"Grey cloud / DNS-only; clients reach the origin directly.",
						false)
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
			cfg.TLS.Terminate = p.yesNo("  Terminate TLS in Menina?",
				"Menina serves :443 and manages certificates (ACME or manual).",
				"TLS is handled upstream (e.g. Cloudflare); Menina serves HTTP on :80.",
				cfg.TLS.Terminate)
			if cfg.TLS.Terminate {
				strat := p.Select("  Certificate strategy", []Option{
					{Label: "ACME / Let's Encrypt", Description: "Menina obtains and renews certificates automatically for routed hostnames."},
					{Label: "cert-manager on k3s (consume manually)", Description: "cert-manager issues certs; Menina serves copies you place in certDir (not consumed automatically)."},
					{Label: "Provide certificates manually", Description: "Place <host>.crt and <host>.key files in certDir."},
				}, 0)
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
				if p.yesNo("  Generate a random push credential for Zot?",
					"Create a random password (stored 0600, referenced as a secret) for `docker login`.",
					"Skip; add credentials later with `menina registry credential --regenerate`.",
					true) {
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
			if p.yesNo("  Configure an outbound deploy webhook?",
				"POST deploy outcomes (success/failure/rollback) as JSON to a receiver you run.",
				"No outbound notifications for now (add later with webhooks.deploy).",
				false) {
				if url := p.Ask("  Webhook URL", ""); url != "" {
					path := filepath.Join(cfg.Host.ConfigDir, "secrets", "deploy-webhook")
					if err := secrets.WriteSecretFile(path, url); err != nil {
						return err
					}
					cfg.Webhooks.Deploy = config.DeployWebhook{Enabled: true, URLRef: "file:" + path}
					if p.yesNo("  Sign payloads with an HMAC secret?",
						"Adds X-Menina-Signature: sha256=<HMAC> so the receiver can verify authenticity.",
						"Send unsigned requests.",
						false) {
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

			if p.yesNo("\nReview configuration?",
				"Print the resolved config.yml for confirmation before writing it.",
				"Write config.yml directly.",
				true) {
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
