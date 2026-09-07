package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/prompt"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

// JudgePromptName is the Prompt Registry entry the LLM Judge reads its
// instructions from. Like the RAG prompt, it is seeded at bootstrap and
// versioned, so "the judge got stricter" is a prompt version change that
// every result records (JudgePromptVersion), not an invisible code edit
// (docs/V0.1_SPEC.md §8, docs/DESIGN_REVIEW.md F-11).
const JudgePromptName = "rag_judge"

// JudgeAlias is the Router alias the judge resolves. It is deliberately
// separate from the answering alias so a stronger (or at least
// different) model can grade a cheaper one.
const JudgeAlias = "judge"

// DefaultJudgePrompt is JudgePromptName's seeded v1. It asks for a strict
// JSON object so parsing is deterministic; the three dimensions are the
// ones docs/V0.1_SPEC.md §8 names.
const DefaultJudgePrompt = `You are a strict evaluator of answers produced by a retrieval-augmented
question-answering system. You will be given a question, the numbered
context passages the system retrieved, the answer it produced, and (when
available) a reference answer written by a human.

Score the answer on three dimensions, each a number from 0.0 to 1.0:

- correctness: Does the answer state the right facts? Compare against
  the reference answer when one is given; otherwise judge against the
  context passages. Wrong or contradictory facts score low. "I don't
  know" scores 0.0 if the context contains the answer.
- groundedness: Is every claim in the answer supported by the context
  passages? Fabricated details, or claims not found in the context,
  lower this score even if they happen to be true.
- relevance: Does the answer address what was actually asked, without
  drifting into unrelated material?

Respond with ONLY a JSON object of this exact shape and nothing else:
{"correctness": 0.0, "groundedness": 0.0, "relevance": 0.0, "reason": "one or two sentences explaining the scores, in the language of the question"}`

// Judgment is the LLM Judge's verdict on one answer.
type Judgment struct {
	Correctness   float64
	Groundedness  float64
	Relevance     float64
	Reason        string
	Model         string
	PromptVersion int
	Usage         llm.Usage
	CostUSD       float64
}

// JudgeInput is everything the judge needs to grade one answer.
type JudgeInput struct {
	Question        string
	Context         string // the numbered context the answer was generated from
	Answer          string
	ReferenceAnswer string // optional
}

// LLMJudge scores a RAG answer for Correctness / Groundedness / Relevance
// using the "judge" alias and the registry's active judge prompt
// (docs/ROADMAP.md W8). Unlike the reranker it is not fail-soft: an
// evaluation run that silently scored everything 0 would be worse than
// one that reports the judge failed, so errors propagate to the caller,
// which records them per case.
type LLMJudge struct {
	Router  *llm.Router
	Prices  llm.PriceTable
	Traces  trace.Store
	Prompts prompt.Store
	Alias   string
}

func NewLLMJudge(router *llm.Router, prices llm.PriceTable, traces trace.Store, prompts prompt.Store) *LLMJudge {
	return &LLMJudge{Router: router, Prices: prices, Traces: traces, Prompts: prompts, Alias: JudgeAlias}
}

// judgePrompt resolves the active judge prompt and its version, falling
// back to DefaultJudgePrompt (version 0 = "not from the registry") if the
// registry is unavailable or unseeded.
func (j *LLMJudge) judgePrompt(ctx context.Context) (string, int) {
	if j.Prompts == nil {
		return DefaultJudgePrompt, 0
	}
	p, err := j.Prompts.GetPromptByName(ctx, JudgePromptName)
	if err != nil {
		return DefaultJudgePrompt, 0
	}
	v, err := j.Prompts.GetActiveVersion(ctx, p.ID)
	if err != nil {
		return DefaultJudgePrompt, 0
	}
	return v.Content, v.Version
}

