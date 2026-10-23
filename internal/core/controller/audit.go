package controller

import (
	"net/http"
	"strconv"

	"github.com/moureau-dev/menina/internal/core/audit"
)

func (s *Service) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.db.AuditEvents(r.Context(), audit.Filter{
		ActorID: r.URL.Query().Get("actor"),
		Project: r.URL.Query().Get("project"),
		Action:  r.URL.Query().Get("action"),
		Status:  r.URL.Query().Get("status"),
		Limit:   limit,
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
