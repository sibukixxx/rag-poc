package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

func openKnowledgeStore(t *testing.T) *sqlite.KnowledgeStore {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "forgeai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return sqlite.NewKnowledgeStore(db)
}

func TestEnsureKnowledgeBaseIsIdempotent(t *testing.T) {
	store := openKnowledgeStore(t)
	ctx := t.Context()

	a, err := store.EnsureKnowledgeBase(ctx, "Demo", "demo")
	if err != nil {
		t.Fatalf("EnsureKnowledgeBase: %v", err)
	}
	b, err := store.EnsureKnowledgeBase(ctx, "Demo (renamed, ignored)", "demo")
	if err != nil {
		t.Fatalf("EnsureKnowledgeBase (second call): %v", err)
	}
	if a.ID != b.ID {
		t.Errorf("expected same KB id on repeat call, got %s vs %s", a.ID, b.ID)
	}

	list, err := store.ListKnowledgeBases(ctx)
	if err != nil {
		t.Fatalf("ListKnowledgeBases: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected exactly 1 knowledge base, got %d", len(list))
	}
}

func TestDocumentAndChunkLifecycle(t *testing.T) {
	store := openKnowledgeStore(t)
	ctx := t.Context()

	kb, err := store.EnsureKnowledgeBase(ctx, "Demo", "demo")
	if err != nil {
		t.Fatalf("EnsureKnowledgeBase: %v", err)
	}

	doc := knowledge.Document{ID: "doc-1", KnowledgeBaseID: kb.ID, Filename: "a.txt", Status: knowledge.DocumentStatusPending}
	if err := store.CreateDocument(ctx, doc); err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	page := 1
	chunks := []knowledge.Chunk{
		{ID: "chunk-1", DocumentID: "doc-1", Index: 0, Text: "hello", TokenCount: 1, Page: &page, Hash: "h1"},
		{ID: "chunk-2", DocumentID: "doc-1", Index: 1, Text: "world", TokenCount: 1, Hash: "h2"},
	}
	if err := store.ReplaceChunks(ctx, "doc-1", chunks); err != nil {
		t.Fatalf("ReplaceChunks: %v", err)
	}

	if err := store.UpdateDocumentStatus(ctx, "doc-1", knowledge.DocumentStatusReady, ""); err != nil {
		t.Fatalf("UpdateDocumentStatus: %v", err)
	}

	got, err := store.GetDocument(ctx, "doc-1")
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if got.Status != knowledge.DocumentStatusReady {
		t.Errorf("expected status ready, got %s", got.Status)
	}
	if got.ChunkCount != 2 {
		t.Errorf("expected chunk_count 2, got %d", got.ChunkCount)
	}

	gotChunks, err := store.ListChunks(ctx, "doc-1")
	if err != nil {
		t.Fatalf("ListChunks: %v", err)
	}
	if len(gotChunks) != 2 || gotChunks[0].Page == nil || *gotChunks[0].Page != 1 {
		t.Errorf("got chunks %+v", gotChunks)
	}
	if gotChunks[1].Page != nil {
		t.Errorf("expected nil page for chunk without a page number, got %v", *gotChunks[1].Page)
	}

	// Re-ingest: ReplaceChunks must fully swap, not append.
	if err := store.ReplaceChunks(ctx, "doc-1", []knowledge.Chunk{
		{ID: "chunk-3", DocumentID: "doc-1", Index: 0, Text: "new content", TokenCount: 2, Hash: "h3"},
	}); err != nil {
		t.Fatalf("ReplaceChunks (re-ingest): %v", err)
	}
	gotChunks, err = store.ListChunks(ctx, "doc-1")
	if err != nil {
		t.Fatalf("ListChunks (after re-ingest): %v", err)
	}
	if len(gotChunks) != 1 || gotChunks[0].ID != "chunk-3" {
		t.Errorf("expected chunks fully replaced, got %+v", gotChunks)
	}

	list, err := store.ListDocuments(ctx, kb.ID)
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 document, got %d", len(list))
	}
}

func TestEmbeddingHashLookupRoundTrip(t *testing.T) {
	store := openKnowledgeStore(t)
	ctx := t.Context()

	kb, _ := store.EnsureKnowledgeBase(ctx, "Demo", "demo")
	_ = store.CreateDocument(ctx, knowledge.Document{ID: "doc-1", KnowledgeBaseID: kb.ID, Filename: "a.txt"})
	_ = store.ReplaceChunks(ctx, "doc-1", []knowledge.Chunk{
		{ID: "chunk-1", DocumentID: "doc-1", Index: 0, Text: "hello", Hash: "abc123"},
	})

	if _, found, err := store.FindEmbeddingByHash(ctx, "abc123", "text-embedding-3-small"); err != nil {
		t.Fatalf("FindEmbeddingByHash (miss): %v", err)
	} else if found {
		t.Fatal("expected no embedding before SaveEmbedding")
	}

	vector := []float32{0.1, -0.2, 0.3}
	if err := store.SaveEmbedding(ctx, "chunk-1", "abc123", "text-embedding-3-small", vector); err != nil {
		t.Fatalf("SaveEmbedding: %v", err)
	}

	got, found, err := store.FindEmbeddingByHash(ctx, "abc123", "text-embedding-3-small")
	if err != nil {
		t.Fatalf("FindEmbeddingByHash (hit): %v", err)
	}
	if !found {
		t.Fatal("expected embedding to be found after SaveEmbedding")
	}
	if len(got) != 3 || got[0] != 0.1 || got[1] != -0.2 || got[2] != 0.3 {
		t.Errorf("got vector %+v, want %+v", got, vector)
	}

	// A different model must not match, even with the same hash.
	if _, found, err := store.FindEmbeddingByHash(ctx, "abc123", "some-other-model"); err != nil {
		t.Fatalf("FindEmbeddingByHash (different model): %v", err)
	} else if found {
		t.Error("expected no match for a different embedding model")
	}
}
