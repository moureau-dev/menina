package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// WebhookDelivery is the durable delivery record for one outbound event.
type WebhookDelivery struct {
	ID           string
	DedupKey     string
	Project      string
	Environment  string
	App          string
	Event        string
	Status       string
	Attempts     int
	ResponseCode int
	LastError    string
	Payload      string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeliveredAt  time.Time
}

// RecordWebhookDelivery inserts a pending delivery. A duplicate dedup key keeps
// the first row and returns its ID, which makes replayed events idempotent.
func (db *DB) RecordWebhookDelivery(ctx context.Context, d WebhookDelivery) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `INSERT INTO webhook_deliveries
		(dedup_key, project, environment, app, event, status, payload)
		VALUES (?, ?, ?, ?, ?, 'pending', ?)
		ON CONFLICT(dedup_key) DO UPDATE SET updated_at = unixepoch()
		RETURNING id`,
		d.DedupKey, d.Project, d.Environment, d.App, d.Event, d.Payload).Scan(&id)
	return id, err
}

func (db *DB) CompleteWebhookDelivery(ctx context.Context, id, status string, attempts, responseCode int, lastError string) error {
	var delivered any
	if status == "delivered" {
		delivered = time.Now().Unix()
	}
	_, err := db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = ?, attempts = ?, response_code = ?, last_error = ?, delivered_at = COALESCE(?, delivered_at), updated_at = unixepoch() WHERE id = ?`,
		status, attempts, responseCode, lastError, delivered, id)
	return err
}

func (db *DB) WebhookDeliveries(ctx context.Context, status string, limit int) ([]WebhookDelivery, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id, dedup_key, project, environment, app, event, status, attempts, response_code, last_error, payload, created_at, updated_at, delivered_at
		FROM webhook_deliveries WHERE ` + activeWhere
	args := []any{}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WebhookDelivery{}
	for rows.Next() {
		var d WebhookDelivery
		var created, updated int64
		var delivered sql.NullInt64
		if err := rows.Scan(&d.ID, &d.DedupKey, &d.Project, &d.Environment, &d.App, &d.Event, &d.Status, &d.Attempts, &d.ResponseCode, &d.LastError, &d.Payload, &created, &updated, &delivered); err != nil {
			return nil, err
		}
		d.CreatedAt = time.Unix(created, 0)
		d.UpdatedAt = time.Unix(updated, 0)
		if delivered.Valid {
			d.DeliveredAt = time.Unix(delivered.Int64, 0)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (db *DB) WebhookDeliveryByID(ctx context.Context, id string) (WebhookDelivery, bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, dedup_key, project, environment, app, event, status, attempts, response_code, last_error, payload, created_at, updated_at, delivered_at
		FROM webhook_deliveries WHERE id = ? AND `+activeWhere, id)
	if err != nil {
		return WebhookDelivery{}, false, err
	}
	defer rows.Close()
	var d WebhookDelivery
	if !rows.Next() {
		return d, false, rows.Err()
	}
	var created, updated int64
	var delivered sql.NullInt64
	if err := rows.Scan(&d.ID, &d.DedupKey, &d.Project, &d.Environment, &d.App, &d.Event, &d.Status, &d.Attempts, &d.ResponseCode, &d.LastError, &d.Payload, &created, &updated, &delivered); err != nil {
		return d, false, err
	}
	d.CreatedAt = time.Unix(created, 0)
	d.UpdatedAt = time.Unix(updated, 0)
	if delivered.Valid {
		d.DeliveredAt = time.Unix(delivered.Int64, 0)
	}
	return d, true, rows.Err()
}

// MarshalWebhookPayload encodes an event for storage without secrets.
func MarshalWebhookPayload(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
