package llm

import "context"

// Embedder turns text into vectors for retrieval. Like LLM, it's
// implemented once per provider (openaicompat covers OpenAI-compatible
// /embeddings endpoints) so the ingestion pipeline never depends on a
// concrete provider (docs/V0.1_SPEC.md §3).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
	Model() string
}
