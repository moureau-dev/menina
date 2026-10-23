// Package contract defines the operational state operations used by the
// controller and worker. SQLite implements it today; other stores can implement
// the same interface.
package contract

import (
	"context"
	"time"

	"github.com/moureau-dev/menina/internal/core/release"
	"github.com/moureau-dev/menina/internal/core/state/sqlite"
)

// Store is the operational state used by the control plane.
type Store interface {
	ActiveRelease(ctx context.Context, project, environment, app string) (sqlite.Release, bool, error)
	CancelJob(ctx context.Context, id string) (bool, error)
	ClaimJob(ctx context.Context, id string) (bool, error)
	CompleteJob(ctx context.Context, id string, digest string) error
	CompleteWebhookDelivery(ctx context.Context, id, status string, attempts, responseCode int, lastError string) error
	CountJobsByStatus(ctx context.Context) (map[string]int, error)
	CreateRelease(ctx context.Context, r sqlite.Release) (string, error)
	DeleteObserved(ctx context.Context, project, environment, app string) error
	FailJob(ctx context.Context, id string, errmsg string) (bool, error)
	FailJobTerminal(ctx context.Context, id string, errmsg string) error
	IngestRegistryEvent(ctx context.Context, e *sqlite.Event, jobs []sqlite.Job) (bool, error)
	InterruptedReleases(ctx context.Context, olderThan time.Duration) ([]sqlite.Release, error)
	JobByID(ctx context.Context, id string) (sqlite.Job, bool, error)
	JobEventsAfter(ctx context.Context, jobID string, after int64) ([]sqlite.JobEvent, error)
	JobIDByDedupKey(ctx context.Context, dedup string) (string, bool, error)
	LatestJobByTarget(ctx context.Context, project, environment, app string) (sqlite.Job, bool, error)
	LatestRelease(ctx context.Context, project, environment, app string) (sqlite.Release, bool, error)
	ListJobs(ctx context.Context, status string, limit int) ([]sqlite.Job, error)
	Observed(ctx context.Context) ([]sqlite.Observed, error)
	PendingEvents(ctx context.Context, limit int) ([]sqlite.Event, error)
	PendingJobs(ctx context.Context, limit int) ([]sqlite.Job, error)
	RecordWebhookDelivery(ctx context.Context, d sqlite.WebhookDelivery) (string, error)
	ReleaseByID(ctx context.Context, id string) (sqlite.Release, bool, error)
	Releases(ctx context.Context, project, environment, app string) ([]sqlite.Release, error)
	ReleasesByEnvironment(ctx context.Context, project, environment string) ([]sqlite.Release, error)
	ReleaseStats(ctx context.Context) ([]sqlite.ReleaseStat, error)
	ReplaceRoutes(ctx context.Context, routes []sqlite.Route) error
	RequeueProcessingJobs(ctx context.Context) (int64, error)
	Routes(ctx context.Context) ([]sqlite.Route, error)
	SetJobRelease(ctx context.Context, id, releaseID string) error
	SetMeta(ctx context.Context, key, value string) error
	SoftDeletedTargets(ctx context.Context) ([]sqlite.SoftDeleteTarget, error)
	SupersedePendingJobs(ctx context.Context, project, environment, app, keepID string) error
	SyncObservedSoftDeletes(ctx context.Context, targets []sqlite.SoftDeleteTarget) error
	TransitionRelease(ctx context.Context, id string, state release.State, message, actor string) error
	UpdateReleaseFields(ctx context.Context, id, verification, migration, recovery, secretHash string) error
	UpsertObserved(ctx context.Context, o sqlite.Observed) error
	WebhookDeliveries(ctx context.Context, status string, limit int) ([]sqlite.WebhookDelivery, error)
	WebhookDeliveryByID(ctx context.Context, id string) (sqlite.WebhookDelivery, bool, error)
}

var _ Store = (*sqlite.DB)(nil)
