package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/deployment"
)

type DeploymentStore struct {
	db *sql.DB
}

func NewDeploymentStore(db *sql.DB) *DeploymentStore { return &DeploymentStore{db: db} }

var _ deployment.Store = (*DeploymentStore)(nil)

func (s *DeploymentStore) CreateDeployment(ctx context.Context, d deployment.Deployment) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deployments
		(id, slug, knowledge_base_id, prompt_name, prompt_version, prompt_content, alias, top_k, rerank, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, d.ID, d.Slug, d.KnowledgeBaseID, d.PromptName, d.PromptVersion, d.PromptContent,
		d.Alias, d.TopK, boolInt(d.Rerank), d.CreatedAt.Format(timeLayout))
	if err != nil {
		return fmt.Errorf("creating deployment %s: %w", d.Slug, err)
	}
	return nil
}

func (s *DeploymentStore) GetDeployment(ctx context.Context, id string) (*deployment.Deployment, error) {
	return s.scanDeployment(s.db.QueryRowContext(ctx, `
		SELECT id, slug, knowledge_base_id, prompt_name, prompt_version, prompt_content, alias, top_k, rerank, created_at
		FROM deployments WHERE id = ?
	`, id))
}

func (s *DeploymentStore) GetDeploymentBySlug(ctx context.Context, slug string) (*deployment.Deployment, error) {
	return s.scanDeployment(s.db.QueryRowContext(ctx, `
		SELECT id, slug, knowledge_base_id, prompt_name, prompt_version, prompt_content, alias, top_k, rerank, created_at
		FROM deployments WHERE slug = ?
	`, slug))
}

func (s *DeploymentStore) scanDeployment(row *sql.Row) (*deployment.Deployment, error) {
	var d deployment.Deployment
	var rerank int
	var createdAt string
	if err := row.Scan(&d.ID, &d.Slug, &d.KnowledgeBaseID, &d.PromptName, &d.PromptVersion,
		&d.PromptContent, &d.Alias, &d.TopK, &rerank, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, deployment.ErrNotFound
		}
		return nil, fmt.Errorf("scanning deployment: %w", err)
	}
	d.Rerank = rerank != 0
	d.CreatedAt, _ = time.Parse(timeLayout, createdAt)
	return &d, nil
}

func (s *DeploymentStore) ListDeployments(ctx context.Context) ([]deployment.Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, slug, knowledge_base_id, prompt_name, prompt_version, prompt_content, alias, top_k, rerank, created_at
		FROM deployments ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing deployments: %w", err)
	}
	defer rows.Close()

	var out []deployment.Deployment
	for rows.Next() {
		var d deployment.Deployment
		var rerank int
		var createdAt string
		if err := rows.Scan(&d.ID, &d.Slug, &d.KnowledgeBaseID, &d.PromptName, &d.PromptVersion,
			&d.PromptContent, &d.Alias, &d.TopK, &rerank, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning deployment: %w", err)
		}
		d.Rerank = rerank != 0
		d.CreatedAt, _ = time.Parse(timeLayout, createdAt)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *DeploymentStore) CreateToken(ctx context.Context, t deployment.Token) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runtime_api_tokens
		(id, deployment_id, name, token_prefix, token_hash, created_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL)
	`, t.ID, t.DeploymentID, t.Name, t.Prefix, t.Hash, t.CreatedAt.Format(timeLayout))
	if err != nil {
		return fmt.Errorf("creating runtime token: %w", err)
	}
	return nil
}

func (s *DeploymentStore) GetTokenByHash(ctx context.Context, hash string) (*deployment.Token, error) {
	var t deployment.Token
	var createdAt string
	var revokedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, deployment_id, name, token_prefix, token_hash, created_at, revoked_at
		FROM runtime_api_tokens WHERE token_hash = ?
	`, hash).Scan(&t.ID, &t.DeploymentID, &t.Name, &t.Prefix, &t.Hash, &createdAt, &revokedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, deployment.ErrNotFound
		}
		return nil, fmt.Errorf("loading runtime token: %w", err)
	}
	t.CreatedAt, _ = time.Parse(timeLayout, createdAt)
	if revokedAt.Valid {
		v, _ := time.Parse(timeLayout, revokedAt.String)
		t.RevokedAt = &v
	}
	return &t, nil
}

func (s *DeploymentStore) ListTokens(ctx context.Context, deploymentID string) ([]deployment.Token, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, deployment_id, name, token_prefix, token_hash, created_at, revoked_at
		FROM runtime_api_tokens WHERE deployment_id = ? ORDER BY created_at DESC
	`, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("listing runtime tokens: %w", err)
	}
	defer rows.Close()

	var out []deployment.Token
	for rows.Next() {
		var t deployment.Token
		var createdAt string
		var revokedAt sql.NullString
		if err := rows.Scan(&t.ID, &t.DeploymentID, &t.Name, &t.Prefix, &t.Hash, &createdAt, &revokedAt); err != nil {
			return nil, fmt.Errorf("scanning runtime token: %w", err)
		}
		t.CreatedAt, _ = time.Parse(timeLayout, createdAt)
		if revokedAt.Valid {
			v, _ := time.Parse(timeLayout, revokedAt.String)
			t.RevokedAt = &v
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *DeploymentStore) RevokeToken(ctx context.Context, id string, revokedAt time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE runtime_api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		revokedAt.Format(timeLayout), id,
	)
	if err != nil {
		return fmt.Errorf("revoking runtime token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking revoked token: %w", err)
	}
	if n == 0 {
		return deployment.ErrNotFound
	}
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
