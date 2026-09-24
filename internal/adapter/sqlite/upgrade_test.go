package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/deployment"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

// lastPreV01Migration is the newest schema that shipped on main before the
// W10 Deployment work. An operator upgrading from that build must keep all
// existing knowledge while gaining the new tables (V0.1_SPEC §9 item 10).
const lastPreV01Migration = "migrations/0009_source_sync.sql"

func applyMigrationsThrough(t *testing.T, db *sql.DB, last string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.Glob(embeddedMigrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	for _, path := range entries {
		if path > last {
			break
		}
		content, err := embeddedMigrations.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(content)); err != nil {
			t.Fatalf("applying %s: %v", path, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrateUpgradesPreV01DatabaseWithoutLosingKnowledge(t *testing.T) {
	// Arrange: a database created by the last pre-v0.1 build, holding a
	// knowledge base with one indexed document.
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	applyMigrationsThrough(t, db, lastPreV01Migration)

	store := NewKnowledgeStore(db)
	kb, err := store.EnsureKnowledgeBase(ctx, "Legacy", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	doc := knowledge.Document{
		ID: "doc-legacy", KnowledgeBaseID: kb.ID, Filename: "legacy.md", MimeType: "text/markdown",
		SizeBytes: 42, Status: knowledge.DocumentStatusReady, CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceChunks(ctx, doc.ID, []knowledge.Chunk{{
		ID: "chunk-legacy", DocumentID: doc.ID, Text: "legacy refund policy text", TokenCount: 4, Hash: "h1",
	}}); err != nil {
		t.Fatal(err)
	}

	// Act: the current binary opens the same database.
	if err := Migrate(db); err != nil {
		t.Fatalf("upgrading pre-v0.1 database: %v", err)
	}

	// Assert: existing knowledge is still searchable ...
	results, err := NewFTSStore(db).Search(ctx, kb.ID, "refund policy", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ChunkID != "chunk-legacy" {
		t.Fatalf("FTS results after upgrade = %+v, want the legacy chunk", results)
	}
	// ... and the new W10 tables accept a Deployment of that old KB.
	dep := deployment.Deployment{
		ID: "dep-upgrade", Slug: "legacy-app", KnowledgeBaseID: kb.ID,
		PromptName: "rag_system", PromptVersion: 1, PromptContent: "p", Alias: "normal", TopK: 5,
		CreatedAt: time.Now().UTC(),
	}
	if err := NewDeploymentStore(db).CreateDeployment(ctx, dep); err != nil {
		t.Fatalf("creating deployment on upgraded database: %v", err)
	}
}
