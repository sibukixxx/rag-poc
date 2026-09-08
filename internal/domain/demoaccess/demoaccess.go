// Package demoaccess defines the customer-demo identity boundary.
package demoaccess

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound           = errors.New("demo user not found")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountUnavailable = errors.New("demo account is disabled or expired")
)

type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	Email        string     `json:"email"`
	Company      string     `json:"company"`
	PasswordHash []byte     `json:"-"`
	ExpiresAt    time.Time  `json:"expires_at"`
	DisabledAt   *time.Time `json:"disabled_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (u User) Available(now time.Time) bool {
	return u.DisabledAt == nil && now.Before(u.ExpiresAt)
}

type Store interface {
	CreateUser(ctx context.Context, user User) error
	GetUserByUsername(ctx context.Context, username string) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	DisableUser(ctx context.Context, username string, disabledAt time.Time) error
	CreateSession(ctx context.Context, tokenHash, userID string, expiresAt time.Time) error
	GetUserBySession(ctx context.Context, tokenHash string, now time.Time) (User, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteSessionsForUser(ctx context.Context, userID string) error
}
