package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/deployment"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

func setupLifecycleHTTPTest(t *testing.T) (http.Handler, string) {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	ks := sqlite.NewKnowledgeStore(db)
	kb, err := ks.EnsureKnowledgeBase(ctx, "Customer", "customer")
	if err != nil {
		t.Fatal(err)
	}
	if err := ks.CreateDocument(ctx, knowledge.Document{ID: "doc-1", KnowledgeBaseID: kb.ID, Filename: "a.md", Status: knowledge.DocumentStatusReady, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := ks.ReplaceChunks(ctx, "doc-1", []knowledge.Chunk{{ID: "c1", DocumentID: "doc-1", Text: "t", TokenCount: 1, Hash: "h"}}); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.NewDeploymentStore(db).CreateDeployment(ctx, deployment.Deployment{
		ID: "dep-1", Slug: "app", KnowledgeBaseID: kb.ID, PromptName: "rag_system", PromptVersion: 1,
		PromptContent: "p", Alias: "normal", TopK: 3, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	h := NewLifecycleHandler(usecase.NewDataLifecycleUseCase(sqlite.NewLifecycleStore(db), usecase.RetentionPolicy{}))
	r := chi.NewRouter()
	r.Delete("/knowledge-bases/{id}", h.DeleteKnowledgeBase)
	r.Delete("/knowledge-bases/{id}/documents/{docID}", h.DeleteDocument)
	return r, kb.ID
}

func serveDelete(h http.Handler, path string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, path, nil))
	return rr
}

func TestLifecycleHTTPDeleteDocumentReturnsDeletionReport(t *testing.T) {
	h, kbID := setupLifecycleHTTPTest(t)

	rr := serveDelete(h, "/knowledge-bases/"+kbID+"/documents/doc-1")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	want := `{"knowledge_bases":0,"documents":1,"chunks":1,"embeddings":0,"fts_rows":1,"source_connections":0,"source_items":0,"datasets":0,"evaluation_runs":0,"deployments":0}`
	if got := strings.TrimSpace(rr.Body.String()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestLifecycleHTTPDeleteKnowledgeBaseWithDeploymentsRequiresExplicitOptIn(t *testing.T) {
	h, kbID := setupLifecycleHTTPTest(t)

	refused := serveDelete(h, "/knowledge-bases/"+kbID)
	if refused.Code != http.StatusConflict {
		t.Fatalf("status without opt-in = %d, want 409; body=%s", refused.Code, refused.Body.String())
	}
	if got := strings.TrimSpace(refused.Body.String()); got != "knowledge base has deployments; retry with include_deployments=true to delete them too" {
		t.Fatalf("409 body = %q", got)
	}

	deleted := serveDelete(h, "/knowledge-bases/"+kbID+"?include_deployments=true")
	if deleted.Code != http.StatusOK {
		t.Fatalf("status with opt-in = %d body=%s", deleted.Code, deleted.Body.String())
	}
	if !strings.Contains(deleted.Body.String(), `"knowledge_bases":1`) || !strings.Contains(deleted.Body.String(), `"deployments":1`) {
		t.Fatalf("deletion report = %s, want the knowledge base and its deployment", deleted.Body.String())
	}
}
