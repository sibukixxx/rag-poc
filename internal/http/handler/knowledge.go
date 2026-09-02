package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

type KnowledgeHandler struct {
	store  knowledge.Store
	ingest *usecase.IngestUseCase
}

func NewKnowledgeHandler(store knowledge.Store, ingest *usecase.IngestUseCase) *KnowledgeHandler {
	return &KnowledgeHandler{store: store, ingest: ingest}
}

type knowledgeBaseDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type documentDTO struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	MimeType   string `json:"mime_type"`
	SizeBytes  int64  `json:"size_bytes"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	ChunkCount int    `json:"chunk_count"`
}

func toKnowledgeBaseDTO(kb knowledge.KnowledgeBase) knowledgeBaseDTO {
	return knowledgeBaseDTO{ID: kb.ID, Name: kb.Name, Slug: kb.Slug}
}

func toDocumentDTO(d knowledge.Document) documentDTO {
	return documentDTO{
		ID: d.ID, Filename: d.Filename, MimeType: d.MimeType, SizeBytes: d.SizeBytes,
		Status: string(d.Status), Error: d.Error, ChunkCount: d.ChunkCount,
	}
}

var slugSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	slug := slugSanitizer.ReplaceAllString(strings.ToLower(name), "-")
	return strings.Trim(slug, "-")
}

// CreateKnowledgeBase handles POST /api/v1/knowledge-bases. Slug defaults
// to a slugified name; EnsureKnowledgeBase makes this idempotent — POSTing
// the same name twice returns the existing KB rather than erroring.
func (h *KnowledgeHandler) CreateKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name must not be empty", http.StatusBadRequest)
		return
	}
	slug := req.Slug
	if slug == "" {
		slug = slugify(req.Name)
	}
	if slug == "" {
		http.Error(w, "could not derive a slug from name", http.StatusBadRequest)
		return
	}

	kb, err := h.store.EnsureKnowledgeBase(r.Context(), req.Name, slug)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toKnowledgeBaseDTO(*kb))
}

func (h *KnowledgeHandler) ListKnowledgeBases(w http.ResponseWriter, r *http.Request) {
	kbs, err := h.store.ListKnowledgeBases(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]knowledgeBaseDTO, len(kbs))
	for i, kb := range kbs {
		out[i] = toKnowledgeBaseDTO(kb)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// UploadDocument handles POST /api/v1/knowledge-bases/{id}/documents
// (multipart form, field name "file"). Ingestion runs synchronously —
// acceptable for v0.1's file sizes; the response reflects the final
// status (ready/failed) directly rather than requiring polling.
func (h *KnowledgeHandler) UploadDocument(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "id")
	if _, err := h.store.GetKnowledgeBase(r.Context(), kbID); err != nil {
		http.Error(w, "knowledge base not found", http.StatusNotFound)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "expected a multipart 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "reading upload: "+err.Error(), http.StatusInternalServerError)
		return
	}

	mimeType := header.Header.Get("Content-Type")
	doc, err := h.ingest.IngestFile(r.Context(), kbID, header.Filename, mimeType, data)
	if err != nil && doc == nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toDocumentDTO(*doc))
}

func (h *KnowledgeHandler) ListDocuments(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "id")
	docs, err := h.store.ListDocuments(r.Context(), kbID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]documentDTO, len(docs))
	for i, d := range docs {
		out[i] = toDocumentDTO(d)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
