package usecase_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// memKnowledgeStore is a minimal in-memory knowledge.Store for testing
// IngestUseCase without a real database.
type memKnowledgeStore struct {
	mu         sync.Mutex
	kbs        map[string]knowledge.KnowledgeBase
	docs       map[string]knowledge.Document
	chunks     map[string][]knowledge.Chunk    // documentID -> chunks
	embeddings map[string]map[string][]float32 // hash -> model -> vector
}

func newMemKnowledgeStore() *memKnowledgeStore {
	return &memKnowledgeStore{
		kbs:        map[string]knowledge.KnowledgeBase{},
		docs:       map[string]knowledge.Document{},
		chunks:     map[string][]knowledge.Chunk{},
		embeddings: map[string]map[string][]float32{},
	}
}

func (m *memKnowledgeStore) EnsureKnowledgeBase(ctx context.Context, name, slug string) (*knowledge.KnowledgeBase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, kb := range m.kbs {
		if kb.Slug == slug {
			return &kb, nil
		}
	}
	kb := knowledge.KnowledgeBase{ID: uuid.NewString(), Name: name, Slug: slug}
	m.kbs[kb.ID] = kb
	return &kb, nil
}

func (m *memKnowledgeStore) GetKnowledgeBase(ctx context.Context, id string) (*knowledge.KnowledgeBase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kb, ok := m.kbs[id]
	if !ok {
		return nil, errNotFound
	}
	return &kb, nil
}

func (m *memKnowledgeStore) ListKnowledgeBases(ctx context.Context) ([]knowledge.KnowledgeBase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []knowledge.KnowledgeBase
	for _, kb := range m.kbs {
		out = append(out, kb)
	}
	return out, nil
}

func (m *memKnowledgeStore) CreateDocument(ctx context.Context, d knowledge.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[d.ID] = d
	return nil
}

func (m *memKnowledgeStore) UpdateDocumentStatus(ctx context.Context, id string, status knowledge.DocumentStatus, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.docs[id]
	d.Status = status
	d.Error = errMsg
	m.docs[id] = d
	return nil
}

func (m *memKnowledgeStore) GetDocument(ctx context.Context, id string) (*knowledge.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[id]
	if !ok {
		return nil, errNotFound
	}
	d.ChunkCount = len(m.chunks[id])
	return &d, nil
}

func (m *memKnowledgeStore) ListDocuments(ctx context.Context, knowledgeBaseID string) ([]knowledge.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []knowledge.Document
	for _, d := range m.docs {
		if d.KnowledgeBaseID == knowledgeBaseID {
			d.ChunkCount = len(m.chunks[d.ID])
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *memKnowledgeStore) ReplaceChunks(ctx context.Context, documentID string, chunks []knowledge.Chunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]knowledge.Chunk, len(chunks))
	copy(cp, chunks)
	m.chunks[documentID] = cp
	return nil
}

func (m *memKnowledgeStore) ListChunks(ctx context.Context, documentID string) ([]knowledge.Chunk, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.chunks[documentID], nil
}

func (m *memKnowledgeStore) FindEmbeddingByHash(ctx context.Context, hash, model string) ([]float32, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byModel, ok := m.embeddings[hash]
	if !ok {
		return nil, false, nil
	}
	vec, ok := byModel[model]
	return vec, ok, nil
}

func (m *memKnowledgeStore) SaveEmbedding(ctx context.Context, chunkID, hash, model string, vector []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.embeddings[hash] == nil {
		m.embeddings[hash] = map[string][]float32{}
	}
	m.embeddings[hash][model] = vector
	return nil
}

type notFoundErr struct{}

func (notFoundErr) Error() string { return "not found" }

var errNotFound = notFoundErr{}

// passthroughLoader treats the whole file as a single page of text, used
// by tests instead of the real extractor package.
type passthroughLoader struct{}

func (passthroughLoader) Supports(filename, mimeType string) bool { return true }
func (passthroughLoader) Load(ctx context.Context, data []byte, meta knowledge.FileMeta) ([]knowledge.Page, error) {
	return []knowledge.Page{{Text: string(data)}}, nil
}

type staticLoaders struct{ loader knowledge.Loader }

func (s staticLoaders) Find(filename, mimeType string) (knowledge.Loader, bool) {
	return s.loader, true
}

// countingEmbedder records how many times Embed was called and how many
// texts it was asked to embed in total, so tests can assert the
// hash-based skip actually avoids redundant calls.
type countingEmbedder struct {
	mu        sync.Mutex
	calls     int
	textsSeen int
}

func (e *countingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.calls++
	e.textsSeen += len(texts)
	e.mu.Unlock()

	vectors := make([][]float32, len(texts))
	for i, t := range texts {
		vectors[i] = []float32{float32(len(t))}
	}
	return vectors, nil
}

