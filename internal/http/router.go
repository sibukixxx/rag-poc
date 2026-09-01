// Package http wires ForgeAI's HTTP surface: the management API
// (/api/v1, session-authenticated, added incrementally per
// docs/ROADMAP.md) and, from W10 onward, the runtime API (/runtime/v1).
package http

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/sibukixxx/rag-poc/internal/http/handler"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// Deps carries the dependencies handlers need. It grows as usecases are
// added (W2+); keeping it as a struct avoids reshuffling NewRouter's
// signature every week.
type Deps struct {
	DB      *sql.DB
	Version string
	Chat    *usecase.ChatUseCase
}

func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Logger)

	health := handler.NewHealthHandler(deps.DB, deps.Version)
	chat := handler.NewChatHandler(deps.Chat)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", health.Check)
		r.Post("/chat", chat.Stream)
	})

	r.Handle("/*", staticHandler())

	return r
}
