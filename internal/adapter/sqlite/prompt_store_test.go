package sqlite_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
)

func openPromptStore(t *testing.T) *sqlite.PromptStore {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "forgeai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return sqlite.NewPromptStore(db)
}

func TestEnsurePromptIsIdempotent(t *testing.T) {
	store := openPromptStore(t)
	ctx := t.Context()

	a, err := store.EnsurePrompt(ctx, "rag_system")
	if err != nil {
		t.Fatalf("EnsurePrompt: %v", err)
	}
	b, err := store.EnsurePrompt(ctx, "rag_system")
	if err != nil {
		t.Fatalf("EnsurePrompt (second call): %v", err)
	}
	if a.ID != b.ID {
		t.Errorf("expected the same prompt id on repeat call, got %s vs %s", a.ID, b.ID)
	}
	if a.ActiveVersion != 0 {
		t.Errorf("expected a freshly created prompt to have no active version yet, got %d", a.ActiveVersion)
	}

	list, err := store.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected exactly 1 prompt, got %d", len(list))
	}
}

func TestFirstVersionBecomesActiveAutomatically(t *testing.T) {
	store := openPromptStore(t)
	ctx := t.Context()

	p, err := store.EnsurePrompt(ctx, "rag_system")
	if err != nil {
		t.Fatalf("EnsurePrompt: %v", err)
	}

	v1, err := store.CreateVersion(ctx, p.ID, "You are a helpful assistant. v1")
	if err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	if v1.Version != 1 {
		t.Errorf("expected first version to be numbered 1, got %d", v1.Version)
	}

	active, err := store.GetActiveVersion(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetActiveVersion: %v", err)
	}
	if active.Version != 1 || active.Content != v1.Content {
		t.Errorf("expected v1 to be active automatically, got %+v", active)
	}
}

func TestSecondVersionRequiresExplicitActivation(t *testing.T) {
	store := openPromptStore(t)
	ctx := t.Context()

	p, _ := store.EnsurePrompt(ctx, "rag_system")
	if _, err := store.CreateVersion(ctx, p.ID, "v1 content"); err != nil {
		t.Fatalf("CreateVersion v1: %v", err)
	}
	v2, err := store.CreateVersion(ctx, p.ID, "v2 content")
	if err != nil {
		t.Fatalf("CreateVersion v2: %v", err)
	}
	if v2.Version != 2 {
		t.Fatalf("expected second version numbered 2, got %d", v2.Version)
	}

	// v1 should still be active until explicitly switched.
	active, err := store.GetActiveVersion(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetActiveVersion: %v", err)
	}
	if active.Version != 1 {
		t.Fatalf("expected v1 to still be active, got v%d", active.Version)
	}

	// This is the W6 acceptance criterion: switching to v2 changes what
	// GetActiveVersion (and therefore the RAG pipeline) sees.
	if err := store.SetActiveVersion(ctx, p.ID, 2); err != nil {
		t.Fatalf("SetActiveVersion: %v", err)
	}
	active, err = store.GetActiveVersion(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetActiveVersion after switch: %v", err)
	}
	if active.Version != 2 || active.Content != "v2 content" {
		t.Fatalf("expected v2 to be active after switching, got %+v", active)
	}
}

func TestSetActiveVersionRejectsUnknownVersion(t *testing.T) {
	store := openPromptStore(t)
	ctx := t.Context()
	p, _ := store.EnsurePrompt(ctx, "rag_system")
	_, _ = store.CreateVersion(ctx, p.ID, "v1")

	if err := store.SetActiveVersion(ctx, p.ID, 99); err == nil {
		t.Fatal("expected an error for a non-existent version")
	}
}

func TestListVersionsOrdered(t *testing.T) {
	store := openPromptStore(t)
	ctx := t.Context()
	p, _ := store.EnsurePrompt(ctx, "rag_system")
	for _, content := range []string{"v1", "v2", "v3"} {
		if _, err := store.CreateVersion(ctx, p.ID, content); err != nil {
			t.Fatalf("CreateVersion(%s): %v", content, err)
		}
	}

	versions, err := store.ListVersions(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	for i, v := range versions {
		if v.Version != i+1 {
			t.Errorf("expected versions in ascending order, got %+v at index %d", v, i)
		}
	}
}

func TestGetActiveVersionErrorsWithoutAnyVersion(t *testing.T) {
	store := openPromptStore(t)
	ctx := t.Context()
	p, _ := store.EnsurePrompt(ctx, "rag_system")

	if _, err := store.GetActiveVersion(ctx, p.ID); err == nil || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows for a prompt with no versions, got %v", err)
	}
}
