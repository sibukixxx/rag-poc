package vecmem_test

import (
	"path/filepath"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/adapter/vecmem"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

func TestSearchRanksByCosineSimilarityAndScopesToKB(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "forgeai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ks := sqlite.NewKnowledgeStore(db)
	ctx := t.Context()

	kbA, _ := ks.EnsureKnowledgeBase(ctx, "A", "kb-a")
	kbB, _ := ks.EnsureKnowledgeBase(ctx, "B", "kb-b")

	_ = ks.CreateDocument(ctx, knowledge.Document{ID: "doc-a", KnowledgeBaseID: kbA.ID, Filename: "a.txt"})
	_ = ks.ReplaceChunks(ctx, "doc-a", []knowledge.Chunk{
		{ID: "close", DocumentID: "doc-a", Index: 0, Text: "close to query", Hash: "h1"},
		{ID: "far", DocumentID: "doc-a", Index: 1, Text: "far from query", Hash: "h2"},
		{ID: "orthogonal", DocumentID: "doc-a", Index: 2, Text: "orthogonal", Hash: "h3"},
	})
	_ = ks.SaveEmbedding(ctx, "close", "h1", "m", []float32{1, 0, 0})
	_ = ks.SaveEmbedding(ctx, "far", "h2", "m", []float32{0.5, 0.5, 0})
	_ = ks.SaveEmbedding(ctx, "orthogonal", "h3", "m", []float32{0, 1, 0})

	_ = ks.CreateDocument(ctx, knowledge.Document{ID: "doc-b", KnowledgeBaseID: kbB.ID, Filename: "b.txt"})
	_ = ks.ReplaceChunks(ctx, "doc-b", []knowledge.Chunk{
		{ID: "other-kb", DocumentID: "doc-b", Index: 0, Text: "belongs to kb-b", Hash: "h4"},
	})
	_ = ks.SaveEmbedding(ctx, "other-kb", "h4", "m", []float32{1, 0, 0}) // identical vector, different KB

	store := vecmem.New(db)
	results, err := store.Search(ctx, kbA.ID, []float32{1, 0, 0}, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results scoped to kbA, got %d: %+v", len(results), results)
	}
	if results[0].ChunkID != "close" {
		t.Errorf("expected 'close' to rank first, got %+v", results[0])
	}
	if results[len(results)-1].ChunkID != "orthogonal" {
		t.Errorf("expected 'orthogonal' to rank last, got %+v", results[len(results)-1])
	}
	for _, r := range results {
		if r.ChunkID == "other-kb" {
			t.Error("expected kbA search to never return kbB's chunk despite an identical vector")
		}
	}
}

func TestSearchRespectsTopK(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "forgeai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ks := sqlite.NewKnowledgeStore(db)
	ctx := t.Context()
	kb, _ := ks.EnsureKnowledgeBase(ctx, "Demo", "demo")
	_ = ks.CreateDocument(ctx, knowledge.Document{ID: "doc-1", KnowledgeBaseID: kb.ID, Filename: "a.txt"})

	chunks := make([]knowledge.Chunk, 5)
	for i := range chunks {
		chunks[i] = knowledge.Chunk{ID: string(rune('a' + i)), DocumentID: "doc-1", Index: i, Text: "x", Hash: string(rune('a' + i))}
	}
	_ = ks.ReplaceChunks(ctx, "doc-1", chunks)
	for _, c := range chunks {
		_ = ks.SaveEmbedding(ctx, c.ID, c.Hash, "m", []float32{1, 0})
	}

	store := vecmem.New(db)
	results, err := store.Search(ctx, kb.ID, []float32{1, 0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected topK=2 to limit results to 2, got %d", len(results))
	}
}
