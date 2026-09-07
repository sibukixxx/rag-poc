// Package trace records every pipeline execution (LLM calls now; embed /
// retrieve / rerank / judge from W3-W8 onward) as a Trace containing one or
// more Spans, so token usage, cost, and latency are always inspectable
// (docs/V0.1_SPEC.md §9, docs/DESIGN_REVIEW.md F-2).
package trace

import (
	"context"
	"time"
)

type Status string

const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

type SpanKind string

const (
	SpanKindLLM      SpanKind = "llm"
	SpanKindEmbed    SpanKind = "embed"
	SpanKindRetrieve SpanKind = "retrieve"
	SpanKindRerank   SpanKind = "rerank"
	SpanKindJudge    SpanKind = "judge"
)

// Span is one unit of work inside a Trace (e.g. a single LLM call).
type Span struct {
	ID           string
	TraceID      string
	Kind         SpanKind
	Name         string
	StartedAt    time.Time
	DurationMS   int64
	Model        string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	Status       Status
	Error        string
	Input        string
	Output       string
}

// Trace is the top-level record of one request (e.g. one chat turn).
type Trace struct {
	ID         string
	Name       string
	StartedAt  time.Time
	DurationMS int64
	Status     Status
	CostUSD    float64
}

// Store persists traces and their spans. Implementations must accept
// CreateSpan calls for a trace that hasn't been finalized yet.
type Store interface {
	CreateTrace(ctx context.Context, t Trace) error
	CreateSpan(ctx context.Context, s Span) error
	GetTrace(ctx context.Context, id string) (*Trace, []Span, error)
	ListTraces(ctx context.Context, limit int) ([]Trace, error)
}
