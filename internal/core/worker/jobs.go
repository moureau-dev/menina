package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moureau-dev/menina/internal/core/cluster"
	"github.com/moureau-dev/menina/internal/core/config"
	"github.com/moureau-dev/menina/internal/core/notify"
	"github.com/moureau-dev/menina/internal/core/registry"
	"github.com/moureau-dev/menina/internal/core/release"
	"github.com/moureau-dev/menina/internal/core/secrets"
	"github.com/moureau-dev/menina/internal/core/state/sqlite"
)

// PinJob binds the job to the resolved app configuration it was queued
// against. A deploy executes the pinned snapshot even if config changes before
// the worker picks it up.
func PinJob(job sqlite.Job, app *config.ResolvedApp, version int64, actor, source string) sqlite.Job {
	if app == nil {
		return job
	}
	data, err := json.Marshal(app)
	if err == nil {
		sum := sha256.Sum256(data)
		job.ConfigSnapshot = string(data)
		job.ConfigHash = "sha256:" + hex.EncodeToString(sum[:])
	}
	job.ConfigVersion = version
	job.Actor = actor
	job.Source = source
	return job
}

// jobApp returns the app pinned to a job, or falls back to current config for
// jobs queued before pinning was introduced.
func jobApp(job sqlite.Job, snap *config.ResolvedConfig) (*config.ResolvedApp, error) {
	if job.ConfigSnapshot != "" {
		var app config.ResolvedApp
		if err := json.Unmarshal([]byte(job.ConfigSnapshot), &app); err != nil {
			return nil, fmt.Errorf("decode pinned configuration for job %s: %w", job.ID, err)
		}
		return &app, nil
	}
	if snap == nil {
		return nil, fmt.Errorf("no resolved configuration available")
	}
	return snap.FindApp(job.Project, job.Environment, job.App)
}

type Jobs struct {
	db         *sqlite.DB
	engine     *k8s.Engine
	cluster    k8s.Cluster
	snapshot   func() *config.ResolvedConfig
	domain     string
	scheme     string
	allowHooks bool
	log        *slog.Logger
	sender     *notify.Sender

	targetMu sync.Mutex
	targets  map[string]*sync.Mutex
}

func NewJobs(db *sqlite.DB, engine *k8s.Engine, cluster k8s.Cluster, snapshot func() *config.ResolvedConfig, domain string, log *slog.Logger) *Jobs {
	return &Jobs{db: db, engine: engine, cluster: cluster, snapshot: snapshot, domain: domain, log: log, targets: map[string]*sync.Mutex{}}
}

// targetLock serializes releases for one app. The control plane is a single
// process, so an in-process lock is sufficient to keep two workers from
// deploying the same target concurrently.
func (j *Jobs) targetLock(project, environment, app string) *sync.Mutex {
	key := project + "\x00" + environment + "\x00" + app
	j.targetMu.Lock()
	defer j.targetMu.Unlock()
	mu := j.targets[key]
	if mu == nil {
		mu = &sync.Mutex{}
		j.targets[key] = mu
	}
	return mu
}

func (j *Jobs) SetSender(sender *notify.Sender) { j.sender = sender }

// SetAllowHooks controls whether app lifecycle hooks may execute. Hooks are
// disabled unless the host explicitly opts in.
func (j *Jobs) SetAllowHooks(allow bool) { j.allowHooks = allow }

