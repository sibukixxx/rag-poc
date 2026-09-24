// Package llmaudit decorates llm.LLM and llm.Embedder so every provider
// call leaves a provider.invoke audit event (#24): which configured
// provider and model received a request, from which actor, through which
// endpoint origin, and whether it succeeded. Prompts, documents, and
// provider responses are never recorded.
package llmaudit

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/audit"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
)

type endpoint struct {
	origin string
	class  string
}

// describeEndpoint reduces a base URL to its origin (no path, query, or
// credentials) and classifies the host without DNS lookups.
func describeEndpoint(baseURL string) endpoint {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return endpoint{origin: "invalid", class: "unknown"}
	}
	origin := u.Scheme + "://" + u.Host
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return endpoint{origin: origin, class: "loopback"}
	}
	ip := net.ParseIP(host)
	switch {
	case ip == nil:
		return endpoint{origin: origin, class: "public"}
	case ip.IsLoopback():
		return endpoint{origin: origin, class: "loopback"}
	case ip.IsPrivate():
		return endpoint{origin: origin, class: "private"}
	default:
		return endpoint{origin: origin, class: "public"}
	}
}

func record(ctx context.Context, rec audit.Recorder, ep endpoint, meta map[string]string, err error) {
	meta["endpoint"] = ep.origin
	meta["endpoint_class"] = ep.class
	outcome := audit.OutcomeSuccess
	if err != nil {
		outcome = audit.OutcomeFailure
	}
	_ = rec.Record(ctx, audit.Event{
		OccurredAt: time.Now(), Action: audit.ActionProviderInvoke, Outcome: outcome,
		Actor: audit.ActorFrom(ctx), Metadata: meta,
	})
}

type auditedLLM struct {
	next     llm.LLM
	provider string
	ep       endpoint
	rec      audit.Recorder
}

// WrapLLM returns next unchanged when rec is nil.
func WrapLLM(next llm.LLM, provider, baseURL string, rec audit.Recorder) llm.LLM {
	if rec == nil {
		return next
	}
	return &auditedLLM{next: next, provider: provider, ep: describeEndpoint(baseURL), rec: rec}
}

func (a *auditedLLM) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	resp, err := a.next.Generate(ctx, req)
	record(ctx, a.rec, a.ep, map[string]string{"provider": a.provider, "model": req.Model, "operation": "generate"}, err)
	return resp, err
}

// Stream records the request when the provider accepts it; errors that
// happen mid-stream are visible in the request's Trace.
func (a *auditedLLM) Stream(ctx context.Context, req llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	ch, err := a.next.Stream(ctx, req)
	record(ctx, a.rec, a.ep, map[string]string{"provider": a.provider, "model": req.Model, "operation": "stream"}, err)
	return ch, err
}

type auditedEmbedder struct {
	next     llm.Embedder
	provider string
	ep       endpoint
	rec      audit.Recorder
}

// WrapEmbedder returns next unchanged when rec is nil.
func WrapEmbedder(next llm.Embedder, provider, baseURL string, rec audit.Recorder) llm.Embedder {
	if rec == nil {
		return next
	}
	return &auditedEmbedder{next: next, provider: provider, ep: describeEndpoint(baseURL), rec: rec}
}

func (a *auditedEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, err := a.next.Embed(ctx, texts)
	record(ctx, a.rec, a.ep, map[string]string{
		"provider": a.provider, "model": a.next.Model(), "operation": "embed", "inputs": strconv.Itoa(len(texts)),
	}, err)
	return vecs, err
}

func (a *auditedEmbedder) Dimensions() int { return a.next.Dimensions() }
func (a *auditedEmbedder) Model() string   { return a.next.Model() }
