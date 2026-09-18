#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ACCEPTANCE FAIL: $*" >&2
  exit 1
}

echo "== G0: Go vet =="
go vet ./...

echo "== G0: Go tests =="
CGO_ENABLED=0 go test ./...

echo "== G2: focused W10 runtime tests =="
CGO_ENABLED=0 go test ./internal/http/handler -run 'TestRuntimeHTTP(AuthenticationSearchAndRevocation|ChatUsesFrozenPromptAndStreamsCitation)$'

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
