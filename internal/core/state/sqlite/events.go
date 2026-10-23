package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Event struct {
	ID          string
	DedupKey    string
	Registry    string
	Repository  string
	Tag         string
	Digest      string
	Delivery    string
	Status      string
	ReceivedAt  time.Time
	ProcessedAt time.Time
}

// Job is a queued deployment action. Configuration fields are pinned when the
// job is created so a slow job cannot deploy configuration written after it was
// queued.
type Job struct {
	ID             string
	DedupKey       string
	Kind           string
	Project        string
	Environment    string
	App            string
	Repository     string
	Tag            string
	DesiredDigest  string
	Status         string
	Attempts       int
	LastError      string
	ConfigVersion  int64
	ConfigSnapshot string `json:"-"`
	ConfigHash     string
	ReleaseID      string
	Actor          string
	Source         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StartedAt      time.Time
	FinishedAt     time.Time
	ExitCode       int
}

const (
	JobStatusPending    = "pending"
	JobStatusProcessing = "processing"
	JobStatusDone       = "done"
	JobStatusFailed     = "failed"
	JobStatusSuperseded = "superseded"
	JobStatusCancelled  = "cancelled"
)

const maxJobAttempts = 10

const jobSelect = `SELECT id, dedup_key, kind, project, environment, app, repository, tag,
	desired_digest, status, attempts, last_error, config_version, config_snapshot,
	config_hash, release_id, actor, source, created_at, updated_at, started_at,
	finished_at, exit_code FROM jobs`

