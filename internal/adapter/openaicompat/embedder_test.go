package openaicompat_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/egress"
	"github.com/sibukixxx/rag-poc/internal/adapter/openaicompat"
)

func TestEmbedLocalOnlyBlocksExternalProvider(t *testing.T) {
	embedder := openaicompat.NewEmbedderWithEgress("https://api.openai.com/v1", "secret", "test-model", 2, egress.Policy{Mode: egress.ModeLocalOnly})
	_, err := embedder.Embed(context.Background(), []string{"must not leave process"})
	if !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("Embed error = %v, want ErrBlocked", err)
	}
}

func TestEmbedReturnsVectorsInInputOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Deliberately out of order to verify the client sorts by index.
		fmt.Fprint(w, `{"data":[
			{"index":1,"embedding":[0.4,0.5]},
			{"index":0,"embedding":[0.1,0.2]}
		]}`)
	}))
	defer srv.Close()

	embedder := openaicompat.NewEmbedder(srv.URL, "test-key", "text-embedding-3-small", 2)
	vectors, err := embedder.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vectors) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vectors))
	}
	if vectors[0][0] != 0.1 || vectors[1][0] != 0.4 {
		t.Errorf("got vectors out of order: %+v", vectors)
	}
	if embedder.Dimensions() != 2 || embedder.Model() != "text-embedding-3-small" {
		t.Errorf("got Dimensions=%d Model=%q", embedder.Dimensions(), embedder.Model())
	}
}

func TestEmbedEmptyInputReturnsNoRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	embedder := openaicompat.NewEmbedder(srv.URL, "test-key", "text-embedding-3-small", 2)
	vectors, err := embedder.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if vectors != nil {
		t.Errorf("expected nil vectors for empty input, got %+v", vectors)
	}
	if called {
		t.Error("expected no HTTP call for empty input")
	}
}

func TestEmbedMismatchedCountIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[0.1]}]}`)
	}))
	defer srv.Close()

	embedder := openaicompat.NewEmbedder(srv.URL, "test-key", "text-embedding-3-small", 1)
	_, err := embedder.Embed(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("expected error on count mismatch")
	}
}
