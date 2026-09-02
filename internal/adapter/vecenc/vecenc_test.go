package vecenc_test

import (
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/vecenc"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	original := []float32{0.1, -0.2, 3.14159, 0, -0}
	decoded, err := vecenc.Decode(vecenc.Encode(original))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(decoded) != len(original) {
		t.Fatalf("got %d values, want %d", len(decoded), len(original))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("index %d: got %v, want %v", i, decoded[i], original[i])
		}
	}
}

func TestEncodeEmptyVector(t *testing.T) {
	decoded, err := vecenc.Decode(vecenc.Encode(nil))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("expected empty result, got %+v", decoded)
	}
}

func TestDecodeInvalidLength(t *testing.T) {
	if _, err := vecenc.Decode([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error for a length that isn't a multiple of 4")
	}
}
