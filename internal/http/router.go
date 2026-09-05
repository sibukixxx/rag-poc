// Package http wires ForgeAI's HTTP surface: the management API
// (/api/v1, session-authenticated, added incrementally per
// docs/ROADMAP.md) and, from W10 onward, the runtime API (/runtime/v1).
package http

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/sibukixxx/rag-poc/internal/domain/eval"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/prompt"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
	"github.com/sibukixxx/rag-poc/internal/http/handler"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// Body size limits. Everything is buffered in memory by the handlers, so
// these are the real bound on per-request memory and on how much text can
// be pushed to the tokenizer / embeddings API in one call.
const (
	maxJSONBody   = 1 << 20  // 1 MiB: chat and KB-create bodies
	maxUploadBody = 32 << 20 // 32 MiB: one document upload
	// maxDatasetImportBody is bigger than maxJSONBody/maxUploadBody's JSON
	// case: a 50+ question golden dataset (JSON or CSV) is still small,
	// but bigger than a typical chat/search request.
	maxDatasetImportBody = 4 << 20 // 4 MiB
	// maxInflight bounds concurrent requests (each ingest/chat can be
	// CPU- and provider-cost-heavy).
	maxInflight = 8
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
	Datasets  eval.Store
	Eval      *usecase.EvaluationUseCase
}

func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(cloudflareRealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Logger)
	r.Use(securityHeaders)

	health := handler.NewHealthHandler(deps.DB, deps.Version)
	chat := handler.NewChatHandler(deps.Chat)
	kb := handler.NewKnowledgeHandler(deps.Knowledge, deps.Ingest, deps.Search, deps.RAGChat)
	prompts := handler.NewPromptHandler(deps.Prompts)
	traces := handler.NewTraceHandler(deps.Traces)
	evaluations := handler.NewEvalHandler(deps.Datasets, deps.Eval)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(denyCrossSite)
		r.Use(chimiddleware.Throttle(maxInflight))

		r.Get("/health", health.Check)
		r.With(limitBody(maxJSONBody)).Post("/chat", chat.Stream)
		r.With(limitBody(maxJSONBody)).Post("/knowledge-bases", kb.CreateKnowledgeBase)
		r.Get("/knowledge-bases", kb.ListKnowledgeBases)
		r.With(limitBody(maxUploadBody)).Post("/knowledge-bases/{id}/documents", kb.UploadDocument)
		r.Get("/knowledge-bases/{id}/documents", kb.ListDocuments)
		r.With(limitBody(maxJSONBody)).Post("/knowledge-bases/{id}/search", kb.Search)
		r.With(limitBody(maxJSONBody)).Post("/knowledge-bases/{id}/chat", kb.Chat)
		r.With(limitBody(maxJSONBody)).Post("/prompts", prompts.Create)
		r.Get("/prompts", prompts.List)
		r.Get("/prompts/{id}/versions", prompts.ListVersions)
		r.With(limitBody(maxJSONBody)).Post("/prompts/{id}/versions", prompts.CreateVersion)
		r.With(limitBody(maxJSONBody)).Post("/prompts/{id}/activate", prompts.Activate)
		r.Get("/traces", traces.List)
		r.Get("/traces/{id}", traces.Get)
		r.With(limitBody(maxJSONBody)).Post("/datasets", evaluations.CreateDataset)
		r.Get("/datasets", evaluations.ListDatasets)
		r.With(limitBody(maxDatasetImportBody)).Post("/datasets/{id}/cases", evaluations.ImportCases)
		r.Get("/datasets/{id}/cases", evaluations.ListCases)
		r.With(limitBody(maxJSONBody)).Post("/evaluations", evaluations.CreateEvaluation)
		r.Get("/evaluations", evaluations.ListEvaluations)
		r.Get("/evaluations/{id}", evaluations.GetEvaluation)
	})

	r.Handle("/*", noDirListing(staticHandler()))

	return r
}

// limitBody caps the request body; handlers see an *http.MaxBytesError
// once the limit is exceeded.
func limitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// denyCrossSite is the CSRF guard. The API has no auth of its own and is
// meant to sit behind Cloudflare Access, whose session cookie a browser
// would attach to a cross-site form POST from an attacker's page. Browsers
// send Sec-Fetch-Site on every request; anything other than same-origin
// (or a direct navigation) is rejected for state-changing methods. Bodies
// declared as text/plain are refused too, since that is the only way a
// cross-site <form> can submit a JSON-looking body without a preflight.
// Non-browser clients (curl, SDKs) send neither header and are unaffected.
func denyCrossSite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "cross-site request rejected", http.StatusForbidden)
				return
			}
			if ct := r.Header.Get("Content-Type"); strings.HasPrefix(strings.ToLower(ct), "text/plain") {
				http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders hardens the embedded SPA. The Playground loads only
// same-origin assets, so a strict CSP is safe; framing is denied so an
// Access-authenticated session cannot be clickjacked.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// cloudflareRealIP replaces chi's RealIP, which trusts X-Forwarded-For and
// X-Real-IP from anyone. Behind Cloudflare Tunnel only CF-Connecting-IP is
// set by the edge; a client cannot forge it because it is overwritten.
func cloudflareRealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
			r.RemoteAddr = ip
		}
		next.ServeHTTP(w, r)
	})
}

// noDirListing keeps http.FileServer from rendering directory indexes of
// the embedded bundle (e.g. /assets/).
func noDirListing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
