package usecase_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

type fakeLLM struct {
	genResp    *llm.GenerateResponse
	genErr     error
	streamEvts []llm.StreamEvent
}

func (f *fakeLLM) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return f.genResp, f.genErr
}

func (f *fakeLLM) Stream(ctx context.Context, req llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, len(f.streamEvts))
	for _, ev := range f.streamEvts {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

type memTraceStore struct {
	mu     sync.Mutex
	traces map[string]trace.Trace
	spans  map[string][]trace.Span
}

func newMemTraceStore() *memTraceStore {
	return &memTraceStore{traces: map[string]trace.Trace{}, spans: map[string][]trace.Span{}}
}

func (m *memTraceStore) CreateTrace(ctx context.Context, t trace.Trace) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.traces[t.ID] = t
	return nil
}

func (m *memTraceStore) CreateSpan(ctx context.Context, s trace.Span) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spans[s.TraceID] = append(m.spans[s.TraceID], s)
	return nil
}

func (m *memTraceStore) GetTrace(ctx context.Context, id string) (*trace.Trace, []trace.Span, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.traces[id]
	if !ok {
		return nil, nil, errors.New("not found")
	}
	return &t, m.spans[id], nil
}

func (m *memTraceStore) ListTraces(ctx context.Context, limit int) ([]trace.Trace, error) {
	return nil, nil
}

func testRouter(t *testing.T, provider llm.LLM) *llm.Router {
	t.Helper()
	r := llm.NewRouter()
	r.RegisterProvider("default", provider)
	r.RegisterAlias("normal", llm.Alias{Provider: "default", Model: "gpt-4o-mini"})
	return r
}

func testPrices() llm.PriceTable {
	return llm.PriceTable{
		Prices: map[string]llm.ModelPricing{
			"gpt-4o-mini": {InputPer1M: 0.15, OutputPer1M: 0.60},
		},
		DisplayCurrency: "USD",
		USDToDisplay:    1,
	}
}

func TestChatRecordsTraceWithUsageAndCost(t *testing.T) {
	provider := &fakeLLM{genResp: &llm.GenerateResponse{
		Content: "hi there",
		Usage:   llm.Usage{InputTokens: 1000, OutputTokens: 500},
	}}
	traces := newMemTraceStore()
	uc := usecase.NewChatUseCase(testRouter(t, provider), testPrices(), traces)

	resp, traceID, err := uc.Chat(context.Background(), "normal", []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hi there" {
		t.Errorf("got content %q", resp.Content)
	}

	tr, spans, err := traces.GetTrace(context.Background(), traceID)
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if tr.Status != trace.StatusOK {
		t.Errorf("expected trace status ok, got %s", tr.Status)
	}
	wantCost := 1000.0/1_000_000*0.15 + 500.0/1_000_000*0.60
	if tr.CostUSD != wantCost {
		t.Errorf("got trace cost %v, want %v", tr.CostUSD, wantCost)
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].InputTokens != 1000 || spans[0].OutputTokens != 500 {
		t.Errorf("got span usage %+v", spans[0])
	}
}

func TestChatRecordsErrorStatus(t *testing.T) {
	provider := &fakeLLM{genErr: errors.New("upstream boom")}
	traces := newMemTraceStore()
	uc := usecase.NewChatUseCase(testRouter(t, provider), testPrices(), traces)

	_, traceID, err := uc.Chat(context.Background(), "normal", []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	if err == nil {
		t.Fatal("expected error")
	}

	tr, spans, getErr := traces.GetTrace(context.Background(), traceID)
	if getErr != nil {
		t.Fatalf("GetTrace: %v", getErr)
	}
	if tr.Status != trace.StatusError {
		t.Errorf("expected trace status error, got %s", tr.Status)
	}
	if spans[0].Error != "upstream boom" {
		t.Errorf("got span error %q", spans[0].Error)
	}
}

func TestChatUnknownAliasReturnsErrorWithoutTrace(t *testing.T) {
	traces := newMemTraceStore()
	uc := usecase.NewChatUseCase(llm.NewRouter(), testPrices(), traces)

	_, _, err := uc.Chat(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown alias")
	}
}

func TestChatStreamForwardsDeltasAndFinalCost(t *testing.T) {
	provider := &fakeLLM{streamEvts: []llm.StreamEvent{
		{Delta: "Hel"},
		{Delta: "lo"},
		{Done: true, Usage: llm.Usage{InputTokens: 100, OutputTokens: 50}},
	}}
	traces := newMemTraceStore()
	uc := usecase.NewChatUseCase(testRouter(t, provider), testPrices(), traces)

	result, err := uc.ChatStream(context.Background(), "normal", []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var content string
	var final usecase.ChatStreamEvent
	for ev := range result.Events {
		if ev.Err != nil {
			t.Fatalf("unexpected stream error: %v", ev.Err)
		}
		content += ev.Delta
		if ev.Done {
			final = ev
		}
	}

	if content != "Hello" {
		t.Errorf("got content %q", content)
	}
	wantCost := 100.0/1_000_000*0.15 + 50.0/1_000_000*0.60
	if final.CostUSD != wantCost {
		t.Errorf("got final cost %v, want %v", final.CostUSD, wantCost)
	}

	tr, spans, err := traces.GetTrace(context.Background(), result.TraceID)
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if tr.Status != trace.StatusOK || tr.CostUSD != wantCost {
		t.Errorf("got trace %+v", tr)
	}
	if spans[0].InputTokens != 100 || spans[0].OutputTokens != 50 {
		t.Errorf("got span %+v", spans[0])
	}
}
