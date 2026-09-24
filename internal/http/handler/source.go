package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/domain/bulk"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// JobScheduler runs queued ingestion jobs outside the request.
type JobScheduler interface {
	Enqueue(jobID string)
}

// SourceHandler is the management API for source connections and bulk
// ingestion jobs (#30). The browser configures; the server does the work.
type SourceHandler struct {
	bulk      *usecase.BulkIngestUseCase
	scheduler JobScheduler
}

func NewSourceHandler(uc *usecase.BulkIngestUseCase, scheduler JobScheduler) *SourceHandler {
	return &SourceHandler{bulk: uc, scheduler: scheduler}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeSourceError maps use-case errors to stable responses.
func writeSourceError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, usecase.ErrInvalidSourceConfig):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, usecase.ErrJobStateConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, source.ErrNotFound), errors.Is(err, bulk.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	default:
		log.Printf("sources: %s: %v", what, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// CreateConnection handles POST /api/v1/source-connections.
func (h *SourceHandler) CreateConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KnowledgeBaseID string   `json:"knowledge_base_id"`
		Provider        string   `json:"provider"`
		Name            string   `json:"name"`
		Root            string   `json:"root"`
		Include         []string `json:"include"`
		Exclude         []string `json:"exclude"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.KnowledgeBaseID == "" {
		http.Error(w, "knowledge_base_id must not be empty", http.StatusBadRequest)
		return
	}
	if req.Provider != usecase.FilesystemProvider {
		http.Error(w, `provider must be "filesystem"`, http.StatusBadRequest)
		return
	}
	conn, err := h.bulk.CreateFilesystemConnection(r.Context(), req.KnowledgeBaseID, req.Name,
		usecase.FilesystemSourceConfig{Root: req.Root, Include: req.Include, Exclude: req.Exclude})
	if err != nil {
		writeSourceError(w, "creating connection", err)
		return
	}
	var cfg usecase.FilesystemSourceConfig
	_ = json.Unmarshal(conn.Config, &cfg)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": conn.ID, "knowledge_base_id": conn.KnowledgeBaseID, "provider": conn.Provider, "name": conn.Name,
		"root": cfg.Root, "include": cfg.Include, "exclude": cfg.Exclude, "enabled": conn.Enabled,
	})
}

// ListConnections handles GET /api/v1/source-connections?knowledge_base_id=.
func (h *SourceHandler) ListConnections(w http.ResponseWriter, r *http.Request) {
	kbID := r.URL.Query().Get("knowledge_base_id")
	if kbID == "" {
		http.Error(w, "knowledge_base_id is required", http.StatusBadRequest)
		return
	}
	conns, err := h.bulk.ListConnections(r.Context(), kbID)
	if err != nil {
		writeSourceError(w, "listing connections", err)
		return
	}
	writeJSON(w, http.StatusOK, conns)
}

// StartJob handles POST /api/v1/source-connections/{id}/jobs.
func (h *SourceHandler) StartJob(w http.ResponseWriter, r *http.Request) {
	job, err := h.bulk.StartJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeSourceError(w, "starting job", err)
		return
	}
	h.scheduler.Enqueue(job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

// ListJobs handles GET /api/v1/source-connections/{id}/jobs.
func (h *SourceHandler) ListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.bulk.ListJobs(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeSourceError(w, "listing jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

// GetJob handles GET /api/v1/ingestion-jobs/{id}.
func (h *SourceHandler) GetJob(w http.ResponseWriter, r *http.Request) {
	p, err := h.bulk.Progress(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeSourceError(w, "loading job", err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// ControlJob handles POST /api/v1/ingestion-jobs/{id}/{action} for
// pause, cancel, and resume.
func (h *SourceHandler) ControlJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var err error
	switch chi.URLParam(r, "action") {
	case "pause":
		err = h.bulk.Pause(r.Context(), id)
	case "cancel":
		err = h.bulk.Cancel(r.Context(), id)
	case "resume":
		if err = h.bulk.Resume(r.Context(), id); err == nil {
			h.scheduler.Enqueue(id)
		}
	default:
		http.Error(w, "action must be pause, cancel, or resume", http.StatusBadRequest)
		return
	}
	if err != nil {
		writeSourceError(w, "controlling job", err)
		return
	}
	p, err := h.bulk.Progress(r.Context(), id)
	if err != nil {
		writeSourceError(w, "loading job", err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
