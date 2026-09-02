// Package extractor implements the built-in knowledge.Loader
// implementations: plain text/Markdown, HTML, CSV, JSON, and best-effort
// PDF (docs/DESIGN_REVIEW.md F-5). An external converter (docling, etc.)
// can be added later as another Loader without touching the ingestion
// usecase.
package extractor

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

// TextLoader handles formats that are already plain text: .txt and .md.
// Markdown is not rendered — its raw text (headings, lists, etc. as
// written) is what gets embedded, which is fine for retrieval.
type TextLoader struct{}

var _ knowledge.Loader = TextLoader{}

func (TextLoader) Supports(filename, mimeType string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".txt" || ext == ".md" || ext == ".markdown" ||
		mimeType == "text/plain" || mimeType == "text/markdown"
}

func (TextLoader) Load(_ context.Context, data []byte, _ knowledge.FileMeta) ([]knowledge.Page, error) {
	return []knowledge.Page{{Text: string(data)}}, nil
}
