package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/deployment"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/prompt"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
)

const (
	defaultDeploymentTopK = 5
	maxDeploymentTopK     = 50
	runtimeTokenBytes     = 32
)

var (
	deploymentSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	ErrUnauthorizedRuntime = errors.New("unauthorized runtime token")
)

type CreateDeploymentInput struct {
	Slug            string
	KnowledgeBaseID string
	Alias           string
	TopK            int
	Rerank          bool
}

type IssuedRuntimeToken struct {
	Token  deployment.Token
	Secret string
}

type DeploymentUseCase struct {
	Store     deployment.Store
	Knowledge knowledge.Store
	Prompts   prompt.Store
	Router    *llm.Router
	Now       func() time.Time
	NewID     func() string
}

func NewDeploymentUseCase(store deployment.Store, knowledge knowledge.Store, prompts prompt.Store, router *llm.Router) *DeploymentUseCase {
	return &DeploymentUseCase{
		Store: store, Knowledge: knowledge, Prompts: prompts, Router: router,
		Now: time.Now, NewID: uuid.NewString,
	}
}

func (u *DeploymentUseCase) Create(ctx context.Context, in CreateDeploymentInput) (*deployment.Deployment, error) {
	slug := strings.TrimSpace(strings.ToLower(in.Slug))
	if !deploymentSlugPattern.MatchString(slug) || len(slug) > 128 {
		return nil, fmt.Errorf("invalid deployment slug")
	}
	if strings.TrimSpace(in.KnowledgeBaseID) == "" {
		return nil, fmt.Errorf("knowledge_base_id must not be empty")
	}
	if _, err := u.Knowledge.GetKnowledgeBase(ctx, in.KnowledgeBaseID); err != nil {
		return nil, fmt.Errorf("loading knowledge base: %w", err)
	}

	alias := strings.TrimSpace(in.Alias)
	if alias == "" {
		alias = "normal"
	}
	if u.Router != nil {
		if _, _, err := u.Router.Resolve(alias); err != nil {
			return nil, fmt.Errorf("invalid model alias %q: %w", alias, err)
		}
	}

	topK := in.TopK
	if topK == 0 {
		topK = defaultDeploymentTopK
	}
	if topK < 1 || topK > maxDeploymentTopK {
		return nil, fmt.Errorf("top_k must be between 1 and %d", maxDeploymentTopK)
	}

	p, err := u.Prompts.GetPromptByName(ctx, RAGPromptName)
	if err != nil {
		return nil, fmt.Errorf("loading %s prompt: %w", RAGPromptName, err)
	}
	v, err := u.Prompts.GetActiveVersion(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("loading active %s prompt: %w", RAGPromptName, err)
	}

	d := deployment.Deployment{
		ID: u.NewID(), Slug: slug, KnowledgeBaseID: in.KnowledgeBaseID,
		PromptName: RAGPromptName, PromptVersion: v.Version, PromptContent: v.Content,
		Alias: alias, TopK: topK, Rerank: in.Rerank, CreatedAt: u.Now(),
	}
	if err := u.Store.CreateDeployment(ctx, d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (u *DeploymentUseCase) List(ctx context.Context) ([]deployment.Deployment, error) {
	return u.Store.ListDeployments(ctx)
}

func (u *DeploymentUseCase) IssueToken(ctx context.Context, deploymentID, name string) (*IssuedRuntimeToken, error) {
	if _, err := u.Store.GetDeployment(ctx, deploymentID); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	if len(name) > 128 {
		return nil, fmt.Errorf("token name too long")
	}

	secret, err := newRuntimeToken()
	if err != nil {
		return nil, err
	}
	hash := hashRuntimeToken(secret)
	prefix := secret
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	t := deployment.Token{
		ID: u.NewID(), DeploymentID: deploymentID, Name: name,
		Prefix: prefix, Hash: hash, CreatedAt: u.Now(),
	}
	if err := u.Store.CreateToken(ctx, t); err != nil {
		return nil, err
	}
	return &IssuedRuntimeToken{Token: t, Secret: secret}, nil
}

func (u *DeploymentUseCase) ListTokens(ctx context.Context, deploymentID string) ([]deployment.Token, error) {
	if _, err := u.Store.GetDeployment(ctx, deploymentID); err != nil {
		return nil, err
	}
	return u.Store.ListTokens(ctx, deploymentID)
}

func (u *DeploymentUseCase) RevokeToken(ctx context.Context, deploymentID, tokenID string) error {
	tokens, err := u.Store.ListTokens(ctx, deploymentID)
	if err != nil {
		return err
	}
	found := false
	for _, t := range tokens {
		if t.ID == tokenID {
			found = true
			break
		}
	}
	if !found {
		return deployment.ErrNotFound
	}
	return u.Store.RevokeToken(ctx, tokenID, u.Now())
}

func newRuntimeToken() (string, error) {
	buf := make([]byte, runtimeTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating runtime token: %w", err)
	}
	return "fai_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashRuntimeToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type RuntimeUseCase struct {
	Store   deployment.Store
	Search  *SearchUseCase
	RAGChat *RAGChatUseCase
}

func NewRuntimeUseCase(store deployment.Store, search *SearchUseCase, ragChat *RAGChatUseCase) *RuntimeUseCase {
	return &RuntimeUseCase{Store: store, Search: search, RAGChat: ragChat}
}

func (u *RuntimeUseCase) Authenticate(ctx context.Context, slug, rawToken string) (*deployment.Deployment, *deployment.Token, error) {
	if strings.TrimSpace(rawToken) == "" {
		return nil, nil, ErrUnauthorizedRuntime
	}
	d, err := u.Store.GetDeploymentBySlug(ctx, slug)
	if err != nil {
		return nil, nil, ErrUnauthorizedRuntime
	}
	t, err := u.Store.GetTokenByHash(ctx, hashRuntimeToken(rawToken))
	if err != nil || !t.Active() || t.DeploymentID != d.ID {
		return nil, nil, ErrUnauthorizedRuntime
	}
	return d, t, nil
}

func (u *RuntimeUseCase) SearchDeployment(ctx context.Context, d *deployment.Deployment, query string) ([]retrieval.Result, error) {
	return u.Search.Search(ctx, d.KnowledgeBaseID, query, retrieval.Options{TopK: d.TopK, Rerank: d.Rerank})
}

func (u *RuntimeUseCase) ChatDeployment(ctx context.Context, d *deployment.Deployment, query string) (*RAGStreamResult, error) {
	return u.RAGChat.ChatStreamConfigured(ctx, d.KnowledgeBaseID, d.Alias, query, d.TopK, d.Rerank, d.PromptContent)
}
