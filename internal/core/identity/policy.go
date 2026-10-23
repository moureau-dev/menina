package identity

import "strings"

// Target identifies the resource an action applies to.
type Target struct {
	Project     string
	Environment string
	App         string
}

// Decision is the result of an authorization check.
type Decision struct {
	Allowed bool
	Reason  string
}

// Policy evaluates scopes for an action and target.
type Policy struct {
	Scopes []string
}

// Authorize returns whether the policy permits the action on the target.
func (p Policy) Authorize(required Scope, target Target) Decision {
	if required == ScopeAdmin {
		for _, scope := range p.Scopes {
			if scope == string(ScopeAdmin) {
				return Decision{Allowed: true}
			}
		}
		return Decision{Reason: "admin scope is required"}
	}
	for _, raw := range p.Scopes {
		if raw == string(ScopeAdmin) || raw == string(required) {
			return Decision{Allowed: true}
		}
		if !strings.HasPrefix(raw, "project:") {
			continue
		}
		resource, action, ok := parseScoped(raw, "project:")
		if !ok || action != string(required) {
			continue
		}
		if target.Project == "" {
			continue
		}
		name, environment, app, hasEnv, hasApp := parseResource(resource)
		if name != target.Project {
			continue
		}
		if hasApp {
			if target.App != "" && app == target.App && (environment == "" || environment == target.Environment) {
				return Decision{Allowed: true}
			}
			continue
		}
		if hasEnv {
			if target.Environment != "" && environment == target.Environment {
				return Decision{Allowed: true}
			}
			continue
		}
		return Decision{Allowed: true}
	}
	return Decision{Reason: "scope " + string(required) + " is required for this target"}
}
