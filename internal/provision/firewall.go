package provision

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// PortRule is a host TCP/UDP port to open in the host firewall.
type PortRule struct {
	Port     int
	Protocol string // "TCP" or "UDP"
}

func (r PortRule) String() string { return fmt.Sprintf("%d/%s", r.Port, strings.ToLower(r.Protocol)) }

// OpenHostPorts makes the given L4 ports reachable from outside, best-effort:
// it opens them in ufw when an active ufw is present, otherwise it logs the
// ports the operator must open manually. External firewalls (e.g. a cloud
// provider's) cannot be managed from here.
func OpenHostPorts(ctx context.Context, runner CommandRunner, log *slog.Logger, rules []PortRule) {
	if len(rules) == 0 {
		return
	}
	seen := map[string]struct{}{}
	var uniq []PortRule
	for _, r := range rules {
		if _, ok := seen[r.String()]; ok {
			continue
		}
		seen[r.String()] = struct{}{}
		uniq = append(uniq, r)
	}

	out, err := runner.Run(ctx, "ufw", "status")
	if err == nil && strings.Contains(strings.ToLower(out), "status: active") {
		for _, r := range uniq {
			if _, err := runner.Run(ctx, "ufw", "allow", r.String()); err != nil {
				log.Warn("could not open firewall port with ufw", "port", r.String(), "error", err)
			} else {
				log.Info("opened firewall port", "port", r.String())
			}
		}
		return
	}

	names := make([]string, len(uniq))
	for i, r := range uniq {
		names[i] = r.String()
	}
	log.Info("ensure these ports are open in your firewall for L4-exposed apps", "ports", strings.Join(names, ", "))
}
