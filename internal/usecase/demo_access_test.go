package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/demoaccess"
)

type memoryDemoStore struct {
	users    map[string]demoaccess.User
	sessions map[string]string
}

func newMemoryDemoStore() *memoryDemoStore {
	return &memoryDemoStore{users: map[string]demoaccess.User{}, sessions: map[string]string{}}
}

func (s *memoryDemoStore) CreateUser(_ context.Context, u demoaccess.User) error {
	if _, exists := s.users[u.Username]; exists {
		return errors.New("duplicate")
	}
	s.users[u.Username] = u
	return nil
}
func (s *memoryDemoStore) GetUserByUsername(_ context.Context, username string) (demoaccess.User, error) {
	u, ok := s.users[username]
	if !ok {
		return demoaccess.User{}, demoaccess.ErrNotFound
	}
	return u, nil
}
func (s *memoryDemoStore) ListUsers(context.Context) ([]demoaccess.User, error) { return nil, nil }
func (s *memoryDemoStore) DisableUser(_ context.Context, username string, at time.Time) error {
	u, ok := s.users[username]
	if !ok {
		return demoaccess.ErrNotFound
	}
	u.DisabledAt = &at
	s.users[username] = u
	return s.DeleteSessionsForUser(context.Background(), u.ID)
}
func (s *memoryDemoStore) CreateSession(_ context.Context, tokenHash, userID string, _ time.Time) error {
	s.sessions[tokenHash] = userID
	return nil
}
func (s *memoryDemoStore) GetUserBySession(_ context.Context, tokenHash string, _ time.Time) (demoaccess.User, error) {
	id, ok := s.sessions[tokenHash]
	if !ok {
		return demoaccess.User{}, demoaccess.ErrNotFound
	}
	for _, u := range s.users {
		if u.ID == id {
			return u, nil
		}
	}
	return demoaccess.User{}, demoaccess.ErrNotFound
}
func (s *memoryDemoStore) DeleteSession(_ context.Context, tokenHash string) error {
	delete(s.sessions, tokenHash)
	return nil
}
func (s *memoryDemoStore) DeleteSessionsForUser(_ context.Context, userID string) error {
	for token, id := range s.sessions {
		if id == userID {
			delete(s.sessions, token)
		}
	}
	return nil
}

func TestDemoAccessLifecycle(t *testing.T) {
	store := newMemoryDemoStore()
	uc := NewDemoAccessUseCase(store, time.Hour)
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	uc.now = func() time.Time { return now }

	user, password, err := uc.CreateUser(context.Background(), "demo-customer", "USER@EXAMPLE.COM", "Example Inc.", now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "user@example.com" || password == "" || string(user.PasswordHash) == password {
		t.Fatalf("unexpected issued account: %#v", user)
	}

	_, _, _, err = uc.Authenticate(context.Background(), user.Username, "wrong-password", user.Email)
	if !errors.Is(err, demoaccess.ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
	_, _, _, err = uc.Authenticate(context.Background(), user.Username, password, "other@example.com")
	if !errors.Is(err, demoaccess.ErrInvalidCredentials) {
		t.Fatalf("mismatched Access email error = %v", err)
	}

	loggedIn, token, expiresAt, err := uc.Authenticate(context.Background(), user.Username, password, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	if loggedIn.ID != user.ID || token == "" || !expiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected login result: %#v %q %s", loggedIn, token, expiresAt)
	}
	current, err := uc.CurrentUser(context.Background(), token, user.Email)
	if err != nil || current.ID != user.ID {
		t.Fatalf("current user = %#v, %v", current, err)
	}
	if err := store.DisableUser(context.Background(), user.Username, now); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.CurrentUser(context.Background(), token, user.Email); !errors.Is(err, demoaccess.ErrInvalidCredentials) {
		t.Fatalf("revoked session error = %v", err)
	}
}

func TestDemoPasswordHash(t *testing.T) {
	hash, err := hashDemoPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyDemoPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password did not verify")
	}
	if verifyDemoPassword(hash, "incorrect") {
		t.Fatal("incorrect password verified")
	}
}
