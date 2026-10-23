package config

import (
	"fmt"
	"net"
	"regexp"
	"sort"
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
	seenPorts := map[string]string{}
	for _, rp := range rc.Projects {
		for _, re := range rp.Environments {
			if err := ValidateDependencies(re); err != nil {
				return err
			}
			for _, ra := range re.Apps {
				if !ra.Enabled {
					continue
				}
				if err := validateAppReleaseSpec(ra); err != nil {
					return err
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
				if err := validateExposedPorts(ra, seenPorts); err != nil {
					return err
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

// validateExposedPorts checks the app's raw TCP/UDP exposure and records each
// port so two apps cannot claim the same host port.
func validateAppReleaseSpec(ra *ResolvedApp) error {
	if ra.Verification != nil && ra.Verification.Enabled != nil && *ra.Verification.Enabled {
		if ra.Verification.Path != "" {
			if ra.Workload.Type != WorkloadService {
				return fmt.Errorf("app %s/%s/%s is a worker and cannot verify an HTTP path", ra.Project, ra.Environment, ra.Name)
			}
			if !strings.HasPrefix(ra.Verification.Path, "/") {
				return fmt.Errorf("app %s/%s/%s verification.path must start with /", ra.Project, ra.Environment, ra.Name)
			}
			if !ra.Exposure.Enabled || len(ra.Exposure.Hosts) == 0 {
				return fmt.Errorf("app %s/%s/%s verification.path requires HTTP exposure with hosts", ra.Project, ra.Environment, ra.Name)
			}
		}
		if len(ra.Verification.ExpectStatus) > 0 {
			for _, status := range ra.Verification.ExpectStatus {
				if status < 100 || status > 599 {
					return fmt.Errorf("app %s/%s/%s verification.expectStatus %d is not an HTTP status", ra.Project, ra.Environment, ra.Name, status)
				}
			}
		}
	}
	for _, state := range ra.DependsOn {
		if state == "" {
			return fmt.Errorf("app %s/%s/%s has an empty dependsOn entry", ra.Project, ra.Environment, ra.Name)
		}
	}
	return nil
}

// ValidateDependencies rejects dependency cycles within an environment.
func ValidateDependencies(re *ResolvedEnvironment) error {
	visiting := map[string]bool{}
	done := map[string]bool{}
	var walk func(string, []string) error
	walk = func(name string, path []string) error {
		if done[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("dependency cycle in %s/%s: %s", re.Project, re.Name, strings.Join(append(path, name), " -> "))
		}
		visiting[name] = true
		app := re.Apps[name]
		if app != nil {
			for _, dep := range app.DependsOn {
				if _, ok := re.Apps[dep]; !ok {
					return fmt.Errorf("app %s/%s/%s depends on %q which is not in the environment", re.Project, re.Name, name, dep)
				}
				if err := walk(dep, append(path, name)); err != nil {
					return err
				}
			}
		}
		visiting[name] = false
		done[name] = true
		return nil
	}
	names := make([]string, 0, len(re.Apps))
	for name := range re.Apps {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := walk(name, nil); err != nil {
			return err
		}
	}
	return nil
}

func validateExposedPorts(ra *ResolvedApp, seen map[string]string) error {
	owner := fmt.Sprintf("%s/%s/%s", ra.Project, ra.Environment, ra.Name)
	check := func(proto string, ports []int) error {
		for _, p := range ports {
			if p < 1 || p > 65535 {
				return fmt.Errorf("app %s exposes invalid %s port %d", owner, proto, p)
			}
			if proto == "tcp" && (p == 80 || p == 443) {
				return fmt.Errorf("app %s cannot expose tcp port %d: it is the Menina proxy's public port", owner, p)
			}
			key := proto + "/" + fmt.Sprintf("%d", p)
			if other, ok := seen[key]; ok {
				return fmt.Errorf("%s port %d is exposed by both %s and %s", proto, p, other, owner)
			}
			seen[key] = owner
		}
		return nil
	}
	if err := check("tcp", ra.Exposure.Ports.TCP); err != nil {
		return err
	}
	return check("udp", ra.Exposure.Ports.UDP)
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