// emitDeploy sends a best-effort outbound deploy webhook for the app, if the
// project has one configured and subscribed to this event.
func (j *Jobs) EmitDeploy(app *config.ResolvedApp, ev notify.Event) {
	if j.sender == nil || app.Webhook == nil || !app.Webhook.Enabled || app.Webhook.URLRef == "" {
		return
	}
	resolver := secrets.NewResolver()
	url, err := resolver.Resolve(app.Webhook.URLRef)
	if err != nil {
		j.log.Warn("deploy webhook url could not be resolved", "app", app.Name, "error", err)
		return
	}
	secret := ""
	if app.Webhook.SecretRef != "" {
		if secret, err = resolver.Resolve(app.Webhook.SecretRef); err != nil {
			j.log.Warn("deploy webhook secret could not be resolved", "app", app.Name, "error", err)
		}
	}
	ev.Project = app.Project
	ev.Environment = app.Environment
	ev.App = app.Name
	ev.Repository = app.Repository
	ev.Tag = app.Tag
	sender := j.sender
	webhook := notify.Webhook{URL: url, Secret: secret, Events: app.Webhook.Events}
	ev.Timestamp = time.Now().UTC()
	payload := sqlite.MarshalWebhookPayload(ev)
	dedup := fmt.Sprintf("%s|%s|%s|%s|%s|%d", ev.Event, ev.Project, ev.Environment, ev.App, ev.Digest, ev.Timestamp.UnixNano())
	deliveryID, err := j.db.RecordWebhookDelivery(context.Background(), sqlite.WebhookDelivery{
		DedupKey: dedup, Project: ev.Project, Environment: ev.Environment, App: ev.App, Event: ev.Event, Payload: payload,
	})
	if err != nil {
		j.log.Warn("record webhook delivery", "app", app.Name, "error", err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := sender.SendResult(ctx, webhook, ev)
		if deliveryID != "" {
			status := result.Status
			if err != nil && status == "" {
				status = "failed"
			}
			_ = j.db.CompleteWebhookDelivery(context.Background(), deliveryID, status, result.Attempts, result.ResponseCode, result.Error)
		}
		if err != nil {
			j.log.Warn("deploy webhook delivery failed", "app", app.Name, "event", ev.Event, "error", err)
		}
	}()
}

// ReplayWebhook re-sends a stored delivery. It is an explicit operator action;
// the original delivery history is preserved.
func (j *Jobs) ReplayWebhook(ctx context.Context, id string) (string, error) {
	delivery, ok, err := j.db.WebhookDeliveryByID(ctx, id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("webhook delivery %q not found", id)
	}
	snap := j.snapshot()
	if snap == nil {
		return "", fmt.Errorf("no resolved configuration available")
	}
	app, err := snap.FindApp(delivery.Project, delivery.Environment, delivery.App)
	if err != nil {
		return "", err
	}
	var ev notify.Event
	if delivery.Payload != "" {
		if err := json.Unmarshal([]byte(delivery.Payload), &ev); err != nil {
			return "", fmt.Errorf("decode stored event: %w", err)
		}
	}
	ev.Event = delivery.Event
	j.EmitDeploy(app, ev)
	return delivery.ID, nil
}

func actorFor(kind string) string {
	switch kind {
	case "manual":
		return "manual"
	case "rollback":
		return "rollback"
	case "reconcile":
		return "reconcile"
	default:
		return "registry"
	}
}

func (j *Jobs) MatchJobs(event registry.PushEvent) []sqlite.Job {
	snap := j.snapshot()
	if snap == nil {
		return nil
	}
	repo := strings.TrimPrefix(event.Repository, "/")
	var jobs []sqlite.Job
	for _, rp := range snap.Projects {
		for _, re := range rp.Environments {
			for _, app := range re.Apps {
				if !app.Enabled {
					continue
				}
				if !repositoryMatches(j.domain, app.Repository, repo) {
					continue
				}
				if app.Tag != event.Tag {
					continue
				}
				dedup := registry.DedupKey(j.domain, app.Repository, app.Tag, event.DeliveryID)
				job := sqlite.Job{
					DedupKey:    dedup,
					Kind:        "deploy",
					Project:     app.Project,
					Environment: app.Environment,
					App:         app.Name,
					Repository:  app.Repository,
					Tag:         app.Tag,
				}
				jobs = append(jobs, PinJob(job, app, snap.Version, "registry", "registry-push"))
			}
		}
	}
	return jobs
}

func repositoryMatches(domain, configured, eventRepo string) bool {
	configured = strings.TrimPrefix(configured, "/")
	eventRepo = strings.TrimPrefix(eventRepo, "/")
	if configured == eventRepo {
		return true
	}
	if domain != "" {
		d := strings.TrimRight(domain, "/")
		if strings.HasPrefix(configured, d+"/") && strings.TrimPrefix(configured, d+"/") == eventRepo {
			return true
		}
	}
	return strings.HasSuffix(configured, "/"+eventRepo)
}

func (j *Jobs) RunWorkers(ctx context.Context, workers int) {
	if workers <= 0 {
		workers = 4
	}
	for i := 0; i < workers; i++ {
		go j.workerLoop(ctx, i)
	}
}

func (j *Jobs) workerLoop(ctx context.Context, id int) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := j.processBatch(ctx); err != nil {
				j.log.Warn("job batch failed", "worker", id, "error", err)
			}
		}
	}
}

