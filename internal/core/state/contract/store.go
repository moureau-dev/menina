// Package contract defines the operational state operations used by the
// controller and worker. SQLite implements it today; other stores can implement
// the same interface.
package contract

import (
	"context"
	"time"

	"github.com/moureau-dev/menina/internal/core/audit"
	"github.com/moureau-dev/menina/internal/core/identity"
	"github.com/moureau-dev/menina/internal/core/release"
	"github.com/moureau-dev/menina/internal/core/state/sqlite"
)

// Store is the operational state used by the control plane.
type Store interface {
	ActiveRelease(ctx context.Context, project, environment, app string) (sqlite.Release, bool, error)
	AuditEvents(ctx context.Context, f audit.Filter) ([]audit.Event, error)
	AddProfileKey(ctx context.Context, k identity.Key) (string, error)
	CancelJob(ctx context.Context, id string) (bool, error)
	ClaimJob(ctx context.Context, id string) (bool, error)
	CompleteJob(ctx context.Context, id string, digest string) error
	CompleteWebhookDelivery(ctx context.Context, id, status string, attempts, responseCode int, lastError string) error
	ConsumeEnrollmentToken(ctx context.Context, tokenHash string, now time.Time, key identity.Key) (identity.Profile, identity.Key, error)
	CountJobsByStatus(ctx context.Context) (map[string]int, error)
	CreateEnrollmentToken(ctx context.Context, t identity.EnrollmentToken) (string, error)
	CreateProfile(ctx context.Context, p identity.Profile) (string, error)
	CreateRelease(ctx context.Context, r sqlite.Release) (string, error)
	DeleteObserved(ctx context.Context, project, environment, app string) error
	FailJob(ctx context.Context, id string, errmsg string) (bool, error)
	FailJobTerminal(ctx context.Context, id string, errmsg string) error
	IngestRegistryEvent(ctx context.Context, e *sqlite.Event, jobs []sqlite.Job) (bool, error)
	InterruptedReleases(ctx context.Context, olderThan time.Duration) ([]sqlite.Release, error)
	JobByID(ctx context.Context, id string) (sqlite.Job, bool, error)
	KeyByFingerprint(ctx context.Context, fingerprint string) (identity.Key, bool, error)
	KeyByID(ctx context.Context, id string) (identity.Key, bool, error)
	JobEventsAfter(ctx context.Context, jobID string, after int64) ([]sqlite.JobEvent, error)
	JobIDByDedupKey(ctx context.Context, dedup string) (string, bool, error)
	LatestJobByTarget(ctx context.Context, project, environment, app string) (sqlite.Job, bool, error)
	LatestRelease(ctx context.Context, project, environment, app string) (sqlite.Release, bool, error)
	ListJobs(ctx context.Context, status string, limit int) ([]sqlite.Job, error)
	Observed(ctx context.Context) ([]sqlite.Observed, error)
	PendingEvents(ctx context.Context, limit int) ([]sqlite.Event, error)
	PendingJobs(ctx context.Context, limit int) ([]sqlite.Job, error)
	ProfileByID(ctx context.Context, id string) (identity.Profile, bool, error)
	ProfileKeys(ctx context.Context, profileID string) ([]identity.Key, error)
	Profiles(ctx context.Context) ([]identity.Profile, error)
	RecordAudit(ctx context.Context, e audit.Event) (string, error)
	RecordWebhookDelivery(ctx context.Context, d sqlite.WebhookDelivery) (string, error)
	ReleaseByID(ctx context.Context, id string) (sqlite.Release, bool, error)
	Releases(ctx context.Context, project, environment, app string) ([]sqlite.Release, error)
	ReleasesByEnvironment(ctx context.Context, project, environment string) ([]sqlite.Release, error)
	ReleaseStats(ctx context.Context) ([]sqlite.ReleaseStat, error)
	ReplaceRoutes(ctx context.Context, routes []sqlite.Route) error
	RequeueProcessingJobs(ctx context.Context) (int64, error)
	ResolveKey(ctx context.Context, fingerprint string) (identity.Profile, identity.Key, bool, error)
	RevokeKey(ctx context.Context, id string) error
	RotateKey(ctx context.Context, oldKeyID string, newKey identity.Key, revokeOld bool) (identity.Key, error)
	Routes(ctx context.Context) ([]sqlite.Route, error)
	SetJobRelease(ctx context.Context, id, releaseID string) error
	SetMeta(ctx context.Context, key, value string) error
	SoftDeletedTargets(ctx context.Context) ([]sqlite.SoftDeleteTarget, error)
	SupersedePendingJobs(ctx context.Context, project, environment, app, keepID string) error
	SyncObservedSoftDeletes(ctx context.Context, targets []sqlite.SoftDeleteTarget) error
	TransitionRelease(ctx context.Context, id string, state release.State, message, actor string) error
	UpdateKeyExpiry(ctx context.Context, id string, expiresAt time.Time) error
	UpdateProfile(ctx context.Context, p identity.Profile) error
	UpdateReleaseFields(ctx context.Context, id, verification, migration, recovery, secretHash string) error
	UpsertObserved(ctx context.Context, o sqlite.Observed) error
	WebhookDeliveries(ctx context.Context, status string, limit int) ([]sqlite.WebhookDelivery, error)
	WebhookDeliveryByID(ctx context.Context, id string) (sqlite.WebhookDelivery, bool, error)
}

var _ Store = (*sqlite.DB)(nil)
