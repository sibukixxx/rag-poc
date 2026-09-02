package extractor

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

// CSVLoader turns each data row into its own Page ("col: value | col:
// value ..."), so each row is independently retrievable rather than the
// whole file being one indivisible blob.
type CSVLoader struct{}

var _ knowledge.Loader = CSVLoader{}

func (CSVLoader) Supports(filename, mimeType string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".csv" || mimeType == "text/csv"
}

func (CSVLoader) Load(_ context.Context, data []byte, _ knowledge.FileMeta) ([]knowledge.Page, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("csv: reading header: %w", err)
	}

	var pages []knowledge.Page
	for rowNum := 1; ; rowNum++ {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv: reading row %d: %w", rowNum, err)
		}

		var parts []string
		for i, value := range record {
			key := fmt.Sprintf("col%d", i+1)
			if i < len(header) && header[i] != "" {
				key = header[i]
			}
			parts = append(parts, key+": "+value)
		}
		pages = append(pages, knowledge.Page{Number: rowNum, Text: strings.Join(parts, " | ")})
	}
	return pages, nil
}
