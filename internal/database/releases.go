package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/moureau-dev/menina/internal/release"
)

// Release is the durable record of one app release attempt.
type Release struct {
	ID             string
	Project        string
	Environment    string
	App            string
	Repository     string
	Tag            string
	Digest         string
	Image          string
	State          string
	PreviousID     string
	ConfigVersion  int64
	ConfigHash     string
	ConfigSnapshot string `json:"-"`
	SecretHash     string
	Verification   string
	Migration      string
	Recovery       string
	SourceRepo     string
	SourceRevision string
	Actor          string
	Message        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	FinishedAt     time.Time
}

// ReleaseEvent records one state transition.
type ReleaseEvent struct {
	ID        string
	ReleaseID string
	State     string
	Message   string
	Actor     string
	CreatedAt time.Time
}

// CreateRelease inserts a release and returns its generated ID.
func (db *DB) CreateRelease(ctx context.Context, r Release) (string, error) {
	if r.State == "" {
		r.State = string(release.Prepared)
	}
	var id string
	err := db.QueryRowContext(ctx, `INSERT INTO releases
		(project, environment, app, repository, tag, digest, image, status, previous_id,
		 config_version, config_hash, config_snapshot, secret_hash, verification, migration, recovery,
		 source_repo, source_revision, actor, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`,
		r.Project, r.Environment, r.App, r.Repository, r.Tag, r.Digest, r.Image, r.State, r.PreviousID,
		r.ConfigVersion, r.ConfigHash, r.ConfigSnapshot, r.SecretHash, r.Verification, r.Migration, r.Recovery,
		r.SourceRepo, r.SourceRevision, r.Actor, r.Message).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create release: %w", err)
	}
	_, _ = db.AddReleaseEvent(ctx, id, r.State, r.Message, r.Actor)
	return id, nil
}

// TransitionRelease moves a release to another state after validating the
// transition. It returns an error if the release does not exist or the
// transition is invalid.
func (db *DB) TransitionRelease(ctx context.Context, id string, state release.State, message, actor string) error {
	current, ok, err := db.ReleaseByID(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("release %q not found", id)
	}
	if !release.CanTransition(release.State(current.State), state) {
		return fmt.Errorf("release %s cannot transition from %s to %s", id, current.State, state)
	}
	var finished any
	if state.Terminal() || state == release.Failed || state == release.RolledBack {
		finished = time.Now().Unix()
	}
	_, err = db.ExecContext(ctx, `UPDATE releases SET status = ?, message = ?, updated_at = unixepoch(), finished_at = COALESCE(?, finished_at) WHERE id = ?`,
		state, message, finished, id)
	if err != nil {
		return err
	}
	_, _ = db.AddReleaseEvent(ctx, id, string(state), message, actor)
	return nil
}

// UpdateReleaseFields records verification, migration, recovery and secret hash
// details without changing the state.
func (db *DB) UpdateReleaseFields(ctx context.Context, id, verification, migration, recovery, secretHash string) error {
	_, err := db.ExecContext(ctx, `UPDATE releases SET
		verification = CASE WHEN ? = '' THEN verification ELSE ? END,
		migration = CASE WHEN ? = '' THEN migration ELSE ? END,
		recovery = CASE WHEN ? = '' THEN recovery ELSE ? END,
		secret_hash = CASE WHEN ? = '' THEN secret_hash ELSE ? END,
		updated_at = unixepoch()
		WHERE id = ?`,
		verification, verification, migration, migration, recovery, recovery, secretHash, secretHash, id)
	return err
}

func (db *DB) AddReleaseEvent(ctx context.Context, releaseID, state, message, actor string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `INSERT INTO release_events (release_id, state, message, actor)
		VALUES (?, ?, ?, ?) RETURNING id`, releaseID, state, message, actor).Scan(&id)
	return id, err
}

