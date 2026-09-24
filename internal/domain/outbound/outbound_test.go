package outbound_test

import (
	"reflect"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/domain/outbound"
)

func mustDetector(t *testing.T, builtin []string, custom []outbound.Rule) *outbound.Detector {
	t.Helper()
	d, err := outbound.NewDetector(builtin, custom)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDetectorRedactsSupportedPatterns(t *testing.T) {
	d := mustDetector(t, []string{"email", "phone"}, []outbound.Rule{{Name: "employee_id", Pattern: `EMP-\d{6}`}})
	tests := []struct {
		name  string
		input string
		want  string
		found map[string]int
	}{
		{"email", "Contact taro.yamada+sales@example.co.jp today", "Contact [REDACTED:email] today", map[string]int{"email": 1}},
		{"japanese mobile with hyphens", "携帯は090-1234-5678です", "携帯は[REDACTED:phone]です", map[string]int{"phone": 1}},
		{"tokyo landline", "TEL 03-1234-5678", "TEL [REDACTED:phone]", map[string]int{"phone": 1}},
		{"international", "call +81 90 1234 5678", "call [REDACTED:phone]", map[string]int{"phone": 1}},
		{"mobile without separators", "09012345678", "[REDACTED:phone]", map[string]int{"phone": 1}},
		{"custom identifier", "owner EMP-004211 approved", "owner [REDACTED:employee_id] approved", map[string]int{"employee_id": 1}},
		{"several kinds", "a@b.io / 080-0000-1111 / EMP-123456", "[REDACTED:email] / [REDACTED:phone] / [REDACTED:employee_id]", map[string]int{"email": 1, "phone": 1, "employee_id": 1}},
		{"date is not a phone", "released 2026-09-24", "released 2026-09-24", map[string]int{}},
		{"version is not a phone", "go 1.25.3", "go 1.25.3", map[string]int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := d.Redact(tt.input)

			if got != tt.want {
				t.Fatalf("Redact(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if !reflect.DeepEqual(found, tt.found) {
				t.Fatalf("matches = %v, want %v", found, tt.found)
			}
		})
	}
}

func TestNewDetectorRejectsInvalidRules(t *testing.T) {
	tests := []struct {
		name    string
		builtin []string
		custom  []outbound.Rule
		want    string
	}{
		{"unknown builtin", []string{"ssn"}, nil, `unknown builtin detector "ssn" (supported: email, phone)`},
		{"bad regex", nil, []outbound.Rule{{Name: "acct", Pattern: `ACCT-(\d+`}}, "sensitive rule \"acct\": invalid pattern: error parsing regexp: missing closing ): `ACCT-(\\d+`"},
		{"empty pattern", nil, []outbound.Rule{{Name: "acct", Pattern: ""}}, `sensitive rule "acct": pattern must not be empty`},
		{"pattern matching empty string", nil, []outbound.Rule{{Name: "acct", Pattern: `\d*`}}, `sensitive rule "acct": pattern must not match the empty string`},
		{"bad name", nil, []outbound.Rule{{Name: "Account ID", Pattern: `A\d+`}}, `sensitive rule name "Account ID" must match [a-z0-9_]+`},
		{"duplicate name", []string{"email"}, []outbound.Rule{{Name: "email", Pattern: `x@y`}}, `duplicate sensitive rule name "email"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := outbound.NewDetector(tt.builtin, tt.custom)

			if err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParsePolicyRejectsUnknownValues(t *testing.T) {
	for _, v := range []string{"", "allow", "deny_sensitive", "redact_known_patterns"} {
		if _, err := outbound.ParsePolicy(v); err != nil {
			t.Errorf("ParsePolicy(%q) error = %v", v, err)
		}
	}
	_, err := outbound.ParsePolicy("block")
	if err == nil || err.Error() != `outbound policy must be one of allow, deny_sensitive, redact_known_patterns; got "block"` {
		t.Fatalf("error = %v", err)
	}
}
