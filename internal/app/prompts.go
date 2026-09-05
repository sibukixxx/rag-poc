package app

import (
	"context"
	"fmt"

	"github.com/sibukixxx/rag-poc/internal/domain/prompt"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// seedDefaultPrompts ensures each pipeline prompt exists in the registry
// with a v1 matching its historical hardcoded content, so a fresh install
// behaves exactly as it did before the registry existed and an operator
// can immediately add v2 and switch to it (docs/ROADMAP.md W6). W8 adds
// the LLM Judge's prompt to the same mechanism (docs/DESIGN_REVIEW.md F-11).
func seedDefaultPrompts(ctx context.Context, store prompt.Store) error {
	seeds := []struct{ name, content string }{
		{usecase.RAGPromptName, usecase.DefaultRAGSystemPrompt},
		{usecase.JudgePromptName, usecase.DefaultJudgePrompt},
	}
	for _, s := range seeds {
		if err := seedPrompt(ctx, store, s.name, s.content); err != nil {
			return err
		}
	}
	return nil
}

func seedPrompt(ctx context.Context, store prompt.Store, name, content string) error {
	p, err := store.EnsurePrompt(ctx, name)
	if err != nil {
		return fmt.Errorf("ensuring prompt %s: %w", name, err)
	}
	versions, err := store.ListVersions(ctx, p.ID)
	if err != nil {
		return fmt.Errorf("listing versions for prompt %s: %w", name, err)
	}
	if len(versions) == 0 {
		if _, err := store.CreateVersion(ctx, p.ID, content); err != nil {
			return fmt.Errorf("seeding v1 for prompt %s: %w", name, err)
		}
	}
	return nil
}
