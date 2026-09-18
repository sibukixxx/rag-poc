package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/deployment"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

type runtimeTestEmbedder struct{}

func (runtimeTestEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return [][]float32{{1}}, nil
}
func (runtimeTestEmbedder) Dimensions() int { return 1 }
func (runtimeTestEmbedder) Model() string   { return "test-embed" }

type runtimeTestSearcher struct {
	results []retrieval.Result
}

func (s runtimeTestSearcher) Search(context.Context, string, []float32, int) ([]retrieval.Result, error) {
	return s.results, nil
}

type runtimeTestKeywordSearcher struct {
	results []retrieval.Result
}

func (s runtimeTestKeywordSearcher) Search(context.Context, string, string, int) ([]retrieval.Result, error) {
	return s.results, nil
}

type runtimeTestLLM struct {
	lastSystemPrompt string
}

func (m *runtimeTestLLM) Generate(context.Context, llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return &llm.GenerateResponse{Content: "answer [1]", Model: "test-model"}, nil
}

func (m *runtimeTestLLM) Stream(_ context.Context, req llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	for _, msg := range req.Messages {
		if msg.Role == llm.RoleSystem {
			m.lastSystemPrompt = msg.Content
		}
	}
	ch := make(chan llm.StreamEvent, 2)
	ch <- llm.StreamEvent{Delta: "answer [1]"}
	ch <- llm.StreamEvent{Done: true, Usage: llm.Usage{InputTokens: 10, OutputTokens: 3}}
	close(ch)
	return ch, nil
}

func runtimeTokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func setupRuntimeHTTPTest(t *testing.T) (*DeploymentHandler, *sqlite.DeploymentStore, string, string) {
	t.Helper()

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	kb, err := sqlite.NewKnowledgeStore(db).EnsureKnowledgeBase(ctx, "Dogfood", "dogfood")
	if err != nil {
		t.Fatal(err)
	}

	store := sqlite.NewDeploymentStore(db)
	dep := deployment.Deployment{
		ID: "dep-dogfood", Slug: "techvit-dogfood", KnowledgeBaseID: kb.ID,
		PromptName: usecase.RAGPromptName, PromptVersion: 1, PromptContent: "FROZEN_PROMPT",
		Alias: "normal", TopK: 5, CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateDeployment(ctx, dep); err != nil {
		t.Fatal(err)
	}

	rawToken := "fai_runtime_http_test_secret"
	tok := deployment.Token{
		ID: "tok-dogfood", DeploymentID: dep.ID, Name: "test",
		Prefix: "fai_runtime_", Hash: runtimeTokenHash(rawToken), CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateToken(ctx, tok); err != nil {
		t.Fatal(err)
	}

	result := retrieval.Result{
		ChunkID: "chunk-1", DocumentID: "doc-1", Filename: "techvit.md",
		Text: "ForgeAI dogfood evidence.", Score: 1,
	}
	search := usecase.NewSearchUseCase(
		runtimeTestSearcher{results: []retrieval.Result{result}},
		runtimeTestKeywordSearcher{results: []retrieval.Result{result}},
		runtimeTestEmbedder{},
		nil,
		nil,
	)
	provider := &runtimeTestLLM{}
	router := llm.NewRouter()
	router.RegisterProvider("test", provider)
	router.RegisterAlias("normal", llm.Alias{Provider: "test", Model: "test-model"})
	tokz, err := tokenizer.New()
	if err != nil {
		t.Fatal(err)
	}
	rag := usecase.NewRAGChatUseCase(search, router, llm.PriceTable{}, nil, tokz, nil)
	runtimeUC := usecase.NewRuntimeUseCase(store, search, rag)
	h := NewDeploymentHandler(nil, runtimeUC)

	return h, store, rawToken, provider.lastSystemPrompt
}

func TestRuntimeHTTPAuthenticationSearchAndRevocation(t *testing.T) {
	h, store, rawToken, _ := setupRuntimeHTTPTest(t)
	r := chi.NewRouter()
	r.Post("/runtime/v1/apps/{slug}/search", h.RuntimeSearch)

	call := func(slug, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/runtime/v1/apps/"+slug+"/search", strings.NewReader(`{"query":"what is ForgeAI?"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr
	}

	if got := call("techvit-dogfood", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", got.Code)
	}
	if got := call("other-app", rawToken); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong deployment status = %d, want 401", got.Code)
	}
	got := call("techvit-dogfood", rawToken)
	if got.Code != http.StatusOK {
		t.Fatalf("valid search status = %d body=%s", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Body.String(), "techvit.md") || !strings.Contains(got.Body.String(), "techvit-dogfood") {
		t.Fatalf("runtime search body missing result/deployment: %s", got.Body.String())
	}

	if err := store.RevokeToken(context.Background(), "tok-dogfood", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := call("techvit-dogfood", rawToken); got.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, want 401", got.Code)
	}
}

func TestRuntimeHTTPChatUsesFrozenPromptAndStreamsCitation(t *testing.T) {
	// Build explicitly here so the provider instance can be inspected after Stream.
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	kb, err := sqlite.NewKnowledgeStore(db).EnsureKnowledgeBase(ctx, "Dogfood", "dogfood")
	if err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewDeploymentStore(db)
	dep := deployment.Deployment{
		ID: "dep-chat", Slug: "techvit-chat", KnowledgeBaseID: kb.ID,
		PromptName: usecase.RAGPromptName, PromptVersion: 7, PromptContent: "FROZEN_RUNTIME_PROMPT",
		Alias: "normal", TopK: 5, CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateDeployment(ctx, dep); err != nil {
		t.Fatal(err)
	}
	rawToken := "fai_chat_http_test_secret"
	if err := store.CreateToken(ctx, deployment.Token{
		ID: "tok-chat", DeploymentID: dep.ID, Name: "test", Prefix: "fai_chat_",
		Hash: runtimeTokenHash(rawToken), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	result := retrieval.Result{ChunkID: "c1", DocumentID: "d1", Filename: "policy.md", Text: "Runtime answer evidence."}
	search := usecase.NewSearchUseCase(
		runtimeTestSearcher{results: []retrieval.Result{result}},
		runtimeTestKeywordSearcher{results: []retrieval.Result{result}},
		runtimeTestEmbedder{}, nil, nil,
	)
	provider := &runtimeTestLLM{}
	router := llm.NewRouter()
	router.RegisterProvider("test", provider)
	router.RegisterAlias("normal", llm.Alias{Provider: "test", Model: "test-model"})
	tokz, err := tokenizer.New()
	if err != nil {
		t.Fatal(err)
	}
	rag := usecase.NewRAGChatUseCase(search, router, llm.PriceTable{}, nil, tokz, nil)
	h := NewDeploymentHandler(nil, usecase.NewRuntimeUseCase(store, search, rag))

	r := chi.NewRouter()
	r.Post("/runtime/v1/apps/{slug}/chat", h.RuntimeChat)
	req := httptest.NewRequest(http.MethodPost, "/runtime/v1/apps/techvit-chat/chat", strings.NewReader(`{"query":"explain the runtime"}`))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("chat status = %d body=%s", rr.Code, rr.Body.String())
	}
	if provider.lastSystemPrompt != "FROZEN_RUNTIME_PROMPT" {
		t.Fatalf("system prompt = %q, want frozen deployment prompt", provider.lastSystemPrompt)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "answer [1]") || !strings.Contains(body, "policy.md") || !strings.Contains(body, `"done":true`) {
		t.Fatalf("unexpected SSE body: %s", body)
	}
}
