// Package secret defines the storage-agnostic contract for encrypted
// secret values (API keys, etc.). Encryption itself lives in
// internal/adapter/crypto; persistence lives in internal/adapter/sqlite.
package secret

import "context"

// Store persists and retrieves secret values by name. Implementations
// must encrypt values at rest.
type Store interface {
	Set(ctx context.Context, name string, value []byte) error
	Get(ctx context.Context, name string) ([]byte, error)
	Delete(ctx context.Context, name string) error
}
