// Package openaicompat implements domain/llm.LLM against any
// OpenAI-compatible /chat/completions endpoint (OpenAI, OpenRouter,
// Ollama, vLLM, LM Studio, Azure OpenAI's compatible mode, ...), per the
// v0.1 decision to ship one adapter that covers most providers
// (docs/V0.1_SPEC.md §1).
package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/llm"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New builds a client. baseURL should not have a trailing slash
// (e.g. "https://api.openai.com/v1").
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

var _ llm.LLM = (*Client)(nil)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	Temperature   float64        `json:"temperature,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
	Delta   chatMessage `json:"delta"`
}

type usageJSON struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type chatResponse struct {
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   usageJSON    `json:"usage"`
}

func toChatMessages(msgs []llm.Message) []chatMessage {
	out := make([]chatMessage, len(msgs))
	for i, m := range msgs {
		out[i] = chatMessage{Role: string(m.Role), Content: m.Content}
	}
	return out
}

func (c *Client) newRequest(ctx context.Context, body chatRequest) (*http.Request, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: encoding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return req, nil
}

// readAPIError logs the upstream URL and response body (which can contain
// internal hostnames, org/project IDs, or a masked API key) server-side
// only, and returns an error safe to show to API clients.
func readAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	log.Printf("openaicompat: %s returned %d: %s", resp.Request.URL, resp.StatusCode, string(body))
	return fmt.Errorf("openaicompat: provider returned HTTP %d", resp.StatusCode)
}

func (c *Client) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	httpReq, err := c.newRequest(ctx, chatRequest{
		Model:       req.Model,
		Messages:    toChatMessages(req.Messages),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readAPIError(resp)
	}

	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("openaicompat: decoding response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("openaicompat: response had no choices")
	}

	return &llm.GenerateResponse{
		Content: parsed.Choices[0].Message.Content,
		Model:   req.Model,
		Usage: llm.Usage{
			InputTokens:  parsed.Usage.PromptTokens,
			OutputTokens: parsed.Usage.CompletionTokens,
		},
	}, nil
}

func (c *Client) Stream(ctx context.Context, req llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	httpReq, err := c.newRequest(ctx, chatRequest{
		Model:         req.Model,
		Messages:      toChatMessages(req.Messages),
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, readAPIError(resp)
	}

	ch := make(chan llm.StreamEvent)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var usage llm.Usage
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				ch <- llm.StreamEvent{Done: true, Usage: usage}
				return
			}

			var chunk chatResponse
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				ch <- llm.StreamEvent{Err: fmt.Errorf("openaicompat: decoding stream chunk: %w", err)}
				return
			}
			if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				usage = llm.Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
			}
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				ch <- llm.StreamEvent{Delta: chunk.Choices[0].Delta.Content}
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- llm.StreamEvent{Err: fmt.Errorf("openaicompat: reading stream: %w", err)}
			return
		}
		// Providers that omit "[DONE]" still need a terminal event.
		ch <- llm.StreamEvent{Done: true, Usage: usage}
	}()

	return ch, nil
}
