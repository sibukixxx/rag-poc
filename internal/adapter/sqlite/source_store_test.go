package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
)

func TestSourceStoreDocumentLifecycleAndJobs(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	knowledgeStore := sqlite.NewKnowledgeStore(db)
	kb, err := knowledgeStore.EnsureKnowledgeBase(ctx, "Demo", "demo")
	if err != nil {
		t.Fatalf("EnsureKnowledgeBase: %v", err)
	}
	store := sqlite.NewSourceStore(db)
	now := time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC)
	connection := source.Connection{
		ID: "conn-1", KnowledgeBaseID: kb.ID, Provider: "slack", Name: "Support",
		Config: json.RawMessage(`{"channels":["C1"]}`), SecretName: "source/slack/conn-1",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateConnection(ctx, connection); err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	gotConnection, err := store.GetConnection(ctx, connection.ID)
	if err != nil || gotConnection.Provider != "slack" || !gotConnection.Enabled {
		t.Fatalf("GetConnection: got=%+v err=%v", gotConnection, err)
	}

	createDocument := func(id, text string) {
		t.Helper()
		if err := knowledgeStore.CreateDocument(ctx, knowledge.Document{
			ID: id, KnowledgeBaseID: kb.ID, Filename: id + ".txt", Status: knowledge.DocumentStatusReady,
		}); err != nil {
			t.Fatalf("CreateDocument: %v", err)
		}
		if err := knowledgeStore.ReplaceChunks(ctx, id, []knowledge.Chunk{{
			ID: "chunk-" + id, DocumentID: id, Text: text, Hash: "hash-" + id,
		}}); err != nil {
			t.Fatalf("ReplaceChunks: %v", err)
		}
	}
	createDocument("doc-1", "old text")
	item := source.Item{
		ID: "item-1", ConnectionID: connection.ID, ExternalID: "C1:1", DocumentID: "doc-1",
		Title: "Thread", SourceURL: "https://example.test/1", Metadata: json.RawMessage(`{"channel":"C1"}`),
		Visibility: json.RawMessage(`["C1"]`), ContentHash: "v1", SyncedAt: now,
	}
	if err := store.ReplaceItemDocument(ctx, item); err != nil {
		t.Fatalf("ReplaceItemDocument first: %v", err)
	}

	createDocument("doc-2", "new text")
	item.DocumentID = "doc-2"
	item.ContentHash = "v2"
	if err := store.ReplaceItemDocument(ctx, item); err != nil {
		t.Fatalf("ReplaceItemDocument update: %v", err)
	}
	if _, err := knowledgeStore.GetDocument(ctx, "doc-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old document should be removed, got %v", err)
	}
	var oldFTS int
	if err := db.QueryRow(`SELECT COUNT(1) FROM chunks_fts WHERE document_id = 'doc-1'`).Scan(&oldFTS); err != nil || oldFTS != 0 {
		t.Fatalf("old FTS rows remain: count=%d err=%v", oldFTS, err)
	}
	gotItem, err := store.GetItem(ctx, connection.ID, item.ExternalID)
	if err != nil || gotItem.DocumentID != "doc-2" || string(gotItem.Metadata) != `{"channel":"C1"}` {
		t.Fatalf("GetItem after update: got=%+v err=%v", gotItem, err)
	}

	deleted, err := store.DeleteItemDocument(ctx, connection.ID, item.ExternalID, now.Add(time.Minute))
	if err != nil || !deleted {
		t.Fatalf("DeleteItemDocument: deleted=%v err=%v", deleted, err)
	}
	gotItem, _ = store.GetItem(ctx, connection.ID, item.ExternalID)
	if gotItem.DocumentID != "" || gotItem.DeletedAt == nil {
		t.Fatalf("deleted item still active: %+v", gotItem)
	}

	job := source.SyncJob{
		ID: "job-1", ConnectionID: connection.ID, Status: source.JobStatusRunning,
		StartedAt: now, CursorBefore: "a",
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	finished := now.Add(2 * time.Minute)
	job.Status = source.JobStatusCompleted
	job.CursorAfter = "b"
	job.Created = 1
	job.FinishedAt = &finished
	if err := store.FinishJob(ctx, job); err != nil {
		t.Fatalf("FinishJob: %v", err)
	}
	gotJob, err := store.GetJob(ctx, job.ID)
	if err != nil || gotJob.Status != source.JobStatusCompleted || gotJob.Created != 1 || gotJob.FinishedAt == nil {
		t.Fatalf("GetJob: got=%+v err=%v", gotJob, err)
	}
}
