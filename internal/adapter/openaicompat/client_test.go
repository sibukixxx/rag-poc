package openaicompat_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/openaicompat"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
)

func TestGenerateParsesContentAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("expected Authorization header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"model": "gpt-4o-mini",
			"choices": [{"message": {"role": "assistant", "content": "hello there"}}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 3}
		}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key")
	resp, err := client.Generate(context.Background(), llm.GenerateRequest{
		Model:    "gpt-4o-mini",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content != "hello there" {
		t.Errorf("got content %q", resp.Content)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 3 {
		t.Errorf("got usage %+v", resp.Usage)
	}
}

func TestGenerateReturnsErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error": "invalid api key"}`)
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "bad-key")
	_, err := client.Generate(context.Background(), llm.GenerateRequest{Model: "gpt-4o-mini"})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestStreamEmitsDeltasThenUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		chunks := []string{
			`{"choices":[{"delta":{"content":"Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL, "test-key")
	events, err := client.Stream(context.Background(), llm.GenerateRequest{
		Model:    "gpt-4o-mini",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var content string
	var final llm.StreamEvent
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		content += ev.Delta
		if ev.Done {
			final = ev
		}
	}

	if content != "Hello" {
		t.Errorf("got content %q, want %q", content, "Hello")
	}
	if final.Usage.InputTokens != 5 || final.Usage.OutputTokens != 2 {
		t.Errorf("got final usage %+v", final.Usage)
	}
}
