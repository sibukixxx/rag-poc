package app

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode"
)

// fakeProvider is a deterministic OpenAI-compatible endpoint for the
// acceptance E2E. It is a test double for the only external dependency
// (the model provider); everything else in the E2E runs the real stack.
//
//   - /embeddings returns a hashed bag-of-words vector, so lexically
//     similar texts land close together and retrieval is meaningful.
//   - /chat/completions answers judge prompts with a fixed score JSON and
//     everything else with a short cited answer, streaming when asked.
type fakeProvider struct {
	server *httptest.Server
	dims   int

	mu            sync.Mutex
	systemPrompts []string
	requests      int
}

func newFakeProvider(t *testing.T, dims int) *fakeProvider {
	t.Helper()
	f := &fakeProvider{dims: dims}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/embeddings", f.embeddings)
	mux.HandleFunc("/v1/chat/completions", f.chat)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeProvider) baseURL() string { return f.server.URL + "/v1" }

func (f *fakeProvider) lastSystemPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.systemPrompts) == 0 {
		return ""
	}
	return f.systemPrompts[len(f.systemPrompts)-1]
}

func (f *fakeProvider) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func (f *fakeProvider) embeddings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input []string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()

	type item struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	}
	data := make([]item, len(req.Input))
	for i, text := range req.Input {
		data[i] = item{Embedding: hashedBagOfWords(text, f.dims), Index: i}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  data,
		"usage": map[string]int{"prompt_tokens": len(req.Input) * 8, "total_tokens": len(req.Input) * 8},
	})
}

func hashedBagOfWords(text string, dims int) []float32 {
	vec := make([]float32, dims)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, word := range words {
		h := fnv.New32a()
		_, _ = h.Write([]byte(word))
		vec[h.Sum32()%uint32(dims)]++
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v * v)
	}
	if norm == 0 {
		vec[0] = 1
		return vec
	}
	scale := float32(1 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= scale
	}
	return vec
}

func (f *fakeProvider) chat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var system string
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = m.Content
		}
	}
	f.mu.Lock()
	f.requests++
	f.systemPrompts = append(f.systemPrompts, system)
	f.mu.Unlock()

	answer := "The answer is in the cited passage [1]."
	if strings.Contains(system, "groundedness") {
		answer = `{"correctness": 0.9, "groundedness": 1.0, "relevance": 0.8, "reason": "fake judge"}`
	}

	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "fake-model",
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": answer}}},
			"usage":   map[string]int{"prompt_tokens": 100, "completion_tokens": 20},
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for _, part := range strings.SplitAfter(answer, " ") {
		chunk, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]string{"content": part}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		if flusher != nil {
			flusher.Flush()
		}
	}
	usage, _ := json.Marshal(map[string]any{"choices": []any{}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 20}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
}
