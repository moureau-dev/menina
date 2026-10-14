package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	deployassets "github.com/moureau-dev/menina/deploy"
	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/infrastructure"
	"github.com/moureau-dev/menina/internal/registry"
	"github.com/moureau-dev/menina/internal/secrets"
)

const (
	registriesConfigPath = "/etc/rancher/k3s/registries.yaml"
	zotContainerPort     = "5000"
	zotRootDir           = "/var/lib/registry"
	zotConfigMount       = "/etc/zot"
	zotAuthMount         = "/etc/zot-auth"
	zotConfigMapName     = "zot-config"
	zotPVCName           = "zot-data"
	zotSecretName        = "zot-htpasswd"
	defaultPlatformNS    = "menina-system"
	defaultRegistryNS    = "menina-registry"
)

func zotNamespace(cfg *config.Config) string {
	if cfg.Registry.Namespace != "" {
		return cfg.Registry.Namespace
	}
	return defaultRegistryNS
}

func registrySecretsDir(cfg *config.Config) string {
	return filepath.Join(cfg.Host.ConfigDir, "secrets")
}

func registryPasswordPath(cfg *config.Config) string {
	return filepath.Join(registrySecretsDir(cfg), "registry-password")
}

func notificationsTokenPath(cfg *config.Config) string {
	return filepath.Join(registrySecretsDir(cfg), "notifications-token")
}

func resolveRegistryCreds(cfg *config.Config) (string, string, error) {
	if !cfg.Registry.Auth.Enabled {
		return "", "", nil
	}
	user := cfg.Registry.Auth.Username
	if user == "" {
		user = "menina"
	}
	password, err := secrets.NewResolver().Resolve(cfg.Registry.Auth.PasswordRef)
	if err != nil {
		return "", "", err
	}
	return user, password, nil
}

func newDeployCommand(app *App) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "deploy [infra|platform]",
		Short: "Reconcile infrastructure and platform services",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			app.Opts.ConfigDir = cfg.Host.ConfigDir
			scope := "all"
			if len(args) == 1 {
				scope = args[0]
			}
			if dryRun {
				return printPlan(app, cfg, scope)
			}
			if err := deployInfra(cmd.Context(), app, cfg); err != nil {
				return err
			}
			if scope == "infra" {
				return nil
			}
			return deployPlatform(cmd.Context(), app, cfg)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without changing anything")
	cmd.AddCommand(&cobra.Command{
		Use:   "infra",
		Short: "Reconcile infrastructure (k3s, storage, registry)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			app.Opts.ConfigDir = cfg.Host.ConfigDir
			return deployInfra(cmd.Context(), app, cfg)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "platform",
		Short: "Reconcile the Menina platform services",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.LoadConfig()
			if err != nil {
				return err
			}
			app.Opts.ConfigDir = cfg.Host.ConfigDir
			return deployPlatform(cmd.Context(), app, cfg)
		},
	})
	return cmd
}

func printPlan(app *App, cfg *config.Config, scope string) error {
	app.Infof("Deploy plan (dry run), scope=%s\n", scope)
	if scope == "all" || scope == "infra" {
		app.Infof("Infrastructure:")
		app.Infof("  - ingress ownership: %s", cfg.Kubernetes.IngressOwnership)
		app.Infof("  - k3s managed: %v (disable Traefik: %v)", cfg.Kubernetes.Managed, cfg.Kubernetes.DisableTraefik)
		app.Infof("  - data dir: %s", cfg.Host.DataDir)
		if cfg.Registry.Enabled {
			app.Infof("  - deploy Zot %s to namespace %s (domain %s)", cfg.Registry.ZotVersion, zotNamespace(cfg), cfg.Registry.Domain)
			app.Infof("  - registry auth: %v, retention: keep %d recent tags", cfg.Registry.Auth.Enabled, cfg.Registry.Retention.KeepRecentTags)
		}
		app.Infof("  - write %s for image pulls", registriesConfigPath)
	}
	if scope == "all" || scope == "platform" {
		app.Infof("Platform:")
		app.Infof("  - install systemd units menina-serve.service, menina-proxy.service")
		app.Infof("  - control socket: %s", cfg.Platform.ControlSocket)
		app.Infof("  - reverse proxy: %s / %s (TLS terminate: %v)", cfg.Platform.HTTPListen, cfg.Platform.HTTPSListen, cfg.TLS.Terminate)
		app.Infof("  - networking mode: %s", cfg.Networking.Mode)
	}
	return nil
}

