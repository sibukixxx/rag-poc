package handler

import (
	"errors"
	"net/http"

	"github.com/sibukixxx/rag-poc/internal/domain/outbound"
)

// writeProviderError maps a failed provider-backed operation to a stable
// client response. Raw provider errors stay in the server log (the caller
// logs them); clients see either the outbound-policy block — which is a
// deliberate, documented refusal, not an upstream fault — or fallback.
func writeProviderError(w http.ResponseWriter, err error, fallback string) {
	if errors.Is(err, outbound.ErrBlocked) {
		http.Error(w, outbound.ErrBlocked.Error(), http.StatusUnprocessableEntity)
		return
	}
	http.Error(w, fallback, http.StatusBadGateway)
}
