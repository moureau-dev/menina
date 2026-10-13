package database

import (
	"context"
	"time"
)

type Observed struct {
	ID             string
	Project        string
	Environment    string
	App            string
	DesiredDigest  string
	ObservedDigest string
	Image          string
	Replicas       int
	ReadyReplicas  int
	Status         string
	UpdatedAt      time.Time
}

func (db *DB) UpsertObserved(ctx context.Context, o Observed) error {
	_, err := db.ExecContext(ctx, `INSERT INTO observed
		(project, environment, app, desired_digest, observed_digest, image, replicas, ready_replicas, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project, environment, app) DO UPDATE SET
			desired_digest = excluded.desired_digest,
			observed_digest = excluded.observed_digest,
			image = excluded.image,
			replicas = excluded.replicas,
			ready_replicas = excluded.ready_replicas,
			status = excluded.status,
			updated_at = unixepoch()`,
		o.Project, o.Environment, o.App, o.DesiredDigest, o.ObservedDigest, o.Image, o.Replicas, o.ReadyReplicas, o.Status)
	return err
}

func (db *DB) Observed(ctx context.Context) ([]Observed, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, project, environment, app, desired_digest, observed_digest, image, replicas, ready_replicas, status, updated_at
		FROM observed WHERE `+activeWhere+` ORDER BY project, environment, app`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Observed
	for rows.Next() {
		var o Observed
		var updated int64
		if err := rows.Scan(&o.ID, &o.Project, &o.Environment, &o.App, &o.DesiredDigest, &o.ObservedDigest, &o.Image, &o.Replicas, &o.ReadyReplicas, &o.Status, &updated); err != nil {
			return nil, err
		}
		o.UpdatedAt = time.Unix(updated, 0)
		out = append(out, o)
	}
	return out, rows.Err()
}