func deployInfra(ctx context.Context, app *App, cfg *config.Config) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("menina deploy performs host provisioning and must run as root (try: sudo menina deploy)")
	}
	if err := config.ValidatePlatform(cfg); err != nil {
		return err
	}
	app.Infof("Verifying host and configuration...")
	if err := ensureSystemUser(ctx, app, cfg); err != nil {
		return err
	}
	if cfg.Kubernetes.IngressOwnership == config.IngressOwnershipMenina && cfg.TLS.Terminate {
		for _, addr := range []string{cfg.Platform.HTTPListen, cfg.Platform.HTTPSListen} {
			port, err := infrastructure.ParseListenPort(addr)
			if err != nil {
				return err
			}
			if err := infrastructure.PortFree(ctx, port); err != nil {
				return fmt.Errorf("cannot own public ingress: %w (remediation: stop the component holding the port, or select external ingress ownership)", err)
			}
		}
	}
	runner := infrastructure.ExecRunner{}
	k3 := infrastructure.NewK3s(runner, app.Log)
	if cfg.Kubernetes.Managed {
		if err := k3.EnsureInstalled(ctx, cfg.Kubernetes.DisableTraefik); err != nil {
			return err
		}
		resolveKubeconfig(cfg)
		if err := waitForKubernetes(ctx, app, cfg, 3*time.Minute); err != nil {
			return err
		}
	} else {
		app.Infof("Using existing local cluster (kubeconfig=%s)", orDefault(cfg.Kubernetes.Kubeconfig, "default"))
	}
	if err := ensureServiceKubeconfig(ctx, app, cfg); err != nil {
		return err
	}

	storage := infrastructure.NewStorage(cfg.Host.DataDir)
	if err := storage.EnsureDirs(); err != nil {
		return err
	}
	if err := applyServicePermissions(app, cfg); err != nil {
		app.Log.Warn("could not apply service permissions", "error", err)
	}
	app.Infof("Ensured data directories under %s", cfg.Host.DataDir)
	group := orDefault(cfg.Platform.Group, "menina")
	app.Infof("CLI access is granted to members of the %q group (sudo usermod -aG %s $USER)", group, group)

	if cfg.Registry.Enabled {
		username, password, err := resolveRegistryCreds(cfg)
		if err != nil {
			return err
		}
		registriesYAML := buildRegistriesYAML(cfg, username, password)
		if registriesYAML != "" {
			if err := k3.WriteRegistriesConfig(ctx, registriesConfigPath, registriesYAML); err != nil {
				app.Log.Warn("could not write k3s registries.yaml", "error", err)
			} else {
				app.Infof("Wrote %s", registriesConfigPath)
				if cfg.Kubernetes.Managed {
					if err := k3.Restart(ctx); err != nil {
						app.Log.Warn("could not restart k3s to load registries.yaml", "error", err)
					} else if err := waitForKubernetes(ctx, app, cfg, 3*time.Minute); err != nil {
						return err
					}
				}
			}
		}
		if err := deployZot(ctx, app, cfg); err != nil {
			return err
		}
	}
	return nil
}

