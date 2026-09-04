package crypto_test

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/crypto"
)

func testKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestSecretBoxRoundTrip(t *testing.T) {
	t.Setenv("FORGEAI_TEST_KEY", testKey(t))

	box, err := crypto.NewSecretBox("FORGEAI_TEST_KEY")
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}

	plaintext := []byte("sk-super-secret-api-key")
	ciphertext, nonce, err := box.Seal(plaintext, []byte("openai"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if string(ciphertext) == string(plaintext) {
		t.Fatalf("ciphertext must not equal plaintext")
	}

	got, err := box.Open(ciphertext, nonce, []byte("openai"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("got %q, want %q", got, plaintext)
	}

	// A ciphertext moved to a different secret name must not decrypt.
	if _, err := box.Open(ciphertext, nonce, []byte("anthropic")); err == nil {
		t.Fatalf("Open with a different AAD must fail")
	}
}

func TestSecretBoxMissingKey(t *testing.T) {
	t.Setenv("FORGEAI_TEST_KEY_MISSING", "")
	os := "FORGEAI_TEST_KEY_MISSING_UNSET"
	if _, err := crypto.NewSecretBox(os); err == nil {
		t.Fatalf("expected error for missing master key")
	}
}

func TestGenerateMasterKeyDecodesTo32Bytes(t *testing.T) {
	encoded, err := crypto.GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decoding generated key: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(decoded))
	}
}
