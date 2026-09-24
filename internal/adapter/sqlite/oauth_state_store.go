package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/oauth"
)

// OAuthStateStore keeps pending authorizations between the redirect to the
// provider and its callback. Only the hash of the state is stored.
type OAuthStateStore struct {
	db *sql.DB
}

var _ oauth.StateStore = (*OAuthStateStore)(nil)

func NewOAuthStateStore(db *sql.DB) *OAuthStateStore {
	return &OAuthStateStore{db: db}
}

func (s *OAuthStateStore) SavePending(ctx context.Context, p oauth.PendingAuthorization) error {
	now := time.Now().UTC()
	// Opportunistically drop expired entries so abandoned consents do not accumulate.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM oauth_states WHERE expires_at < ?`, now.Format(timeLayout)); err != nil {
		return fmt.Errorf("pruning oauth states: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO oauth_states (state_hash, connection_id, provider, code_verifier, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, p.StateHash, p.ConnectionID, p.Provider, p.CodeVerifier,
		p.ExpiresAt.UTC().Format(timeLayout), now.Format(timeLayout))
	if err != nil {
		return fmt.Errorf("saving oauth state: %w", err)
	}
	return nil
}

func (s *OAuthStateStore) TakePending(ctx context.Context, stateHash string) (*oauth.PendingAuthorization, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var p oauth.PendingAuthorization
	var expires string
	err = tx.QueryRowContext(ctx, `SELECT state_hash, connection_id, provider, code_verifier, expires_at FROM oauth_states WHERE state_hash = ?`,
		stateHash).Scan(&p.StateHash, &p.ConnectionID, &p.Provider, &p.CodeVerifier, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, oauth.ErrInvalidState
	}
	if err != nil {
		return nil, fmt.Errorf("loading oauth state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM oauth_states WHERE state_hash = ?`, stateHash); err != nil {
		return nil, err
	}
	p.ExpiresAt, _ = time.Parse(timeLayout, expires)
	return &p, tx.Commit()
}