func ensureZotAuthSecret(ctx context.Context, cluster deployment.Cluster, cfg *config.Config) (string, string, error) {
	if !cfg.Registry.Auth.Enabled {
		return "", "", nil
	}
	user, password, err := resolveRegistryCreds(cfg)
	if err != nil {
		return "", "", err
	}
	if password == "" {
		return "", "", fmt.Errorf("registry auth is enabled but no password is available")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	htpasswd := fmt.Sprintf("%s:%s\n", user, string(hash))
	if err := cluster.EnsureSecret(ctx, zotNamespace(cfg), zotSecretName, map[string][]byte{"htpasswd": []byte(htpasswd)}); err != nil {
		return "", "", err
	}
	return zotSecretName, zotAuthMount, nil
}

func zotEventEndpoint(ctx context.Context, app *App, cfg *config.Config) (string, error) {
	if cfg.Registry.Notifications.Endpoint != "" {
		return cfg.Registry.Notifications.Endpoint, nil
	}
	path := orDefault(cfg.Registry.Notifications.Path, "/v2/events")
	port := "9000"
	if _, p, err := net.SplitHostPort(cfg.Platform.EventsListen); err == nil && p != "" {
		port = p
	}
	if ip, err := deployment.NodeInternalIP(ctx, cfg.Kubernetes.Kubeconfig); err == nil && ip != "" {
		return fmt.Sprintf("http://%s:%s%s", ip, port, path), nil
	}
	return "", fmt.Errorf("could not determine a node address for the Zot event sink; set registry.notifications.endpoint explicitly (the cluster must be able to reach Menina's registry listener)")
}

func deployZot(ctx context.Context, app *App, cfg *config.Config) error {
	if err := registry.ValidateAgainstZotVersion(cfg.Registry.ZotVersion); err != nil {
		return err
	}
	cluster, err := deployment.NewKubernetesCluster(cfg.Kubernetes.Kubeconfig)
	if err != nil {
		return fmt.Errorf("connect to Kubernetes to deploy Zot: %w", err)
	}
	if err := cluster.Ping(ctx); err != nil {
		return fmt.Errorf("Kubernetes API unreachable: %w", err)
	}
	ns := zotNamespace(cfg)
	if err := cluster.EnsureNamespace(ctx, ns); err != nil {
		return err
	}
	eventEndpoint, err := zotEventEndpoint(ctx, app, cfg)
	if err != nil {
		return err
	}
	params := registry.ZotParams{
		RootDirectory:    zotRootDir,
		ContainerPort:    zotContainerPort,
		EventAddress:     eventEndpoint,
		KeepRecentTags:   cfg.Registry.Retention.KeepRecentTags,
		RetentionEnabled: cfg.Registry.Retention.Enabled,
	}
	if cfg.Registry.Notifications.TokenRef != "" {
		token, err := secrets.NewResolver().Resolve(cfg.Registry.Notifications.TokenRef)
		if err != nil {
			return err
		}
		params.EventToken = token
	}
	w := deployment.Workload{
		Name:          "zot",
		Namespace:     ns,
		Image:         cfg.Registry.ZotImage + ":" + cfg.Registry.ZotVersion,
		Port:          5000,
		ContainerPort: 5000,
		Replicas:      1,
		ConfigMap:     zotConfigMapName,
		ConfigMount:   zotConfigMount,
		StorageSize:   orDefault(cfg.Registry.PVCSize, "10Gi"),
		MountPath:     zotRootDir,
	}
	secret, mount, err := ensureZotAuthSecret(ctx, cluster, cfg)
	if err != nil {
		return err
	}
	if secret != "" {
		params.HtpasswdPath = mount + "/htpasswd"
		w.SecretName = secret
		w.SecretMount = mount
	}
	zotJSON, err := registry.BuildZotConfig(params)
	if err != nil {
		return err
	}
	if err := cluster.EnsureConfigMap(ctx, ns, zotConfigMapName, map[string]string{"config.json": string(zotJSON)}); err != nil {
		return err
	}
	if err := cluster.EnsurePVC(ctx, ns, zotPVCName, orDefault(cfg.Registry.PVCSize, "10Gi"), cfg.Registry.StorageClass); err != nil {
		return err
	}
	if err := cluster.ApplyWorkload(ctx, w); err != nil {
		return err
	}
	if err := cluster.ApplyService(ctx, w); err != nil {
		return err
	}
	if _, err := deployment.WaitForRollout(ctx, cluster, ns, "zot", deployment.RolloutOptions{Timeout: 5 * time.Minute}); err != nil {
		return fmt.Errorf("Zot did not become ready: %w", err)
	}
	app.Infof("Zot ready in namespace %s (image %s)", ns, w.Image)
	return nil
}

func installServices(ctx context.Context, app *App, systemd *infrastructure.Systemd, services []string, vars map[string]string) error {
	var failed []string
	for _, name := range services {
		content, err := deployassets.RenderUnit(name, vars)
		if err != nil {
			return err
		}
		if err := systemd.Apply(ctx, name, content); err != nil {
			app.Log.Error("failed to install or start service", "unit", name, "error", err)
			failed = append(failed, name)
			continue
		}
		if !systemd.IsActive(ctx, name) {
			app.Log.Error("service is not active after start", "unit", name)
			failed = append(failed, name)
			continue
		}
		app.Infof("Installed and started %s", name)
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed to install/start required services: %s", strings.Join(failed, ", "))
	}
	return nil
}

func deployPlatform(ctx context.Context, app *App, cfg *config.Config) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("menina deploy installs system services and must run as root (try: sudo menina deploy platform)")
	}
	if cfg.Registry.Enabled {
		cluster, err := deployment.NewKubernetesCluster(cfg.Kubernetes.Kubeconfig)
		if err != nil {
			return fmt.Errorf("cannot verify the registry before deploying platform services: %w", err)
		}
		if err := cluster.Ping(ctx); err != nil {
			return fmt.Errorf("Kubernetes API unreachable: %w", err)
		}
		ns := orDefault(cfg.Registry.Namespace, defaultRegistryNS)
		if ip, err := cluster.ServiceClusterIP(ctx, ns, "zot"); err != nil || ip == "" {
			return fmt.Errorf("registry is enabled but the Zot Service (%s/zot) was not found; run `menina deploy infra` first", ns)
		}
	}
	systemd := infrastructure.NewSystemd(infrastructure.ExecRunner{})
	binary, err := os.Executable()
	if err != nil {
		binary = "/usr/local/bin/menina"
	}
	vars := map[string]string{
		"USER":       orDefault(cfg.Platform.User, "menina"),
		"GROUP":      orDefault(cfg.Platform.Group, "menina"),
		"BINARY":     binary,
		"CONFIG_DIR": cfg.Host.ConfigDir,
		"DATA_DIR":   cfg.Host.DataDir,
	}
	services := []string{"menina-serve.service"}
	if cfg.TLS.Terminate || cfg.Networking.Mode == config.NetworkingPublic || cfg.Networking.Mode == config.NetworkingCloudflareTunnel {
		services = append(services, "menina-proxy.service")
	}
	if err := installServices(ctx, app, systemd, services, vars); err != nil {
		return err
	}
	if cfg.Networking.Mode == config.NetworkingCloudflareTunnel {
		app.Infof("Cloudflare Tunnel mode: ensure cloudflared routes the configured hostnames to origin %s", orDefault(cfg.Networking.Cloudflare.OriginURL, "http://127.0.0.1:80"))
	}
	if cfg.Networking.Mode == config.NetworkingPublic {
		if err := configurePublicDNS(ctx, app, cfg); err != nil {
			app.Log.Warn("DNS configuration skipped", "error", err)
		}
	}
	app.Infof("Platform deployment complete. Run `menina status` to verify.")
	return nil
}

