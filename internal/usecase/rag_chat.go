package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/prompt"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

// defaultContextTokenBudget bounds how much retrieved text gets packed
// into the prompt, leaving headroom for the system prompt, the question,
// and the model's own output within typical context windows.
const defaultContextTokenBudget = 2000

// defaultRAGTopK is how many chunks Search contributes before the token
// budget in buildContext does its own trimming.
const defaultRAGTopK = 5

// RAGChatUseCase answers a question by retrieving context from a
// knowledge base (via SearchUseCase) and asking the LLM to answer with
// inline citations (docs/ROADMAP.md W5).
type RAGChatUseCase struct {
	Search             *SearchUseCase
	Router             *llm.Router
	Prices             llm.PriceTable
	Traces             trace.Store
	Tokenizer          *tokenizer.Tokenizer
	Prompts            prompt.Store
	ContextTokenBudget int
}

// NewRAGChatUseCase builds a RAGChatUseCase. prompts may be nil (tests,
// or a caller that doesn't need the registry) — systemPrompt then always
// falls back to DefaultRAGSystemPrompt.
func NewRAGChatUseCase(search *SearchUseCase, router *llm.Router, prices llm.PriceTable, traces trace.Store, tok *tokenizer.Tokenizer, prompts prompt.Store) *RAGChatUseCase {
	return &RAGChatUseCase{
		Search: search, Router: router, Prices: prices, Traces: traces, Tokenizer: tok, Prompts: prompts,
		ContextTokenBudget: defaultContextTokenBudget,
	}
}

// systemPrompt resolves the RAG pipeline's current instructions from the
// Prompt Registry's active version, falling back to
// DefaultRAGSystemPrompt if the registry isn't available or hasn't been
// seeded yet — this is what makes "activate v2" change behavior without
// a code change (docs/ROADMAP.md W6), while staying fail-soft.
func (u *RAGChatUseCase) systemPrompt(ctx context.Context) string {
	if u.Prompts == nil {
		return DefaultRAGSystemPrompt
	}
	p, err := u.Prompts.GetPromptByName(ctx, RAGPromptName)
	if err != nil {
		return DefaultRAGSystemPrompt
	}
	v, err := u.Prompts.GetActiveVersion(ctx, p.ID)
	if err != nil {
		return DefaultRAGSystemPrompt
	}
	return v.Content
}

// RAGStreamEvent mirrors ChatStreamEvent but carries the Citations used
// to ground the answer, populated on the terminal (Done) event once the
// full response — and therefore the final cost — is known.
type RAGStreamEvent struct {
	Delta      string
	Done       bool
	Usage      llm.Usage
	CostUSD    float64
	Citations  []ContextChunk
	NoContext  bool // true when the knowledge base had nothing to retrieve
	Err        error
}

type RAGStreamResult struct {
	Events  <-chan RAGStreamEvent
	TraceID string
}

// ChatStream retrieves context for question, then streams an answer that
// cites it. rerank controls whether the retrieval step's optional LLM
// rerank runs (off by default per docs/ROADMAP.md W5).
func (u *RAGChatUseCase) ChatStream(ctx context.Context, knowledgeBaseID, alias, question string, rerank bool) (*RAGStreamResult, error) {
	results, err := u.Search.Search(ctx, knowledgeBaseID, question, retrieval.Options{TopK: defaultRAGTopK, Rerank: rerank})
	if err != nil {
		return nil, fmt.Errorf("retrieving context: %w", err)
	}

	contextChunks, contextText := buildContext(results, u.Tokenizer, u.ContextTokenBudget)

	provider, model, err := u.Router.Resolve(alias)
	if err != nil {
		return nil, err
	}

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: u.systemPrompt(ctx)},
		{Role: llm.RoleUser, Content: buildRAGUserMessage(contextText, question)},
	}

	upstream, err := provider.Stream(ctx, llm.GenerateRequest{Model: model, Messages: messages})
	if err != nil {
		return nil, err
	}

	traceID := uuid.NewString()
	start := time.Now()
	out := make(chan RAGStreamEvent)

	go func() {
		defer close(out)

		status := trace.StatusOK
		errMsg := ""
		var usage llm.Usage
		var costUSD float64

		for ev := range upstream {
			if ev.Err != nil {
				status = trace.StatusError
				errMsg = ev.Err.Error()
				out <- RAGStreamEvent{Err: ev.Err}
				break
			}
			if ev.Delta != "" {
				out <- RAGStreamEvent{Delta: ev.Delta}
			}
			if ev.Done {
				usage = ev.Usage
				costUSD, _, _ = u.Prices.Cost(model, usage)
				out <- RAGStreamEvent{
					Done: true, Usage: usage, CostUSD: costUSD,
					Citations: contextChunks, NoContext: len(contextChunks) == 0,
				}
			}
		}

		duration := time.Since(start)
		bg := context.Background()
		_ = u.Traces.CreateTrace(bg, trace.Trace{
			ID: traceID, Name: "rag_chat:" + alias, StartedAt: start,
			DurationMS: duration.Milliseconds(), Status: status, CostUSD: costUSD,
		})
		_ = u.Traces.CreateSpan(bg, trace.Span{
			ID: uuid.NewString(), TraceID: traceID, Kind: trace.SpanKindLLM, Name: "rag_chat.stream",
			StartedAt: start, DurationMS: duration.Milliseconds(), Model: model,
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CostUSD: costUSD,
			Status: status, Error: errMsg,
		})
	}()

	return &RAGStreamResult{Events: out, TraceID: traceID}, nil
}
