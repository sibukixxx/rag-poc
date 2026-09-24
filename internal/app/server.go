package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/extractor"
	"github.com/sibukixxx/rag-poc/internal/adapter/llmrerank"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/adapter/vecmem"
	"github.com/sibukixxx/rag-poc/internal/config"
	forgehttp "github.com/sibukixxx/rag-poc/internal/http"
	forgehandler "github.com/sibukixxx/rag-poc/internal/http/handler"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// Handler wires every use case against the App's database and returns the
// complete HTTP surface (management API, runtime API, embedded UI). Serve
// uses it for the real listener; the acceptance E2E drives it in-process.
func (a *App) Handler() (http.Handler, error) {
	// Secrets are optional at boot: a fresh install with no master key set
	// still serves fine as long as providers resolve their key via
	// api_key_env (the default). BuildRouter/BuildEmbedder tolerate a nil
	// store.
	router, embedder := a.Providers()
	prices := BuildPriceTable(a.Config.LLM)
	traces := sqlite.NewTraceStore(a.DB)
	chat := usecase.NewChatUseCase(router, prices, traces)

	tok, err := tokenizer.New()
	if err != nil {
		return nil, fmt.Errorf("loading tokenizer: %w", err)
	}
	knowledgeStore := sqlite.NewKnowledgeStore(a.DB)
	ingest := usecase.NewIngestUseCase(knowledgeStore, extractor.NewDefaultRegistry(), tok, embedder, prices, traces)

	// Hybrid Search: vecmem (embedded brute-force cosine) + FTS5 trigram,
	// merged by RRF, with LLM listwise rerank (alias "cheap") available
	// on request (docs/V0.1_SPEC.md §7).
	vectorSearcher := vecmem.New(a.DB)
	keywordSearcher := sqlite.NewFTSStore(a.DB)
	reranker := llmrerank.New(router, "cheap")
	search := usecase.NewSearchUseCase(vectorSearcher, keywordSearcher, embedder, reranker, traces)

	promptStore := sqlite.NewPromptStore(a.DB)
	if err := seedDefaultPrompts(context.Background(), promptStore); err != nil {
		return nil, fmt.Errorf("seeding default prompts: %w", err)
	}
	ragChat := usecase.NewRAGChatUseCase(search, router, prices, traces, tok, promptStore)

	// Golden Dataset + Retrieval evaluation (docs/ROADMAP.md W7). Reuses
	// the same SearchUseCase a real query would go through, so a run's
	// metrics reflect production retrieval behavior exactly.
	datasets := sqlite.NewEvalStore(a.DB)
	judge := usecase.NewLLMJudge(router, prices, traces, promptStore)
	evalUC := usecase.NewEvaluationUseCase(search, ragChat, judge, datasets, traces)
	compareUC := usecase.NewCompareUseCase(datasets)

	// W10 freezes the evaluated KB/prompt/model/retrieval configuration into
	// an immutable Deployment, then exposes that snapshot through a separate
	// Bearer-authenticated runtime surface.
	deploymentStore := sqlite.NewDeploymentStore(a.DB)
	deploymentUC := usecase.NewDeploymentUseCase(deploymentStore, knowledgeStore, promptStore, router)
	runtimeUC := usecase.NewRuntimeUseCase(deploymentStore, search, ragChat)
	lifecycleUC := a.DataLifecycle()
	auditStore := a.Audit()
	deploymentUC.Audit = auditStore
	runtimeUC.Audit = auditStore

	demoAuthEnabled := envBool("FORGEAI_DEMO_AUTH_ENABLED")
	requireCloudflare := envBool("FORGEAI_REQUIRE_CLOUDFLARE_ACCESS")
	demoAuthUC := usecase.NewDemoAccessUseCase(sqlite.NewDemoAccessStore(a.DB), usecase.DefaultDemoSessionDuration)
	demoAuthUC.Audit = auditStore
	demoAuthHandler := forgehandler.NewDemoAuthHandler(demoAuthUC, demoAuthEnabled, requireCloudflare)

	handler := forgehttp.NewRouter(forgehttp.Deps{
		DB:          a.DB,
		Version:     Version,
		Chat:        chat,
		Knowledge:   knowledgeStore,
		Ingest:      ingest,
		Search:      search,
		RAGChat:     ragChat,
		Prompts:     promptStore,
		Traces:      traces,
		Datasets:    datasets,
		Eval:        evalUC,
		Compare:     compareUC,
		Deployments: deploymentUC,
		Runtime:     runtimeUC,
		Lifecycle:   lifecycleUC,
		Audit:       auditStore,
		DemoAuth:    demoAuthHandler,
	})

	return handler, nil
}

// Serve starts the HTTP server and blocks until it receives SIGINT/SIGTERM,
// then shuts down gracefully.
func (a *App) Serve() error {
	handler, err := a.Handler()
	if err != nil {
		return err
	}
	recordStartup(context.Background(), a.Audit(), a.Config, Version)
	if a.Config.Profile == config.ProfileProduction {
		for _, c := range productionProfileChecks(a.Config, envBool("FORGEAI_DEMO_AUTH_ENABLED")) {
			if !c.OK {
				log.Printf("WARNING production profile: %s: %s", c.Name, c.Info)
			}
		}
	}

	addr := fmt.Sprintf(":%d", a.Config.Server.Port)
	// No WriteTimeout: SSE chat responses set their own write deadline in
	// the handler. ReadTimeout bounds slow-loris request bodies (uploads are
	// size-capped by the router), IdleTimeout reaps idle keep-alives.
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("forgeai listening on http://localhost:%d", a.Config.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-sigCh:
		log.Println("shutting down...")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// DataLifecycle builds the customer-data deletion/retention use case from
// the configured retention policy. The HTTP API and `forgeai data` share it.
func (a *App) DataLifecycle() *usecase.DataLifecycleUseCase {
	uc := usecase.NewDataLifecycleUseCase(sqlite.NewLifecycleStore(a.DB), usecase.RetentionPolicy{
		TraceDays:         a.Config.Retention.TraceDays,
		EvaluationRunDays: a.Config.Retention.EvaluationRunDays,
		AuditDays:         a.Config.Retention.AuditDays,
	})
	auditStore := a.Audit()
	uc.Audit = auditStore
	uc.AuditRetention = auditStore
	return uc
}
