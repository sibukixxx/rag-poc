package extractor

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

// maxPDFPages bounds the page loop: the library trusts the file's own
// /Count, and a hostile PDF can claim billions of pages.
const maxPDFPages = 2000

// PDFLoader is explicitly best-effort (docs/DESIGN_REVIEW.md F-5): pure-Go
// PDF text extraction struggles with tables, multi-column layouts, and
// scanned/image-only pages. A page that fails to extract is skipped
// rather than failing the whole document — partial results beat none.
// For anything beyond simple single-column PDFs, converting to Markdown
// before ingest (via an external converter Loader) is the supported path.
//
// The parser is not hardened against malicious input: it can panic (nested
// arrays, self-referencing object streams) or loop on crafted xref chains.
// Panics are recovered here and turned into an error; the caller bounds
// wall-clock time via ctx (see usecase.IngestUseCase).
type PDFLoader struct{}

var _ knowledge.Loader = PDFLoader{}

func (PDFLoader) Supports(filename, mimeType string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".pdf" || mimeType == "application/pdf"
}

func (PDFLoader) Load(ctx context.Context, data []byte, _ knowledge.FileMeta) (pages []knowledge.Page, err error) {
	defer func() {
		if r := recover(); r != nil {
			pages, err = nil, fmt.Errorf("pdf: malformed input: %v", r)
		}
	}()

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	n := reader.NumPage()
	if n < 0 || n > maxPDFPages {
		return nil, fmt.Errorf("pdf: page count %d out of range (max %d)", n, maxPDFPages)
	}

	for i := 1; i <= n; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
