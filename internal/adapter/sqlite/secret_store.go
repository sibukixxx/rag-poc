package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sibukixxx/rag-poc/internal/adapter/crypto"
	"github.com/sibukixxx/rag-poc/internal/domain/secret"
)

// SecretStore persists secrets in the `secrets` table, encrypted with a
// SecretBox so plaintext never touches disk.
type SecretStore struct {
	db  *sql.DB
	box *crypto.SecretBox
}

var _ secret.Store = (*SecretStore)(nil)

func NewSecretStore(db *sql.DB, box *crypto.SecretBox) *SecretStore {
	return &SecretStore{db: db, box: box}
}

func (s *SecretStore) Set(ctx context.Context, name string, value []byte) error {
	ciphertext, nonce, err := s.box.Seal(value)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO secrets (name, ciphertext, nonce, updated_at)
		VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		ON CONFLICT(name) DO UPDATE SET
			ciphertext = excluded.ciphertext,
			nonce = excluded.nonce,
			updated_at = excluded.updated_at
	`, name, ciphertext, nonce)
	if err != nil {
		return fmt.Errorf("storing secret %s: %w", name, err)
	}
	return nil
}

func (s *SecretStore) Get(ctx context.Context, name string) ([]byte, error) {
	var ciphertext, nonce []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT ciphertext, nonce FROM secrets WHERE name = ?`, name,
	).Scan(&ciphertext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("secret %s: %w", name, sql.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("loading secret %s: %w", name, err)
	}
	return s.box.Open(ciphertext, nonce)
}

func (s *SecretStore) Delete(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, name); err != nil {
		return fmt.Errorf("deleting secret %s: %w", name, err)
	}
	return nil
}
