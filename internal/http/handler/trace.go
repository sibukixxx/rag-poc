package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

type TraceHandler struct {
	store trace.Store
}

func NewTraceHandler(store trace.Store) *TraceHandler {
	return &TraceHandler{store: store}
}

type traceDTO struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	StartedAt  string  `json:"started_at"`
	DurationMS int64   `json:"duration_ms"`
	Status     string  `json:"status"`
	CostUSD    float64 `json:"cost_usd"`
}

type spanDTO struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
	StartedAt    string  `json:"started_at"`
	DurationMS   int64   `json:"duration_ms"`
	Model        string  `json:"model,omitempty"`
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	Status       string  `json:"status"`
	Error        string  `json:"error,omitempty"`
}

func toTraceDTO(t trace.Trace) traceDTO {
	return traceDTO{
		ID: t.ID, Name: t.Name, StartedAt: t.StartedAt.Format(time.RFC3339Nano),
		DurationMS: t.DurationMS, Status: string(t.Status), CostUSD: t.CostUSD,
	}
}

func toSpanDTO(s trace.Span) spanDTO {
	return spanDTO{
		ID: s.ID, Kind: string(s.Kind), Name: s.Name, StartedAt: s.StartedAt.Format(time.RFC3339Nano),
		DurationMS: s.DurationMS, Model: s.Model, InputTokens: s.InputTokens, OutputTokens: s.OutputTokens,
		CostUSD: s.CostUSD, Status: string(s.Status), Error: s.Error,
	}
}

// List handles GET /api/v1/traces?limit=N (default 50).
func (h *TraceHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}

	traces, err := h.store.ListTraces(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]traceDTO, len(traces))
	for i, t := range traces {
		out[i] = toTraceDTO(t)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// Get handles GET /api/v1/traces/{id} -> the trace plus its spans (a flat
// list in v0.1 — spans have no parent/child nesting yet, so "span tree"
// degenerates to a flat, chronologically-ordered list).
func (h *TraceHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, spans, err := h.store.GetTrace(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	spanDTOs := make([]spanDTO, len(spans))
	for i, s := range spans {
		spanDTOs[i] = toSpanDTO(s)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"trace": toTraceDTO(*t), "spans": spanDTOs})
}
