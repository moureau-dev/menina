package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	apierrors "github.com/moureau-dev/menina/internal/api/errors"
	"github.com/moureau-dev/menina/internal/core/identity"
	"github.com/moureau-dev/menina/internal/core/state/contract"
)

// Authorizer decides whether an actor may execute a request.
type Authorizer interface {
	Authorize(ctx context.Context, actor Actor, req Request) error
}

// StoreAuthorizer resolves profiles from the operational store.
type StoreAuthorizer struct {
	store contract.Store
}

// NewStoreAuthorizer builds an authorizer backed by the store.
func NewStoreAuthorizer(store contract.Store) *StoreAuthorizer {
	return &StoreAuthorizer{store: store}
}

// Authorize enforces profile scopes for non-local actors.
func (a *StoreAuthorizer) Authorize(ctx context.Context, actor Actor, req Request) error {
	if actor.ID == "" || actor.ID == "local" {
		return nil
	}
	profile, ok, err := a.store.ProfileByID(ctx, actor.ID)
	if err != nil {
		return err
	}
	if !ok || profile.Status != identity.StatusActive {
		return apierrors.New(apierrors.CodeAuth, "profile is not active", "")
	}
	required, project, environment := requiredRequestScope(req)
	_, _, app := requestTarget(req)
	decision := (identity.Policy{Scopes: profile.Scopes}).Authorize(required, identity.Target{Project: project, Environment: environment, App: app})
	if !decision.Allowed {
		return apierrors.New(apierrors.CodeAuth, "profile lacks the required scope: "+decision.Reason, "ask an administrator to grant the corresponding scope")
	}
	return nil
}

func requestTarget(req Request) (project, environment, app string) {
	u, err := url.Parse(req.Path)
	if err != nil {
		return "", "", ""
	}
	project = u.Query().Get("project")
	environment = u.Query().Get("environment")
	app = u.Query().Get("app")
	project, environment, app = bodyTarget(req.Body, project, environment, app)
	return project, environment, app
}

func requiredRequestScope(req Request) (scope identity.Scope, project, environment string) {
	u, err := url.Parse(req.Path)
	if err != nil {
		return identity.ScopeDeploy, "", ""
	}
	path := u.Path
	project = u.Query().Get("project")
	environment = u.Query().Get("environment")
	switch path {
	case "/deploy", "/rollback", "/promote", "/config/apply":
		project, environment, _ = bodyTarget(req.Body, project, environment, "")
	case "/release":
		if req.Method == http.MethodGet {
			return identity.ScopeRead, project, environment
		}
		project, environment, _ = bodyTarget(req.Body, project, environment, "")
	case "/status", "/snapshot", "/routes", "/releases", "/jobs", "/jobs/watch", "/job-status", "/webhook-deliveries", "/config/revisions", "/health":
		return identity.ScopeRead, project, environment
	case "/jobs/cancel", "/webhook-replay", "/config/rollback":
		return identity.ScopeDeploy, project, environment
	}
	if req.Method == http.MethodGet {
		return identity.ScopeRead, project, environment
	}
	return identity.ScopeDeploy, project, environment
}

func bodyTarget(body json.RawMessage, project, environment, app string) (string, string, string) {
	if len(body) == 0 {
		return project, environment, app
	}
	var target struct {
		Project     string `json:"project"`
		Environment string `json:"environment"`
		App         string `json:"app"`
		Plan        struct {
			Project     string `json:"project"`
			Environment string `json:"environment"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(body, &target); err != nil {
		return project, environment, app
	}
	if target.Project != "" {
		project = target.Project
	}
	if target.Environment != "" {
		environment = target.Environment
	}
	if target.App != "" {
		app = target.App
	}
	if target.Plan.Project != "" {
		project = target.Plan.Project
	}
	if target.Plan.Environment != "" {
		environment = target.Plan.Environment
	}
	return project, environment, app
}

func authorizeError(err error) Response {
	code, message, hint, _ := apierrors.Info(err)
	return Response{Version: Version, Status: http.StatusForbidden, Error: &Error{Code: code, Message: message, Hint: hint}}
}
