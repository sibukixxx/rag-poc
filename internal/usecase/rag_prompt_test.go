package usecase_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/prompt"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// memPromptStore is a minimal in-memory prompt.Store for testing that
// RAGChatUseCase actually reads the active version (docs/ROADMAP.md W6
// acceptance criterion: switching to v2 changes behavior).
type memPromptStore struct {
	mu       sync.Mutex
	prompts  map[string]prompt.Prompt // by id
	byName   map[string]string        // name -> id
	versions map[string][]prompt.Version
}

func newMemPromptStore() *memPromptStore {
	return &memPromptStore{
		prompts:  map[string]prompt.Prompt{},
		byName:   map[string]string{},
		versions: map[string][]prompt.Version{},
	}
}

func (m *memPromptStore) EnsurePrompt(ctx context.Context, name string) (*prompt.Prompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byName[name]; ok {
		p := m.prompts[id]
		return &p, nil
	}
	p := prompt.Prompt{ID: uuid.NewString(), Name: name}
	m.prompts[p.ID] = p
	m.byName[name] = p.ID
	return &p, nil
}

func (m *memPromptStore) GetPrompt(ctx context.Context, id string) (*prompt.Prompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.prompts[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &p, nil
}

func (m *memPromptStore) GetPromptByName(ctx context.Context, name string) (*prompt.Prompt, error) {
	m.mu.Lock()
	id, ok := m.byName[name]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return m.GetPrompt(ctx, id)
}

func (m *memPromptStore) ListPrompts(ctx context.Context) ([]prompt.Prompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []prompt.Prompt
	for _, p := range m.prompts {
		out = append(out, p)
	}
	return out, nil
}

func (m *memPromptStore) CreateVersion(ctx context.Context, promptID, content string) (*prompt.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := len(m.versions[promptID]) + 1
	v := prompt.Version{ID: uuid.NewString(), PromptID: promptID, Version: next, Content: content}
	m.versions[promptID] = append(m.versions[promptID], v)
	if next == 1 {
		p := m.prompts[promptID]
		p.ActiveVersion = 1
		m.prompts[promptID] = p
	}
	return &v, nil
}

func (m *memPromptStore) ListVersions(ctx context.Context, promptID string) ([]prompt.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.versions[promptID], nil
}

func (m *memPromptStore) SetActiveVersion(ctx context.Context, promptID string, version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.prompts[promptID]
	if !ok {
		return fmt.Errorf("not found")
	}
	p.ActiveVersion = version
	m.prompts[promptID] = p
	return nil
}

func (m *memPromptStore) GetActiveVersion(ctx context.Context, promptID string) (*prompt.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.prompts[promptID]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	for _, v := range m.versions[promptID] {
		if v.Version == p.ActiveVersion {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("no active version")
}

// captureLLM records the last system prompt it was asked to generate
// from, so tests can assert which prompt content actually reached the
// model.
type captureLLM struct {
	lastSystemPrompt string
}

func (c *captureLLM) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	panic("not used")
}

func (c *captureLLM) Stream(ctx context.Context, req llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	for _, m := range req.Messages {
		if m.Role == llm.RoleSystem {
			c.lastSystemPrompt = m.Content
		}
	}
	ch := make(chan llm.StreamEvent, 2)
	ch <- llm.StreamEvent{Delta: "answer"}
	ch <- llm.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

// This is the W6 acceptance criterion: switching a prompt's active
// version changes what the RAG pipeline actually sends to the LLM,
// with no code change.
func TestRAGChatUsesActivePromptVersionAndReactsToSwitch(t *testing.T) {
	prompts := newMemPromptStore()
	ctx := context.Background()
	p, err := prompts.EnsurePrompt(ctx, usecase.RAGPromptName)
	if err != nil {
		t.Fatalf("EnsurePrompt: %v", err)
	}
	if _, err := prompts.CreateVersion(ctx, p.ID, "v1 instructions"); err != nil {
		t.Fatalf("CreateVersion v1: %v", err)
	}
	if _, err := prompts.CreateVersion(ctx, p.ID, "v2 instructions"); err != nil {
		t.Fatalf("CreateVersion v2: %v", err)
	}

	embedder := &fakeEmbedder{vector: []float32{1}}
	searchUC := usecase.NewSearchUseCase(&fakeVectorSearcher{}, &fakeKeywordSearcher{}, embedder, nil, nil)
	provider := &captureLLM{}
	router := testRouter(t, provider)
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}

	ragUC := usecase.NewRAGChatUseCase(searchUC, router, testPrices(), newMemTraceStore(), tok, prompts)

	// v1 is active by default (the first version created).
	result, err := ragUC.ChatStream(ctx, "kb-1", "normal", "q", false)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for range result.Events {
	}
	if provider.lastSystemPrompt != "v1 instructions" {
		t.Fatalf("expected v1 instructions to be used, got %q", provider.lastSystemPrompt)
	}

	// Switch to v2 — the very next call must reflect it immediately.
	if err := prompts.SetActiveVersion(ctx, p.ID, 2); err != nil {
		t.Fatalf("SetActiveVersion: %v", err)
	}
	result, err = ragUC.ChatStream(ctx, "kb-1", "normal", "q", false)
	if err != nil {
		t.Fatalf("ChatStream (after switch): %v", err)
	}
	for range result.Events {
	}
	if provider.lastSystemPrompt != "v2 instructions" {
		t.Fatalf("expected v2 instructions after switching, got %q", provider.lastSystemPrompt)
	}
}

func TestRAGChatFallsBackToDefaultPromptWhenRegistryEmpty(t *testing.T) {
	prompts := newMemPromptStore() // no prompt seeded at all
	embedder := &fakeEmbedder{vector: []float32{1}}
	searchUC := usecase.NewSearchUseCase(&fakeVectorSearcher{}, &fakeKeywordSearcher{}, embedder, nil, nil)
	provider := &captureLLM{}
	router := testRouter(t, provider)
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}

	ragUC := usecase.NewRAGChatUseCase(searchUC, router, testPrices(), newMemTraceStore(), tok, prompts)
	result, err := ragUC.ChatStream(context.Background(), "kb-1", "normal", "q", false)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for range result.Events {
	}
	if provider.lastSystemPrompt != usecase.DefaultRAGSystemPrompt {
		t.Fatalf("expected fallback to DefaultRAGSystemPrompt, got %q", provider.lastSystemPrompt)
	}
}
