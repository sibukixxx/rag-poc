// Package source provides provider-neutral connector registration. Concrete
// connectors live in sibling adapter packages and register themselves during
// application wiring.
package source

import (
	"strings"
	"sync"

	domain "github.com/sibukixxx/rag-poc/internal/domain/source"
)

type Registry struct {
	mu         sync.RWMutex
	connectors map[string]domain.Connector
}

var _ domain.Registry = (*Registry)(nil)

func NewRegistry(connectors ...domain.Connector) *Registry {
	r := &Registry{connectors: make(map[string]domain.Connector)}
	for _, connector := range connectors {
		r.Register(connector)
	}
	return r
}

func (r *Registry) Register(connector domain.Connector) {
	if connector == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connectors[normalize(connector.Provider())] = connector
}

func (r *Registry) Get(provider string) (domain.Connector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	connector, ok := r.connectors[normalize(provider)]
	return connector, ok
}

func normalize(provider string) string { return strings.ToLower(strings.TrimSpace(provider)) }
