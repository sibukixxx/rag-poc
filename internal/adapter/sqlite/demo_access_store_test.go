package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/demoaccess"
)

func TestDemoAccessStoreRevokesSessions(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewDemoAccessStore(db)
	now := time.Now().UTC().Truncate(time.Second)
	user := demoaccess.User{
		ID: "user-1", Username: "demo-one", Email: "one@example.com", Company: "Example",
		PasswordHash: []byte("hash"), ExpiresAt: now.Add(time.Hour),
	}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(context.Background(), "token-hash", user.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetUserBySession(context.Background(), "token-hash", now)
	if err != nil || got.Username != user.Username {
		t.Fatalf("GetUserBySession = %#v, %v", got, err)
	}
	if err := store.DisableUser(context.Background(), user.Username, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetUserBySession(context.Background(), "token-hash", now); !errors.Is(err, demoaccess.ErrNotFound) {
		t.Fatalf("session after revoke error = %v", err)
	}
}
