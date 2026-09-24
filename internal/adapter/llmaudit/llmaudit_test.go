package llmaudit_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/llmaudit"
	"github.com/sibukixxx/rag-poc/internal/domain/audit"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
)

type memoryRecorder struct{ events []audit.Event }

func (m *memoryRecorder) Record(_ context.Context, e audit.Event) error {
	m.events = append(m.events, e)
	return nil
}

type fakeLLM struct{ err error }

func (f fakeLLM) Generate(context.Context, llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llm.GenerateResponse{Content: "ok", Model: "m"}, nil
}

func (f fakeLLM) Stream(context.Context, llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}
func (fakeEmbedder) Dimensions() int { return 2 }
func (fakeEmbedder) Model() string   { return "embed-model" }

const secretPrompt = "CUSTOMER-CONFIDENTIAL-PROMPT"

func request() llm.GenerateRequest {
	return llm.GenerateRequest{Model: "gpt-x", Messages: []llm.Message{{Role: llm.RoleUser, Content: secretPrompt}}}
}

func TestLLMGenerateRecordsProviderModelAndEndpointWithoutPrompt(t *testing.T) {
	rec := &memoryRecorder{}
	wrapped := llmaudit.WrapLLM(fakeLLM{}, "openai", "https://api.openai.com/v1/chat?key=x", rec)
	ctx := audit.WithActor(context.Background(), "runtime_token:abc")

	if _, err := wrapped.Generate(ctx, request()); err != nil {
		t.Fatal(err)
	}

	if len(rec.events) != 1 {
		t.Fatalf("events = %+v, want 1", rec.events)
	}
	e := rec.events[0]
	want := map[string]string{"provider": "openai", "model": "gpt-x", "operation": "generate", "endpoint": "https://api.openai.com", "endpoint_class": "public"}
	if e.Action != audit.ActionProviderInvoke || e.Outcome != audit.OutcomeSuccess || e.Actor != "runtime_token:abc" || !reflect.DeepEqual(e.Metadata, want) {
		t.Fatalf("event = %+v, want provider.invoke success with metadata %v", e, want)
	}
	raw, _ := json.Marshal(rec.events)
	if strings.Contains(string(raw), secretPrompt) {
		t.Fatal("audit event contains prompt text")
	}
}

func TestLLMFailuresAreRecordedAsFailure(t *testing.T) {
	rec := &memoryRecorder{}
	wrapped := llmaudit.WrapLLM(fakeLLM{err: errors.New("provider returned HTTP 500")}, "local", "http://127.0.0.1:11434/v1", rec)

	_, genErr := wrapped.Generate(context.Background(), request())
	_, streamErr := wrapped.Stream(context.Background(), request())

	if genErr == nil || streamErr == nil {
		t.Fatal("wrapper must return the provider error unchanged")
	}
	if len(rec.events) != 2 || rec.events[0].Outcome != audit.OutcomeFailure || rec.events[1].Metadata["operation"] != "stream" ||
		rec.events[1].Metadata["endpoint_class"] != "loopback" {
		t.Fatalf("events = %+v", rec.events)
	}
}

func TestEmbedderRecordsBatchSizeNotTexts(t *testing.T) {
	rec := &memoryRecorder{}
	wrapped := llmaudit.WrapEmbedder(fakeEmbedder{}, "embeddings", "http://10.0.0.5:8080/v1", rec)

	if _, err := wrapped.Embed(context.Background(), []string{secretPrompt, "second"}); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"provider": "embeddings", "model": "embed-model", "operation": "embed", "inputs": "2", "endpoint": "http://10.0.0.5:8080", "endpoint_class": "private"}
	if len(rec.events) != 1 || !reflect.DeepEqual(rec.events[0].Metadata, want) {
		t.Fatalf("events = %+v, want metadata %v", rec.events, want)
	}
	if wrapped.Model() != "embed-model" || wrapped.Dimensions() != 2 {
		t.Fatal("wrapper must delegate Model/Dimensions")
	}
}