func configurePublicDNS(ctx context.Context, app *App, cfg *config.Config) error {
	if cfg.Networking.Cloudflare.APITokenSecretRef == "" {
		app.Infof("No Cloudflare API token configured; skipping DNS automation (configure A/CNAME records manually)")
		return nil
	}
	token, err := secrets.NewResolver().Resolve(cfg.Networking.Cloudflare.APITokenSecretRef)
	if err != nil {
		return err
	}
	ip := cfg.Networking.PublicIP
	if ip == "" {
		ip = detectPublicIP(ctx)
	}
	if ip == "" {
		app.Log.Warn("could not determine the public IP; skipping DNS automation")
		return nil
	}
	client := infrastructure.NewCloudflareClient(token)
	zone := cfg.Networking.Cloudflare.Zone
	for _, domain := range dnsTargets(cfg, exposureHosts(cfg)) {
		z := zone
		if z == "" {
			z = baseZone(domain)
		}
		zoneID, err := client.FindZoneID(ctx, z)
		if err != nil {
			return fmt.Errorf("find zone for %s: %w", domain, err)
		}
		if err := client.UpsertDNSRecord(ctx, zoneID, "A", domain, ip, cfg.Networking.Cloudflare.DNSProxied); err != nil {
			return fmt.Errorf("upsert DNS record %s: %w", domain, err)
		}
		app.Infof("DNS: %s -> %s (zone %s)", domain, ip, z)
	}
	return nil
}

func dnsTargets(cfg *config.Config, extraHosts []string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" {
			return
		}
		if _, ok := seen[d]; ok {
			return
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	for _, d := range cfg.Networking.BaseDomains {
		add(d)
	}
	if cfg.Registry.Enabled {
		add(cfg.Registry.Domain)
	}
	for _, h := range extraHosts {
		add(h)
	}
	return out
}

// exposureHosts returns every hostname exposed by the resolved configuration,
// so automatic DNS management covers application hostnames (not just base
// domains and the registry).
func exposureHosts(cfg *config.Config) []string {
	projects, err := config.LoadAllProjects(config.ProjectsDir(cfg.Host.ConfigDir))
	if err != nil {
		return nil
	}
	resolved, err := config.NewResolver(cfg).Resolve(projects, 0)
	if err != nil {
		return nil
	}
	var hosts []string
	for _, rp := range resolved.Projects {
		for _, re := range rp.Environments {
			for _, app := range re.Apps {
				if app.Enabled && app.Exposure.Enabled {
					hosts = append(hosts, app.Exposure.Hosts...)
				}
			}
		}
	}
	return hosts
}