func (db *DB) ReleaseEvents(ctx context.Context, releaseID string) ([]ReleaseEvent, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, release_id, state, message, actor, created_at
		FROM release_events WHERE release_id = ? AND `+activeWhere+` ORDER BY created_at ASC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReleaseEvent
	for rows.Next() {
		var e ReleaseEvent
		var created int64
		if err := rows.Scan(&e.ID, &e.ReleaseID, &e.State, &e.Message, &e.Actor, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = time.Unix(created, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReleaseByID returns one release.
func (db *DB) ReleaseByID(ctx context.Context, id string) (Release, bool, error) {
	rows, err := db.QueryContext(ctx, releaseSelect+` WHERE id = ? AND `+activeWhere, id)
	if err != nil {
		return Release{}, false, err
	}
	defer rows.Close()
	releases, err := scanReleases(rows)
	if err != nil || len(releases) == 0 {
		return Release{}, false, err
	}
	return releases[0], true, nil
}

// Releases returns an app's release history, newest first.
func (db *DB) Releases(ctx context.Context, project, environment, app string) ([]Release, error) {
	rows, err := db.QueryContext(ctx, releaseSelect+`
		WHERE project = ? AND environment = ? AND app = ? AND `+activeWhere+` ORDER BY created_at DESC, rowid DESC`,
		project, environment, app)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReleases(rows)
}

// ReleasesByEnvironment returns the newest release attempt per app in an
// environment. It backs the project/environment release aggregate.
func (db *DB) ReleasesByEnvironment(ctx context.Context, project, environment string) ([]Release, error) {
	rows, err := db.QueryContext(ctx, releaseSelect+`
		WHERE project = ? AND environment = ? AND `+activeWhere+`
		AND id IN (
			SELECT id FROM releases r2
			WHERE r2.project = releases.project AND r2.environment = releases.environment
			AND r2.app = releases.app AND r2.`+activeWhere+`
			ORDER BY r2.created_at DESC, r2.rowid DESC LIMIT 1
		)
		ORDER BY app`, project, environment)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReleases(rows)
}

// LatestRelease returns the newest release attempt for an app in any state.
func (db *DB) LatestRelease(ctx context.Context, project, environment, app string) (Release, bool, error) {
	rows, err := db.QueryContext(ctx, releaseSelect+`
		WHERE project = ? AND environment = ? AND app = ? AND `+activeWhere+`
		ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		project, environment, app)
	if err != nil {
		return Release{}, false, err
	}
	defer rows.Close()
	releases, err := scanReleases(rows)
	if err != nil || len(releases) == 0 {
		return Release{}, false, err
	}
	return releases[0], true, nil
}

// ActiveRelease returns the newest active release for an app.
func (db *DB) ActiveRelease(ctx context.Context, project, environment, app string) (Release, bool, error) {
	return db.releaseByState(ctx, project, environment, app, release.Active)
}

// PreviousRelease returns the newest active release for an app excluding the
// given release ID. It is the previous known-good restore point.
func (db *DB) PreviousRelease(ctx context.Context, project, environment, app, excludeID string) (Release, bool, error) {
	rows, err := db.QueryContext(ctx, releaseSelect+`
		WHERE project = ? AND environment = ? AND app = ? AND id != ? AND status = ? AND `+activeWhere+`
		ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		project, environment, app, excludeID, string(release.Active))
	if err != nil {
		return Release{}, false, err
	}
	defer rows.Close()
	releases, err := scanReleases(rows)
	if err != nil || len(releases) == 0 {
		return Release{}, false, err
	}
	return releases[0], true, nil
}

func (db *DB) releaseByState(ctx context.Context, project, environment, app string, state release.State) (Release, bool, error) {
	rows, err := db.QueryContext(ctx, releaseSelect+`
		WHERE project = ? AND environment = ? AND app = ? AND status = ? AND `+activeWhere+`
		ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		project, environment, app, string(state))
	if err != nil {
		return Release{}, false, err
	}
	defer rows.Close()
	releases, err := scanReleases(rows)
	if err != nil || len(releases) == 0 {
		return Release{}, false, err
	}
	return releases[0], true, nil
}

// HasRelease reports whether a digest has already been recorded for an app.
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

// RecordRelease is the compatibility helper used by callers that only have a
// deployed digest. It records a release that is already active.
func (db *DB) RecordRelease(ctx context.Context, r Release) error {
	if r.State == "" {
		r.State = string(release.Active)
	}
	_, err := db.CreateRelease(ctx, r)
	return err
}

const releaseSelect = `SELECT id, project, environment, app, repository, tag, digest, image, status,
	previous_id, config_version, config_hash, config_snapshot, secret_hash, verification, migration, recovery,
	source_repo, source_revision, actor, message, created_at, updated_at, finished_at
	FROM releases`

func scanReleases(rows *sql.Rows) ([]Release, error) {
	var out []Release
	for rows.Next() {
		var r Release
		var created, updated int64
		var finished sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Project, &r.Environment, &r.App, &r.Repository, &r.Tag, &r.Digest,
			&r.Image, &r.State, &r.PreviousID, &r.ConfigVersion, &r.ConfigHash, &r.ConfigSnapshot, &r.SecretHash,
			&r.Verification, &r.Migration, &r.Recovery, &r.SourceRepo, &r.SourceRevision,
			&r.Actor, &r.Message, &created, &updated, &finished); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(created, 0)
		r.UpdatedAt = time.Unix(updated, 0)
		if finished.Valid {
			r.FinishedAt = time.Unix(finished.Int64, 0)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReleaseStat is one release state with counts and duration totals.
type ReleaseStat struct {
	State         string
	Count         int
	FinishedCount int
	SumSeconds    float64
}

func (db *DB) ReleaseStats(ctx context.Context) ([]ReleaseStat, error) {
	rows, err := db.QueryContext(ctx, `SELECT status, COUNT(1),
		COALESCE(SUM(CASE WHEN finished_at IS NOT NULL THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN finished_at IS NOT NULL THEN finished_at - created_at ELSE 0 END), 0)
		FROM releases WHERE `+activeWhere+` GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReleaseStat
	for rows.Next() {
		var stat ReleaseStat
		if err := rows.Scan(&stat.State, &stat.Count, &stat.FinishedCount, &stat.SumSeconds); err != nil {
			return nil, err
		}
		out = append(out, stat)
	}
	return out, rows.Err()
}

// FailInterruptedReleases marks releases that were left in a non-terminal state
// by a crashed or restarted control plane as failed, unless a job is still
// pending or processing for them. This keeps release history honest after an
// interrupted deploy.
func (db *DB) FailInterruptedReleases(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).Unix()
	res, err := db.ExecContext(ctx, `UPDATE releases SET status = 'failed',
		message = CASE WHEN message = '' THEN 'interrupted; no active job' ELSE message END,
		updated_at = unixepoch(), finished_at = COALESCE(finished_at, unixepoch())
		WHERE status IN ('prepared','migrating','deploying','verifying','recovering')
		AND updated_at < ?
		AND NOT EXISTS (
			SELECT 1 FROM jobs WHERE jobs.release_id = releases.id AND jobs.status IN ('pending','processing')
		)`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// InterruptedReleases returns non-terminal releases older than olderThan that
// have no pending or processing job. They were left behind by a crashed or
// restarted control plane.
func (db *DB) InterruptedReleases(ctx context.Context, olderThan time.Duration) ([]Release, error) {
	cutoff := time.Now().Add(-olderThan).Unix()
	rows, err := db.QueryContext(ctx, releaseSelect+`
		WHERE status IN ('prepared','migrating','deploying','verifying','recovering')
		AND updated_at < ? AND `+activeWhere+`
		AND NOT EXISTS (
			SELECT 1 FROM jobs WHERE jobs.release_id = releases.id AND jobs.status IN ('pending','processing')
		)
		ORDER BY created_at ASC`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReleases(rows)
}
