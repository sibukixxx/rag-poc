package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sibukixxx/rag-poc/internal/domain/audit"
)

// AuditHandler exposes the security audit trail to operators (#24).
type AuditHandler struct {
	store audit.Store
}

func NewAuditHandler(store audit.Store) *AuditHandler {
	return &AuditHandler{store: store}
}

// List handles GET /api/v1/audit-events?limit=N (newest first, max 1000).
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := h.store.ListEvents(r.Context(), limit)
	if err != nil {
		internalError(w, "listing audit events", err)
		return
	}
	if events == nil {
		events = []audit.Event{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(events)
}
