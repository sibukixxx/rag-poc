package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

func TestTraceStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "forgeai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	store := sqlite.NewTraceStore(db)
	ctx := t.Context()
	start := time.Now().Truncate(time.Millisecond)

	tr := trace.Trace{ID: "trace-1", Name: "chat:normal", StartedAt: start, DurationMS: 1200, Status: trace.StatusOK, CostUSD: 0.0021}
	if err := store.CreateTrace(ctx, tr); err != nil {
		t.Fatalf("CreateTrace: %v", err)
	}

	sp := trace.Span{
		ID: "span-1", TraceID: "trace-1", Kind: trace.SpanKindLLM, Name: "llm.generate",
		StartedAt: start, DurationMS: 1200, Model: "gpt-4o-mini",
		InputTokens: 100, OutputTokens: 50, CostUSD: 0.0021, Status: trace.StatusOK,
	}
	if err := store.CreateSpan(ctx, sp); err != nil {
		t.Fatalf("CreateSpan: %v", err)
	}

	gotTrace, gotSpans, err := store.GetTrace(ctx, "trace-1")
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if gotTrace.Name != "chat:normal" || gotTrace.Status != trace.StatusOK {
		t.Errorf("got trace %+v", gotTrace)
	}
	if !gotTrace.StartedAt.Equal(start) {
		t.Errorf("got started_at %v, want %v", gotTrace.StartedAt, start)
	}
	if len(gotSpans) != 1 || gotSpans[0].Model != "gpt-4o-mini" || gotSpans[0].InputTokens != 100 {
		t.Errorf("got spans %+v", gotSpans)
	}

	list, err := store.ListTraces(ctx, 10)
	if err != nil {
		t.Fatalf("ListTraces: %v", err)
	}
	if len(list) != 1 || list[0].ID != "trace-1" {
		t.Errorf("got list %+v", list)
	}
}
