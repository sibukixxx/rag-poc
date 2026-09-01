package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/crypto"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
)

func TestOpenAppliesMigrationsAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forgeai.db")

	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	for _, table := range []string{"projects", "settings", "secrets", "schema_migrations"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("expected table %s to exist: %v", table, err)
		}
	}

	// Re-running migrations against the same DB must be a no-op, not an error.
	if err := sqlite.Migrate(db); err != nil {
		t.Fatalf("second Migrate call should be idempotent, got: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("counting schema_migrations: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 recorded migration, got %d", count)
	}
}

func TestSecretStoreRoundTrip(t *testing.T) {
	t.Setenv("FORGEAI_TEST_MASTER_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")

	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "forgeai.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	box, err := crypto.NewSecretBox("FORGEAI_TEST_MASTER_KEY")
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}

	store := sqlite.NewSecretStore(db, box)
	ctx := t.Context()

	if err := store.Set(ctx, "openai", []byte("sk-test")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get(ctx, "openai")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "sk-test" {
		t.Errorf("got %q, want sk-test", got)
	}

	// A raw read must never see plaintext.
	var raw []byte
	if err := db.QueryRow(`SELECT ciphertext FROM secrets WHERE name = ?`, "openai").Scan(&raw); err != nil {
		t.Fatalf("reading raw ciphertext: %v", err)
	}
	if string(raw) == "sk-test" {
		t.Fatalf("secret stored as plaintext in the database")
	}

	if err := store.Delete(ctx, "openai"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, "openai"); err == nil {
		t.Fatalf("expected error reading deleted secret")
	}
}
