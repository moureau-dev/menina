package api

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/moureau-dev/menina/internal/core/config"
	configplan "github.com/moureau-dev/menina/internal/core/config/plan"
)

// ApplyPlanRequest is the body for /config/apply.
type ApplyPlanRequest struct {
	Plan         configplan.Plan `json:"plan"`
	IfStateToken string          `json:"ifStateToken,omitempty"`
	Verify       bool            `json:"verify,omitempty"`
}

// ApplyPlan executes a reviewed desired-state plan on the host.
func (c *Client) ApplyPlan(ctx context.Context, p configplan.Plan, ifStateToken string, verify bool) (configplan.Result, error) {
	body, err := json.Marshal(ApplyPlanRequest{Plan: p, IfStateToken: ifStateToken, Verify: verify})
	if err != nil {
		return configplan.Result{}, err
	}
	var out configplan.Result
	if err := c.PostJSONDecode(ctx, "/config/apply", body, &out); err != nil {
		return configplan.Result{}, err
	}
	return out, nil
}

// ConfigRevisions lists apply revisions on the host.
func (c *Client) ConfigRevisions(ctx context.Context, project, environment string) ([]config.Revision, error) {
	path := "/config/revisions"
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	if environment != "" {
		q.Set("environment", environment)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Revisions []config.Revision `json:"revisions"`
	}
	if err := c.GetJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Revisions, nil
}

// ConfigRollback restores a previous apply revision.
func (c *Client) ConfigRollback(ctx context.Context, id string) error {
	body, err := json.Marshal(map[string]any{"id": id, "confirm": true})
	if err != nil {
		return err
	}
	return c.PostJSON(ctx, "/config/rollback", body)
}
