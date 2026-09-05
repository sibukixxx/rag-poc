package app

import (
	"fmt"

	"github.com/sibukixxx/rag-poc/internal/adapter/extractor"
	"github.com/sibukixxx/rag-poc/internal/adapter/llmrerank"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/adapter/vecmem"
	"github.com/sibukixxx/rag-poc/internal/domain/eval"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// Knowledge returns a Store backed by this App's database, for CLI
// commands (`forgeai ingest`) that don't need the full HTTP server.
func (a *App) Knowledge() knowledge.Store {
	return sqlite.NewKnowledgeStore(a.DB)
}

// Ingest wires an IngestUseCase against this App's database — the same
// pipeline `POST /api/v1/knowledge-bases/:id/documents` uses — for
// `forgeai ingest`.
func (a *App) Ingest() (*usecase.IngestUseCase, error) {
	secrets, _ := a.Secrets()
	tok, err := tokenizer.New()
	if err != nil {
		return nil, fmt.Errorf("loading tokenizer: %w", err)
	}
	embedder := BuildEmbedder(a.Config.Embedding, secrets)
	prices := BuildPriceTable(a.Config.LLM)
	traces := sqlite.NewTraceStore(a.DB)
	return usecase.NewIngestUseCase(a.Knowledge(), extractor.NewDefaultRegistry(), tok, embedder, prices, traces), nil
}

// Evaluation wires an eval.Store and EvaluationUseCase for `forgeai eval
// import|run`. It builds the same SearchUseCase (Hybrid Search + rerank)
// a real query goes through, so a run's metrics reflect production
// retrieval behavior exactly.
func (a *App) Evaluation() (eval.Store, *usecase.EvaluationUseCase) {
	secrets, _ := a.Secrets()
	router := BuildRouter(a.Config.LLM, secrets)
	traces := sqlite.NewTraceStore(a.DB)
	embedder := BuildEmbedder(a.Config.Embedding, secrets)

	vectorSearcher := vecmem.New(a.DB)
	keywordSearcher := sqlite.NewFTSStore(a.DB)
	reranker := llmrerank.New(router, "cheap")
	search := usecase.NewSearchUseCase(vectorSearcher, keywordSearcher, embedder, reranker, traces)

	datasets := sqlite.NewEvalStore(a.DB)
	return datasets, usecase.NewEvaluationUseCase(search, datasets, traces)
}
