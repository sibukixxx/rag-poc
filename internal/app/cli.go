package app

import (
	"context"
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
func (a *App) Evaluation() (eval.Store, *usecase.EvaluationUseCase, error) {
	secrets, _ := a.Secrets()
	router := BuildRouter(a.Config.LLM, secrets)
	prices := BuildPriceTable(a.Config.LLM)
	traces := sqlite.NewTraceStore(a.DB)
	embedder := BuildEmbedder(a.Config.Embedding, secrets)

	vectorSearcher := vecmem.New(a.DB)
	keywordSearcher := sqlite.NewFTSStore(a.DB)
	reranker := llmrerank.New(router, "cheap")
	search := usecase.NewSearchUseCase(vectorSearcher, keywordSearcher, embedder, reranker, traces)

	// Judge runs generate answers through the same RAG pipeline (and the
	// same registry prompts) the server uses, so CLI and UI runs are
	// comparable.
	tok, err := tokenizer.New()
	if err != nil {
		return nil, nil, fmt.Errorf("loading tokenizer: %w", err)
	}
	promptStore := sqlite.NewPromptStore(a.DB)
	if err := seedDefaultPrompts(context.Background(), promptStore); err != nil {
		return nil, nil, fmt.Errorf("seeding default prompts: %w", err)
	}
	ragChat := usecase.NewRAGChatUseCase(search, router, prices, traces, tok, promptStore)
	judge := usecase.NewLLMJudge(router, prices, traces, promptStore)

	datasets := sqlite.NewEvalStore(a.DB)
	return datasets, usecase.NewEvaluationUseCase(search, ragChat, judge, datasets, traces), nil
}
