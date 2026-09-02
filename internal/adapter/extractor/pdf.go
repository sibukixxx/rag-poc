package extractor

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

// PDFLoader is explicitly best-effort (docs/DESIGN_REVIEW.md F-5): pure-Go
// PDF text extraction struggles with tables, multi-column layouts, and
// scanned/image-only pages. A page that fails to extract is skipped
// rather than failing the whole document — partial results beat none.
// For anything beyond simple single-column PDFs, converting to Markdown
// before ingest (via an external converter Loader) is the supported path.
type PDFLoader struct{}

var _ knowledge.Loader = PDFLoader{}

func (PDFLoader) Supports(filename, mimeType string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".pdf" || mimeType == "application/pdf"
}

func (PDFLoader) Load(_ context.Context, data []byte, _ knowledge.FileMeta) ([]knowledge.Page, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	var pages []knowledge.Page
	for i := 1; i <= reader.NumPage(); i++ {
		p := reader.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			// Best-effort: a page that fails to extract (e.g. scanned
			// image, unsupported encoding) is skipped, not fatal.
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		pages = append(pages, knowledge.Page{Number: i, Text: text})
	}
	return pages, nil
}
