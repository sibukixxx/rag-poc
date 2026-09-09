package source_test

import (
	"context"
	"testing"

	adapter "github.com/sibukixxx/rag-poc/internal/adapter/source"
	domain "github.com/sibukixxx/rag-poc/internal/domain/source"
)

type connector struct{ provider string }

func (c connector) Provider() string { return c.provider }
func (c connector) Pull(context.Context, domain.Connection, string) (domain.Batch, error) {
	return domain.Batch{}, nil
}

func TestRegistryNormalizesProviderNames(t *testing.T) {
	registry := adapter.NewRegistry(connector{provider: "Slack"})
	if _, ok := registry.Get(" slack "); !ok {
		t.Fatal("registered connector was not found with normalized provider name")
	}
}
