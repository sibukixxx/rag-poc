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

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			return
		}
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				sb.WriteString(text)
				sb.WriteString("\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	return []knowledge.Page{{Text: sb.String()}}, nil
}
