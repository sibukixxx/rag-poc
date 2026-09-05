package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/prompt"
)

type PromptStore struct {
	db *sql.DB
}

func NewPromptStore(db *sql.DB) *PromptStore {
	return &PromptStore{db: db}
}

var _ prompt.Store = (*PromptStore)(nil)

func (s *PromptStore) EnsurePrompt(ctx context.Context, name string) (*prompt.Prompt, error) {
	if p, err := s.GetPromptByName(ctx, name); err == nil {
		return p, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	p := prompt.Prompt{ID: uuid.NewString(), Name: name, ActiveVersion: 0, CreatedAt: time.Now()}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO prompts (id, name, active_version, created_at) VALUES (?, ?, ?, ?)`,
		p.ID, p.Name, p.ActiveVersion, p.CreatedAt.Format(timeLayout),
	)
	if err != nil {
		return nil, fmt.Errorf("creating prompt %s: %w", name, err)
	}
	return &p, nil
}

func (s *PromptStore) GetPrompt(ctx context.Context, id string) (*prompt.Prompt, error) {
	return s.scanPromptRow(s.db.QueryRowContext(ctx,
		`SELECT id, name, active_version, created_at FROM prompts WHERE id = ?`, id))
}

func (s *PromptStore) GetPromptByName(ctx context.Context, name string) (*prompt.Prompt, error) {
	return s.scanPromptRow(s.db.QueryRowContext(ctx,
		`SELECT id, name, active_version, created_at FROM prompts WHERE name = ?`, name))
}

func (s *PromptStore) scanPromptRow(row *sql.Row) (*prompt.Prompt, error) {
	var p prompt.Prompt
	var createdAt string
	err := row.Scan(&p.ID, &p.Name, &p.ActiveVersion, &createdAt)
	if err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(timeLayout, createdAt)
	return &p, nil
}

func (s *PromptStore) ListPrompts(ctx context.Context) ([]prompt.Prompt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, active_version, created_at FROM prompts ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing prompts: %w", err)
	}
	defer rows.Close()

	var prompts []prompt.Prompt
	for rows.Next() {
		var p prompt.Prompt
		var createdAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.ActiveVersion, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning prompt: %w", err)
		}
		p.CreatedAt, _ = time.Parse(timeLayout, createdAt)
		prompts = append(prompts, p)
	}
	return prompts, rows.Err()
}

// CreateVersion and the active-version bump for a prompt's first version
// both run in one transaction, so EnsurePrompt+CreateVersion always
// leaves a usable (non-zero) active_version rather than requiring a
// separate SetActiveVersion call for the common case.
func (s *PromptStore) CreateVersion(ctx context.Context, promptID, content string) (*prompt.Version, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning CreateVersion transaction: %w", err)
	}
	defer tx.Rollback()

	var nextVersion int
	var currentActive int
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM prompt_versions WHERE prompt_id = ?`, promptID,
	).Scan(&nextVersion)
	if err != nil {
		return nil, fmt.Errorf("finding next version for prompt %s: %w", promptID, err)
	}
	nextVersion++

	if err := tx.QueryRowContext(ctx, `SELECT active_version FROM prompts WHERE id = ?`, promptID).Scan(&currentActive); err != nil {
		return nil, fmt.Errorf("loading prompt %s: %w", promptID, err)
	}

	v := prompt.Version{ID: uuid.NewString(), PromptID: promptID, Version: nextVersion, Content: content, CreatedAt: time.Now()}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO prompt_versions (id, prompt_id, version, content, created_at) VALUES (?, ?, ?, ?, ?)`,
		v.ID, v.PromptID, v.Version, v.Content, v.CreatedAt.Format(timeLayout),
	)
	if err != nil {
		return nil, fmt.Errorf("inserting prompt version: %w", err)
	}

	// The first version created for a prompt becomes active automatically;
	// later versions require an explicit SetActiveVersion call.
	if currentActive == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE prompts SET active_version = ? WHERE id = ?`, nextVersion, promptID); err != nil {
			return nil, fmt.Errorf("activating first version: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing CreateVersion: %w", err)
	}
	return &v, nil
}

func (s *PromptStore) ListVersions(ctx context.Context, promptID string) ([]prompt.Version, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, prompt_id, version, content, created_at
		FROM prompt_versions WHERE prompt_id = ? ORDER BY version ASC
	`, promptID)
	if err != nil {
		return nil, fmt.Errorf("listing versions for prompt %s: %w", promptID, err)
	}
	defer rows.Close()

	var versions []prompt.Version
	for rows.Next() {
		var v prompt.Version
		var createdAt string
		if err := rows.Scan(&v.ID, &v.PromptID, &v.Version, &v.Content, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning prompt version: %w", err)
		}
		v.CreatedAt, _ = time.Parse(timeLayout, createdAt)
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func (s *PromptStore) SetActiveVersion(ctx context.Context, promptID string, version int) error {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM prompt_versions WHERE prompt_id = ? AND version = ?`, promptID, version,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking version %d exists for prompt %s: %w", version, promptID, err)
	}
	if exists == 0 {
		return fmt.Errorf("prompt %s has no version %d", promptID, version)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE prompts SET active_version = ? WHERE id = ?`, version, promptID); err != nil {
		return fmt.Errorf("activating version %d for prompt %s: %w", version, promptID, err)
	}
	return nil
}

func (s *PromptStore) GetActiveVersion(ctx context.Context, promptID string) (*prompt.Version, error) {
	var v prompt.Version
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT pv.id, pv.prompt_id, pv.version, pv.content, pv.created_at
		FROM prompts p
		JOIN prompt_versions pv ON pv.prompt_id = p.id AND pv.version = p.active_version
		WHERE p.id = ?
	`, promptID).Scan(&v.ID, &v.PromptID, &v.Version, &v.Content, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("prompt %s has no active version: %w", promptID, sql.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("loading active version for prompt %s: %w", promptID, err)
	}
	v.CreatedAt, _ = time.Parse(timeLayout, createdAt)
	return &v, nil
}
