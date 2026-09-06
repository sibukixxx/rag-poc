package app

import (
	"github.com/sibukixxx/rag-poc/internal/adapter/llmrerank"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/adapter/vecmem"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// Evaluation builds the same retrieval pipeline used by the server so CLI
// evaluation measures production behavior rather than a parallel code path.
func (a *App) Evaluation() *usecase.EvaluateUseCase {
	secrets, _ := a.Secrets()
	router := BuildRouter(a.Config.LLM, secrets)
	traces := sqlite.NewTraceStore(a.DB)
	search := usecase.NewSearchUseCase(
		vecmem.New(a.DB),
		sqlite.NewFTSStore(a.DB),
		BuildEmbedder(a.Config.Embedding, secrets),
		llmrerank.New(router, "cheap"),
		traces,
	)
	return &usecase.EvaluateUseCase{Searcher: search, Version: Version, EmbeddingModel: a.Config.Embedding.Model}
}
