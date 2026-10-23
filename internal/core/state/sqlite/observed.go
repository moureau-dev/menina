package sqlite

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
			deleted_at = NULL,
			updated_at = unixepoch()`,
		o.Project, o.Environment, o.App, o.DesiredDigest, o.ObservedDigest, o.Image, o.Replicas, o.ReadyReplicas, o.Status)
	return err
}

// SoftDeleteTarget identifies a soft-deleted target. Empty Environment/App mean
// the whole project (Environment and App empty) or a whole environment
// (Environment set, App empty); a set App scopes to one app across environments.
type SoftDeleteTarget struct {
	Project     string
	Environment string
	App         string
}

// SyncObservedSoftDeletes makes the soft-delete state of the observed rows
// match the given targets: every row is reactivated, then the targets are marked
// soft-deleted (deleted_at set). It is called by the control plane, which is the
// single SQLite writer, from the configuration tree's trash directories.
func (db *DB) SyncObservedSoftDeletes(ctx context.Context, targets []SoftDeleteTarget) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE observed SET deleted_at = NULL, updated_at = unixepoch() WHERE deleted_at IS NOT NULL`); err != nil {
		return err
	}
	for _, t := range targets {
		var q string
		var args []any
		switch {
		case t.App != "":
			q = `UPDATE observed SET deleted_at = unixepoch(), updated_at = unixepoch() WHERE project = ? AND app = ? AND deleted_at IS NULL`
			args = []any{t.Project, t.App}
		case t.Environment != "":
			q = `UPDATE observed SET deleted_at = unixepoch(), updated_at = unixepoch() WHERE project = ? AND environment = ? AND deleted_at IS NULL`
			args = []any{t.Project, t.Environment}
		default:
			q = `UPDATE observed SET deleted_at = unixepoch(), updated_at = unixepoch() WHERE project = ? AND deleted_at IS NULL`
			args = []any{t.Project}
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SoftDeletedTargets returns the (project, environment, app) triples whose
// observed row is soft-deleted.
func (db *DB) SoftDeletedTargets(ctx context.Context) ([]SoftDeleteTarget, error) {
	rows, err := db.QueryContext(ctx, `SELECT project, environment, app FROM observed WHERE deleted_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SoftDeleteTarget
	for rows.Next() {
		var t SoftDeleteTarget
		if err := rows.Scan(&t.Project, &t.Environment, &t.App); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
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

// DeleteObserved removes the observed row for a hard-deleted app. Release
// history is intentionally retained.
func (db *DB) DeleteObserved(ctx context.Context, project, environment, app string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM observed WHERE project = ? AND environment = ? AND app = ?`,
		project, environment, app)
	return err
}
