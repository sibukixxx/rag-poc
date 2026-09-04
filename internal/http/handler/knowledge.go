package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

type KnowledgeHandler struct {
	store   knowledge.Store
	ingest  *usecase.IngestUseCase
	search  *usecase.SearchUseCase
	ragChat *usecase.RAGChatUseCase
}

func NewKnowledgeHandler(store knowledge.Store, ingest *usecase.IngestUseCase, search *usecase.SearchUseCase, ragChat *usecase.RAGChatUseCase) *KnowledgeHandler {
	return &KnowledgeHandler{store: store, ingest: ingest, search: search, ragChat: ragChat}
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

type searchResultDTO struct {
	ChunkID    string  `json:"chunk_id"`
	DocumentID string  `json:"document_id"`
	Filename   string  `json:"filename"`
	Text       string  `json:"text"`
	Score      float64 `json:"score"`
	Page       *int    `json:"page,omitempty"`
	Heading    string  `json:"heading,omitempty"`
}

// Search handles POST /api/v1/knowledge-bases/{id}/search
// ({query, top_k, rerank} -> Hybrid Search results, docs/V0.1_SPEC.md §7).
func (h *KnowledgeHandler) Search(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "id")

	var req struct {
		Query  string `json:"query"`
		TopK   int    `json:"top_k"`
		Rerank bool   `json:"rerank"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	results, err := h.search.Search(r.Context(), kbID, req.Query, retrieval.Options{TopK: req.TopK, Rerank: req.Rerank})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	out := make([]searchResultDTO, len(results))
	for i, res := range results {
		out[i] = searchResultDTO{
			ChunkID: res.ChunkID, DocumentID: res.DocumentID, Filename: res.Filename,
			Text: res.Text, Score: res.Score, Page: res.Page, Heading: res.Heading,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"results": out})
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

type citationDTO struct {
	Index      int    `json:"index"`
	ChunkID    string `json:"chunk_id"`
	DocumentID string `json:"document_id"`
	Filename   string `json:"filename"`
	Page       *int   `json:"page,omitempty"`
	Heading    string `json:"heading,omitempty"`
	Text       string `json:"text"`
}

type ragChatStreamEventDTO struct {
	Delta     string        `json:"delta,omitempty"`
	Done      bool          `json:"done,omitempty"`
	TraceID   string        `json:"trace_id,omitempty"`
	Usage     *usageDTO     `json:"usage,omitempty"`
	CostUSD   float64       `json:"cost_usd,omitempty"`
	Citations []citationDTO `json:"citations,omitempty"`
	NoContext bool          `json:"no_context,omitempty"`
	Error     string        `json:"error,omitempty"`
}

// Chat handles POST /api/v1/knowledge-bases/{id}/chat (SSE): retrieves
// context from the knowledge base and streams an answer that cites it
// (docs/ROADMAP.md W5). Unlike /api/v1/chat, this takes a single query,
// not a message history — each question is answered independently by
// retrieving fresh context for it.
func (h *KnowledgeHandler) Chat(w http.ResponseWriter, r *http.Request) {
	kbID := chi.URLParam(r, "id")

	var req struct {
		Alias  string `json:"alias"`
		Query  string `json:"query"`
		Rerank bool   `json:"rerank"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Alias == "" {
		req.Alias = "normal"
	}
	if strings.TrimSpace(req.Query) == "" {
		http.Error(w, "query must not be empty", http.StatusBadRequest)
		return
	}

	result, err := h.ragChat.ChatStream(r.Context(), kbID, req.Alias, req.Query, req.Rerank)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	write := func(ev ragChatStreamEventDTO) {
		payload, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}

	for ev := range result.Events {
		switch {
		case ev.Err != nil:
			write(ragChatStreamEventDTO{Error: ev.Err.Error(), TraceID: result.TraceID})
		case ev.Done:
			citations := make([]citationDTO, len(ev.Citations))
			for i, c := range ev.Citations {
				citations[i] = citationDTO{
					Index: c.Index, ChunkID: c.ChunkID, DocumentID: c.DocumentID,
					Filename: c.Filename, Page: c.Page, Heading: c.Heading, Text: c.Text,
				}
			}
			write(ragChatStreamEventDTO{
				Done: true, TraceID: result.TraceID,
				Usage:     &usageDTO{InputTokens: ev.Usage.InputTokens, OutputTokens: ev.Usage.OutputTokens},
				CostUSD:   ev.CostUSD,
				Citations: citations,
				NoContext: ev.NoContext,
			})
		default:
			write(ragChatStreamEventDTO{Delta: ev.Delta})
		}
	}
}
