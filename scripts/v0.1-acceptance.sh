#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ACCEPTANCE FAIL: $*" >&2
  exit 1
}

# require_test runs exactly the named tests and fails unless every one of
# them reports PASS. `go test -run` alone exits 0 when nothing matches, which
# would let a renamed or deleted gate test pass silently.
require_test() {
  local pkg="$1"
  shift
  local pattern out
  pattern="^($(IFS='|'; echo "$*"))\$"
  out="$(CGO_ENABLED=0 go test -count=1 -v "$pkg" -run "$pattern" 2>&1)" || {
    echo "$out" >&2
    fail "gate tests failed in $pkg"
  }
  for name in "$@"; do
    grep -q -- "--- PASS: ${name} " <<<"$out" || fail "gate test ${name} did not run in $pkg"
  done
}

echo "== G0: Go vet =="
go vet ./...

echo "== G0: Go tests =="
CGO_ENABLED=0 go test ./...

echo "== G1: core E2E (clean DB + mock provider journey) =="
require_test ./internal/app TestAcceptanceE2EMockProviderJourney

echo "== G6: migration / upgrade from pre-v0.1 schema =="
require_test ./internal/adapter/sqlite TestMigrateUpgradesPreV01DatabaseWithoutLosingKnowledge

echo "== G2: focused W10 runtime tests =="
require_test ./internal/http/handler TestRuntimeHTTPAuthenticationSearchAndRevocation TestRuntimeHTTPChatUsesFrozenPromptAndStreamsCitation

echo "== G3: release packaging =="
command -v goreleaser >/dev/null 2>&1 || fail "goreleaser is required for W11 acceptance"
./scripts/release-smoke.sh

echo "== G4: Security & Privacy evidence =="
security_evidence="docs/security/V0.1_SECURITY_EVIDENCE.md"
[[ -s "$security_evidence" ]] || fail "missing $security_evidence; #21–#25 must be completed with evidence"

if grep -Eq 'PLACEHOLDER|TODO: security gate|NOT IMPLEMENTED' "$security_evidence"; then
  fail "$security_evidence still contains an explicit incomplete marker"
fi

echo "== G5: TechVit dogfooding evidence =="
shopt -s nullglob
dogfood_reports=(docs/dogfooding/*-techvit.md)
shopt -u nullglob
[[ "${#dogfood_reports[@]}" -gt 0 ]] || fail "missing dated TechVit dogfooding report; complete #26"

for report in "${dogfood_reports[@]}"; do
  grep -q '^## Questions' "$report" || fail "$report has no Questions section"
  grep -q '^## Evaluation A' "$report" || fail "$report has no Evaluation A section"
  grep -q '^## Evaluation B' "$report" || fail "$report has no Evaluation B section"
  grep -q '^## Deployment' "$report" || fail "$report has no Deployment section"
  grep -q '^## Issues discovered' "$report" || fail "$report has no Issues discovered section"
done

echo "== G6: release artifacts present =="
[[ -s dist/checksums.txt ]] || fail "release checksums are missing"

echo
echo "Deterministic v0.1 acceptance gates passed."
echo "Before tagging: verify migration/upgrade evidence, real-provider checks if required, and confirm GitHub issues #21 and #26 are closed."
