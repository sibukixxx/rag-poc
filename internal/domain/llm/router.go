package llm

import "fmt"

// Alias maps a business-facing name (cheap/normal/judge) to a concrete
// provider + model, so workflows and evaluation code reference the alias
// only and never hardcode a model name (docs/V0.1_SPEC.md §4).
type Alias struct {
	Provider string
	Model    string
}

// Router resolves aliases to a registered provider LLM + concrete model.
type Router struct {
	providers map[string]LLM
	aliases   map[string]Alias
}

func NewRouter() *Router {
	return &Router{
		providers: make(map[string]LLM),
		aliases:   make(map[string]Alias),
	}
}

func (r *Router) RegisterProvider(name string, provider LLM) {
	r.providers[name] = provider
}

func (r *Router) RegisterAlias(alias string, target Alias) {
	r.aliases[alias] = target
}

// Resolve returns the LLM registered for the alias's provider, and the
// concrete model name to pass in GenerateRequest.Model.
func (r *Router) Resolve(alias string) (LLM, string, error) {
	target, ok := r.aliases[alias]
	if !ok {
		return nil, "", fmt.Errorf("llm: unknown alias %q", alias)
	}
	provider, ok := r.providers[target.Provider]
	if !ok {
		return nil, "", fmt.Errorf("llm: alias %q references unregistered provider %q", alias, target.Provider)
	}
	return provider, target.Model, nil
}

// HasAlias reports whether an alias is registered, without requiring its
// provider to be resolvable (used by `forgeai doctor`).
func (r *Router) HasAlias(alias string) (Alias, bool) {
	target, ok := r.aliases[alias]
	return target, ok
}
