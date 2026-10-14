package config

import "time"

const (
	IngressOwnershipMenina   = "menina"
	IngressOwnershipExternal = "external"

	NetworkingPublic           = "public"
	NetworkingCloudflareTunnel = "cloudflare-tunnel"

	TLSModeNone        = "none"
	TLSModeACME        = "acme"
	TLSModeManual      = "manual"
	TLSModeCertManager = "cert-manager"

	WorkloadService = "service"
	WorkloadWorker  = "worker"
)

type Config struct {
	Host       Host       `yaml:"host"`
	Kubernetes Kubernetes `yaml:"kubernetes"`
	Registry   Registry   `yaml:"registry"`
	Networking Networking `yaml:"networking"`
	TLS        TLS        `yaml:"tls"`
	Platform   Platform   `yaml:"platform"`
	Security   Security   `yaml:"security"`
	Defaults   Defaults   `yaml:"defaults"`
	Webhooks   Webhooks   `yaml:"webhooks"`
}

// Webhooks configures outbound notifications. Deploy webhooks POST a generic
// JSON event to an operator-supplied receiver (which may itself fan out to
// Slack, Discord, Sentry, ...). Menina speaks one provider-agnostic format.
type Webhooks struct {
	Deploy DeployWebhook `yaml:"deploy"`
}

type DeployWebhook struct {
	Enabled   bool     `yaml:"enabled"`
	URLRef    string   `yaml:"urlRef"`
	SecretRef string   `yaml:"secretRef"`
	Events    []string `yaml:"events"`
}

// DefaultDeployEvents are emitted when a deploy webhook is enabled but does not
// specify an event list.
var DefaultDeployEvents = []string{"deploy.succeeded", "deploy.failed", "deploy.rolled_back"}

type Host struct {
	Hostname  string `yaml:"hostname"`
	ConfigDir string `yaml:"configDir"`
	DataDir   string `yaml:"dataDir"`
	OS        string `yaml:"os"`
	Arch      string `yaml:"arch"`
}

type Kubernetes struct {
	Managed          bool   `yaml:"managed"`
	Kubeconfig       string `yaml:"kubeconfig"`
	IngressOwnership string `yaml:"ingressOwnership"`
	DisableTraefik   bool   `yaml:"disableTraefik"`
}

type RegistryAuth struct {
	Enabled     bool   `yaml:"enabled"`
	Username    string `yaml:"username"`
	PasswordRef string `yaml:"passwordSecretRef"`
}

type Retention struct {
	KeepRecentTags   int      `yaml:"keepRecentTags"`
	ProtectedDigests []string `yaml:"protectedDigests"`
	Enabled          bool     `yaml:"enabled"`
}

type Notifications struct {
	Enabled  bool   `yaml:"enabled"`
	TokenRef string `yaml:"tokenRef"`
	Path     string `yaml:"path"`
	Endpoint string `yaml:"endpoint"`
}

type Registry struct {
	Enabled       bool          `yaml:"enabled"`
	Domain        string        `yaml:"domain"`
	StorageDir    string        `yaml:"storageDir"`
	Auth          RegistryAuth  `yaml:"auth"`
	Retention     Retention     `yaml:"retention"`
	Notifications Notifications `yaml:"notifications"`
	ZotImage      string        `yaml:"zotImage"`
	ZotVersion    string        `yaml:"zotVersion"`
	Namespace     string        `yaml:"namespace"`
	PVCSize       string        `yaml:"pvcSize"`
	StorageClass  string        `yaml:"storageClass"`
}

type Cloudflare struct {
	APITokenSecretRef string `yaml:"apiTokenSecretRef"`
	AccountID         string `yaml:"accountId"`
	TunnelID          string `yaml:"tunnelId"`
	Zone              string `yaml:"zone"`
	DNSProxied        bool   `yaml:"dnsProxied"`
	OriginURL         string `yaml:"originUrl"`
	OriginProto       string `yaml:"originProto"`
	OriginHost        string `yaml:"originHost"`
}

type Networking struct {
	Mode        string     `yaml:"mode"`
	BaseDomains []string   `yaml:"baseDomains"`
	PublicIP    string     `yaml:"publicIp"`
	Cloudflare  Cloudflare `yaml:"cloudflare"`
}

type ACME struct {
	Email        string `yaml:"email"`
	DirectoryURL string `yaml:"directoryUrl"`
	StorageDir   string `yaml:"storageDir"`
}

type TLS struct {
	Terminate    bool   `yaml:"terminate"`
	Mode         string `yaml:"mode"`
	HTTPRedirect bool   `yaml:"httpRedirect"`
	CertDir      string `yaml:"certDir"`
	ACME         ACME   `yaml:"acme"`
}

type Platform struct {
	DBPath            string        `yaml:"dbPath"`
	User              string        `yaml:"user"`
	Group             string        `yaml:"group"`
	Namespace         string        `yaml:"namespace"`
	ControlSocket     string        `yaml:"controlSocket"`
	EventsListen      string        `yaml:"eventsListen"`
	HTTPListen        string        `yaml:"httpListen"`
	HTTPSListen       string        `yaml:"httpsListen"`
	ReconcileInterval time.Duration `yaml:"reconcileInterval"`
	Workers           int           `yaml:"workers"`
}

type Security struct {
	ControlTokenFile  string   `yaml:"controlTokenFile"`
	TrustedProxies    []string `yaml:"trustedProxies"`
	EnforceProdPolicy bool     `yaml:"enforceProdPolicy"`
}

type Defaults struct {
	Namespace        string `yaml:"namespace"`
	ImageTagStrategy string `yaml:"imageTagStrategy"`
	Replicas         int    `yaml:"replicas"`
	CPU              string `yaml:"cpu"`
	Memory           string `yaml:"memory"`
}

func Default() Config {
	return Config{
		Host: Host{
			ConfigDir: "/etc/menina",
			DataDir:   "/var/lib/menina",
		},
		Kubernetes: Kubernetes{
			Managed:          true,
			IngressOwnership: IngressOwnershipMenina,
			DisableTraefik:   true,
		},
		Registry: Registry{
			Enabled:    true,
			Domain:     "registry.moureau.dev",
			StorageDir: "/var/lib/menina/registry",
			ZotImage:   "ghcr.io/project-zot/zot",
			ZotVersion: "v2.1.22",
			Namespace:  "menina-registry",
			PVCSize:    "10Gi",
			Retention:  Retention{Enabled: true, KeepRecentTags: 3},
			Notifications: Notifications{
				Enabled: true,
				Path:    "/v2/events",
			},
		},
		Networking: Networking{
			Mode: NetworkingPublic,
			Cloudflare: Cloudflare{
				OriginProto: "http",
			},
		},
		TLS: TLS{
			Terminate:    true,
			Mode:         TLSModeACME,
			HTTPRedirect: true,
			CertDir:      "/var/lib/menina/certs",
		},
		Platform: Platform{
			DBPath:            "/var/lib/menina/menina.db",
			User:              "menina",
			Group:             "menina",
			Namespace:         "menina-system",
			ControlSocket:     "/run/menina/control.sock",
			EventsListen:      ":9000",
			HTTPListen:        ":80",
			HTTPSListen:       ":443",
			ReconcileInterval: 5 * time.Minute,
			Workers:           4,
		},
		Security: Security{
			ControlTokenFile:  "/run/menina/control.token",
			EnforceProdPolicy: true,
		},
		Defaults: Defaults{
			Namespace:        "menina-apps",
			ImageTagStrategy: "environment",
			Replicas:         1,
			CPU:              "250m",
			Memory:           "256Mi",
		},
	}
}
