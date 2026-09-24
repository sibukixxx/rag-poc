package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/domain/lifecycle"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// LifecycleHandler exposes customer-data deletion (#23). Deletion of an
// already-deleted object succeeds with a zero report so clients can retry.
type LifecycleHandler struct {
	lifecycle *usecase.DataLifecycleUseCase
}

func NewLifecycleHandler(uc *usecase.DataLifecycleUseCase) *LifecycleHandler {
	return &LifecycleHandler{lifecycle: uc}
}

// DeleteDocument handles DELETE /api/v1/knowledge-bases/{id}/documents/{docID}.
func (h *LifecycleHandler) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	report, err := h.lifecycle.DeleteDocument(r.Context(), chi.URLParam(r, "docID"))
	if err != nil {
		internalError(w, "deleting document", err)
		return
	}
	writeDeletionReport(w, report)
}

// DeleteKnowledgeBase handles DELETE /api/v1/knowledge-bases/{id}. A
// knowledge base serving a Deployment is only deleted together with its
// deployments when include_deployments=true is passed.
func (h *LifecycleHandler) DeleteKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	include := r.URL.Query().Get("include_deployments") == "true"
	report, err := h.lifecycle.DeleteKnowledgeBase(r.Context(), chi.URLParam(r, "id"), include)
	if errors.Is(err, lifecycle.ErrKnowledgeBaseHasDeployments) {
		http.Error(w, "knowledge base has deployments; retry with include_deployments=true to delete them too", http.StatusConflict)
		return
	}
	if err != nil {
		internalError(w, "deleting knowledge base", err)
		return
	}
	writeDeletionReport(w, report)
}

func writeDeletionReport(w http.ResponseWriter, report lifecycle.DeletionReport) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}
