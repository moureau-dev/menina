package platform

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/notify"
	"github.com/moureau-dev/menina/internal/registry"
	"github.com/moureau-dev/menina/internal/secrets"
)

type Jobs struct {
	db       *database.DB
	engine   *deployment.Engine
	cluster  deployment.Cluster
	snapshot func() *config.ResolvedConfig
	domain   string
	log      *slog.Logger
	sender   *notify.Sender
}

func NewJobs(db *database.DB, engine *deployment.Engine, cluster deployment.Cluster, snapshot func() *config.ResolvedConfig, domain string, log *slog.Logger) *Jobs {
	return &Jobs{db: db, engine: engine, cluster: cluster, snapshot: snapshot, domain: domain, log: log}
}

func (j *Jobs) SetSender(sender *notify.Sender) { j.sender = sender }

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
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := sender.Send(ctx, webhook, ev); err != nil {
			j.log.Warn("deploy webhook delivery failed", "app", app.Name, "event", ev.Event, "error", err)
		}
	}()
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

func (j *Jobs) MatchJobs(event registry.PushEvent) []database.Job {
	snap := j.snapshot()
	if snap == nil {
		return nil
	}
	repo := strings.TrimPrefix(event.Repository, "/")
	var jobs []database.Job
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
				jobs = append(jobs, database.Job{
					DedupKey:    dedup,
					Kind:        "deploy",
					Project:     app.Project,
					Environment: app.Environment,
					App:         app.Name,
					Repository:  app.Repository,
					Tag:         app.Tag,
				})
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

func (j *Jobs) ProcessJob(ctx context.Context, job database.Job) error {
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
	if snap == nil {
		_ = j.db.CompleteJob(ctx, job.ID, "")
		return fmt.Errorf("no resolved configuration available")
	}
	app, err := snap.FindApp(job.Project, job.Environment, job.App)
	if err != nil {
		// The target is no longer part of the desired configuration (deleted,
		// renamed or its environment removed). Drop the job instead of retrying
		// it forever.
		j.log.Warn("job target is no longer configured; dropping job", "project", job.Project, "environment", job.Environment, "app", job.App)
		return j.db.CompleteJob(ctx, job.ID, "")
	}
	if !app.Enabled {
		return j.db.CompleteJob(ctx, job.ID, "")
	}
	digest := job.DesiredDigest
	if digest == "" {
		digest, err = j.engine.ResolveDigest(ctx, app)
		if err != nil {
			if dead, _ := j.db.FailJob(ctx, job.ID, err.Error()); dead {
				j.EmitDeploy(app, notify.Event{Event: "deploy.failed", Action: "deploy", Status: "failed", Actor: actorFor(job.Kind), Digest: digest, Message: fmt.Sprintf("%s/%s/%s deploy failed: %v", app.Project, app.Environment, app.Name, err)})
			}
			return fmt.Errorf("resolve digest: %w", err)
		}
	}
	current, err := j.cluster.WorkloadStatus(ctx, app.Namespace, app.Name)
	if err == nil && current != nil {
		desiredImage := app.Repository + "@" + digest
		if current.Image == desiredImage && current.ReadyReplicas >= current.Replicas && current.Replicas >= app.Replicas {
			j.log.Info("workload already at desired digest; no-op", "app", app.Name, "digest", digest)
			// A previous run may have applied the workload and crashed before
			// recording the release; make sure the history is complete.
			if recorded, _ := j.db.HasRelease(ctx, app.Project, app.Environment, app.Name, digest); !recorded {
				_ = j.db.RecordRelease(ctx, database.Release{
					Project:     app.Project,
					Environment: app.Environment,
					App:         app.Name,
					Repository:  app.Repository,
					Tag:         app.Tag,
					Digest:      digest,
					Status:      "deployed",
				})
			}
			return j.db.CompleteJob(ctx, job.ID, digest)
		}
	}
	result, err := j.engine.DeployResolved(ctx, app, digest)
	if err != nil {
		if dead, _ := j.db.FailJob(ctx, job.ID, err.Error()); dead {
			j.EmitDeploy(app, notify.Event{Event: "deploy.failed", Action: "deploy", Status: "failed", Actor: actorFor(job.Kind), Digest: digest, Message: fmt.Sprintf("%s/%s/%s deploy failed: %v", app.Project, app.Environment, app.Name, err)})
		}
		return fmt.Errorf("deploy: %w", err)
	}
	st, err := deployment.WaitForRollout(ctx, j.cluster, app.Namespace, app.Name, deployment.RolloutOptions{Timeout: 5 * time.Minute})
	if err != nil {
		if dead, _ := j.db.FailJob(ctx, job.ID, err.Error()); dead {
			j.EmitDeploy(app, notify.Event{Event: "deploy.failed", Action: "deploy", Status: "failed", Actor: actorFor(job.Kind), Digest: digest, Message: fmt.Sprintf("%s/%s/%s rollout failed: %v", app.Project, app.Environment, app.Name, err)})
		}
		return fmt.Errorf("rollout: %w", err)
	}
	// Record the release only after the rollout succeeds, so history never
	// claims a deployment that is not actually running in Kubernetes.
	_ = j.db.RecordRelease(ctx, database.Release{
		Project:     app.Project,
		Environment: app.Environment,
		App:         app.Name,
		Repository:  app.Repository,
		Tag:         app.Tag,
		Digest:      digest,
		Status:      "deployed",
	})
	_ = j.db.UpsertObserved(ctx, database.Observed{
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
	j.log.Info("deployment complete", "project", app.Project, "environment", app.Environment, "app", app.Name, "digest", digest)
	eventType, status := "deploy.succeeded", "succeeded"
	if job.Kind == "rollback" {
		eventType, status = "deploy.rolled_back", "rolled_back"
	}
	j.EmitDeploy(app, notify.Event{
		Event:   eventType,
		Action:  "deploy",
		Status:  status,
		Actor:   actorFor(job.Kind),
		Digest:  digest,
		Image:   result.Image,
		Message: fmt.Sprintf("%s/%s/%s %s (%s)", app.Project, app.Environment, app.Name, status, digest),
	})
	return j.db.CompleteJob(ctx, job.ID, digest)
}

func (j *Jobs) Ingest(ctx context.Context, event registry.PushEvent) (bool, int, error) {
	jobs := j.MatchJobs(event)
	ev := &database.Event{
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
