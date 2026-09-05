package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

func setupFTSFixture(t *testing.T) (*sqlite.FTSStore, string, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "forgeai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ks := sqlite.NewKnowledgeStore(db)
	ctx := t.Context()

	kbA, _ := ks.EnsureKnowledgeBase(ctx, "A", "kb-a")
	kbB, _ := ks.EnsureKnowledgeBase(ctx, "B", "kb-b")

	_ = ks.CreateDocument(ctx, knowledge.Document{ID: "doc-a", KnowledgeBaseID: kbA.ID, Filename: "policy.md"})
	_ = ks.ReplaceChunks(ctx, "doc-a", []knowledge.Chunk{
		{ID: "chunk-a1", DocumentID: "doc-a", Index: 0, Text: "返品規定について: 商品到着後7日以内であれば返品可能です。", Hash: "h1"},
		{ID: "chunk-a2", DocumentID: "doc-a", Index: 1, Text: "配送料金は全国一律500円です。", Hash: "h2"},
	})

	_ = ks.CreateDocument(ctx, knowledge.Document{ID: "doc-b", KnowledgeBaseID: kbB.ID, Filename: "other.md"})
	_ = ks.ReplaceChunks(ctx, "doc-b", []knowledge.Chunk{
		{ID: "chunk-b1", DocumentID: "doc-b", Index: 0, Text: "返品規定は別のナレッジベースにも存在します。", Hash: "h3"},
	})

	return sqlite.NewFTSStore(db), kbA.ID, kbB.ID
}

func TestFTSStoreMatchesSubstring(t *testing.T) {
	store, kbA, _ := setupFTSFixture(t)
	results, err := store.Search(t.Context(), kbA, "商品到着", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ChunkID != "chunk-a1" {
		t.Fatalf("got %+v, want a single match on chunk-a1", results)
	}
}

// This is the F-4 regression: a 1-2 character query can't form a trigram,
// so it must fall back to LIKE instead of silently returning nothing.
func TestFTSStoreShortQueryFallsBackToLike(t *testing.T) {
	store, kbA, _ := setupFTSFixture(t)
	results, err := store.Search(t.Context(), kbA, "返品", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ChunkID != "chunk-a1" {
		t.Fatalf("got %+v, want the short query to still match chunk-a1 via LIKE fallback", results)
	}
}

func TestFTSStoreScopesToKnowledgeBase(t *testing.T) {
	store, kbA, kbB := setupFTSFixture(t)

	resultsA, err := store.Search(t.Context(), kbA, "返品規定", 10)
	if err != nil {
		t.Fatalf("Search kbA: %v", err)
	}
	for _, r := range resultsA {
		if r.ChunkID == "chunk-b1" {
			t.Error("expected kbA search to never return kbB's chunk")
		}
	}

	resultsB, err := store.Search(t.Context(), kbB, "返品規定", 10)
	if err != nil {
		t.Fatalf("Search kbB: %v", err)
	}
	if len(resultsB) != 1 || resultsB[0].ChunkID != "chunk-b1" {
		t.Fatalf("got %+v, want only kbB's own chunk", resultsB)
	}
}

func TestFTSStoreNoMatchReturnsEmpty(t *testing.T) {
	store, kbA, _ := setupFTSFixture(t)
	results, err := store.Search(t.Context(), kbA, "全く関係のない単語列", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected no matches, got %+v", results)
	}
}

// ReplaceChunks must clear old FTS rows too, or a re-ingested document
// would surface stale text alongside (or instead of) the new content.
func TestReplaceChunksSyncsFTS(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "forgeai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ks := sqlite.NewKnowledgeStore(db)
	fts := sqlite.NewFTSStore(db)
	ctx := t.Context()

	kb, _ := ks.EnsureKnowledgeBase(ctx, "Demo", "demo")
	_ = ks.CreateDocument(ctx, knowledge.Document{ID: "doc-1", KnowledgeBaseID: kb.ID, Filename: "a.txt"})
	_ = ks.ReplaceChunks(ctx, "doc-1", []knowledge.Chunk{
		{ID: "chunk-1", DocumentID: "doc-1", Index: 0, Text: "オリジナルのテキストです", Hash: "h1"},
	})

	if results, err := fts.Search(ctx, kb.ID, "オリジナルのテキスト", 10); err != nil || len(results) != 1 {
		t.Fatalf("expected original text to be searchable, got %+v, err=%v", results, err)
	}

	_ = ks.ReplaceChunks(ctx, "doc-1", []knowledge.Chunk{
		{ID: "chunk-2", DocumentID: "doc-1", Index: 0, Text: "更新後の新しいテキストです", Hash: "h2"},
	})

	if results, err := fts.Search(ctx, kb.ID, "オリジナルのテキスト", 10); err != nil || len(results) != 0 {
		t.Fatalf("expected stale FTS row to be gone after re-ingest, got %+v, err=%v", results, err)
	}
	if results, err := fts.Search(ctx, kb.ID, "更新後の新しいテキスト", 10); err != nil || len(results) != 1 {
		t.Fatalf("expected new text to be searchable after re-ingest, got %+v, err=%v", results, err)
	}
}
