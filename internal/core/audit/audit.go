// Package audit defines durable records of authenticated actions.
package audit

import "time"

// Status describes the outcome of an audited action.
type Status string

const (
	StatusOK     Status = "ok"
	StatusDenied Status = "denied"
	StatusFailed Status = "failed"
)

// Event is one audited action.
type Event struct {
	ID             string    `json:"id"`
	ActorID        string    `json:"actorId,omitempty"`
	KeyFingerprint string    `json:"keyFingerprint,omitempty"`
	Source         string    `json:"source,omitempty"`
	Action         string    `json:"action"`
	Target         string    `json:"target,omitempty"`
	Project        string    `json:"project,omitempty"`
	Environment    string    `json:"environment,omitempty"`
	App            string    `json:"app,omitempty"`
	PlanID         string    `json:"planId,omitempty"`
	StateToken     string    `json:"stateToken,omitempty"`
	SourceRepo     string    `json:"sourceRepo,omitempty"`
	Status         Status    `json:"status"`
	Message        string    `json:"message,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

// Filter narrows audit queries.
type Filter struct {
	ActorID string
	Project string
	Action  string
	Status  string
	Limit   int
}
