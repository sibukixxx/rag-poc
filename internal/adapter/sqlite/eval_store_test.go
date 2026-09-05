package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/eval"
)

func openEvalStore(t *testing.T) (*sqlite.EvalStore, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "forgeai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	kb, err := sqlite.NewKnowledgeStore(db).EnsureKnowledgeBase(t.Context(), "Demo", "demo")
	if err != nil {
		t.Fatalf("EnsureKnowledgeBase: %v", err)
	}
	return sqlite.NewEvalStore(db), kb.ID
}

func TestDatasetAndCasesRoundTrip(t *testing.T) {
	store, kbID := openEvalStore(t)
	ctx := t.Context()

	ds, err := store.EnsureDataset(ctx, "demo-golden", kbID)
	if err != nil {
		t.Fatalf("EnsureDataset: %v", err)
	}
	if ds.KnowledgeBaseID != kbID {
		t.Errorf("expected KnowledgeBaseID %s, got %s", kbID, ds.KnowledgeBaseID)
	}

	byName, err := store.GetDatasetByName(ctx, "demo-golden")
	if err != nil {
		t.Fatalf("GetDatasetByName: %v", err)
	}
	if byName.ID != ds.ID {
		t.Errorf("expected same dataset by name, got different id")
	}

	cases := []eval.Case{
		{Query: "返品規定について教えて", ExpectedFilenames: []string{"returns.md"}},
		{Query: "配送にかかる日数は？", ExpectedFilenames: []string{"shipping.md", "faq.md"}},
	}
	created, err := store.AddCases(ctx, ds.ID, cases)
	if err != nil {
		t.Fatalf("AddCases: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 created cases, got %d", len(created))
	}

	listed, err := store.ListCases(ctx, ds.ID)
	if err != nil {
		t.Fatalf("ListCases: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(listed))
	}
	if listed[1].Query != "配送にかかる日数は？" {
		t.Errorf("unexpected query for second case: %q", listed[1].Query)
	}
	if len(listed[1].ExpectedFilenames) != 2 || listed[1].ExpectedFilenames[0] != "shipping.md" {
		t.Errorf("expected_filenames did not round-trip: %+v", listed[1].ExpectedFilenames)
	}

	all, err := store.ListDatasets(ctx)
	if err != nil {
		t.Fatalf("ListDatasets: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 dataset, got %d", len(all))
	}
}

func TestEvaluationRunAndCaseResultsRoundTrip(t *testing.T) {
	store, kbID := openEvalStore(t)
	ctx := t.Context()

	ds, err := store.EnsureDataset(ctx, "demo-golden", kbID)
	if err != nil {
		t.Fatalf("EnsureDataset: %v", err)
	}
	cases, err := store.AddCases(ctx, ds.ID, []eval.Case{{Query: "q1", ExpectedFilenames: []string{"a.md"}}})
	if err != nil {
		t.Fatalf("AddCases: %v", err)
	}

	run := eval.Run{ID: "run-1", DatasetID: ds.ID, Status: eval.RunStatusPending, TopK: 10, Rerank: true, StartedAt: time.Now()}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	loaded, err := store.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if loaded.Status != eval.RunStatusPending {
		t.Errorf("expected status pending, got %s", loaded.Status)
	}
	if !loaded.Rerank {
		t.Error("expected Rerank to round-trip as true")
	}
	if loaded.FinishedAt != nil {
		t.Error("expected FinishedAt to be nil before the run finishes")
	}

	run.Status = eval.RunStatusDone
	run.RecallAtK, run.PrecisionAtK, run.MRR, run.HitRate = 1, 1, 1, 1
	finished := run.StartedAt
	run.FinishedAt = &finished
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}

	loaded, err = store.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRun after update: %v", err)
	}
	if loaded.Status != eval.RunStatusDone {
		t.Errorf("expected status done, got %s", loaded.Status)
	}
	if loaded.HitRate != 1 {
		t.Errorf("expected HitRate 1, got %v", loaded.HitRate)
	}
	if loaded.FinishedAt == nil {
		t.Error("expected FinishedAt to be set after the run finishes")
	}

	if err := store.CreateCaseResults(ctx, []eval.CaseResult{
		{RunID: "run-1", CaseID: cases[0].ID, RetrievedFilenames: []string{"a.md", "b.md"}, RecallAtK: 1, PrecisionAtK: 0.5, ReciprocalRank: 1, Hit: true},
	}); err != nil {
		t.Fatalf("CreateCaseResults: %v", err)
	}

	results, err := store.ListCaseResults(ctx, "run-1")
	if err != nil {
		t.Fatalf("ListCaseResults: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 case result, got %d", len(results))
	}
	if !results[0].Hit {
		t.Error("expected Hit to round-trip as true")
	}
	if len(results[0].RetrievedFilenames) != 2 || results[0].RetrievedFilenames[1] != "b.md" {
		t.Errorf("retrieved_filenames did not round-trip: %+v", results[0].RetrievedFilenames)
	}

	runs, err := store.ListRuns(ctx, ds.ID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
}
