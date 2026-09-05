// Package http wires ForgeAI's HTTP surface: the management API
// (/api/v1, session-authenticated, added incrementally per
// docs/ROADMAP.md) and, from W10 onward, the runtime API (/runtime/v1).
package http

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/prompt"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
	"github.com/sibukixxx/rag-poc/internal/http/handler"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// Deps carries the dependencies handlers need. It grows as usecases are
// added (W2+); keeping it as a struct avoids reshuffling NewRouter's
// signature every week.
type Deps struct {
	DB        *sql.DB
	Version   string
	Chat      *usecase.ChatUseCase
	Knowledge knowledge.Store
	Ingest    *usecase.IngestUseCase
	Search    *usecase.SearchUseCase
	RAGChat   *usecase.RAGChatUseCase
	Prompts   prompt.Store
	Traces    trace.Store
}

func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Logger)

	health := handler.NewHealthHandler(deps.DB, deps.Version)
	chat := handler.NewChatHandler(deps.Chat)
	kb := handler.NewKnowledgeHandler(deps.Knowledge, deps.Ingest, deps.Search, deps.RAGChat)
	prompts := handler.NewPromptHandler(deps.Prompts)
	traces := handler.NewTraceHandler(deps.Traces)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", health.Check)
		r.Post("/chat", chat.Stream)
		r.Post("/knowledge-bases", kb.CreateKnowledgeBase)
		r.Get("/knowledge-bases", kb.ListKnowledgeBases)
		r.Post("/knowledge-bases/{id}/documents", kb.UploadDocument)
		r.Get("/knowledge-bases/{id}/documents", kb.ListDocuments)
		r.Post("/knowledge-bases/{id}/search", kb.Search)
		r.Post("/knowledge-bases/{id}/chat", kb.Chat)
		r.Post("/prompts", prompts.Create)
		r.Get("/prompts", prompts.List)
		r.Get("/prompts/{id}/versions", prompts.ListVersions)
		r.Post("/prompts/{id}/versions", prompts.CreateVersion)
		r.Post("/prompts/{id}/activate", prompts.Activate)
		r.Get("/traces", traces.List)
		r.Get("/traces/{id}", traces.Get)
	})

	r.Handle("/*", staticHandler())

	return r
}