func (db *DB) IngestRegistryEvent(ctx context.Context, e *Event, jobs []Job) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `INSERT INTO events
		(dedup_key, registry, repository, tag, digest, delivery, status)
		VALUES (?, ?, ?, ?, ?, ?, 'pending')
		ON CONFLICT(dedup_key) DO NOTHING`,
		e.DedupKey, e.Registry, e.Repository, e.Tag, e.Digest, e.Delivery)
	if err != nil {
		return false, fmt.Errorf("insert event: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return false, nil
	}
	for i := range jobs {
		j := &jobs[i]
		if _, err := tx.ExecContext(ctx, `INSERT INTO jobs
			(dedup_key, kind, project, environment, app, repository, tag, desired_digest, status,
			 config_version, config_snapshot, config_hash, actor, source)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?, ?)
			ON CONFLICT(dedup_key) DO NOTHING`,
			j.DedupKey, j.Kind, j.Project, j.Environment, j.App, j.Repository, j.Tag, j.DesiredDigest,
			j.ConfigVersion, j.ConfigSnapshot, j.ConfigHash, j.Actor, j.Source); err != nil {
			return false, fmt.Errorf("insert job: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (db *DB) PendingEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, dedup_key, registry, repository, tag, digest, delivery, status, created_at, processed_at
		FROM events WHERE status = 'pending' AND `+activeWhere+` ORDER BY created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var created int64
		var processed sql.NullInt64
		if err := rows.Scan(&e.ID, &e.DedupKey, &e.Registry, &e.Repository, &e.Tag, &e.Digest, &e.Delivery, &e.Status, &created, &processed); err != nil {
			return nil, err
		}
		e.ReceivedAt = time.Unix(created, 0)
		if processed.Valid {
			e.ProcessedAt = time.Unix(processed.Int64, 0)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (db *DB) PendingJobs(ctx context.Context, limit int) ([]Job, error) {
	rows, err := db.QueryContext(ctx, jobSelect+`
		WHERE status = ? AND next_attempt_at <= unixepoch() AND `+activeWhere+` ORDER BY created_at ASC LIMIT ?`, JobStatusPending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (db *DB) JobsByStatus(ctx context.Context, status string) ([]Job, error) {
	rows, err := db.QueryContext(ctx, jobSelect+`
		WHERE status = ? AND `+activeWhere+` ORDER BY created_at ASC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// ListJobs returns jobs, newest first. An empty status returns all active
// jobs; a non-empty status filters to that status.
func (db *DB) ListJobs(ctx context.Context, status string, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 100
	}
	query := jobSelect + ` WHERE ` + activeWhere
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
	return scanJobs(rows)
}

// CancelJob marks a pending or processing job as cancelled. It returns false
// when the job is not in a cancellable state (or does not exist).
func (db *DB) CancelJob(ctx context.Context, id string) (bool, error) {
	res, err := db.ExecContext(ctx, `UPDATE jobs SET status = ?, updated_at = unixepoch(), finished_at = unixepoch()
		WHERE id = ? AND status IN (?, ?)`,
		JobStatusCancelled, id, JobStatusPending, JobStatusProcessing)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		db.recordJobEvent(ctx, id)
	}
	return n > 0, nil
}

func (db *DB) LatestJobByTarget(ctx context.Context, project, environment, app string) (Job, bool, error) {
	rows, err := db.QueryContext(ctx, jobSelect+`
		WHERE project = ? AND environment = ? AND app = ? AND `+activeWhere+` ORDER BY created_at DESC LIMIT 1`,
		project, environment, app)
	if err != nil {
		return Job{}, false, err
	}
	defer rows.Close()
	jobs, err := scanJobs(rows)
	if err != nil || len(jobs) == 0 {
		return Job{}, false, err
	}
	return jobs[0], true, nil
}

func (db *DB) JobByID(ctx context.Context, id string) (Job, bool, error) {
	rows, err := db.QueryContext(ctx, jobSelect+` WHERE id = ? AND `+activeWhere, id)
	if err != nil {
		return Job{}, false, err
	}
	defer rows.Close()
	jobs, err := scanJobs(rows)
	if err != nil || len(jobs) == 0 {
		return Job{}, false, err
	}
	return jobs[0], true, nil
}

func (db *DB) JobIDByDedupKey(ctx context.Context, dedup string) (string, bool, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM jobs WHERE dedup_key = ?`, dedup).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func (db *DB) ClaimJob(ctx context.Context, id string) (bool, error) {
	res, err := db.ExecContext(ctx, `UPDATE jobs SET status = ?, updated_at = unixepoch(), started_at = unixepoch() WHERE id = ? AND status = ?`,
		JobStatusProcessing, id, JobStatusPending)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		db.recordJobEvent(ctx, id)
	}
	return n > 0, nil
}

func (db *DB) CompleteJob(ctx context.Context, id string, digest string) error {
	res, err := db.ExecContext(ctx, `UPDATE jobs SET status = ?, desired_digest = ?, updated_at = unixepoch(), finished_at = unixepoch(), exit_code = 0 WHERE id = ? AND status = ?`,
		JobStatusDone, digest, id, JobStatusProcessing)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// The job was cancelled while it was running; keep the cancellation.
		return nil
	}
	db.recordJobEvent(ctx, id)
	return nil
}

// SetJobRelease links a job to the release it is driving.
func (db *DB) SetJobRelease(ctx context.Context, id, releaseID string) error {
	_, err := db.ExecContext(ctx, `UPDATE jobs SET release_id = ?, updated_at = unixepoch() WHERE id = ?`, releaseID, id)
	return err
}

// FailJob requeues a failed job with exponential backoff. It returns true when
// the job has been dead-lettered (no more retries).
func (db *DB) FailJob(ctx context.Context, id string, errmsg string) (bool, error) {
	var attempts int
	if err := db.QueryRowContext(ctx, `SELECT attempts FROM jobs WHERE id = ?`, id).Scan(&attempts); err != nil {
		return false, err
	}
	next := attempts + 1
	if next >= maxJobAttempts {
		res, err := db.ExecContext(ctx, `UPDATE jobs SET status = ?, attempts = ?, last_error = ?, updated_at = unixepoch(), finished_at = unixepoch(), exit_code = 1 WHERE id = ? AND status = ?`,
			JobStatusFailed, next, errmsg, id, JobStatusProcessing)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return false, nil
		}
		db.recordJobEvent(ctx, id)
		return true, nil
	}
	res, err := db.ExecContext(ctx, `UPDATE jobs SET status = ?, attempts = ?, last_error = ?, next_attempt_at = unixepoch() + ?, updated_at = unixepoch() WHERE id = ? AND status = ?`,
		JobStatusPending, next, errmsg, jobBackoffSeconds(next), id, JobStatusProcessing)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	db.recordJobEvent(ctx, id)
	return false, nil
}

// FailJobTerminal marks a job failed without retrying. It is used when retrying
// cannot help, for example a deterministic verification failure.
func (db *DB) FailJobTerminal(ctx context.Context, id string, errmsg string) error {
	res, err := db.ExecContext(ctx, `UPDATE jobs SET status = ?, attempts = attempts + 1, last_error = ?, updated_at = unixepoch(), finished_at = unixepoch(), exit_code = 1 WHERE id = ? AND status = ?`,
		JobStatusFailed, errmsg, id, JobStatusProcessing)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		db.recordJobEvent(ctx, id)
	}
	return nil
}

func jobBackoffSeconds(attempt int) int {
	secs := 30
	for i := 1; i < attempt; i++ {
		if secs >= 3600 {
			break
		}
		secs *= 2
	}
	if secs > 3600 {
		secs = 3600
	}
	return secs
}

func (db *DB) RequeueProcessingJobs(ctx context.Context) (int64, error) {
	res, err := db.ExecContext(ctx, `UPDATE jobs SET status = ?, next_attempt_at = 0, updated_at = unixepoch() WHERE status = ?`,
		JobStatusPending, JobStatusProcessing)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (db *DB) SupersedePendingJobs(ctx context.Context, project, environment, app, keepID string) error {
	_, err := db.ExecContext(ctx, `UPDATE jobs SET status = 'superseded', updated_at = unixepoch(), finished_at = unixepoch()
		WHERE project = ? AND environment = ? AND app = ? AND status = ? AND id != ?`,
		project, environment, app, JobStatusPending, keepID)
	return err
}

func (db *DB) CountJobsByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := db.QueryContext(ctx, `SELECT status, COUNT(1) FROM jobs WHERE `+activeWhere+` GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	var out []Job
	for rows.Next() {
		var j Job
		var created, updated int64
		var started, finished sql.NullInt64
		var exit sql.NullInt64
		if err := rows.Scan(&j.ID, &j.DedupKey, &j.Kind, &j.Project, &j.Environment, &j.App,
			&j.Repository, &j.Tag, &j.DesiredDigest, &j.Status, &j.Attempts, &j.LastError,
			&j.ConfigVersion, &j.ConfigSnapshot, &j.ConfigHash, &j.ReleaseID, &j.Actor, &j.Source,
			&created, &updated, &started, &finished, &exit); err != nil {
			return nil, err
		}
		j.CreatedAt = time.Unix(created, 0)
		j.UpdatedAt = time.Unix(updated, 0)
		if started.Valid {
			j.StartedAt = time.Unix(started.Int64, 0)
		}
		if finished.Valid {
			j.FinishedAt = time.Unix(finished.Int64, 0)
		}
		if exit.Valid {
			j.ExitCode = int(exit.Int64)
		} else {
			switch j.Status {
			case JobStatusDone:
				j.ExitCode = 0
			case JobStatusFailed:
				j.ExitCode = 1
			default:
				j.ExitCode = -1
			}
		}
		if !finished.Valid {
			switch j.Status {
			case JobStatusDone, JobStatusFailed, JobStatusCancelled, JobStatusSuperseded:
				j.FinishedAt = j.UpdatedAt
			}
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// JobEvent is one durable job status change. Its integer ID is a resume cursor
// for job watch.
type JobEvent struct {
	ID        int64
	JobID     string
	Status    string
	Attempts  int
	Message   string
	ExitCode  int
	CreatedAt time.Time
}

func (db *DB) recordJobEvent(ctx context.Context, id string) {
	job, ok, err := db.JobByID(ctx, id)
	if err != nil || !ok {
		return
	}
	_, _ = db.ExecContext(ctx, `INSERT INTO job_events (job_id, status, attempts, message, exit_code) VALUES (?, ?, ?, ?, ?)`,
		job.ID, job.Status, job.Attempts, job.LastError, job.ExitCode)
}

// JobEventsAfter returns status change events after a cursor, oldest first.
func (db *DB) JobEventsAfter(ctx context.Context, jobID string, after int64) ([]JobEvent, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, job_id, status, attempts, message, exit_code, created_at FROM job_events
		WHERE job_id = ? AND id > ? ORDER BY id ASC LIMIT 100`, jobID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobEvent
	for rows.Next() {
		var e JobEvent
		var created int64
		if err := rows.Scan(&e.ID, &e.JobID, &e.Status, &e.Attempts, &e.Message, &e.ExitCode, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = time.Unix(created, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}