var twoLabelPublicSuffixes = map[string]struct{}{
	"co.uk": {}, "org.uk": {}, "ac.uk": {}, "gov.uk": {},
	"com.au": {}, "net.au": {}, "org.au": {},
	"com.br": {}, "com.mx": {}, "com.ar": {},
	"co.jp": {}, "co.nz": {}, "co.za": {}, "co.in": {},
}

func baseZone(domain string) string {
	labels := strings.Split(strings.TrimSpace(domain), ".")
	if len(labels) <= 2 {
		return strings.Join(labels, ".")
	}
	lastTwo := strings.Join(labels[len(labels)-2:], ".")
	if _, ok := twoLabelPublicSuffixes[lastTwo]; ok && len(labels) >= 3 {
		return strings.Join(labels[len(labels)-3:], ".")
	}
	return lastTwo
}

func detectPublicIP(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return ""
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(string(data))
	if net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}

// applyServicePermissions implements the privilege model: the data directory is
// owned by the service user, while the configuration directory is owned by
// root but group-writable by the "menina" group so administrators can use the
// CLI without sudo after being added to that group.
func applyServicePermissions(app *App, cfg *config.Config) error {
	if os.Geteuid() != 0 {
		return nil
	}
	userName := orDefault(cfg.Platform.User, "menina")
	groupName := orDefault(cfg.Platform.Group, "menina")
	u, err := user.Lookup(userName)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	if g, err := user.LookupGroup(groupName); err == nil {
		if v, err := strconv.Atoi(g.Gid); err == nil {
			gid = v
		}
	}
	dataPerms := os.FileMode(0o750)
	if err := filepath.Walk(cfg.Host.DataDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := os.Chown(path, uid, gid); err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(path, dataPerms)
		}
		return nil
	}); err != nil {
		return err
	}
	return filepath.Walk(cfg.Host.ConfigDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := os.Chown(path, 0, gid); err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(path, 0o770|os.ModeSetgid)
		}
		if filepath.Base(path) == "kubeconfig" || filepath.Base(cfg.Security.ControlTokenFile) == filepath.Base(path) {
			return os.Chmod(path, 0o640)
		}
		return os.Chmod(path, 0o660)
	})
}

func ensureSystemUser(ctx context.Context, app *App, cfg *config.Config) error {
	name := orDefault(cfg.Platform.User, "menina")
	group := orDefault(cfg.Platform.Group, name)
	um := infrastructure.NewUserManager(infrastructure.ExecRunner{})
	if err := um.Ensure(ctx, name); err != nil {
		return fmt.Errorf("ensure service user %q: %w", name, err)
	}
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		if err := um.AddToGroup(ctx, sudoUser, group); err != nil {
			app.Log.Warn("could not add invoking user to the menina group", "user", sudoUser, "group", group, "error", err)
		} else {
			app.Infof("Added %s to the %q group (log out and back in to use the CLI without sudo)", sudoUser, group)
		}
	}
	app.Log.Info("ensured service user", "user", name)
	return nil
}

func resolveKubeconfig(cfg *config.Config) {
	if cfg.Kubernetes.Kubeconfig != "" {
		return
	}
	for _, candidate := range []string{"/etc/rancher/k3s/k3s.yaml", "/etc/rancher/rke2/rke2.yaml"} {
		if _, err := os.Stat(candidate); err == nil {
			cfg.Kubernetes.Kubeconfig = candidate
			return
		}
	}
}