// Judge grades one answer. traceID, if non-empty, is the trace the judge
// span is attached to (the evaluation run's per-case trace); otherwise a
// standalone trace is created.
func (j *LLMJudge) Judge(ctx context.Context, in JudgeInput, traceID string) (*Judgment, error) {
	provider, model, err := j.Router.Resolve(j.Alias)
	if err != nil {
		return nil, err
	}
	system, version := j.judgePrompt(ctx)

	start := time.Now()
	resp, err := provider.Generate(ctx, llm.GenerateRequest{
		Model:       model,
		Temperature: 0,
		MaxTokens:   defaultMaxTokens,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: buildJudgeUserMessage(in)},
		},
	})
	duration := time.Since(start)

	var judgment *Judgment
	if err == nil {
		judgment, err = parseJudgment(resp.Content)
	}

	status, errMsg := trace.StatusOK, ""
	var usage llm.Usage
	var costUSD float64
	if resp != nil {
		usage = resp.Usage
		costUSD, _, _ = j.Prices.Cost(model, usage)
	}
	if err != nil {
		status, errMsg = trace.StatusError, err.Error()
	}
	j.recordSpan(traceID, model, start, duration, usage, costUSD, status, errMsg)
	if err != nil {
		return nil, err
	}

	judgment.Model = model
	judgment.PromptVersion = version
	judgment.Usage = usage
	judgment.CostUSD = costUSD
	return judgment, nil
}

func (j *LLMJudge) recordSpan(traceID, model string, start time.Time, duration time.Duration, usage llm.Usage, costUSD float64, status trace.Status, errMsg string) {
	if j.Traces == nil {
		return
	}
	bg := context.Background()
	if traceID == "" {
		traceID = uuid.NewString()
		_ = j.Traces.CreateTrace(bg, trace.Trace{
			ID: traceID, Name: "judge:" + j.Alias, StartedAt: start,
			DurationMS: duration.Milliseconds(), Status: status, CostUSD: costUSD,
		})
	}
	_ = j.Traces.CreateSpan(bg, trace.Span{
		ID: uuid.NewString(), TraceID: traceID, Kind: trace.SpanKindJudge, Name: "judge.generate",
		StartedAt: start, DurationMS: duration.Milliseconds(), Model: model,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CostUSD: costUSD,
		Status: status, Error: errMsg,
	})
}

func buildJudgeUserMessage(in JudgeInput) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Question:\n%s\n\n", in.Question)
	ctxText := strings.TrimSpace(in.Context)
	if ctxText == "" {
		ctxText = "(no context was retrieved)"
	}
	fmt.Fprintf(&sb, "Context passages:\n%s\n\n", ctxText)
	fmt.Fprintf(&sb, "Answer to evaluate:\n%s\n\n", in.Answer)
	if ref := strings.TrimSpace(in.ReferenceAnswer); ref != "" {
		fmt.Fprintf(&sb, "Reference answer:\n%s\n", ref)
	} else {
		sb.WriteString("Reference answer: (none provided — judge correctness against the context passages)\n")
	}
	return sb.String()
}

// objectPattern finds the first {...} in the response, tolerating models
// that wrap the JSON in prose or a code fence despite instructions.
var objectPattern = regexp.MustCompile(`(?s)\{.*\}`)

func parseJudgment(content string) (*Judgment, error) {
	match := objectPattern.FindString(content)
	if match == "" {
		return nil, fmt.Errorf("judge: no JSON object found in response")
	}
	var raw struct {
		Correctness  *float64 `json:"correctness"`
		Groundedness *float64 `json:"groundedness"`
		Relevance    *float64 `json:"relevance"`
		Reason       string   `json:"reason"`
	}
	if err := json.Unmarshal([]byte(match), &raw); err != nil {
		return nil, fmt.Errorf("judge: parsing response: %w", err)
	}
	if raw.Correctness == nil || raw.Groundedness == nil || raw.Relevance == nil {
		return nil, fmt.Errorf("judge: response is missing one of correctness/groundedness/relevance")
	}
	return &Judgment{
		Correctness:  clamp01(*raw.Correctness),
		Groundedness: clamp01(*raw.Groundedness),
		Relevance:    clamp01(*raw.Relevance),
		Reason:       strings.TrimSpace(raw.Reason),
	}, nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
