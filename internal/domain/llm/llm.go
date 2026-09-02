// Package llm defines ForgeAI's provider-agnostic LLM contract. Concrete
// providers (OpenAI-compatible, and later Anthropic/Gemini/etc.) live in
// internal/adapter/*; business logic depends only on this package so a
// provider swap never touches usecases (docs/V0.1_SPEC.md §3-4).
package llm

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role
	Content string
}

type GenerateRequest struct {
	Model       string
	Messages    []Message
	Temperature float64
	MaxTokens   int
}

// Usage is returned by every LLM call so cost can be computed without a
// separate telemetry pass (docs/DESIGN_REVIEW.md F-2: measured from day 1).
type Usage struct {
	InputTokens  int
	OutputTokens int
}

type GenerateResponse struct {
	Content string
	Model   string
	Usage   Usage
}

// StreamEvent is one increment of a streamed generation. Usage is only
// populated on the final event (Done == true), once the upstream provider
// reports it.
type StreamEvent struct {
	Delta string
	Done  bool
	Usage Usage
	Err   error
}

// LLM is implemented once per provider. Generate is used for non-streaming
// calls (evaluation, judging); Stream backs the interactive Playground.
type LLM interface {
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
	Stream(ctx context.Context, req GenerateRequest) (<-chan StreamEvent, error)
}