func (j *Jobs) processBatch(ctx context.Context) error {
	jobs, err := j.db.PendingJobs(ctx, 10)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := j.ProcessJob(ctx, job); err != nil {
			j.log.Warn("job failed", "job", job.ID, "app", job.App, "error", err)
		}
	}
	return nil
}

func (j *Jobs) ProcessJob(ctx context.Context, job sqlite.Job) error {
	lock := j.targetLock(job.Project, job.Environment, job.App)
	lock.Lock()
	defer lock.Unlock()

	claimed, err := j.db.ClaimJob(ctx, job.ID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	if err := j.db.SupersedePendingJobs(ctx, job.Project, job.Environment, job.App, job.ID); err != nil {
		j.log.Warn("supersede pending jobs", "error", err)
	}
	snap := j.snapshot()
	if snap == nil && job.ConfigSnapshot == "" {
		_ = j.db.CompleteJob(ctx, job.ID, "")
		return fmt.Errorf("no resolved configuration available")
	}
	if snap != nil {
		if _, err := snap.FindApp(job.Project, job.Environment, job.App); err != nil {
			j.log.Warn("job target is no longer configured; dropping job", "project", job.Project, "environment", job.Environment, "app", job.App)
			return j.db.CompleteJob(ctx, job.ID, "")
		}
	}
	app, err := jobApp(job, snap)
	if err != nil {
		_ = j.db.FailJobTerminal(ctx, job.ID, err.Error())
		return err
	}
	if !app.Enabled {
		return j.db.CompleteJob(ctx, job.ID, "")
	}
	actor := job.Actor
	if actor == "" {
		actor = actorFor(job.Kind)
	}
	digest := job.DesiredDigest
	if digest == "" {
		digest, err = j.engine.ResolveDigest(ctx, app)
		if err != nil {
			return j.releaseFailed(ctx, job, app, "", "", digest, release.TriggerDeployError, true, true, fmt.Errorf("resolve digest: %w", err))
		}
	}
	image := k8s.ImageForDigest(app, digest)
	if err := j.waitForDependencies(ctx, app, snap); err != nil {
		return j.releaseFailed(ctx, job, app, "", "", digest, release.TriggerTimeout, true, true, err)
	}
	secretHash, err := k8s.SecretFingerprint(app)
	if err != nil {
		return j.releaseFailed(ctx, job, app, "", "", digest, release.TriggerDeployError, true, true, err)
	}

	// An idempotent retry: the workload already runs the desired image with the
	// same injected secrets. Record an active release if history is missing and
	// finish without a rollout.
	if current, err := j.cluster.WorkloadStatus(ctx, app.Namespace, app.Name); err == nil && current != nil {
		secretOK := secretHash == "" || current.SecretHash == secretHash
		if current.Image == image && secretOK && current.ReadyReplicas >= current.Replicas && current.Replicas >= app.Replicas.Min {
			active, hasActive, _ := j.db.ActiveRelease(ctx, app.Project, app.Environment, app.Name)
			configMatches := job.ConfigHash == "" || active.ConfigHash == "" || active.ConfigHash == job.ConfigHash
			if hasActive && active.Digest == digest && configMatches {
				j.log.Info("workload already at desired image and config; no-op", "app", app.Name, "image", image)
				if err := j.db.SetJobRelease(ctx, job.ID, active.ID); err != nil {
					return err
				}
				return j.db.CompleteJob(ctx, job.ID, digest)
			}
			if !hasActive {
				j.log.Info("workload already at desired image; recording release", "app", app.Name, "image", image)
				id, err := j.createRelease(ctx, job, app, digest, image, secretHash, "")
				if err != nil {
					return err
				}
				if err := j.db.SetJobRelease(ctx, job.ID, id); err != nil {
					return err
				}
				if err := j.db.TransitionRelease(ctx, id, release.Active, "workload already at desired image", actor); err != nil {
					return err
				}
				return j.db.CompleteJob(ctx, job.ID, digest)
			}
			// Same image but changed configuration or a stale secret hash: fall
			// through and apply so the workload picks the new config.
		}
	}

	previous, hasPrevious, err := j.db.ActiveRelease(ctx, app.Project, app.Environment, app.Name)
	if err != nil {
		return err
	}
	previousID := ""
	if hasPrevious {
		previousID = previous.ID
	}
	relID, err := j.createRelease(ctx, job, app, digest, image, secretHash, previousID)
	if err != nil {
		return err
	}
	_ = j.db.SetJobRelease(ctx, job.ID, relID)
	if j.jobCancelled(ctx, job.ID) {
		_ = j.db.TransitionRelease(ctx, relID, release.Failed, "job cancelled", actor)
		return nil
	}

	if previousID != "" && app.Storage != nil && app.Storage.Enabled && app.Storage.SnapshotBeforeDeploy {
		snapshot := oneOffName(app.Name, "snapshot")
		if err := j.cluster.CreateVolumeSnapshot(ctx, app.Namespace, app.Name+"-data", snapshot); err != nil {
			return j.releaseFailed(ctx, job, app, relID, previousID, digest, release.TriggerDeployError, true, true, err)
		}
		j.log.Info("volume snapshot requested", "app", app.Name, "snapshot", snapshot)
	}

	if app.Hooks != nil && app.Hooks.PreDeploy != nil {
		if err := j.runHook(ctx, app, image, "pre-deploy", app.Hooks.PreDeploy); err != nil {
			return j.releaseFailed(ctx, job, app, relID, previousID, digest, release.TriggerDeployError, true, true, err)
		}
	}
	migrated := false
	if app.Hooks != nil && app.Hooks.Migrate != nil {
		if err := j.db.TransitionRelease(ctx, relID, release.Migrating, "migration started", actor); err != nil {
			return err
		}
		if err := j.runHook(ctx, app, image, "migrate", app.Hooks.Migrate); err != nil {
			// A migration may have partially run. Do not retry it automatically;
			// apply the explicit recovery policy instead.
			return j.releaseFailed(ctx, job, app, relID, previousID, digest, release.TriggerDeployError, false, true, err)
		}
		migrated = true
	}
	if j.jobCancelled(ctx, job.ID) {
		_ = j.db.TransitionRelease(ctx, relID, release.Failed, "job cancelled", actor)
		return nil
	}
	if err := j.db.TransitionRelease(ctx, relID, release.Deploying, "applying workload", actor); err != nil {
		return err
	}
	result, err := j.engine.DeployResolved(ctx, app, digest)
	if err != nil {
		return j.releaseFailed(ctx, job, app, relID, previousID, digest, release.TriggerDeployError, !migrated, true, fmt.Errorf("deploy: %w", err))
	}
	st, err := k8s.WaitForRollout(ctx, j.cluster, app.Namespace, app.Name, k8s.RolloutOptions{Timeout: 5 * time.Minute})
	if err != nil {
		trigger := release.TriggerDeployError
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, k8s.ErrRolloutTimeout) {
			trigger = release.TriggerTimeout
		}
		return j.releaseFailed(ctx, job, app, relID, previousID, digest, trigger, !migrated, true, fmt.Errorf("rollout: %w", err))
	}

	if j.jobCancelled(ctx, job.ID) {
		_ = j.db.TransitionRelease(ctx, relID, release.Failed, "job cancelled", actor)
		return nil
	}
	if err := j.db.TransitionRelease(ctx, relID, release.Verifying, "rollout complete", actor); err != nil {
		return err
	}
	verification := j.verifyRelease(ctx, app, image)
	if err := j.db.UpdateReleaseFields(ctx, relID, verification.String(), "", "", secretHash); err != nil {
		j.log.Warn("record verification", "release", relID, "error", err)
	}
	if !verification.OK {
		recoverable := app.Verification == nil || app.Verification.FailurePolicy != config.VerificationFailureKeep
		return j.releaseFailed(ctx, job, app, relID, previousID, digest, release.TriggerVerificationFailed, false, recoverable, fmt.Errorf("verification failed: %s", verification.Error))
	}
	if app.Hooks != nil && app.Hooks.PostDeploy != nil {
		if err := j.runHook(ctx, app, image, "post-deploy", app.Hooks.PostDeploy); err != nil {
			return j.releaseFailed(ctx, job, app, relID, previousID, digest, release.TriggerDeployError, !migrated, true, err)
		}
	}
	if j.jobCancelled(ctx, job.ID) {
		_ = j.db.TransitionRelease(ctx, relID, release.Failed, "job cancelled", actor)
		return nil
	}
	finalState := release.Active
	finalMessage := "release active"
	if job.Kind == "rollback" {
		finalState = release.RolledBack
		finalMessage = "rolled back to recorded digest"
	}
	if err := j.db.TransitionRelease(ctx, relID, finalState, finalMessage, actor); err != nil {
		return err
	}

	_ = j.db.UpsertObserved(ctx, sqlite.Observed{
		Project:        app.Project,
		Environment:    app.Environment,
		App:            app.Name,
		DesiredDigest:  digest,
		ObservedDigest: digest,
		Image:          result.Image,
		Replicas:       st.Replicas,
		ReadyReplicas:  st.ReadyReplicas,
		Status:         "ready",
	})
	j.log.Info("deployment complete", "project", app.Project, "environment", app.Environment, "app", app.Name, "digest", digest, "release", relID)
	eventType, status := "deploy.succeeded", "succeeded"
	if job.Kind == "rollback" {
		eventType, status = "deploy.rolled_back", "rolled_back"
	}
	j.EmitDeploy(app, notify.Event{
		Event:         eventType,
		Action:        "deploy",
		Status:        status,
		Actor:         actor,
		Digest:        digest,
		Image:         result.Image,
		ReleaseID:     relID,
		ConfigVersion: job.ConfigVersion,
		Message:       fmt.Sprintf("%s/%s/%s %s (%s)", app.Project, app.Environment, app.Name, status, digest),
	})
	return j.db.CompleteJob(ctx, job.ID, digest)
}

