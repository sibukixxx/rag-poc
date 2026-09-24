package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/deployment"
	"github.com/sibukixxx/rag-poc/internal/domain/eval"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/lifecycle"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

type lifecycleFixture struct {
	db     *sql.DB
	path   string
	kbID   string
	docA   string
	docB   string
	connID string
}

// newLifecycleFixture builds a knowledge base holding two indexed documents
// (one of them source-synced), a Golden Dataset with a judged run, and a
// source connection — every customer-derived artifact the v0.1 storage
// model persists for a knowledge base.
func newLifecycleFixture(t *testing.T) lifecycleFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "forgeai.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ks := sqlite.NewKnowledgeStore(db)
	kb, err := ks.EnsureKnowledgeBase(ctx, "Customer", "customer")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct{ id, text string }{
		{"doc-a", "CONFIDENTIAL-MARKER-A customer contract terms"},
		{"doc-b", "public product overview"},
	} {
		if err := ks.CreateDocument(ctx, knowledge.Document{
			ID: d.id, KnowledgeBaseID: kb.ID, Filename: d.id + ".md", MimeType: "text/markdown",
			Status: knowledge.DocumentStatusReady, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		chunkID := d.id + "-c0"
		if err := ks.ReplaceChunks(ctx, d.id, []knowledge.Chunk{{ID: chunkID, DocumentID: d.id, Text: d.text, TokenCount: 5, Hash: "h-" + d.id}}); err != nil {
			t.Fatal(err)
		}
		if err := ks.SaveEmbedding(ctx, chunkID, "h-"+d.id, "m", []float32{1, 0}); err != nil {
			t.Fatal(err)
		}
	}

	ss := sqlite.NewSourceStore(db)
	conn := source.Connection{
		ID: "conn-1", KnowledgeBaseID: kb.ID, Provider: "http_feed", Name: "feed",
		Config: json.RawMessage(`{}`), Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := ss.CreateConnection(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO source_items (id, connection_id, external_id, document_id, title, source_url, metadata_json, visibility_json, content_hash, synced_at)
		VALUES ('item-a', 'conn-1', 'ext-a', 'doc-a', 'Contract', 'https://example.test/a', '{}', '[]', 'h', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	es := sqlite.NewEvalStore(db)
	ds, err := es.EnsureDataset(ctx, "golden", kb.ID)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := es.AddCases(ctx, ds.ID, []eval.Case{{Query: "contract terms?", ExpectedFilenames: []string{"doc-a.md"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := es.CreateRun(ctx, eval.Run{ID: "run-1", DatasetID: ds.ID, Status: eval.RunStatusDone, TopK: 3, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := es.CreateCaseResults(ctx, []eval.CaseResult{{ID: "res-1", RunID: "run-1", CaseID: cases[0].ID, Answer: "CONFIDENTIAL-MARKER-A answer"}}); err != nil {
		t.Fatal(err)
	}

	return lifecycleFixture{db: db, path: path, kbID: kb.ID, docA: "doc-a", docB: "doc-b", connID: conn.ID}
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestLifecycleStoreDeleteDocumentRemovesEveryDerivedArtifactOfThatDocumentOnly(t *testing.T) {
	f := newLifecycleFixture(t)
	store := sqlite.NewLifecycleStore(f.db)

	got, err := store.DeleteDocument(context.Background(), f.docA)
	if err != nil {
		t.Fatal(err)
	}

	want := lifecycle.DeletionReport{Documents: 1, Chunks: 1, Embeddings: 1, FTSRows: 1, SourceItems: 1}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("deletion report mismatch:\nwant %+v\ngot  %+v", want, got)
	}
	for _, q := range []string{
		`SELECT COUNT(1) FROM documents WHERE id = 'doc-a'`,
		`SELECT COUNT(1) FROM chunks WHERE document_id = 'doc-a'`,
		`SELECT COUNT(1) FROM embeddings WHERE chunk_id = 'doc-a-c0'`,
		`SELECT COUNT(1) FROM chunks_fts WHERE document_id = 'doc-a'`,
		`SELECT COUNT(1) FROM source_items WHERE external_id = 'ext-a'`,
	} {
		if n := countRows(t, f.db, q); n != 0 {
			t.Errorf("%s = %d after deletion, want 0", q, n)
		}
	}
	if n := countRows(t, f.db, `SELECT COUNT(1) FROM chunks_fts WHERE document_id = 'doc-b'`); n != 1 {
		t.Errorf("other document's FTS rows = %d, want 1 (untouched)", n)
	}
}

func TestLifecycleStoreDeleteDocumentIsIdempotent(t *testing.T) {
	f := newLifecycleFixture(t)
	store := sqlite.NewLifecycleStore(f.db)
	if _, err := store.DeleteDocument(context.Background(), f.docA); err != nil {
		t.Fatal(err)
	}

	got, err := store.DeleteDocument(context.Background(), f.docA)

	if err != nil {
		t.Fatalf("second deletion returned error: %v", err)
	}
	if !reflect.DeepEqual(lifecycle.DeletionReport{}, got) {
		t.Fatalf("second deletion should report nothing removed:\nwant %+v\ngot  %+v", lifecycle.DeletionReport{}, got)
	}
}

func TestLifecycleStoreDeleteKnowledgeBaseRefusesWhenDeploymentsExistUnlessIncluded(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	ds := sqlite.NewDeploymentStore(f.db)
	if err := ds.CreateDeployment(ctx, deployment.Deployment{
		ID: "dep-1", Slug: "customer-app", KnowledgeBaseID: f.kbID, PromptName: "rag_system",
		PromptVersion: 1, PromptContent: "p", Alias: "normal", TopK: 3, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := ds.CreateToken(ctx, deployment.Token{ID: "tok-1", DeploymentID: "dep-1", Name: "t", Prefix: "fai_", Hash: "x", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewLifecycleStore(f.db)

	_, err := store.DeleteKnowledgeBase(ctx, f.kbID, false)
	if !errors.Is(err, lifecycle.ErrKnowledgeBaseHasDeployments) {
		t.Fatalf("delete without includeDeployments error = %v, want ErrKnowledgeBaseHasDeployments", err)
	}
	if n := countRows(t, f.db, `SELECT COUNT(1) FROM chunks`); n != 2 {
		t.Fatalf("refused deletion still removed chunks: %d left, want 2", n)
	}

	got, err := store.DeleteKnowledgeBase(ctx, f.kbID, true)
	if err != nil {
		t.Fatal(err)
	}

	want := lifecycle.DeletionReport{
		KnowledgeBases: 1, Documents: 2, Chunks: 2, Embeddings: 2, FTSRows: 2,
		SourceConnections: 1, SourceItems: 1, Datasets: 1, EvaluationRuns: 1, Deployments: 1,
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("deletion report mismatch:\nwant %+v\ngot  %+v", want, got)
	}
	for _, table := range []string{
		"knowledge_bases", "documents", "chunks", "embeddings", "chunks_fts", "source_connections",
		"source_items", "source_sync_jobs", "datasets", "dataset_cases", "evaluation_runs",
		"evaluation_results", "deployments", "runtime_api_tokens",
	} {
		if n := countRows(t, f.db, `SELECT COUNT(1) FROM `+table); n != 0 {
			t.Errorf("%s has %d rows after knowledge-base deletion, want 0", table, n)
		}
	}
}

func TestLifecycleStoreDeleteKnowledgeBaseMissingIsIdempotent(t *testing.T) {
	f := newLifecycleFixture(t)

	got, err := sqlite.NewLifecycleStore(f.db).DeleteKnowledgeBase(context.Background(), "no-such-kb", false)

	if err != nil {
		t.Fatalf("deleting a missing knowledge base returned error: %v", err)
	}
	if !reflect.DeepEqual(lifecycle.DeletionReport{}, got) {
		t.Fatalf("missing knowledge base should report nothing removed:\nwant %+v\ngot  %+v", lifecycle.DeletionReport{}, got)
	}
}

func TestLifecycleStoreDeletedTextIsNotLeftInTheDatabaseFileAfterCompact(t *testing.T) {
	f := newLifecycleFixture(t)
	store := sqlite.NewLifecycleStore(f.db)
	ctx := context.Background()
	if _, err := store.DeleteKnowledgeBase(ctx, f.kbID, true); err != nil {
		t.Fatal(err)
	}

	if err := store.Compact(ctx); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{f.path, f.path + "-wal"} {
		data, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("CONFIDENTIAL-MARKER-A")) {
			t.Fatalf("%s still contains deleted customer text", filepath.Base(p))
		}
	}
}

func TestLifecycleStoreDeleteTracesStartedBeforeKeepsNewerTraces(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	ts := sqlite.NewTraceStore(f.db)
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	for _, tr := range []trace.Trace{
		{ID: "old", Name: "chat", StartedAt: now.AddDate(0, 0, -40), Status: trace.StatusOK},
		{ID: "new", Name: "chat", StartedAt: now.AddDate(0, 0, -1), Status: trace.StatusOK},
	} {
		if err := ts.CreateTrace(ctx, tr); err != nil {
			t.Fatal(err)
		}
		if err := ts.CreateSpan(ctx, trace.Span{ID: tr.ID + "-s", TraceID: tr.ID, Kind: trace.SpanKindLLM, Name: "x", StartedAt: tr.StartedAt, Status: trace.StatusOK}); err != nil {
			t.Fatal(err)
		}
	}

	n, err := sqlite.NewLifecycleStore(f.db).DeleteTracesStartedBefore(ctx, now.AddDate(0, 0, -30))

	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted traces = %d, want 1", n)
	}
	if got := countRows(t, f.db, `SELECT COUNT(1) FROM spans`); got != 1 {
		t.Fatalf("spans left = %d, want 1 (only the newer trace's span)", got)
	}
	if got := countRows(t, f.db, `SELECT COUNT(1) FROM traces WHERE id = 'new'`); got != 1 {
		t.Fatal("newer trace was deleted")
	}
}

func TestLifecycleStoreDeleteEvaluationRunsStartedBeforeKeepsDatasets(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()

	n, err := sqlite.NewLifecycleStore(f.db).DeleteEvaluationRunsStartedBefore(ctx, time.Now().UTC().Add(time.Hour))

	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted runs = %d, want 1", n)
	}
	if got := countRows(t, f.db, `SELECT COUNT(1) FROM evaluation_results`); got != 0 {
		t.Fatalf("evaluation_results left = %d, want 0", got)
	}
	if got := countRows(t, f.db, `SELECT COUNT(1) FROM dataset_cases`); got != 1 {
		t.Fatalf("dataset_cases = %d, want 1 (retention must not delete Golden Dataset definitions)", got)
	}
}

func TestLifecycleStoreCompactLeavesNoFTSIndexSegmentsForDeletedText(t *testing.T) {
	// FTS5 only tombstones deleted rows; their terms stay in index segments
	// until a merge. After a full purge only the index's structure records
	// (id 1: averages, id 10: structure) may remain.
	f := newLifecycleFixture(t)
	store := sqlite.NewLifecycleStore(f.db)
	ctx := context.Background()
	if _, err := store.DeleteKnowledgeBase(ctx, f.kbID, true); err != nil {
		t.Fatal(err)
	}

	if err := store.Compact(ctx); err != nil {
		t.Fatal(err)
	}

	if n := countRows(t, f.db, `SELECT COUNT(1) FROM chunks_fts_data WHERE id NOT IN (1, 10)`); n != 0 {
		t.Fatalf("FTS index still holds %d segment rows for deleted text", n)
	}
}
