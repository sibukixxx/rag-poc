package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/domain/prompt"
)

type PromptHandler struct {
	store prompt.Store
}

func NewPromptHandler(store prompt.Store) *PromptHandler {
	return &PromptHandler{store: store}
}

type promptDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ActiveVersion int    `json:"active_version"`
}

type promptVersionDTO struct {
	Version   int    `json:"version"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

func toPromptDTO(p prompt.Prompt) promptDTO {
	return promptDTO{ID: p.ID, Name: p.Name, ActiveVersion: p.ActiveVersion}
}

func toPromptVersionDTO(v prompt.Version) promptVersionDTO {
	return promptVersionDTO{Version: v.Version, Content: v.Content, CreatedAt: v.CreatedAt.Format(time.RFC3339Nano)}
}

// Create handles POST /api/v1/prompts ({name}). EnsurePrompt makes this
// idempotent — POSTing the same name twice returns the existing prompt.
func (h *PromptHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name must not be empty", http.StatusBadRequest)
		return
	}

	p, err := h.store.EnsurePrompt(r.Context(), req.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toPromptDTO(*p))
}

func (h *PromptHandler) List(w http.ResponseWriter, r *http.Request) {
	prompts, err := h.store.ListPrompts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]promptDTO, len(prompts))
	for i, p := range prompts {
		out[i] = toPromptDTO(p)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *PromptHandler) ListVersions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	versions, err := h.store.ListVersions(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]promptVersionDTO, len(versions))
	for i, v := range versions {
		out[i] = toPromptVersionDTO(v)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// CreateVersion handles POST /api/v1/prompts/{id}/versions ({content}).
// The first version created for a prompt becomes active automatically
// (see PromptStore.CreateVersion); later ones need an explicit Activate.
func (h *PromptHandler) CreateVersion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Content == "" {
		http.Error(w, "content must not be empty", http.StatusBadRequest)
		return
	}

	v, err := h.store.CreateVersion(r.Context(), id, req.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toPromptVersionDTO(*v))
}

// Activate handles POST /api/v1/prompts/{id}/activate ({version}) — this
// is what "switching to v2" means at the API level (docs/ROADMAP.md W6).
func (h *PromptHandler) Activate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if err := h.store.SetActiveVersion(r.Context(), id, req.Version); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p, err := h.store.GetPrompt(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toPromptDTO(*p))
}
