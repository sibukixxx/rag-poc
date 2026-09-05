// Package llmrerank implements retrieval.Reranker as an LLM listwise
// rerank: the candidates are shown to a cheap model, which returns their
// indices in relevance order (docs/DESIGN_REVIEW.md F-7 — v0.1 has no
// cross-encoder runtime, so this is the only reranker). It's opt-in and
// off by default because it adds cost and latency.
package llmrerank

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
)

const maxCandidateChars = 300

type Reranker struct {
	router *llm.Router
	alias  string
}

// New builds a Reranker that resolves alias (typically "cheap") through
// router for each call, so it always uses whatever model that alias
// currently points to.
func New(router *llm.Router, alias string) *Reranker {
	if alias == "" {
		alias = "cheap"
	}
	return &Reranker{router: router, alias: alias}
}

var _ retrieval.Reranker = (*Reranker)(nil)

// Rerank degrades gracefully: any failure (no provider, API error, an
// unparseable response) falls back to the candidates' incoming order
// truncated to topK, rather than failing the whole search. Reranking is
// an optional quality boost, not something Hybrid Search should depend
// on to return results at all.
func (r *Reranker) Rerank(ctx context.Context, query string, candidates []retrieval.Result, topK int) ([]retrieval.Result, error) {
	fallback := func() []retrieval.Result {
		if topK > 0 && len(candidates) > topK {
			return candidates[:topK]
		}
		return candidates
	}

	if len(candidates) == 0 {
		return candidates, nil
	}

	provider, model, err := r.router.Resolve(r.alias)
	if err != nil {
		return fallback(), nil
	}

	resp, err := provider.Generate(ctx, llm.GenerateRequest{
		Model:       model,
		Temperature: 0,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: rerankSystemPrompt},
			{Role: llm.RoleUser, Content: buildRerankPrompt(query, candidates)},
		},
	})
	if err != nil {
		return fallback(), nil
	}

	order, err := parseIndices(resp.Content)
	if err != nil {
		return fallback(), nil
	}

	seen := make(map[int]bool, len(order))
	reordered := make([]retrieval.Result, 0, min(len(order), max(topK, len(candidates))))
	for _, idx := range order {
		if idx < 0 || idx >= len(candidates) || seen[idx] {
			continue
		}
		seen[idx] = true
		reordered = append(reordered, candidates[idx])
		if topK > 0 && len(reordered) >= topK {
			break
		}
	}
	if len(reordered) == 0 {
		return fallback(), nil
	}
	return reordered, nil
}

const rerankSystemPrompt = `You rank search results by relevance to a query.
Given a query and a numbered list of candidate passages, return ONLY a JSON
array of the candidate indices ordered from most to least relevant.
Example output: [2, 0, 1]
Do not include any other text.`

func buildRerankPrompt(query string, candidates []retrieval.Result) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Query: %s\n\nCandidates:\n", query)
	for i, c := range candidates {
		text := c.Text
		if r := []rune(text); len(r) > maxCandidateChars {
			text = string(r[:maxCandidateChars]) + "..."
		}
		fmt.Fprintf(&sb, "[%d] %s\n", i, text)
	}
	return sb.String()
}

var arrayPattern = regexp.MustCompile(`\[[\d,\s]*\]`)

// parseIndices extracts the first JSON array of integers found in the
// response, tolerating models that add stray text despite instructions.
func parseIndices(content string) ([]int, error) {
	match := arrayPattern.FindString(content)
	if match == "" {
		return nil, fmt.Errorf("llmrerank: no JSON array found in response")
	}
	var indices []int
	if err := json.Unmarshal([]byte(match), &indices); err != nil {
		return nil, fmt.Errorf("llmrerank: parsing indices: %w", err)
	}
	return indices, nil
}
