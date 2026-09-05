package app

import (
	"context"
	"fmt"

	"github.com/sibukixxx/rag-poc/internal/domain/prompt"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// seedDefaultPrompts ensures the RAG pipeline's prompt exists in the
// registry with a v1 matching its historical hardcoded content, so a
// fresh install behaves exactly as it did before the registry existed
// and an operator can immediately add v2 and switch to it
// (docs/ROADMAP.md W6).
func seedDefaultPrompts(ctx context.Context, store prompt.Store) error {
	p, err := store.EnsurePrompt(ctx, usecase.RAGPromptName)
	if err != nil {
		return fmt.Errorf("ensuring prompt %s: %w", usecase.RAGPromptName, err)
	}

	versions, err := store.ListVersions(ctx, p.ID)
	if err != nil {
		return fmt.Errorf("listing versions for prompt %s: %w", usecase.RAGPromptName, err)
	}
	if len(versions) == 0 {
		if _, err := store.CreateVersion(ctx, p.ID, usecase.DefaultRAGSystemPrompt); err != nil {
			return fmt.Errorf("seeding v1 for prompt %s: %w", usecase.RAGPromptName, err)
		}
	}
	return nil
}
