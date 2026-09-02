package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/llm"
)

// Embedder implements domain/llm.Embedder against an OpenAI-compatible
// /embeddings endpoint.
type Embedder struct {
	baseURL string
	apiKey  string
	model   string
	dims    int
	http    *http.Client
}

func NewEmbedder(baseURL, apiKey, model string, dims int) *Embedder {
	return &Embedder{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		dims:    dims,
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

var _ llm.Embedder = (*Embedder)(nil)

func (e *Embedder) Dimensions() int { return e.dims }
func (e *Embedder) Model() string   { return e.model }

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingDataItem struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type embeddingResponse struct {
	Data []embeddingDataItem `json:"data"`
}

func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	payload, err := json.Marshal(embeddingRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("openaicompat: encoding embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: building embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readAPIError(resp)
	}

	var parsed embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("openaicompat: decoding embedding response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("openaicompat: expected %d embeddings, got %d", len(texts), len(parsed.Data))
	}

	// The API is documented to preserve input order via `index`, but sort
	// defensively rather than trust it silently.
	sort.Slice(parsed.Data, func(i, j int) bool { return parsed.Data[i].Index < parsed.Data[j].Index })

	vectors := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		vectors[i] = d.Embedding
	}
	return vectors, nil
}