func (j *Jobs) createRelease(ctx context.Context, job sqlite.Job, app *config.ResolvedApp, digest, image, secretHash, previousID string) (string, error) {
	verification, migration, recovery := "", "", ""
	if app.Verification != nil {
		verification = marshalSpec(app.Verification)
	}
	if app.Hooks != nil && app.Hooks.Migrate != nil {
		migration = marshalSpec(app.Hooks.Migrate)
	}
	if app.Recovery != nil {
		recovery = marshalSpec(app.Recovery)
	}
	return j.db.CreateRelease(ctx, sqlite.Release{
		Project:        app.Project,
		Environment:    app.Environment,
		App:            app.Name,
		Repository:     app.Repository,
		Tag:            app.Tag,
		Digest:         digest,
		Image:          image,
		PreviousID:     previousID,
		ConfigVersion:  job.ConfigVersion,
		ConfigHash:     job.ConfigHash,
		ConfigSnapshot: job.ConfigSnapshot,
		SecretHash:     secretHash,
		Verification:   verification,
		Migration:      migration,
		Recovery:       recovery,
		SourceRepo:     app.Source.Repo,
		SourceRevision: app.Source.Revision,
		Actor:          job.Actor,
	})
}

func marshalSpec(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func (j *Jobs) runHook(ctx context.Context, app *config.ResolvedApp, image, kind string, hook *config.HookSpec) error {
	if hook == nil {
		return nil
	}
	if !j.allowHooks {
		return fmt.Errorf("%s hook refused: app hooks are disabled on this host (security.allowAppHooks)", kind)
	}
	secretName := ""
	if len(app.Secrets) > 0 {
		var err error
		secretName, _, err = j.engine.EnsureAppSecrets(ctx, app)
		if err != nil {
			return err
		}
	}
	name := oneOffName(app.Name, kind)
	req := k8s.JobRequest{
		Namespace:     app.Namespace,
		Name:          name,
		Project:       app.Project,
		Environment:   app.Environment,
		App:           app.Name,
		Image:         image,
		Command:       hook.Command,
		Env:           app.Env,
		SecretEnvName: secretName,
		SecretEnv:     map[string]string{},
		Timeout:       hook.Timeout,
		PullPolicy:    app.PullPolicy,
	}
	for envName := range app.Secrets {
		req.SecretEnv[envName] = envName
	}
	res, err := j.cluster.RunJob(ctx, req)
	if res != nil && res.Logs != "" {
		logs := res.Logs
		if len(logs) > 4096 {
			logs = logs[len(logs)-4096:]
		}
		j.log.Info("app hook output", "app", app.Name, "hook", kind, "job", name, "logs", logs)
	}
	if err != nil {
		return fmt.Errorf("%s hook %s failed: %w", kind, name, err)
	}
	return nil
}

// waitForDependencies blocks until apps named in dependsOn are ready. Ordering
// is deliberately minimal: only explicit dependsOn edges are honored, with a
// bounded wait so a broken dependency cannot hang a worker forever.
func (j *Jobs) waitForDependencies(ctx context.Context, app *config.ResolvedApp, snap *config.ResolvedConfig) error {
	if len(app.DependsOn) == 0 || snap == nil {
		return nil
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		allReady := true
		for _, name := range app.DependsOn {
			dep, err := snap.FindApp(app.Project, app.Environment, name)
			if err != nil {
				return fmt.Errorf("dependency %q: %w", name, err)
			}
			if !dep.Enabled {
				continue
			}
			st, err := j.cluster.WorkloadStatus(ctx, dep.Namespace, dep.Name)
			if err != nil || st == nil || st.ReadyReplicas < dep.Replicas.Min {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("dependencies %v did not become ready within 5m", app.DependsOn)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// releaseFailed records the failed release state, reports the failure, retries
// transient jobs and, when the job is terminal, applies the app's explicit
// recovery policy. Recovery restores the previous image; it never claims to
// reverse data or migration changes.
func (j *Jobs) releaseFailed(ctx context.Context, job sqlite.Job, app *config.ResolvedApp, relID, previousID, digest string, trigger release.RecoveryTrigger, retryable, recoverable bool, cause error) error {
	actor := job.Actor
	if actor == "" {
		actor = actorFor(job.Kind)
	}
	if relID != "" {
		if err := j.db.TransitionRelease(ctx, relID, release.Failed, cause.Error(), actor); err != nil {
			j.log.Warn("mark release failed", "release", relID, "error", err)
		}
	}
	j.EmitDeploy(app, notify.Event{
		Event:         "deploy.failed",
		Action:        "deploy",
		Status:        "failed",
		Actor:         actor,
		Digest:        digest,
		ReleaseID:     relID,
		ConfigVersion: job.ConfigVersion,
		Message:       fmt.Sprintf("%s/%s/%s deploy failed: %v", app.Project, app.Environment, app.Name, cause),
	})
	terminal := !retryable
	dead := false
	if terminal {
		_ = j.db.FailJobTerminal(ctx, job.ID, cause.Error())
		dead = !j.jobCancelled(ctx, job.ID)
	} else if d, err := j.db.FailJob(ctx, job.ID, cause.Error()); err == nil {
		dead = d
	}
	if dead && recoverable {
		j.recoverRelease(ctx, app, relID, previousID, trigger)
	}
	return cause
}

func (j *Jobs) recoverRelease(ctx context.Context, app *config.ResolvedApp, relID, previousID string, trigger release.RecoveryTrigger) {
	if relID == "" || previousID == "" || app.Recovery == nil {
		return
	}
	policy := release.RecoveryPolicy(app.Recovery.Policy)
	var triggers []release.RecoveryTrigger
	for _, t := range app.Recovery.Triggers {
		triggers = append(triggers, release.RecoveryTrigger(t))
	}
	if policy == release.RecoveryRequireApproval {
		j.EmitDeploy(app, notify.Event{Event: "deploy.recovery_pending", Action: "recover", Status: "pending_approval", Actor: "system", Message: fmt.Sprintf("%s/%s/%s failed (%s); recovery to the previous release requires approval", app.Project, app.Environment, app.Name, trigger)})
		return
	}
	if !release.ShouldRecover(policy, triggers, trigger) {
		return
	}
	previous, ok, err := j.db.ReleaseByID(ctx, previousID)
	if err != nil || !ok {
		j.log.Warn("recovery skipped: previous release unavailable", "release", previousID, "error", err)
		return
	}
	if previous.Image == "" {
		switch {
		case previous.Digest != "":
			previous.Image = previous.Repository + "@" + previous.Digest
		case previous.Tag != "":
			previous.Image = previous.Repository + ":" + previous.Tag
		}
	}
	if previous.Image == "" {
		j.log.Warn("recovery skipped: previous release image unavailable", "release", previousID)
		return
	}
	if err := j.db.TransitionRelease(ctx, relID, release.Recovering, "recovering previous release "+previous.ID, "system"); err != nil {
		j.log.Warn("start recovery", "release", relID, "error", err)
		return
	}
	recoverApp := app
	if previous.ConfigSnapshot != "" {
		var pinned config.ResolvedApp
		if err := json.Unmarshal([]byte(previous.ConfigSnapshot), &pinned); err == nil {
			recoverApp = &pinned
		}
	}
	j.log.Info("recovering previous release", "app", recoverApp.Name, "release", relID, "previous", previous.ID, "image", previous.Image)
	if _, err := j.engine.Apply(ctx, recoverApp, previous.Image); err != nil {
		_ = j.db.TransitionRelease(ctx, relID, release.Failed, "recovery apply failed: "+err.Error(), "system")
		j.EmitDeploy(app, notify.Event{Event: "deploy.recovery_failed", Action: "recover", Status: "failed", Actor: "system", Digest: previous.Digest, Message: fmt.Sprintf("%s/%s/%s recovery failed: %v", app.Project, app.Environment, app.Name, err)})
		return
	}
	st, err := k8s.WaitForRollout(ctx, j.cluster, recoverApp.Namespace, recoverApp.Name, k8s.RolloutOptions{Timeout: 5 * time.Minute})
	if err != nil {
		_ = j.db.TransitionRelease(ctx, relID, release.Failed, "recovery rollout failed: "+err.Error(), "system")
		j.EmitDeploy(app, notify.Event{Event: "deploy.recovery_failed", Action: "recover", Status: "failed", Actor: "system", Digest: previous.Digest, Message: fmt.Sprintf("%s/%s/%s recovery rollout failed: %v", app.Project, app.Environment, app.Name, err)})
		return
	}
	_ = j.db.UpsertObserved(ctx, sqlite.Observed{
		Project:        app.Project,
		Environment:    app.Environment,
		App:            app.Name,
		DesiredDigest:  previous.Digest,
		ObservedDigest: previous.Digest,
		Image:          previous.Image,
		Replicas:       st.Replicas,
		ReadyReplicas:  st.ReadyReplicas,
		Status:         "ready",
	})
	failedRelease, _, _ := j.db.ReleaseByID(ctx, relID)
	migrationState := previous.Migration
	if failedRelease.Migration != "" {
		migrationState = failedRelease.Migration
	}
	restored, createErr := j.db.CreateRelease(ctx, sqlite.Release{
		Project:        previous.Project,
		Environment:    previous.Environment,
		App:            previous.App,
		Repository:     previous.Repository,
		Tag:            previous.Tag,
		Digest:         previous.Digest,
		Image:          previous.Image,
		State:          string(release.RolledBack),
		PreviousID:     previous.ID,
		ConfigVersion:  previous.ConfigVersion,
		ConfigHash:     previous.ConfigHash,
		ConfigSnapshot: previous.ConfigSnapshot,
		SecretHash:     previous.SecretHash,
		Migration:      migrationState,
		Recovery:       previous.Recovery,
		SourceRepo:     previous.SourceRepo,
		SourceRevision: previous.SourceRevision,
		Actor:          "system",
		Message:        "restored previous release after failure",
	})
	if createErr != nil {
		j.log.Warn("record restored release", "error", createErr)
	}
	_ = j.db.TransitionRelease(ctx, relID, release.Failed, "recovery restored previous release", "system")
	if restored != "" {
		_ = j.db.TransitionRelease(ctx, restored, release.RolledBack, "restored previous release "+previous.ID, "system")
	}
	j.EmitDeploy(app, notify.Event{Event: "deploy.rolled_back", Action: "recover", Status: "rolled_back", Actor: "system", Digest: previous.Digest, Image: previous.Image, Message: fmt.Sprintf("%s/%s/%s recovered to %s", app.Project, app.Environment, app.Name, previous.Digest)})
}

func (j *Jobs) Ingest(ctx context.Context, event registry.PushEvent) (bool, int, error) {
	jobs := j.MatchJobs(event)
	ev := &sqlite.Event{
		DedupKey:   registry.DedupKey(j.domain, event.Repository, event.Digest, event.DeliveryID),
		Registry:   j.domain,
		Repository: event.Repository,
		Tag:        event.Tag,
		Digest:     event.Digest,
		Delivery:   event.DeliveryID,
	}
	created, err := j.db.IngestRegistryEvent(ctx, ev, jobs)
	if err != nil {
		return false, 0, err
	}
	return created, len(jobs), nil
}

// oneOffName builds a DNS-safe Job/Snapshot name that keeps the unique suffix
// even when the app name is long.
func oneOffName(app, kind string) string {
	suffix := "-" + kind + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	base := k8s.SanitizeName(app)
	if max := 63 - len(suffix); len(base) > max {
		base = strings.TrimRight(base[:max], "-")
	}
	return base + suffix
}

// HandleInterruptedRelease marks a release left by a restarted control plane as
// failed and applies the interrupted recovery trigger.
func (j *Jobs) HandleInterruptedRelease(ctx context.Context, rel sqlite.Release) {
	lock := j.targetLock(rel.Project, rel.Environment, rel.App)
	lock.Lock()
	defer lock.Unlock()

	if err := j.db.TransitionRelease(ctx, rel.ID, release.Failed, "interrupted; no active job", "system"); err != nil {
		j.log.Warn("mark interrupted release failed", "release", rel.ID, "error", err)
		return
	}
	snap := j.snapshot()
	if snap == nil {
		return
	}
	app, err := snap.FindApp(rel.Project, rel.Environment, rel.App)
	if err != nil {
		return
	}
	j.recoverRelease(ctx, app, rel.ID, rel.PreviousID, release.TriggerInterrupted)
}

func (j *Jobs) jobCancelled(ctx context.Context, id string) bool {
	job, ok, err := j.db.JobByID(ctx, id)
	return err == nil && ok && job.Status == sqlite.JobStatusCancelled
}
