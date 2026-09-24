package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/oauth"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
)

func TestSourceStoreTracksAuthStateAndEnablement(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	kb, err := sqlite.NewKnowledgeStore(db).EnsureKnowledgeBase(ctx, "K", "k")
	if err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewSourceStore(db)
	now := time.Now().UTC()
	if err := store.CreateConnection(ctx, source.Connection{ID: "c1", KnowledgeBaseID: kb.ID, Provider: "feed", Name: "n",
		Config: json.RawMessage(`{}`), Enabled: true, AuthState: source.AuthAuthorizationRequired, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateConnectionAuth(ctx, "c1", source.AuthAuthorized, "source-oauth:c1", "", now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetConnectionEnabled(ctx, "c1", false, now); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetConnection(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthState != source.AuthAuthorized || got.SecretName != "source-oauth:c1" || got.Enabled || got.LastError != "" {
		t.Fatalf("connection = %+v", got)
	}
	if err := store.UpdateConnectionAuth(ctx, "missing", source.AuthAuthorized, "", "", now); !errors.Is(err, source.ErrNotFound) {
		t.Fatalf("update of missing connection error = %v", err)
	}
}

func TestSourceStoreDeleteConnectionRemovesItsDocumentsOnly(t *testing.T) {
	db, _, connID := newBulkFixture(t)
	ctx := context.Background()
	ks := sqlite.NewKnowledgeStore(db)
	var kbID string
	if err := db.QueryRow(`SELECT knowledge_base_id FROM source_connections WHERE id = ?`, connID).Scan(&kbID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"synced", "uploaded"} {
		if err := ks.CreateDocument(ctx, knowledge.Document{ID: id, KnowledgeBaseID: kbID, Filename: id + ".md", Status: knowledge.DocumentStatusReady}); err != nil {
			t.Fatal(err)
		}
		if err := ks.ReplaceChunks(ctx, id, []knowledge.Chunk{{ID: id + "-c", DocumentID: id, Text: id + " text", TokenCount: 1, Hash: id}}); err != nil {
			t.Fatal(err)
		}
	}
	store := sqlite.NewSourceStore(db)
	if err := store.ReplaceItemDocument(ctx, source.Item{ID: "i1", ConnectionID: connID, ExternalID: "synced.md", DocumentID: "synced", ContentHash: "h", SyncedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	removed, err := store.DeleteConnection(ctx, connID)

	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed documents = %d, want 1", removed)
	}
	if n := countRows(t, db, `SELECT COUNT(1) FROM documents WHERE id = 'uploaded'`); n != 1 {
		t.Fatal("a manually uploaded document was deleted with the connection")
	}
	for _, q := range []string{
		`SELECT COUNT(1) FROM documents WHERE id = 'synced'`,
		`SELECT COUNT(1) FROM chunks_fts WHERE document_id = 'synced'`,
		`SELECT COUNT(1) FROM source_connections`,
		`SELECT COUNT(1) FROM ingestion_jobs`,
	} {
		if n := countRows(t, db, q); n != 0 {
			t.Errorf("%s = %d after disconnect", q, n)
		}
	}
}

func TestOAuthStateStoreIsSingleUse(t *testing.T) {
	db, _, connID := newBulkFixture(t)
	ctx := context.Background()
	store := sqlite.NewOAuthStateStore(db)
	pending := oauth.PendingAuthorization{StateHash: "h1", ConnectionID: connID, Provider: "fake", CodeVerifier: "v1", ExpiresAt: time.Now().Add(time.Minute).UTC().Truncate(time.Millisecond)}
	if err := store.SavePending(ctx, pending); err != nil {
		t.Fatal(err)
	}

	got, err := store.TakePending(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	_, again := store.TakePending(ctx, "h1")

	if got.ConnectionID != connID || got.CodeVerifier != "v1" || !got.ExpiresAt.Equal(pending.ExpiresAt) {
		t.Fatalf("pending = %+v, want %+v", got, pending)
	}
	if !errors.Is(again, oauth.ErrInvalidState) {
		t.Fatalf("second take error = %v, want ErrInvalidState", again)
	}
}
