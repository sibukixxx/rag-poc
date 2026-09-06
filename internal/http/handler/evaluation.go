package handler

import (
	"encoding/json"
	"net/http"

	"github.com/sibukixxx/rag-poc/internal/domain/evaluation"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

type EvaluationHandler struct{ evaluate *usecase.EvaluateUseCase }

func NewEvaluationHandler(evaluate *usecase.EvaluateUseCase) *EvaluationHandler {
	return &EvaluationHandler{evaluate: evaluate}
}

type runEvaluationRequest struct {
	Dataset evaluation.Dataset `json:"dataset"`
	Config  evaluation.Config  `json:"config"`
}

func (h *EvaluationHandler) Run(w http.ResponseWriter, r *http.Request) {
	var req runEvaluationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	run, err := h.evaluate.Run(r.Context(), req.Dataset, req.Config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

type compareEvaluationRequest struct {
	Baseline evaluation.Run `json:"baseline"`
	Candidate evaluation.Run `json:"candidate"`
}

func (h *EvaluationHandler) Compare(w http.ResponseWriter, r *http.Request) {
	var req compareEvaluationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Baseline.SchemaVersion != evaluation.SchemaVersion || req.Candidate.SchemaVersion != evaluation.SchemaVersion {
		http.Error(w, "unsupported or missing evaluation schema_version", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, evaluation.Compare(req.Baseline, req.Candidate))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