func waitForKubernetes(ctx context.Context, app *App, cfg *config.Config, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		cluster, err := deployment.NewKubernetesCluster(cfg.Kubernetes.Kubeconfig)
		if err == nil {
			if err := cluster.Ping(ctx); err == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("kubernetes API did not become ready within %s (kubeconfig=%s)", timeout, cfg.Kubernetes.Kubeconfig)
		}
		app.Log.Info("waiting for the Kubernetes API to become ready...")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// ensureServiceKubeconfig provisions a scoped Kubernetes identity for the
// service (ServiceAccount + ClusterRole/Binding + token) and writes a kubeconfig
// that uses only that identity. It never falls back to the admin kubeconfig.
func ensureServiceKubeconfig(ctx context.Context, app *App, cfg *config.Config) error {
	if os.Geteuid() != 0 {
		return nil
	}
	src := cfg.Kubernetes.Kubeconfig
	if src == "" {
		resolveKubeconfig(cfg)
		src = cfg.Kubernetes.Kubeconfig
	}
	if src == "" {
		return fmt.Errorf("no Kubernetes kubeconfig found; cannot provision the Menina service identity")
	}
	ns := orDefault(cfg.Platform.Namespace, defaultPlatformNS)
	data, err := deployment.ProvisionServiceAccess(ctx, src, ns, "menina")
	if err != nil {
		return fmt.Errorf("provision scoped Kubernetes access (refusing to use the admin kubeconfig): %w", err)
	}
	dst := filepath.Join(cfg.Host.ConfigDir, "kubeconfig")
	if err := os.MkdirAll(cfg.Host.ConfigDir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o640); err != nil {
		return err
	}
	if err := chownToService(cfg, dst); err != nil {
		app.Log.Warn("could not chown kubeconfig for the service user", "error", err)
	}
	cfg.Kubernetes.Kubeconfig = dst
	if err := config.SavePlatform(cfg.Host.ConfigDir, cfg); err != nil {
		return err
	}
	return nil
}

func chownToService(cfg *config.Config, path string) error {
	u, err := user.Lookup(orDefault(cfg.Platform.User, "menina"))
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return os.Chown(path, uid, gid)
}

func buildRegistriesYAML(cfg *config.Config, username, password string) string {
	if !cfg.Registry.Enabled || cfg.Registry.Domain == "" {
		return ""
	}
	scheme := "https"
	if !cfg.TLS.Terminate {
		scheme = "http"
	}
	var b []byte
	b = append(b, "mirrors:\n"...)
	b = append(b, fmt.Sprintf("  %q:\n", cfg.Registry.Domain)...)
	b = append(b, "    endpoint:\n"...)
	b = append(b, fmt.Sprintf("      - %q\n", scheme+"://"+cfg.Registry.Domain)...)
	if username != "" {
		b = append(b, "configs:\n"...)
		b = append(b, fmt.Sprintf("  %q:\n", cfg.Registry.Domain)...)
		b = append(b, "    auth:\n"...)
		b = append(b, fmt.Sprintf("      username: %q\n", username)...)
		b = append(b, fmt.Sprintf("      password: %q\n", password)...)
		if scheme == "http" {
			b = append(b, "    tls:\n"...)
			b = append(b, "      insecure_skip_verify: true\n"...)
		}
	}
	return string(b)
}

// applyRegistryCredentials reconciles the Zot htpasswd secret, the k3s
// containerd credentials and rolls managed workloads so they pick up rotated
// credentials. It is used by credential rotation.
func applyRegistryCredentials(ctx context.Context, app *App, cfg *config.Config) error {
	username, password, err := resolveRegistryCreds(cfg)
	if err != nil {
		return err
	}
	k3 := infrastructure.NewK3s(infrastructure.ExecRunner{}, app.Log)
	if err := k3.WriteRegistriesConfig(ctx, registriesConfigPath, buildRegistriesYAML(cfg, username, password)); err != nil {
		return fmt.Errorf("write %s: %w", registriesConfigPath, err)
	}
	app.Infof("Updated %s", registriesConfigPath)
	if cfg.Kubernetes.Managed {
		if err := k3.Restart(ctx); err != nil {
			app.Log.Warn("could not restart k3s to load rotated credentials", "error", err)
		} else if err := waitForKubernetes(ctx, app, cfg, 3*time.Minute); err != nil {
			return err
		}
	}

	cluster, err := deployment.NewKubernetesCluster(cfg.Kubernetes.Kubeconfig)
	if err != nil {
		app.Log.Warn("registry credential rotated on disk, but Kubernetes was unreachable; re-run `menina deploy` to apply it", "error", err)
		return nil
	}
	if _, _, err := ensureZotAuthSecret(ctx, cluster, cfg); err != nil {
		return err
	}
	workloads, err := cluster.ListWorkloads(ctx)
	if err != nil {
		app.Log.Warn("could not list managed workloads to roll", "error", err)
		return nil
	}
	for _, w := range workloads {
		if w.Namespace == zotNamespace(cfg) {
			continue
		}
		if err := cluster.RestartWorkload(ctx, w.Namespace, w.Name); err != nil {
			app.Log.Warn("could not restart workload", "namespace", w.Namespace, "workload", w.Name, "error", err)
		}
	}
	app.Infof("Applied registry credentials to Zot and rolled %d managed workload(s)", len(workloads))
	return nil
}
