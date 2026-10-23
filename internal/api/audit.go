package api

import (
	"context"
	"net/url"
	"strconv"

	"github.com/moureau-dev/menina/internal/core/audit"
)

// AuditEvents lists audit events on the host.
func (c *Client) AuditEvents(ctx context.Context, f audit.Filter) ([]audit.Event, error) {
	q := url.Values{}
	if f.ActorID != "" {
		q.Set("actor", f.ActorID)
	}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	if f.Action != "" {
		q.Set("action", f.Action)
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	path := "/audit"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Events []audit.Event `json:"events"`
	}
	if err := c.GetJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Events, nil
}
