package extractor

import "github.com/sibukixxx/rag-poc/internal/domain/knowledge"

// Registry dispatches to the first Loader that supports a file. Order
// matters only in that built-ins are tried in registration order; an
// external converter registered before the built-ins would take priority
// for formats it also supports (docs/DESIGN_REVIEW.md F-5).
type Registry struct {
	loaders []knowledge.Loader
}

// NewDefaultRegistry returns a Registry with all built-in loaders
// (TXT/MD, HTML, CSV, JSON, best-effort PDF).
func NewDefaultRegistry() *Registry {
	return &Registry{loaders: []knowledge.Loader{
		TextLoader{},
		HTMLLoader{},
		CSVLoader{},
		JSONLoader{},
		PDFLoader{},
	}}
}

func (r *Registry) Register(l knowledge.Loader) {
	r.loaders = append(r.loaders, l)
}

func (r *Registry) Find(filename, mimeType string) (knowledge.Loader, bool) {
	for _, l := range r.loaders {
		if l.Supports(filename, mimeType) {
			return l, true
		}
	}
	return nil, false
}
