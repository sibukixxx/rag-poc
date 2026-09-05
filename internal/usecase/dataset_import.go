package usecase

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sibukixxx/rag-poc/internal/domain/eval"
)

// datasetCaseJSON mirrors the JSON shape of one imported case: a query and
// the filenames (within the dataset's knowledge base) a good retrieval
// result should surface.
type datasetCaseJSON struct {
	Query             string   `json:"query"`
	ExpectedFilenames []string `json:"expected_filenames"`
}

// ParseDatasetCasesJSON accepts either a bare JSON array of cases or an
// object of the form {"cases": [...]} (docs/ROADMAP.md W7: "JSON / CSV
// インポート").
func ParseDatasetCasesJSON(data []byte) ([]eval.Case, error) {
	var raw []datasetCaseJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		var wrapper struct {
			Cases []datasetCaseJSON `json:"cases"`
		}
		if err2 := json.Unmarshal(data, &wrapper); err2 != nil {
			return nil, fmt.Errorf("invalid JSON (expected an array of cases, or {\"cases\": [...]}): %w", err)
		}
		raw = wrapper.Cases
	}

	cases := make([]eval.Case, 0, len(raw))
	for i, c := range raw {
		validated, err := validateCase(c.Query, c.ExpectedFilenames)
		if err != nil {
			return nil, fmt.Errorf("case %d: %w", i, err)
		}
		cases = append(cases, validated)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no cases found in JSON")
	}
	return cases, nil
}

// ParseDatasetCasesCSV expects a header row with "query" and
// "expected_filenames" columns; expected_filenames holds one or more
// filenames separated by "|" (a plain comma would collide with the CSV
// delimiter itself, e.g. "returns.md|faq.md").
func ParseDatasetCasesCSV(data []byte) ([]eval.Case, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err == io.EOF {
		return nil, fmt.Errorf("CSV has no header row")
	}
	if err != nil {
		return nil, fmt.Errorf("reading CSV header: %w", err)
	}

	queryCol, expectedCol := -1, -1
	for i, h := range header {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "query":
			queryCol = i
		case "expected_filenames", "expected_documents":
			expectedCol = i
		}
	}
	if queryCol == -1 || expectedCol == -1 {
		return nil, fmt.Errorf(`CSV header must include "query" and "expected_filenames" columns`)
	}

	var cases []eval.Case
	for row := 1; ; row++ {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading CSV row %d: %w", row, err)
		}
		if queryCol >= len(record) || expectedCol >= len(record) {
			return nil, fmt.Errorf("row %d: missing query/expected_filenames column", row)
		}

		var filenames []string
		for _, f := range strings.Split(record[expectedCol], "|") {
			if f = strings.TrimSpace(f); f != "" {
				filenames = append(filenames, f)
			}
		}
		validated, err := validateCase(record[queryCol], filenames)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", row, err)
		}
		cases = append(cases, validated)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no cases found in CSV")
	}
	return cases, nil
}

func validateCase(query string, expectedFilenames []string) (eval.Case, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return eval.Case{}, fmt.Errorf("query must not be empty")
	}
	var filenames []string
	for _, f := range expectedFilenames {
		if f = strings.TrimSpace(f); f != "" {
			filenames = append(filenames, f)
		}
	}
	if len(filenames) == 0 {
		return eval.Case{}, fmt.Errorf("expected_filenames must contain at least one filename")
	}
	return eval.Case{Query: query, ExpectedFilenames: filenames}, nil
}
