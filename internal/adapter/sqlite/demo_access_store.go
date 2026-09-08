package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/demoaccess"
)

type DemoAccessStore struct{ db *sql.DB }

func NewDemoAccessStore(db *sql.DB) *DemoAccessStore { return &DemoAccessStore{db: db} }

func (s *DemoAccessStore) CreateUser(ctx context.Context, u demoaccess.User) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO demo_users
		(id, username, email, company, password_hash, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.Email, u.Company, u.PasswordHash, formatDemoTime(u.ExpiresAt))
	if err != nil {
		return fmt.Errorf("creating demo user: %w", err)
	}
	return nil
}

func (s *DemoAccessStore) GetUserByUsername(ctx context.Context, username string) (demoaccess.User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, email, company, password_hash,
		expires_at, disabled_at, created_at FROM demo_users WHERE username = ?`, username)
	return scanDemoUser(row)
}

func (s *DemoAccessStore) ListUsers(ctx context.Context) ([]demoaccess.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, email, company, password_hash,
		expires_at, disabled_at, created_at FROM demo_users ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing demo users: %w", err)
	}
	defer rows.Close()
	var users []demoaccess.User
	for rows.Next() {
		u, err := scanDemoUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *DemoAccessStore) DisableUser(ctx context.Context, username string, disabledAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("disabling demo user: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE demo_users SET disabled_at = ? WHERE username = ?`, formatDemoTime(disabledAt), username)
	if err != nil {
		return fmt.Errorf("disabling demo user: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking disabled demo user: %w", err)
	}
	if n == 0 {
		return demoaccess.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM demo_sessions WHERE user_id = (SELECT id FROM demo_users WHERE username = ?)`, username); err != nil {
		return fmt.Errorf("revoking demo sessions: %w", err)
	}
	return tx.Commit()
}

func (s *DemoAccessStore) CreateSession(ctx context.Context, tokenHash, userID string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO demo_sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash, userID, formatDemoTime(expiresAt))
	if err != nil {
		return fmt.Errorf("creating demo session: %w", err)
	}
	return nil
}

func (s *DemoAccessStore) GetUserBySession(ctx context.Context, tokenHash string, now time.Time) (demoaccess.User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.email, u.company, u.password_hash,
		u.expires_at, u.disabled_at, u.created_at
		FROM demo_sessions s JOIN demo_users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, formatDemoTime(now))
	return scanDemoUser(row)
}

func (s *DemoAccessStore) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM demo_sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *DemoAccessStore) DeleteSessionsForUser(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM demo_sessions WHERE user_id = ?`, userID)
	return err
}

type demoUserScanner interface{ Scan(dest ...any) error }

func scanDemoUser(row demoUserScanner) (demoaccess.User, error) {
	var u demoaccess.User
	var expiresAt, createdAt string
	var disabledAt sql.NullString
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.Company, &u.PasswordHash, &expiresAt, &disabledAt, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return demoaccess.User{}, demoaccess.ErrNotFound
		}
		return demoaccess.User{}, fmt.Errorf("scanning demo user: %w", err)
	}
	var err error
	if u.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt); err != nil {
		return demoaccess.User{}, fmt.Errorf("parsing demo user expiry: %w", err)
	}
	if u.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return demoaccess.User{}, fmt.Errorf("parsing demo user creation: %w", err)
	}
	if disabledAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, disabledAt.String)
		if err != nil {
			return demoaccess.User{}, fmt.Errorf("parsing demo user disabled time: %w", err)
		}
		u.DisabledAt = &t
	}
	return u, nil
}

func formatDemoTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
