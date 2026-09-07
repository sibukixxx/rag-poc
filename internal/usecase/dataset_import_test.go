package usecase_test

import (
	"testing"

	"github.com/sibukixxx/rag-poc/internal/usecase"
)

func TestParseDatasetCasesJSONBareArray(t *testing.T) {
	data := []byte(`[
		{"query": "返品規定について教えて", "expected_filenames": ["returns.md"]},
		{"query": "配送にかかる日数は？", "expected_filenames": ["shipping.md", "faq.md"], "expected_answer": "2〜4営業日"}
	]`)
	cases, err := usecase.ParseDatasetCasesJSON(data)
	if err != nil {
		t.Fatalf("ParseDatasetCasesJSON: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(cases))
	}
	if cases[1].ExpectedFilenames[1] != "faq.md" {
		t.Errorf("unexpected expected_filenames: %+v", cases[1].ExpectedFilenames)
	}
	if cases[1].ExpectedAnswer != "2〜4営業日" || cases[0].ExpectedAnswer != "" {
		t.Errorf("expected_answer not parsed: %+v", cases)
	}
}

func TestParseDatasetCasesJSONWrapper(t *testing.T) {
	data := []byte(`{"cases": [{"query": "q1", "expected_filenames": ["a.md"]}]}`)
	cases, err := usecase.ParseDatasetCasesJSON(data)
	if err != nil {
		t.Fatalf("ParseDatasetCasesJSON: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(cases))
	}
}

func TestParseDatasetCasesJSONRejectsMissingExpectedFilenames(t *testing.T) {
	data := []byte(`[{"query": "q1", "expected_filenames": []}]`)
	if _, err := usecase.ParseDatasetCasesJSON(data); err == nil {
		t.Fatal("expected an error for a case with no expected filenames")
	}
}

func TestParseDatasetCasesCSV(t *testing.T) {
	data := []byte("query,expected_filenames,expected_answer\n" +
		"返品規定について教えて,returns.md,到着後30日以内\n" +
		"配送にかかる日数は？,shipping.md|faq.md,\n")
	cases, err := usecase.ParseDatasetCasesCSV(data)
	if err != nil {
		t.Fatalf("ParseDatasetCasesCSV: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(cases))
	}
	if len(cases[1].ExpectedFilenames) != 2 || cases[1].ExpectedFilenames[0] != "shipping.md" {
		t.Errorf("unexpected expected_filenames: %+v", cases[1].ExpectedFilenames)
	}
	if cases[0].ExpectedAnswer != "到着後30日以内" || cases[1].ExpectedAnswer != "" {
		t.Errorf("expected_answer column not parsed: %+v", cases)
	}
}

func TestParseDatasetCasesCSVRejectsMissingHeader(t *testing.T) {
	data := []byte("question,docs\nq1,a.md\n")
	if _, err := usecase.ParseDatasetCasesCSV(data); err == nil {
		t.Fatal("expected an error for a CSV missing the required header columns")
	}
}