func (e *countingEmbedder) Dimensions() int { return 1 }
func (e *countingEmbedder) Model() string   { return "test-embed-model" }

func newIngestUseCase(t *testing.T, store *memKnowledgeStore, embedder llm.Embedder) *usecase.IngestUseCase {
	t.Helper()
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}
	return usecase.NewIngestUseCase(store, staticLoaders{passthroughLoader{}}, tok, embedder, testPrices(), newMemTraceStore())
}

func TestIngestFileCreatesChunksAndEmbeddings(t *testing.T) {
	store := newMemKnowledgeStore()
	embedder := &countingEmbedder{}
	uc := newIngestUseCase(t, store, embedder)

	kb, err := store.EnsureKnowledgeBase(context.Background(), "Demo", "demo")
	if err != nil {
		t.Fatalf("EnsureKnowledgeBase: %v", err)
	}

	doc, err := uc.IngestFile(context.Background(), kb.ID, "notes.txt", "text/plain", []byte("hello world, this is ForgeAI"))
	if err != nil {
		t.Fatalf("IngestFile: %v", err)
	}
	if doc.Status != knowledge.DocumentStatusReady {
		t.Fatalf("expected status ready, got %s (%s)", doc.Status, doc.Error)
	}
	if doc.ChunkCount == 0 {
		t.Fatal("expected at least one chunk")
	}
	if embedder.calls != 1 {
		t.Errorf("expected exactly 1 embed call for a fresh document, got %d", embedder.calls)
	}

	chunks, err := store.ListChunks(context.Background(), doc.ID)
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	for _, c := range chunks {
		if _, found, _ := store.FindEmbeddingByHash(context.Background(), c.Hash, embedder.Model()); !found {
			t.Errorf("expected an embedding for chunk %s", c.ID)
		}
	}
}

// This is the acceptance criterion from docs/ROADMAP.md W3: re-ingesting
// identical content must not call the embedder again.
func TestReIngestingIdenticalContentSkipsEmbedding(t *testing.T) {
	store := newMemKnowledgeStore()
	embedder := &countingEmbedder{}
	uc := newIngestUseCase(t, store, embedder)

	kb, _ := store.EnsureKnowledgeBase(context.Background(), "Demo", "demo")
	content := []byte("The return policy allows returns within 7 days of delivery.")

	first, err := uc.IngestFile(context.Background(), kb.ID, "policy.txt", "text/plain", content)
	if err != nil {
		t.Fatalf("first IngestFile: %v", err)
	}
	if embedder.calls != 1 {
		t.Fatalf("expected 1 embed call after first ingest, got %d", embedder.calls)
	}

	// Re-ingest the identical content as a brand new Document (e.g. the
	// same file re-uploaded). The chunk hashes are identical, so no new
	// embedding calls should happen.
	second, err := uc.IngestFile(context.Background(), kb.ID, "policy.txt", "text/plain", content)
	if err != nil {
		t.Fatalf("second IngestFile: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("expected a distinct document ID for the re-ingested file")
	}
	if embedder.calls != 1 {
		t.Errorf("expected embed calls to stay at 1 after re-ingesting identical content, got %d", embedder.calls)
	}
	if second.ChunkCount != first.ChunkCount {
		t.Errorf("expected the same chunk count on re-ingest, got %d vs %d", second.ChunkCount, first.ChunkCount)
	}

	// Changing the content must trigger a new embedding call.
	if _, err := uc.IngestFile(context.Background(), kb.ID, "policy-v2.txt", "text/plain", []byte("Completely different content that was never seen before.")); err != nil {
		t.Fatalf("third IngestFile: %v", err)
	}
	if embedder.calls != 2 {
		t.Errorf("expected a second embed call for genuinely new content, got %d calls", embedder.calls)
	}
}

func TestIngestFileUnsupportedTypeFails(t *testing.T) {
	store := newMemKnowledgeStore()
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}
	uc := usecase.NewIngestUseCase(store, noLoaders{}, tok, &countingEmbedder{}, testPrices(), newMemTraceStore())

	kb, _ := store.EnsureKnowledgeBase(context.Background(), "Demo", "demo")

	doc, err := uc.IngestFile(context.Background(), kb.ID, "weird.bin", "application/octet-stream", []byte{0x00, 0x01})
	if err == nil {
		t.Fatal("expected an error for an unsupported file type")
	}
	if doc.Status != knowledge.DocumentStatusFailed {
		t.Errorf("expected status failed, got %s", doc.Status)
	}
}

type noLoaders struct{}

func (noLoaders) Find(filename, mimeType string) (knowledge.Loader, bool) { return nil, false }
