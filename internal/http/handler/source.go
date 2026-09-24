package handler

import (
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/domain/bulk"
	"github.com/sibukixxx/rag-poc/internal/domain/oauth"
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
	bulk            *usecase.BulkIngestUseCase
	control         *usecase.SourceControlUseCase
	scheduler       JobScheduler
	oauthConnectors []string
}

// NewSourceHandler wires the source API. oauthConnectors lists the API
// connectors this server can authorize (shown by the catalog).
func NewSourceHandler(uc *usecase.BulkIngestUseCase, control *usecase.SourceControlUseCase, scheduler JobScheduler, oauthConnectors []string) *SourceHandler {
	return &SourceHandler{bulk: uc, control: control, scheduler: scheduler, oauthConnectors: oauthConnectors}
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
	case errors.Is(err, oauth.ErrInvalidState):
		http.Error(w, "authorization request is invalid or expired; start the authorization again", http.StatusBadRequest)
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
		KnowledgeBaseID string          `json:"knowledge_base_id"`
		Provider        string          `json:"provider"`
		Name            string          `json:"name"`
		Root            string          `json:"root"`
		Include         []string        `json:"include"`
		Exclude         []string        `json:"exclude"`
		OAuthProvider   string          `json:"oauth_provider"`
		Scope           json.RawMessage `json:"scope"`
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
		if req.OAuthProvider == "" {
			http.Error(w, `provider must be "filesystem", or an OAuth connector with oauth_provider`, http.StatusBadRequest)
			return
		}
		conn, err := h.control.CreateOAuthConnection(r.Context(), req.KnowledgeBaseID, req.Provider, req.Name, req.OAuthProvider, req.Scope)
		if err != nil {
			writeSourceError(w, "creating connection", err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id": conn.ID, "knowledge_base_id": conn.KnowledgeBaseID, "provider": conn.Provider, "name": conn.Name,
			"auth_state": conn.AuthState, "enabled": conn.Enabled,
		})
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
	conns, err := h.control.ListConnections(r.Context(), kbID)
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

// Catalog handles GET /api/v1/source-catalog.
func (h *SourceHandler) Catalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.control.Catalog(h.oauthConnectors))
}

// Authorize handles POST /api/v1/source-connections/{id}/authorize and
// returns the provider URL for the browser to open.
func (h *SourceHandler) Authorize(w http.ResponseWriter, r *http.Request) {
	authURL, err := h.control.StartAuthorization(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeSourceError(w, "starting authorization", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"authorization_url": authURL})
}

// Sync handles POST /api/v1/source-connections/{id}/sync.
func (h *SourceHandler) Sync(w http.ResponseWriter, r *http.Request) {
	ref, err := h.control.SyncNow(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeSourceError(w, "starting sync", err)
		return
	}
	writeJSON(w, http.StatusAccepted, ref)
}

// SetEnabled handles POST /api/v1/source-connections/{id}/enable|disable.
func (h *SourceHandler) SetEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h.control.SetEnabled(r.Context(), chi.URLParam(r, "id"), enabled); err != nil {
			writeSourceError(w, "updating connection", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
	}
}

// Disconnect handles DELETE /api/v1/source-connections/{id}.
func (h *SourceHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	removed, err := h.control.Disconnect(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeSourceError(w, "disconnecting", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"documents_removed": removed})
}

var callbackPage = template.Must(template.New("cb").Parse(`<!doctype html>
<html lang="ja"><head><meta charset="utf-8"><title>ForgeAI</title>
<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;color:#1f2933}
h1{font-size:1.25rem}a{color:#2563eb}</style></head>
<body><h1>{{.Title}}</h1><p>{{.Body}}</p><p><a href="/">ForgeAI に戻る</a></p></body></html>`))

// OAuthCallback handles GET /oauth/callback, the provider redirect. The
// state parameter is the CSRF protection: it must match a pending,
// unexpired authorization started by this server.
func (h *SourceHandler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := h.control.CompleteAuthorization(r.Context(), q.Get("state"), q.Get("code"), q.Get("error"))
	if err != nil {
		status := http.StatusBadRequest
		body := "認可要求が無効か期限切れです。ForgeAI の画面から認可をやり直してください。"
		if !errors.Is(err, oauth.ErrInvalidState) && q.Get("error") == "" {
			status = http.StatusBadGateway
			body = "プロバイダとのトークン交換に失敗しました。設定を確認して、もう一度認可してください。"
			log.Printf("sources: oauth callback: %v", err)
		} else if q.Get("error") != "" {
			body = "プロバイダで認可が許可されませんでした。"
		}
		w.WriteHeader(status)
		_ = callbackPage.Execute(w, map[string]string{"Title": "接続できませんでした", "Body": body})
		return
	}
	_ = callbackPage.Execute(w, map[string]string{
		"Title": "接続が完了しました",
		"Body":  "認可情報はサーバー内に暗号化して保存しました。この画面を閉じても、同期はサーバーで続きます。",
	})
}
