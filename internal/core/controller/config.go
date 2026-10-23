package controller

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/moureau-dev/menina/internal/api"
	apierrors "github.com/moureau-dev/menina/internal/api/errors"
	"github.com/moureau-dev/menina/internal/core/config"
	configplan "github.com/moureau-dev/menina/internal/core/config/plan"
)

func (s *Service) handleConfigApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req api.ApplyPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, apierrors.New(apierrors.CodeValidation, err.Error(), ""))
		return
	}
	actor := api.ActorFromContext(r.Context())
	if actor.ID == "" {
		actor.ID = "cli"
	}
	result, err := configplan.Apply(s.cfg.Host.ConfigDir, &req.Plan, req.IfStateToken, req.Verify, actor.ID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleConfigRevisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	revisions, err := config.ListRevisions(s.cfg.Host.ConfigDir)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	project, environment := r.URL.Query().Get("project"), r.URL.Query().Get("environment")
	filtered := revisions[:0]
	for _, rev := range revisions {
		if project != "" && rev.Project != project {
			continue
		}
		if environment != "" && rev.Environment != environment {
			continue
		}
		filtered = append(filtered, rev)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].CreatedAt.After(filtered[j].CreatedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"revisions": filtered})
}

func (s *Service) handleConfigRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID      string `json:"id"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, apierrors.New(apierrors.CodeValidation, err.Error(), ""))
		return
	}
	if !req.Confirm {
		writeAPIError(w, apierrors.New(apierrors.CodeValidation, "config rollback requires confirmation", "review `config revisions` and retry with --yes"))
		return
	}
	lock, err := config.AcquireLock(s.cfg.Host.ConfigDir, 10*time.Second)
	if err != nil {
		writeAPIError(w, apierrors.Wrap(apierrors.CodeConflict, err))
		return
	}
	defer lock.Release()
	if err := lock.Check(); err != nil {
		writeAPIError(w, apierrors.Wrap(apierrors.CodeConflict, err))
		return
	}
	if err := config.RestoreRevision(s.cfg.Host.ConfigDir, req.ID); err != nil {
		writeAPIError(w, apierrors.Wrap(apierrors.CodeNotFound, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restored": req.ID})
}

func writeAPIError(w http.ResponseWriter, err error) {
	code, message, hint, _ := apierrors.Info(err)
	status := http.StatusInternalServerError
	switch code {
	case apierrors.CodeValidation:
		status = http.StatusBadRequest
	case apierrors.CodeNotFound:
		status = http.StatusNotFound
	case apierrors.CodeConflict:
		status = http.StatusConflict
	case apierrors.CodeAuth:
		status = http.StatusForbidden
	case apierrors.CodeTimeout:
		status = http.StatusGatewayTimeout
	case apierrors.CodeUnsupported:
		status = http.StatusNotImplemented
	}
	body := map[string]any{"code": code, "message": message}
	if hint != "" {
		body["hint"] = hint
	}
	writeJSON(w, status, map[string]any{"error": body})
}
