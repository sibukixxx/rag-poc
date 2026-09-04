// Package usecase orchestrates domain interfaces into application
// behavior. ChatUseCase is the first: it drives an LLM call (streaming or
// not) through the Router and records every call as a Trace+Span with
// tokens/cost/latency (docs/DESIGN_REVIEW.md F-2).
package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

// defaultMaxTokens caps output length server-side so a single Playground
// request cannot run up an unbounded provider bill.
const defaultMaxTokens = 2048

type ChatUseCase struct {
	Router *llm.Router
	Prices llm.PriceTable
	Traces trace.Store
}

func NewChatUseCase(router *llm.Router, prices llm.PriceTable, traces trace.Store) *ChatUseCase {
	return &ChatUseCase{Router: router, Prices: prices, Traces: traces}
}

// Chat performs a single non-streaming completion (used by evaluation/judge
// usecases from W7 onward) and records it as a one-span trace.
func (c *ChatUseCase) Chat(ctx context.Context, alias string, messages []llm.Message) (*llm.GenerateResponse, string, error) {
	provider, model, err := c.Router.Resolve(alias)
	if err != nil {
		return nil, "", err
	}

	traceID := uuid.NewString()
	start := time.Now()
	resp, err := provider.Generate(ctx, llm.GenerateRequest{Model: model, Messages: messages, MaxTokens: defaultMaxTokens})
	duration := time.Since(start)

	status := trace.StatusOK
	errMsg := ""
	var usage llm.Usage
	if err != nil {
		status = trace.StatusError
		errMsg = err.Error()
	} else {
		usage = resp.Usage
	}
	costUSD, _, _ := c.Prices.Cost(model, usage)

	bg := context.Background()
	_ = c.Traces.CreateTrace(bg, trace.Trace{
		ID: traceID, Name: "chat:" + alias, StartedAt: start,
		DurationMS: duration.Milliseconds(), Status: status, CostUSD: costUSD,
	})
	_ = c.Traces.CreateSpan(bg, trace.Span{
		ID: uuid.NewString(), TraceID: traceID, Kind: trace.SpanKindLLM, Name: "llm.generate",
		StartedAt: start, DurationMS: duration.Milliseconds(), Model: model,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CostUSD: costUSD,
		Status: status, Error: errMsg,
	})

	if err != nil {
		return nil, traceID, err
	}
	return resp, traceID, nil
}

// ChatStreamEvent is the usecase-level stream event: unlike llm.StreamEvent,
// its terminal event carries the computed cost, so the HTTP layer doesn't
// need its own copy of the price table.
type ChatStreamEvent struct {
	Delta   string
	Done    bool
	Usage   llm.Usage
	CostUSD float64
	Err     error
}

type ChatStreamResult struct {
	Events  <-chan ChatStreamEvent
	TraceID string
}

// ChatStream performs a streaming completion. The trace is written after
// the stream ends (including on client disconnect or upstream error), using
// a background context so it survives cancellation of the request context.
func (c *ChatUseCase) ChatStream(ctx context.Context, alias string, messages []llm.Message) (*ChatStreamResult, error) {
	provider, model, err := c.Router.Resolve(alias)
	if err != nil {
		return nil, err
	}

	upstream, err := provider.Stream(ctx, llm.GenerateRequest{Model: model, Messages: messages, MaxTokens: defaultMaxTokens})
	if err != nil {
		return nil, err
	}

	traceID := uuid.NewString()
	start := time.Now()
	out := make(chan ChatStreamEvent)

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
				out <- ChatStreamEvent{Err: ev.Err}
				break
			}
			if ev.Delta != "" {
				out <- ChatStreamEvent{Delta: ev.Delta}
			}
			if ev.Done {
				usage = ev.Usage
				costUSD, _, _ = c.Prices.Cost(model, usage)
				out <- ChatStreamEvent{Done: true, Usage: usage, CostUSD: costUSD}
			}
		}

		duration := time.Since(start)
		bg := context.Background()
		_ = c.Traces.CreateTrace(bg, trace.Trace{
			ID: traceID, Name: "chat:" + alias, StartedAt: start,
			DurationMS: duration.Milliseconds(), Status: status, CostUSD: costUSD,
		})
		_ = c.Traces.CreateSpan(bg, trace.Span{
			ID: uuid.NewString(), TraceID: traceID, Kind: trace.SpanKindLLM, Name: "llm.stream",
			StartedAt: start, DurationMS: duration.Milliseconds(), Model: model,
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CostUSD: costUSD,
			Status: status, Error: errMsg,
		})
	}()

	return &ChatStreamResult{Events: out, TraceID: traceID}, nil
}
