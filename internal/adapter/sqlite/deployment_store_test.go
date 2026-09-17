package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/deployment"
)

func TestDeploymentStoreRoundTripAndTokenRevocation(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	kb, err := NewKnowledgeStore(db).EnsureKnowledgeBase(ctx, "Runtime KB", "runtime-kb")
	if err != nil {
		t.Fatal(err)
	}
	store := NewDeploymentStore(db)
	now := time.Now().UTC().Truncate(time.Second)
	d := deployment.Deployment{
		ID: "dep-1", Slug: "support-prod", KnowledgeBaseID: kb.ID,
		PromptName: "rag_system", PromptVersion: 3, PromptContent: "frozen prompt",
		Alias: "normal", TopK: 7, Rerank: true, CreatedAt: now,
	}
	if err := store.CreateDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetDeploymentBySlug(ctx, d.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.PromptVersion != 3 || got.PromptContent != "frozen prompt" || !got.Rerank || got.TopK != 7 {
		t.Fatalf("deployment round trip = %#v", got)
	}

	tok := deployment.Token{
		ID: "tok-1", DeploymentID: d.ID, Name: "client", Prefix: "fai_example", Hash: "hash-1", CreatedAt: now,
	}
	if err := store.CreateToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetTokenByHash(ctx, tok.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Active() || loaded.Hash != tok.Hash || loaded.DeploymentID != d.ID {
		t.Fatalf("token round trip = %#v", loaded)
	}

	revokedAt := now.Add(time.Minute)
	if err := store.RevokeToken(ctx, tok.ID, revokedAt); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetTokenByHash(ctx, tok.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Active() || loaded.RevokedAt == nil {
		t.Fatalf("expected revoked token, got %#v", loaded)
	}
	if err := store.RevokeToken(ctx, "missing", revokedAt); !errors.Is(err, deployment.ErrNotFound) {
		t.Fatalf("missing revoke error = %v", err)
	}
}
