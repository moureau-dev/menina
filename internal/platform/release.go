package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/moureau-dev/menina/internal/config"
	"github.com/moureau-dev/menina/internal/database"
	"github.com/moureau-dev/menina/internal/deployment"
	"github.com/moureau-dev/menina/internal/notify"
	"github.com/moureau-dev/menina/internal/release"
)

// releaseComponent is one app's outcome inside an environment release.
type releaseComponent struct {
	App         string `json:"app"`
	ReleaseID   string `json:"releaseId"`
	State       string `json:"state"`
	Digest      string `json:"digest,omitempty"`
	Image       string `json:"image,omitempty"`
	CodeState   string `json:"codeState"`
	ConfigState string `json:"configState"`
	DataState   string `json:"dataState"`
	Message     string `json:"message,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

// environmentRelease is an aggregate, never an atomic operation.
type environmentRelease struct {
	Project     string             `json:"project"`
	Environment string             `json:"environment"`
	Status      string             `json:"status"`
	Components  []releaseComponent `json:"components"`
	Code        string             `json:"code"`
	Config      string             `json:"config"`
	Data        string             `json:"data"`
	GeneratedAt string             `json:"generatedAt"`
}

func (s *Service) handleRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	project, environment, app := q.Get("project"), q.Get("environment"), q.Get("app")
	if app != "" {
		releases, err := s.db.Releases(r.Context(), project, environment, app)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"releases": releases})
		return
	}
	aggregate, err := s.environmentRelease(r, project, environment)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, aggregate)
}

func (s *Service) environmentRelease(r *http.Request, project, environment string) (*environmentRelease, error) {
	releases, err := s.db.ReleasesByEnvironment(r.Context(), project, environment)
	if err != nil {
		return nil, err
	}
	out := &environmentRelease{
		Project: project, Environment: environment,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	statuses := map[string]bool{}
	seen := map[string]bool{}
	anyFailed, allFailed := false, len(releases) > 0
	anyUnreleased := false
	for _, rel := range releases {
		seen[rel.App] = true
		component := releaseComponent{
			App:         rel.App,
			ReleaseID:   rel.ID,
			State:       rel.State,
			Digest:      rel.Digest,
			Image:       rel.Image,
			CodeState:   rel.State,
			ConfigState: "pinned",
			DataState:   "none",
			Message:     rel.Message,
		}
		if rel.ConfigVersion == 0 && rel.ConfigSnapshot == "" {
			component.ConfigState = "unpinned"
		}
		if rel.Migration != "" {
			component.DataState = "migrated"
			switch release.State(rel.State) {
			case release.Failed, release.Recovering, release.RolledBack:
				component.DataState = "migration-not-reversed"
			}
		}
		if !rel.UpdatedAt.IsZero() {
			component.UpdatedAt = rel.UpdatedAt.UTC().Format(time.RFC3339)
		}
		out.Components = append(out.Components, component)
		statuses[rel.State] = true
		if rel.State == string(release.Failed) {
			anyFailed = true
		} else {
			allFailed = false
		}
	}
	if s.watcher != nil {
		if snap := s.watcher.Snapshot(); snap != nil && snap.Resolved != nil {
			if rp, ok := snap.Resolved.Projects[project]; ok {
				if env, ok := rp.Environments[environment]; ok {
					names := make([]string, 0, len(env.Apps))
					for name := range env.Apps {
						names = append(names, name)
					}
					sort.Strings(names)
					for _, name := range names {
						app := env.Apps[name]
						if app == nil || !app.Enabled || seen[name] {
							continue
						}
						out.Components = append(out.Components, releaseComponent{
							App: name, State: "unreleased", CodeState: "unreleased",
							ConfigState: "pinned", DataState: "none", Message: "no release recorded",
						})
						anyUnreleased = true
					}
				}
			}
		}
	}
	sort.Slice(out.Components, func(i, j int) bool { return out.Components[i].App < out.Components[j].App })
	switch {
	case len(out.Components) == 0:
		out.Status = "empty"
	case anyFailed && allFailed:
		out.Status = "failed"
	case anyFailed:
		out.Status = "partially_failed"
	case anyUnreleased:
		out.Status = "in_progress"
	case statuses[string(release.Active)] || statuses[string(release.RolledBack)]:
		out.Status = "succeeded"
	default:
		out.Status = "in_progress"
	}
	out.Code = out.Status
	out.Config = "pinned"
	for _, c := range out.Components {
		if c.ConfigState != "pinned" {
			out.Config = "partially_unpinned"
		}
		if c.DataState == "migration-not-reversed" {
			out.Data = "migration_not_reversed"
		}
	}
	if out.Data == "" {
		out.Data = "none"
	}
	return out, nil
}

type promoteRequest struct {
	Project         string `json:"project"`
	FromEnvironment string `json:"fromEnvironment"`
	ToEnvironment   string `json:"toEnvironment"`
	App             string `json:"app"`
	StateToken      string `json:"stateToken"`
	DryRun          bool   `json:"dryRun"`
	Confirm         bool   `json:"confirm"`
}

type promotePlan struct {
	Project         string `json:"project"`
	App             string `json:"app"`
	FromEnvironment string `json:"fromEnvironment"`
	ToEnvironment   string `json:"toEnvironment"`
	Digest          string `json:"digest"`
	Image           string `json:"image"`
	SourceRelease   string `json:"sourceRelease"`
	StateToken      string `json:"stateToken"`
	ConfigVersion   int64  `json:"configVersion"`
}

func (s *Service) handlePromote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req promoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Project == "" || req.App == "" || req.FromEnvironment == "" || req.ToEnvironment == "" {
		http.Error(w, "project, app, fromEnvironment and toEnvironment are required", http.StatusBadRequest)
		return
	}
	if !req.DryRun {
		if err := s.requireProductionConfirmation(req.ToEnvironment, req.Confirm); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}
	snap := s.watcher.Snapshot()
	if snap == nil || snap.Resolved == nil {
		http.Error(w, "no resolved configuration", http.StatusServiceUnavailable)
		return
	}
	if _, err := snap.Resolved.FindApp(req.Project, req.FromEnvironment, req.App); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	target, err := snap.Resolved.FindApp(req.Project, req.ToEnvironment, req.App)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !target.Enabled {
		http.Error(w, fmt.Sprintf("%s/%s/%s is disabled and cannot receive a promotion", req.Project, req.ToEnvironment, req.App), http.StatusBadRequest)
		return
	}
	sourceRelease, ok, err := s.db.ActiveRelease(r.Context(), req.Project, req.FromEnvironment, req.App)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, fmt.Sprintf("%s/%s/%s has no active release to promote", req.Project, req.FromEnvironment, req.App), http.StatusConflict)
		return
	}
	if sourceRelease.Digest == "" {
		http.Error(w, "source release has no immutable digest; promote requires a bundled-registry digest", http.StatusBadRequest)
		return
	}
	token, err := environmentStateToken(snap.Resolved, req.Project, req.ToEnvironment)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	plan := promotePlan{
		Project: req.Project, App: req.App,
		FromEnvironment: req.FromEnvironment, ToEnvironment: req.ToEnvironment,
		Digest: sourceRelease.Digest, Image: sourceRelease.Image,
		SourceRelease: sourceRelease.ID, StateToken: token, ConfigVersion: snap.Resolved.Version,
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "queued": false})
		return
	}
	if req.StateToken != "" && req.StateToken != token {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{"code": "conflict", "message": "target environment state changed since the plan was reviewed", "hint": "re-run promote --dry-run and review the new plan"},
			"plan":  plan,
		})
		return
	}
	if sourceRelease.Repository != target.Repository {
		http.Error(w, fmt.Sprintf("source and target repositories differ (%s vs %s); a digest is repository-scoped", sourceRelease.Repository, target.Repository), http.StatusBadRequest)
		return
	}
	dedup := fmt.Sprintf("promote|%s|%s|%s|%s|%d", req.Project, req.FromEnvironment, req.ToEnvironment, req.App, time.Now().UnixNano())
	job := PinJob(database.Job{
		DedupKey:      dedup,
		Kind:          "promote",
		Project:       req.Project,
		Environment:   req.ToEnvironment,
		App:           req.App,
		Repository:    target.Repository,
		Tag:           target.Tag,
		DesiredDigest: sourceRelease.Digest,
	}, target, snap.Resolved.Version, "promotion", fmt.Sprintf("%s/%s", req.Project, req.FromEnvironment))
	_, err = s.db.IngestRegistryEvent(r.Context(), &database.Event{
		DedupKey: dedup, Registry: s.cfg.Registry.Domain, Repository: target.Repository, Digest: sourceRelease.Digest,
	}, []database.Job{job})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, _, _ := s.db.JobIDByDedupKey(r.Context(), dedup)
	s.jobs.EmitDeploy(target, notify.Event{
		Event: "deploy.queued", Action: "promote", Status: "queued", Actor: "promotion",
		Digest: sourceRelease.Digest, Message: fmt.Sprintf("%s/%s/%s promotion from %s queued", req.Project, req.ToEnvironment, req.App, req.FromEnvironment),
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "jobId": id, "plan": plan})
}

func environmentStateToken(rc *config.ResolvedConfig, project, environment string) (string, error) {
	rp, ok := rc.Projects[project]
	if !ok {
		return "", fmt.Errorf("project %q not found", project)
	}
	env, ok := rp.Environments[environment]
	if !ok {
		return "", fmt.Errorf("environment %q not found in project %q", environment, project)
	}
	data, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// secretStale reports whether injected secrets changed since the active
// release rolled out. It returns false when the release or app is unknown.
func (s *Service) secretStale(r *http.Request, app *config.ResolvedApp) (bool, string) {
	if len(app.Secrets) == 0 {
		return false, ""
	}
	active, ok, err := s.db.ActiveRelease(r.Context(), app.Project, app.Environment, app.Name)
	if err != nil || !ok || active.SecretHash == "" {
		return false, ""
	}
	current, err := deployment.SecretFingerprint(app)
	if err != nil || current == "" {
		return false, ""
	}
	if current == active.SecretHash {
		return false, ""
	}
	return true, "secret rotated, rollout stale"
}
