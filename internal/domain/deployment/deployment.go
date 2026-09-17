// Package deployment defines immutable RAG runtime snapshots and their
// bearer tokens. A Deployment freezes the configuration that was evaluated
// before publication so later Prompt Registry changes cannot silently alter
// a running application.
package deployment

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("deployment not found")

type Deployment struct {
	ID              string
	Slug            string
	KnowledgeBaseID string
	PromptName      string
	PromptVersion   int
	PromptContent   string
	Alias           string
	TopK            int
	Rerank          bool
	CreatedAt       time.Time
}

type Token struct {
	ID           string
	DeploymentID string
	Name         string
	Prefix       string
	Hash         string
	CreatedAt    time.Time
	RevokedAt    *time.Time
}

func (t Token) Active() bool { return t.RevokedAt == nil }

type Store interface {
	CreateDeployment(ctx context.Context, d Deployment) error
	GetDeployment(ctx context.Context, id string) (*Deployment, error)
	GetDeploymentBySlug(ctx context.Context, slug string) (*Deployment, error)
	ListDeployments(ctx context.Context) ([]Deployment, error)

	CreateToken(ctx context.Context, t Token) error
	GetTokenByHash(ctx context.Context, hash string) (*Token, error)
	ListTokens(ctx context.Context, deploymentID string) ([]Token, error)
	RevokeToken(ctx context.Context, id string, revokedAt time.Time) error
}
