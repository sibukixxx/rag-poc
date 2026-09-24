// Package llmguard enforces the outbound sensitive-data policy (#25) on
// every provider call. It decorates llm.LLM and llm.Embedder inside the
// provider wiring, below all use cases, so chat, RAG, reranking, judging,
// and embeddings share one enforcement point. Redaction happens on a copy
// of the request before the provider adapter serializes it.
package llmguard

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/audit"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/outbound"
)

// Guard applies one policy with one detector.
type Guard struct {
	policy   outbound.Policy
	detector *outbound.Detector
	rec      audit.Recorder
}

// New builds a Guard. rec may be nil (no audit events).
func New(policy outbound.Policy, detector *outbound.Detector, rec audit.Recorder) *Guard {
	return &Guard{policy: policy, detector: detector, rec: rec}
}

func (g *Guard) active() bool {
	return g != nil && g.detector != nil && g.policy != outbound.PolicyAllow && g.policy != ""
}

// WrapLLM returns next itself under the allow policy.
func (g *Guard) WrapLLM(next llm.LLM, provider string) llm.LLM {
	if !g.active() {
		return next
	}
	return &guardedLLM{guard: g, next: next, provider: provider}
}

// WrapEmbedder returns next itself under the allow policy.
func (g *Guard) WrapEmbedder(next llm.Embedder, provider string) llm.Embedder {
	if !g.active() {
		return next
	}
	return &guardedEmbedder{guard: g, next: next, provider: provider}
}

// apply redacts texts, records matches, and reports whether the call may
// proceed. The returned slice is a copy; texts is never modified.
func (g *Guard) apply(ctx context.Context, texts []string, meta map[string]string) ([]string, error) {
	out := make([]string, len(texts))
	total := map[string]int{}
	for i, t := range texts {
		redacted, found := g.detector.Redact(t)
		out[i] = redacted
		for name, n := range found {
			total[name] += n
		}
	}
	if len(total) == 0 {
		return texts, nil
	}
	meta["policy"] = string(g.policy)
	meta["matches"] = formatMatches(total)
	if g.policy == outbound.PolicyDenySensitive {
		g.record(ctx, audit.OutcomeDenied, meta)
		return nil, outbound.ErrBlocked
	}
	g.record(ctx, audit.OutcomeSuccess, meta)
	return out, nil
}

func (g *Guard) record(ctx context.Context, outcome audit.Outcome, meta map[string]string) {
	if g.rec == nil {
		return
	}
	_ = g.rec.Record(ctx, audit.Event{
		OccurredAt: time.Now(), Action: audit.ActionEgressPolicyApplied, Outcome: outcome,
		Actor: audit.ActorFrom(ctx), Metadata: meta,
	})
}

// formatMatches renders rule counts deterministically: "email=1,phone=2".
func formatMatches(counts map[string]int) string {
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n + "=" + strconv.Itoa(counts[n])
	}
	return strings.Join(parts, ",")
}

type guardedLLM struct {
	guard    *Guard
	next     llm.LLM
	provider string
}

func (l *guardedLLM) prepare(ctx context.Context, req llm.GenerateRequest, operation string) (llm.GenerateRequest, error) {
	texts := make([]string, len(req.Messages))
	for i, m := range req.Messages {
		texts[i] = m.Content
	}
	out, err := l.guard.apply(ctx, texts, map[string]string{"operation": operation, "provider": l.provider, "model": req.Model})
	if err != nil {
		return llm.GenerateRequest{}, err
	}
	msgs := make([]llm.Message, len(req.Messages))
	for i, m := range req.Messages {
		m.Content = out[i]
		msgs[i] = m
	}
	req.Messages = msgs
	return req, nil
}

func (l *guardedLLM) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	safe, err := l.prepare(ctx, req, "generate")
	if err != nil {
		return nil, err
	}
	return l.next.Generate(ctx, safe)
}

func (l *guardedLLM) Stream(ctx context.Context, req llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	safe, err := l.prepare(ctx, req, "stream")
	if err != nil {
		return nil, err
	}
	return l.next.Stream(ctx, safe)
}

type guardedEmbedder struct {
	guard    *Guard
	next     llm.Embedder
	provider string
}

func (e *guardedEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	safe, err := e.guard.apply(ctx, texts, map[string]string{"operation": "embed", "provider": e.provider, "model": e.next.Model()})
	if err != nil {
		return nil, err
	}
	return e.next.Embed(ctx, safe)
}

func (e *guardedEmbedder) Dimensions() int { return e.next.Dimensions() }
func (e *guardedEmbedder) Model() string   { return e.next.Model() }
