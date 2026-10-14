package config

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

var hostnameRe = regexp.MustCompile(`^(\*\.)?([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

func ValidatePlatform(cfg *Config) error {
	var errs []string
	if cfg.Host.DataDir == "" {
		errs = append(errs, "host.dataDir is required")
	}
	if cfg.Host.ConfigDir == "" {
		errs = append(errs, "host.configDir is required")
	}
	switch cfg.Kubernetes.IngressOwnership {
	case "", IngressOwnershipMenina, IngressOwnershipExternal:
	default:
		errs = append(errs, fmt.Sprintf("kubernetes.ingressOwnership %q is invalid", cfg.Kubernetes.IngressOwnership))
	}
	switch cfg.Networking.Mode {
	case "", NetworkingPublic, NetworkingCloudflareTunnel:
	default:
		errs = append(errs, fmt.Sprintf("networking.mode %q is invalid", cfg.Networking.Mode))
	}
	switch cfg.TLS.Mode {
	case "", TLSModeNone, TLSModeACME, TLSModeManual, TLSModeCertManager:
	default:
		errs = append(errs, fmt.Sprintf("tls.mode %q is invalid", cfg.TLS.Mode))
	}
	if cfg.TLS.Terminate && cfg.TLS.Mode == TLSModeCertManager {
		errs = append(errs, "tls.mode cert-manager cannot terminate TLS in a host-level proxy; use manual or acme, or disable termination")
	}
	if cfg.Networking.Mode == NetworkingCloudflareTunnel {
		if cfg.TLS.Terminate && cfg.TLS.Mode == TLSModeACME {
			errs = append(errs, "cloudflare tunnel mode terminates public TLS at Cloudflare; disable tls.terminate (or use manual certificates at the origin)")
		}
		if cfg.Networking.Cloudflare.OriginURL == "" {
			errs = append(errs, "networking.cloudflare.originUrl is required for tunnel mode")
		} else {
			wantProto := "http"
			if cfg.TLS.Terminate {
				wantProto = "https"
			}
			if cfg.Networking.Cloudflare.OriginProto != "" && cfg.Networking.Cloudflare.OriginProto != wantProto {
				errs = append(errs, fmt.Sprintf("networking.cloudflare.originProto %q does not match tls.terminate=%v (expected %q)", cfg.Networking.Cloudflare.OriginProto, cfg.TLS.Terminate, wantProto))
			}
		}
	}
	if cfg.Registry.Enabled {
		if cfg.Registry.Domain == "" {
			errs = append(errs, "registry.domain is required when registry is enabled")
		}
		if cfg.Registry.StorageDir == "" {
			errs = append(errs, "registry.storageDir is required when registry is enabled")
		}
		if cfg.Registry.Notifications.Enabled && cfg.Registry.Notifications.TokenRef == "" {
			errs = append(errs, "registry.notifications is enabled but no tokenRef is configured; refusing an unauthenticated webhook (run `menina setup` or set registry.notifications.tokenRef, or disable notifications)")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func ValidateResolved(rc *ResolvedConfig) error {
	seenHosts := map[string]string{}
	for _, rp := range rc.Projects {
		for _, re := range rp.Environments {
			for _, ra := range re.Apps {
				if !ra.Enabled {
					continue
				}
				if ra.Repository == "" {
					return fmt.Errorf("app %s/%s/%s has no image repository", ra.Project, ra.Environment, ra.Name)
				}
				if ra.Workload.Type != WorkloadService && ra.Workload.Type != WorkloadWorker {
					return fmt.Errorf("app %s/%s/%s has invalid workload type %q", ra.Project, ra.Environment, ra.Name, ra.Workload.Type)
				}
				if ra.Workload.Type == WorkloadService && ra.Workload.Port == 0 {
					return fmt.Errorf("service app %s/%s/%s must declare workload.port", ra.Project, ra.Environment, ra.Name)
				}
				for _, h := range ra.Exposure.Hosts {
					if strings.Contains(h, "*") {
						return fmt.Errorf("app %s/%s/%s uses wildcard host %q: wildcards are not supported; list explicit hostnames (routing and ACME use individual hostnames)", ra.Project, ra.Environment, ra.Name, h)
					}
					if !hostnameRe.MatchString(h) {
						return fmt.Errorf("app %s/%s/%s has invalid exposure host %q", ra.Project, ra.Environment, ra.Name, h)
					}
					key := strings.ToLower(h)
					if owner, ok := seenHosts[key]; ok {
						return fmt.Errorf("exposure host %q is claimed by both %s and %s/%s/%s", h, owner, ra.Project, ra.Environment, ra.Name)
					}
					seenHosts[key] = fmt.Sprintf("%s/%s/%s", ra.Project, ra.Environment, ra.Name)
				}
			}
		}
	}
	return nil
}

func ValidateHostPort(host string) error {
	_, port, err := net.SplitHostPort(host)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", host, err)
	}
	if port == "" {
		return fmt.Errorf("listen address %q has no port", host)
	}
	return nil
}
