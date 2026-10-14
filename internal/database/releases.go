package database

import (
	"context"
	"database/sql"
	"time"
)

type Release struct {
	ID          string
	Project     string
	Environment string
	App         string
	Repository  string
	Tag         string
	Digest      string
	Status      string
	CreatedAt   time.Time
}

func (db *DB) RecordRelease(ctx context.Context, r Release) error {
	_, err := db.ExecContext(ctx, `INSERT INTO releases
		(project, environment, app, repository, tag, digest, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.Project, r.Environment, r.App, r.Repository, r.Tag, r.Digest, r.Status)
	return err
}

func (db *DB) Releases(ctx context.Context, project, environment, app string) ([]Release, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, project, environment, app, repository, tag, digest, status, created_at
		FROM releases WHERE project = ? AND environment = ? AND app = ? AND `+activeWhere+` ORDER BY created_at DESC`, project, environment, app)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReleases(rows)
}

func (db *DB) HasRelease(ctx context.Context, project, environment, app, digest string) (bool, error) {
	if digest == "" {
		return false, nil
	}
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM releases
		WHERE project = ? AND environment = ? AND app = ? AND digest = ? AND `+activeWhere,
		project, environment, app, digest).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func scanReleases(rows *sql.Rows) ([]Release, error) {
	var out []Release
	for rows.Next() {
		var r Release
		var created int64
		if err := rows.Scan(&r.ID, &r.Project, &r.Environment, &r.App, &r.Repository, &r.Tag, &r.Digest, &r.Status, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(created, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}
