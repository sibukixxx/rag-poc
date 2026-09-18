package usecase_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

func TestDeploymentFreezesPromptAndRuntimeTokenScope(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	knowledge := sqlite.NewKnowledgeStore(db)
	kb, err := knowledge.EnsureKnowledgeBase(ctx, "Support", "support")
	if err != nil {
		t.Fatal(err)
	}
	prompts := sqlite.NewPromptStore(db)
	p, err := prompts.EnsurePrompt(ctx, usecase.RAGPromptName)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := prompts.CreateVersion(ctx, p.ID, "PROMPT_V1")
	if err != nil {
		t.Fatal(err)
	}

	store := sqlite.NewDeploymentStore(db)
	deployUC := usecase.NewDeploymentUseCase(store, knowledge, prompts, nil)
	d, err := deployUC.Create(ctx, usecase.CreateDeploymentInput{
		Slug: "support-prod", KnowledgeBaseID: kb.ID, Alias: "normal", TopK: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.PromptVersion != v1.Version || d.PromptContent != "PROMPT_V1" {
		t.Fatalf("deployment did not snapshot v1: %#v", d)
	}

	v2, err := prompts.CreateVersion(ctx, p.ID, "PROMPT_V2")
	if err != nil {
		t.Fatal(err)
	}
	if err := prompts.SetActiveVersion(ctx, p.ID, v2.Version); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetDeployment(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PromptVersion != v1.Version || loaded.PromptContent != "PROMPT_V1" {
		t.Fatalf("deployment changed after prompt activation: %#v", loaded)
	}

	issued, err := deployUC.IssueToken(ctx, d.ID, "integration")
	if err != nil {
		t.Fatal(err)
	}
	if issued.Secret == "" || issued.Token.Hash == "" || issued.Token.Hash == issued.Secret {
		t.Fatalf("runtime token was not safely hashed: %#v", issued.Token)
	}

	runtimeUC := usecase.NewRuntimeUseCase(store, nil, nil)
	authedDeployment, authedToken, err := runtimeUC.Authenticate(ctx, d.Slug, issued.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if authedDeployment.ID != d.ID || authedToken.ID != issued.Token.ID {
		t.Fatalf("unexpected auth result: %#v %#v", authedDeployment, authedToken)
	}
	if _, _, err := runtimeUC.Authenticate(ctx, "other-app", issued.Secret); !errors.Is(err, usecase.ErrUnauthorizedRuntime) {
		t.Fatalf("wrong deployment auth error = %v", err)
	}
	if err := deployUC.RevokeToken(ctx, d.ID, issued.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtimeUC.Authenticate(ctx, d.Slug, issued.Secret); !errors.Is(err, usecase.ErrUnauthorizedRuntime) {
		t.Fatalf("revoked token auth error = %v", err)
	}
}
