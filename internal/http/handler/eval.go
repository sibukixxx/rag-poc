package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sibukixxx/rag-poc/internal/domain/eval"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

type EvalHandler struct {
	datasets eval.Store
	eval     *usecase.EvaluationUseCase
}

func NewEvalHandler(datasets eval.Store, evalUC *usecase.EvaluationUseCase) *EvalHandler {
	return &EvalHandler{datasets: datasets, eval: evalUC}
}

type datasetDTO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
}

func toDatasetDTO(d eval.Dataset) datasetDTO {
	return datasetDTO{ID: d.ID, Name: d.Name, KnowledgeBaseID: d.KnowledgeBaseID}
}

type caseDTO struct {
	ID                string   `json:"id"`
	Query             string   `json:"query"`
	ExpectedFilenames []string `json:"expected_filenames"`
}

func toCaseDTO(c eval.Case) caseDTO {
	return caseDTO{ID: c.ID, Query: c.Query, ExpectedFilenames: c.ExpectedFilenames}
}

// CreateDataset handles POST /api/v1/datasets ({name, knowledge_base_id}).
// Like knowledge base creation, a POST for an existing name is idempotent
// and simply returns that dataset.
func (h *EvalHandler) CreateDataset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		KnowledgeBaseID string `json:"knowledge_base_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name must not be empty", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.KnowledgeBaseID) == "" {
		http.Error(w, "knowledge_base_id must not be empty", http.StatusBadRequest)
		return
	}
	if len(req.Name) > maxNameLen {
		http.Error(w, "name too long", http.StatusBadRequest)
		return
	}

	d, err := h.datasets.EnsureDataset(r.Context(), req.Name, req.KnowledgeBaseID)
	if err != nil {
		internalError(w, "creating dataset", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toDatasetDTO(*d))
}

func (h *EvalHandler) ListDatasets(w http.ResponseWriter, r *http.Request) {
	datasets, err := h.datasets.ListDatasets(r.Context())
	if err != nil {
		internalError(w, "listing datasets", err)
		return
	}
	out := make([]datasetDTO, len(datasets))
	for i, d := range datasets {
		out[i] = toDatasetDTO(d)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// ImportCases handles POST /api/v1/datasets/{id}/cases. It accepts either
// a JSON body (array of cases, or {"cases": [...]}) or a multipart upload
// with a CSV "file" field (docs/ROADMAP.md W7: "JSON / CSV インポート").
func (h *EvalHandler) ImportCases(w http.ResponseWriter, r *http.Request) {
	datasetID := chi.URLParam(r, "id")
	if _, err := h.datasets.GetDataset(r.Context(), datasetID); err != nil {
		http.Error(w, "dataset not found", http.StatusNotFound)
		return
	}

	contentType := r.Header.Get("Content-Type")
	var cases []eval.Case
	var err error

	if strings.HasPrefix(strings.ToLower(contentType), "multipart/") {
		if parseErr := r.ParseMultipartForm(multipartMemoryLimit); parseErr != nil {
			http.Error(w, "malformed multipart body", http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()

		file, _, ferr := r.FormFile("file")
		if ferr != nil {
			http.Error(w, "expected a multipart 'file' field", http.StatusBadRequest)
			return
		}
		defer file.Close()

		data, rerr := io.ReadAll(file)
		if rerr != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(rerr, &tooLarge) {
				http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
				return
			}
			internalError(w, "reading dataset import upload", rerr)
			return
		}
		cases, err = usecase.ParseDatasetCasesCSV(data)
	} else {
		body, rerr := io.ReadAll(r.Body)
		if rerr != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(rerr, &tooLarge) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		cases, err = usecase.ParseDatasetCasesJSON(body)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	created, err := h.datasets.AddCases(r.Context(), datasetID, cases)
	if err != nil {
		internalError(w, "importing dataset cases", err)
		return
	}

	out := make([]caseDTO, len(created))
	for i, c := range created {
		out[i] = toCaseDTO(c)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"cases": out})
}

func (h *EvalHandler) ListCases(w http.ResponseWriter, r *http.Request) {
	datasetID := chi.URLParam(r, "id")
	cases, err := h.datasets.ListCases(r.Context(), datasetID)
	if err != nil {
		internalError(w, "listing dataset cases", err)
		return
	}
	out := make([]caseDTO, len(cases))
	for i, c := range cases {
		out[i] = toCaseDTO(c)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

type runDTO struct {
	ID           string  `json:"id"`
	DatasetID    string  `json:"dataset_id"`
	Status       string  `json:"status"`
	Error        string  `json:"error,omitempty"`
	TopK         int     `json:"top_k"`
	Rerank       bool    `json:"rerank"`
	RecallAtK    float64 `json:"recall_at_k"`
	PrecisionAtK float64 `json:"precision_at_k"`
	MRR          float64 `json:"mrr"`
	HitRate      float64 `json:"hit_rate"`
	StartedAt    string  `json:"started_at"`
	FinishedAt   *string `json:"finished_at,omitempty"`
}

func toRunDTO(r eval.Run) runDTO {
	dto := runDTO{
		ID: r.ID, DatasetID: r.DatasetID, Status: string(r.Status), Error: r.Error,
		TopK: r.TopK, Rerank: r.Rerank, RecallAtK: r.RecallAtK, PrecisionAtK: r.PrecisionAtK,
		MRR: r.MRR, HitRate: r.HitRate, StartedAt: r.StartedAt.Format(time.RFC3339Nano),
	}
	if r.FinishedAt != nil {
		s := r.FinishedAt.Format(time.RFC3339Nano)
		dto.FinishedAt = &s
	}
	return dto
}

type caseResultDTO struct {
	CaseID             string   `json:"case_id"`
	RetrievedFilenames []string `json:"retrieved_filenames"`
	RecallAtK          float64  `json:"recall_at_k"`
	PrecisionAtK       float64  `json:"precision_at_k"`
	ReciprocalRank     float64  `json:"reciprocal_rank"`
	Hit                bool     `json:"hit"`
	Error              string   `json:"error,omitempty"`
}

func toCaseResultDTO(r eval.CaseResult) caseResultDTO {
	return caseResultDTO{
		CaseID: r.CaseID, RetrievedFilenames: r.RetrievedFilenames, RecallAtK: r.RecallAtK,
		PrecisionAtK: r.PrecisionAtK, ReciprocalRank: r.ReciprocalRank, Hit: r.Hit, Error: r.Error,
	}
}

// CreateEvaluation handles POST /api/v1/evaluations ({dataset_id, top_k,
// rerank}). The run starts in the background (docs/V0.1_SPEC.md §6: "run
// 開始（非同期）") and the response carries just the pending run so the
// caller can poll GetEvaluation for progress.
func (h *EvalHandler) CreateEvaluation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DatasetID string `json:"dataset_id"`
		TopK      int    `json:"top_k"`
		Rerank    bool   `json:"rerank"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.DatasetID) == "" {
		http.Error(w, "dataset_id must not be empty", http.StatusBadRequest)
		return
	}
	if _, err := h.datasets.GetDataset(r.Context(), req.DatasetID); err != nil {
		http.Error(w, "dataset not found", http.StatusNotFound)
		return
	}

	run, err := h.eval.CreateRun(r.Context(), req.DatasetID, req.TopK, req.Rerank)
	if err != nil {
		internalError(w, "creating evaluation run", err)
		return
	}

	// Run detached from the request context: the HTTP response returns
	// immediately, but scoring 50+ cases can take longer than a client is
	// willing to hold a connection open for.
	runID := run.ID
	go func() {
		if err := h.eval.Execute(context.Background(), runID); err != nil {
			log.Printf("eval: run %s failed: %v", runID, err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(toRunDTO(*run))
}

// GetEvaluation handles GET /api/v1/evaluations/{id} -> the run's current
// status/metrics plus any case results recorded so far.
func (h *EvalHandler) GetEvaluation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, err := h.datasets.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "evaluation run not found", http.StatusNotFound)
		return
	}
	results, err := h.datasets.ListCaseResults(r.Context(), id)
	if err != nil {
		internalError(w, "loading evaluation results", err)
		return
	}
	out := make([]caseResultDTO, len(results))
	for i, cr := range results {
		out[i] = toCaseResultDTO(cr)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"run": toRunDTO(*run), "results": out})
}

// ListEvaluations handles GET /api/v1/evaluations?dataset_id=.
func (h *EvalHandler) ListEvaluations(w http.ResponseWriter, r *http.Request) {
	datasetID := r.URL.Query().Get("dataset_id")
	if strings.TrimSpace(datasetID) == "" {
		http.Error(w, "dataset_id query parameter is required", http.StatusBadRequest)
		return
	}
	runs, err := h.datasets.ListRuns(r.Context(), datasetID)
	if err != nil {
		internalError(w, "listing evaluation runs", err)
		return
	}
	out := make([]runDTO, len(runs))
	for i, run := range runs {
		out[i] = toRunDTO(run)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
