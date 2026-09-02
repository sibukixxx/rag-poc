package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/extractor"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	forgehttp "github.com/sibukixxx/rag-poc/internal/http"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// Serve starts the HTTP server and blocks until it receives SIGINT/SIGTERM,
// then shuts down gracefully.
func (a *App) Serve() error {
	// Secrets are optional at boot: a fresh install with no master key set
	// still serves fine as long as providers resolve their key via
	// api_key_env (the default). BuildRouter/BuildEmbedder tolerate a nil
	// store.
	secrets, _ := a.Secrets()

	router := BuildRouter(a.Config.LLM, secrets)
	prices := BuildPriceTable(a.Config.LLM)
	traces := sqlite.NewTraceStore(a.DB)
	chat := usecase.NewChatUseCase(router, prices, traces)

	tok, err := tokenizer.New()
	if err != nil {
		return fmt.Errorf("loading tokenizer: %w", err)
	}
	knowledgeStore := sqlite.NewKnowledgeStore(a.DB)
	embedder := BuildEmbedder(a.Config.Embedding, secrets)
	ingest := usecase.NewIngestUseCase(knowledgeStore, extractor.NewDefaultRegistry(), tok, embedder, prices, traces)

	handler := forgehttp.NewRouter(forgehttp.Deps{
		DB:        a.DB,
		Version:   Version,
		Chat:      chat,
		Knowledge: knowledgeStore,
		Ingest:    ingest,
	})

	addr := fmt.Sprintf(":%d", a.Config.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
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
