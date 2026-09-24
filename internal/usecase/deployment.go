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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/audit"
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
	deploymentSlugPattern  = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
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
	Audit     audit.Recorder
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
	recordAudit(ctx, u.Audit, audit.ActionDeploymentCreate, audit.OutcomeSuccess, "deployment:"+d.ID, map[string]string{
		"slug": d.Slug, "knowledge_base_id": d.KnowledgeBaseID, "alias": d.Alias,
		"prompt_version": strconv.Itoa(d.PromptVersion), "top_k": strconv.Itoa(d.TopK), "rerank": strconv.FormatBool(d.Rerank),
	})
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
	recordAudit(ctx, u.Audit, audit.ActionRuntimeTokenIssue, audit.OutcomeSuccess, "deployment:"+deploymentID, map[string]string{
		"token_id": t.ID, "token_name": t.Name, "token_fingerprint": audit.Fingerprint(secret),
	})
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
	if err := u.Store.RevokeToken(ctx, tokenID, u.Now()); err != nil {
		return err
	}
	recordAudit(ctx, u.Audit, audit.ActionRuntimeTokenRevoke, audit.OutcomeSuccess, "deployment:"+deploymentID, map[string]string{"token_id": tokenID})
	return nil
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
	Audit   audit.Recorder
}

func NewRuntimeUseCase(store deployment.Store, search *SearchUseCase, ragChat *RAGChatUseCase) *RuntimeUseCase {
	return &RuntimeUseCase{Store: store, Search: search, RAGChat: ragChat}
}

// Authenticate resolves a Bearer token for a deployment slug. Rejections
// are audited with a token fingerprint (never the token); successful
// requests are not, since Runtime traffic would flood the trail.
func (u *RuntimeUseCase) Authenticate(ctx context.Context, slug, rawToken string) (*deployment.Deployment, *deployment.Token, error) {
	reject := func(reason string) (*deployment.Deployment, *deployment.Token, error) {
		meta := map[string]string{"reason": reason}
		if rawToken != "" {
			meta["token_fingerprint"] = audit.Fingerprint(rawToken)
		}
		recordAudit(ctx, u.Audit, audit.ActionRuntimeAuthRejected, audit.OutcomeDenied, "deployment_slug:"+truncateForAudit(slug), meta)
		return nil, nil, ErrUnauthorizedRuntime
	}
	if strings.TrimSpace(rawToken) == "" {
		return reject("missing_token")
	}
	d, err := u.Store.GetDeploymentBySlug(ctx, slug)
	if err != nil {
		return reject("unknown_deployment")
	}
	t, err := u.Store.GetTokenByHash(ctx, hashRuntimeToken(rawToken))
	switch {
	case err != nil:
		return reject("unknown_token")
	case !t.Active():
		return reject("revoked_token")
	case t.DeploymentID != d.ID:
		return reject("wrong_deployment")
	}
	return d, t, nil
}

// truncateForAudit bounds caller-supplied strings stored in audit rows.
func truncateForAudit(s string) string {
	if len(s) > 128 {
		return s[:128]
	}
	return s
}

func (u *RuntimeUseCase) SearchDeployment(ctx context.Context, d *deployment.Deployment, query string) ([]retrieval.Result, error) {
	return u.Search.Search(ctx, d.KnowledgeBaseID, query, retrieval.Options{TopK: d.TopK, Rerank: d.Rerank})
}

func (u *RuntimeUseCase) ChatDeployment(ctx context.Context, d *deployment.Deployment, query string) (*RAGStreamResult, error) {
	return u.RAGChat.ChatStreamConfigured(ctx, d.KnowledgeBaseID, d.Alias, query, d.TopK, d.Rerank, d.PromptContent)
}
