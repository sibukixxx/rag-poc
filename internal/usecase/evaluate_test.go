package usecase_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/eval"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// memEvalStore is a minimal in-memory eval.Store for exercising
// EvaluationUseCase without a real database.
type memEvalStore struct {
	mu      sync.Mutex
	dataset eval.Dataset
	cases   []eval.Case
	runs    map[string]eval.Run
	results map[string][]eval.CaseResult
}

func newMemEvalStore(dataset eval.Dataset, cases []eval.Case) *memEvalStore {
	return &memEvalStore{
		dataset: dataset, cases: cases,
		runs: map[string]eval.Run{}, results: map[string][]eval.CaseResult{},
	}
}

func (m *memEvalStore) EnsureDataset(ctx context.Context, name, kbID string) (*eval.Dataset, error) {
	return nil, nil
}
func (m *memEvalStore) GetDataset(ctx context.Context, id string) (*eval.Dataset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.dataset
	return &d, nil
}
func (m *memEvalStore) GetDatasetByName(ctx context.Context, name string) (*eval.Dataset, error) {
	return m.GetDataset(ctx, "")
}
func (m *memEvalStore) ListDatasets(ctx context.Context) ([]eval.Dataset, error) {
	return []eval.Dataset{m.dataset}, nil
}
func (m *memEvalStore) AddCases(ctx context.Context, datasetID string, cases []eval.Case) ([]eval.Case, error) {
	return nil, nil
}
func (m *memEvalStore) ListCases(ctx context.Context, datasetID string) ([]eval.Case, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cases, nil
}
func (m *memEvalStore) CreateRun(ctx context.Context, run eval.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[run.ID] = run
	return nil
}
func (m *memEvalStore) UpdateRun(ctx context.Context, run eval.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[run.ID] = run
	return nil
}
func (m *memEvalStore) GetRun(ctx context.Context, id string) (*eval.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return nil, errNotFound
	}
	return &r, nil
}
func (m *memEvalStore) ListRuns(ctx context.Context, datasetID string) ([]eval.Run, error) {
	return nil, nil
}
func (m *memEvalStore) CreateCaseResults(ctx context.Context, results []eval.CaseResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range results {
		m.results[r.RunID] = append(m.results[r.RunID], r)
	}
	return nil
}
func (m *memEvalStore) ListCaseResults(ctx context.Context, runID string) ([]eval.CaseResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.results[runID], nil
}

// fakeQueryKeywordSearcher returns different results per query text, so
// EvaluationUseCase's per-case scoring can be exercised end to end through
// a real SearchUseCase (vector search is left empty; RRF then reduces to
// the keyword list's order).
type fakeQueryKeywordSearcher struct {
	byQuery map[string][]retrieval.Result
}

func (f *fakeQueryKeywordSearcher) Search(ctx context.Context, kbID, query string, topK int) ([]retrieval.Result, error) {
	return f.byQuery[query], nil
}

type emptyVectorSearcher struct{}

func (emptyVectorSearcher) Search(ctx context.Context, kbID string, vector []float32, topK int) ([]retrieval.Result, error) {
	return nil, nil
}

func TestEvaluationUseCaseComputesAggregateRetrievalMetrics(t *testing.T) {
	dataset := eval.Dataset{ID: "ds-1", Name: "demo-golden", KnowledgeBaseID: "kb-1"}
	cases := []eval.Case{
		{ID: "case-1", DatasetID: "ds-1", Query: "case1 query", ExpectedFilenames: []string{"doc-a.md"}},
		{ID: "case-2", DatasetID: "ds-1", Query: "case2 query", ExpectedFilenames: []string{"doc-c.md", "doc-d.md"}},
	}
	store := newMemEvalStore(dataset, cases)

	kw := &fakeQueryKeywordSearcher{byQuery: map[string][]retrieval.Result{
		"case1 query": {
			{ChunkID: uuid.NewString(), Filename: "doc-a.md"},
			{ChunkID: uuid.NewString(), Filename: "doc-b.md"},
		},
		"case2 query": {
			{ChunkID: uuid.NewString(), Filename: "doc-x.md"},
		},
	}}
	embedder := &fakeEmbedder{vector: []float32{1, 0}}
	search := usecase.NewSearchUseCase(emptyVectorSearcher{}, kw, embedder, nil, nil)

	uc := usecase.NewEvaluationUseCase(search, store, nil)

	run, err := uc.CreateRun(context.Background(), dataset.ID, 10, false)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := uc.Execute(context.Background(), run.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	final, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if final.Status != eval.RunStatusDone {
		t.Fatalf("expected run to finish done, got %s (error: %s)", final.Status, final.Error)
	}

	// case1: hit at rank 1 -> recall 1/1, precision 1/2, RR 1
	// case2: no hit -> recall 0, precision 0, RR 0
	wantRecall, wantPrecision, wantMRR, wantHitRate := 0.5, 0.25, 0.5, 0.5
	if final.RecallAtK != wantRecall {
		t.Errorf("RecallAtK = %v, want %v", final.RecallAtK, wantRecall)
	}
	if final.PrecisionAtK != wantPrecision {
		t.Errorf("PrecisionAtK = %v, want %v", final.PrecisionAtK, wantPrecision)
	}
	if final.MRR != wantMRR {
		t.Errorf("MRR = %v, want %v", final.MRR, wantMRR)
	}
	if final.HitRate != wantHitRate {
		t.Errorf("HitRate = %v, want %v", final.HitRate, wantHitRate)
	}
	if final.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}

	results, err := store.ListCaseResults(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListCaseResults: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 case results, got %d", len(results))
	}
}

func TestEvaluationUseCaseFailsRunWhenDatasetHasNoCases(t *testing.T) {
	dataset := eval.Dataset{ID: "ds-empty", Name: "empty", KnowledgeBaseID: "kb-1"}
	store := newMemEvalStore(dataset, nil)
	embedder := &fakeEmbedder{vector: []float32{1}}
	search := usecase.NewSearchUseCase(emptyVectorSearcher{}, &fakeQueryKeywordSearcher{}, embedder, nil, nil)
	uc := usecase.NewEvaluationUseCase(search, store, nil)

	run, err := uc.CreateRun(context.Background(), dataset.ID, 10, false)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := uc.Execute(context.Background(), run.ID); err == nil {
		t.Fatal("expected Execute to error for a dataset with no cases")
	}

	final, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if final.Status != eval.RunStatusFailed {
		t.Fatalf("expected run to be marked failed, got %s", final.Status)
	}
	if final.Error == "" {
		t.Error("expected a non-empty error message on the failed run")
	}
}
