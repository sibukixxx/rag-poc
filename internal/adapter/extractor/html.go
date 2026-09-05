package extractor

import (
	"context"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

// HTMLLoader strips tags/scripts/styles and keeps visible text, one Page
// per file (HTML has no natural pagination for our purposes).
type HTMLLoader struct{}

var _ knowledge.Loader = HTMLLoader{}

func (HTMLLoader) Supports(filename, mimeType string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".html" || ext == ".htm" || mimeType == "text/html"
}

func (HTMLLoader) Load(_ context.Context, data []byte, _ knowledge.FileMeta) ([]knowledge.Page, error) {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}

	// Iterative depth-first walk with an explicit stack: x/net/html does not
	// cap nesting depth, so a file of a few MB of "<div>" would overflow the
	// goroutine stack with a recursive walk (a fatal, unrecoverable error).
	var sb strings.Builder
	stack := []*html.Node{doc}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			continue
		}
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				sb.WriteString(text)
				sb.WriteString("\n")
			}
		}
		// Push children in reverse so they are visited in document order.
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}

	return []knowledge.Page{{Text: sb.String()}}, nil
}
