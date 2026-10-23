package sqlite

import (
	"context"
	"time"

	"github.com/moureau-dev/menina/internal/core/audit"
)

// RecordAudit inserts one audit event.
func (db *DB) RecordAudit(ctx context.Context, e audit.Event) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `INSERT INTO audit_events
		(actor_id, key_fingerprint, source, action, target, project, environment, app, plan_id, state_token, source_repo, status, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		e.ActorID, e.KeyFingerprint, e.Source, e.Action, e.Target, e.Project, e.Environment, e.App, e.PlanID, e.StateToken, e.SourceRepo, e.Status, e.Message).Scan(&id)
	return id, err
}

// AuditEvents returns audit events newest first.
func (db *DB) AuditEvents(ctx context.Context, f audit.Filter) ([]audit.Event, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	query := `SELECT id, created_at, actor_id, key_fingerprint, source, action, target, project, environment, app, plan_id, state_token, source_repo, status, message
		FROM audit_events WHERE 1=1`
	args := []any{}
	if f.ActorID != "" {
		query += ` AND actor_id = ?`
		args = append(args, f.ActorID)
	}
	if f.Project != "" {
		query += ` AND project = ?`
		args = append(args, f.Project)
	}
	if f.Action != "" {
		query += ` AND action = ?`
		args = append(args, f.Action)
	}
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []audit.Event
	for rows.Next() {
		var e audit.Event
		var created int64
		if err := rows.Scan(&e.ID, &created, &e.ActorID, &e.KeyFingerprint, &e.Source, &e.Action, &e.Target, &e.Project, &e.Environment, &e.App, &e.PlanID, &e.StateToken, &e.SourceRepo, &e.Status, &e.Message); err != nil {
			return nil, err
		}
		e.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
