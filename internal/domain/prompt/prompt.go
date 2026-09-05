// Package prompt implements the Prompt Registry: named prompts with
// sequentially versioned content and one "active" version that the RAG
// pipeline reads at request time. Editing a prompt's behavior — e.g. its
// citation instructions — becomes "add a version, activate it" rather
// than a code change and redeploy (docs/ROADMAP.md W6).
package prompt

import (
	"context"
	"time"
)

type Prompt struct {
	ID            string
	Name          string
	ActiveVersion int
	CreatedAt     time.Time
}

type Version struct {
	ID        string
	PromptID  string
	Version   int
	Content   string
	CreatedAt time.Time
}

// Store persists prompts and their versions.
type Store interface {
	// EnsurePrompt returns the prompt with the given name, creating it
	// (with no versions yet) if it doesn't exist.
	EnsurePrompt(ctx context.Context, name string) (*Prompt, error)
	GetPrompt(ctx context.Context, id string) (*Prompt, error)
	GetPromptByName(ctx context.Context, name string) (*Prompt, error)
	ListPrompts(ctx context.Context) ([]Prompt, error)

	// CreateVersion appends a new version (auto-incrementing from the
	// prompt's current highest version, starting at 1) and returns it.
	CreateVersion(ctx context.Context, promptID, content string) (*Version, error)
	ListVersions(ctx context.Context, promptID string) ([]Version, error)

	// SetActiveVersion switches which version GetActiveVersion returns.
	// It errors if the version doesn't exist for this prompt.
	SetActiveVersion(ctx context.Context, promptID string, version int) error
	GetActiveVersion(ctx context.Context, promptID string) (*Version, error)
}
