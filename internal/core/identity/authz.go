package identity

import (
	"fmt"
	"strings"
)

// Allows reports whether a profile's scopes permit an action for a project and
// environment. Empty project or environment means the request is not scoped.
func Allows(scopes []string, required Scope, project, environment string) bool {
	return AllowsTarget(scopes, required, Target{Project: project, Environment: environment})
}

// AllowsTarget reports whether a profile's scopes permit an action on a target.
func AllowsTarget(scopes []string, required Scope, target Target) bool {
	return Policy{Scopes: scopes}.Authorize(required, target).Allowed
}

func parseScoped(scope, prefix string) (resource, action string, ok bool) {
	rest := strings.TrimPrefix(scope, prefix)
	idx := strings.LastIndex(rest, ":")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

func parseResource(resource string) (name, environment, app string, hasEnv, hasApp bool) {
	parts := strings.SplitN(resource, "/", 3)
	name = parts[0]
	if len(parts) > 1 && parts[1] != "" {
		environment, hasEnv = parts[1], true
	}
	if len(parts) > 2 && parts[2] != "" {
		app, hasApp = parts[2], true
	}
	return name, environment, app, hasEnv, hasApp
}

// ValidateScope checks one scope string.
func ValidateScope(scope string) error {
	for _, allowed := range AllScopes {
		if scope == string(allowed) {
			return nil
		}
	}
	if strings.HasPrefix(scope, "project:") {
		resource, action, ok := parseScoped(scope, "project:")
		if !ok || resource == "" {
			return fmt.Errorf("invalid project scope %q", scope)
		}
		name, env, app, hasEnv, hasApp := parseResource(resource)
		if name == "" || (hasEnv && env == "") || (hasApp && app == "") {
			return fmt.Errorf("invalid project scope %q", scope)
		}
		for _, allowed := range AllScopes {
			if action == string(allowed) && allowed != ScopeAdmin {
				return nil
			}
		}
		return fmt.Errorf("unknown scope action %q", action)
	}
	return fmt.Errorf("unknown scope %q", scope)
}

// CheckCommand returns the scope required for an allowlisted host CLI command.
func CheckCommand(profile Profile, args []string) error {
	required, _, _, ok := RequiredCommandScope(args)
	if !ok {
		return fmt.Errorf("command is not allowed")
	}
	if decision := (Policy{Scopes: profile.Scopes}).Authorize(required, CommandTarget(args)); !decision.Allowed {
		return fmt.Errorf("profile %q lacks %s scope: %s", profile.Name, required, decision.Reason)
	}
	return nil
}

// CommandTarget extracts the project, environment, and app from CLI arguments.
func CommandTarget(args []string) Target {
	project, environment := commandTarget(args)
	app := ""
	if len(args) > 2 && args[0] == "app" && !strings.HasPrefix(args[2], "-") {
		app = args[2]
	}
	for i, arg := range args {
		if strings.HasPrefix(arg, "--app=") {
			app = strings.TrimPrefix(arg, "--app=")
		}
		if arg == "--app" && i+1 < len(args) {
			app = args[i+1]
		}
	}
	return Target{Project: project, Environment: environment, App: app}
}

// RequiredCommandScope maps a host CLI command to a scope and its target.
func RequiredCommandScope(args []string) (scope Scope, project, environment string, ok bool) {
	if len(args) == 0 {
		return "", "", "", false
	}
	project, environment = commandTarget(args)
	group := args[0]
	sub := ""
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		sub = args[1]
	}
	switch group {
	case "version", "capabilities":
		return ScopeRead, project, environment, true
	case "status", "routes", "doctor":
		return ScopeRead, project, environment, true
	case "app":
		switch sub {
		case "status", "list", "logs":
			return ScopeRead, project, environment, true
		default:
			return ScopeDeploy, project, environment, true
		}
	case "release":
		return ScopeRead, project, environment, true
	case "job":
		switch sub {
		case "list", "watch":
			return ScopeRead, project, environment, true
		default:
			return ScopeDeploy, project, environment, true
		}
	case "secret":
		return ScopeSecrets, project, environment, true
	case "notify":
		switch sub {
		case "list", "deliveries":
			return ScopeRead, project, environment, true
		default:
			return ScopeDeploy, project, environment, true
		}
	case "registry":
		switch sub {
		case "status":
			return ScopeRead, project, environment, true
		default:
			return ScopeRegistry, project, environment, true
		}
	case "audit", "whoami":
		return ScopeRead, project, environment, true
	case "config":
		if sub == "rollback" {
			return ScopeDeploy, project, environment, true
		}
		return ScopeRead, project, environment, true
	case "apply":
		return ScopeDeploy, project, environment, true
	case "api-ssh":
		return ScopeRead, project, environment, true
	default:
		return "", "", "", false
	}
}

func commandTarget(args []string) (project, environment string) {
	for i, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--project="):
			project = strings.TrimPrefix(arg, "--project=")
		case arg == "--project" && i+1 < len(args):
			project = args[i+1]
		case strings.HasPrefix(arg, "--environment="):
			environment = strings.TrimPrefix(arg, "--environment=")
		case arg == "--environment" && i+1 < len(args):
			environment = args[i+1]
		case arg == "-e" && i+1 < len(args):
			environment = args[i+1]
		}
	}
	return project, environment
}
