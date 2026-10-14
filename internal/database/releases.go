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

func (db *DB) ReleasesForProject(ctx context.Context, project string) ([]Release, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, project, environment, app, repository, tag, digest, status, created_at
		FROM releases WHERE project = ? AND `+activeWhere+` ORDER BY created_at DESC`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReleases(rows)
}

func (db *DB) ProtectedDigests(ctx context.Context) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT digest FROM releases WHERE digest != '' AND `+activeWhere)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out[d] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows2, err := db.QueryContext(ctx, `SELECT DISTINCT desired_digest FROM jobs WHERE desired_digest != '' AND `+activeWhere)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var d string
		if err := rows2.Scan(&d); err != nil {
			return nil, err
		}
		out[d] = struct{}{}
	}
	return out, rows2.Err()
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
