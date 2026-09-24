// Package outbound is the sensitive-data policy for text sent to external
// model providers (#25). Detection is deterministic pattern matching only:
// it catches the documented formats and nothing else, so it must never be
// described as removing all personal data. Private Mode (local_only) remains
// the option for "customer content must not leave the environment".
package outbound

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Policy decides what happens when outbound text matches a rule.
type Policy string

const (
	// PolicyAllow sends text unchanged. It is an explicit operator choice
	// and says nothing about whether data is private.
	PolicyAllow Policy = "allow"
	// PolicyDenySensitive blocks the provider call when any rule matches.
	PolicyDenySensitive Policy = "deny_sensitive"
	// PolicyRedactKnownPatterns replaces matches before transmission.
	PolicyRedactKnownPatterns Policy = "redact_known_patterns"
)

// ErrBlocked is returned instead of calling a provider under
// deny_sensitive. Its message never contains the matched value.
var ErrBlocked = errors.New("request blocked by outbound sensitive-data policy")

// ParsePolicy validates a configured policy; "" means allow.
func ParsePolicy(v string) (Policy, error) {
	switch Policy(v) {
	case "", PolicyAllow:
		return PolicyAllow, nil
	case PolicyDenySensitive, PolicyRedactKnownPatterns:
		return Policy(v), nil
	}
	return "", fmt.Errorf("outbound policy must be one of allow, deny_sensitive, redact_known_patterns; got %q", v)
}

// Rule is an operator-defined pattern (RE2 syntax) such as an employee or
// account identifier format.
type Rule struct {
	Name    string `yaml:"name"`
	Pattern string `yaml:"pattern"`
}

// Builtin detectors. The phone pattern covers Japanese domestic numbers
// with hyphens (03-1234-5678, 090-1234-5678), 11-digit mobiles without
// separators, and +country-code numbers separated by spaces or hyphens.
// Numbers in other layouts (parentheses, dots) are not detected.
var builtins = map[string]string{
	"email": `[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`,
	"phone": `(?:\+\d{1,3}[\s\-]?\d{1,4}[\s\-]?\d{1,4}[\s\-]?\d{3,4}\b|\b0\d{1,4}-\d{1,4}-\d{3,4}\b|\b0[5789]0\d{8}\b)`,
}

var ruleName = regexp.MustCompile(`^[a-z0-9_]+$`)

type compiledRule struct {
	name string
	re   *regexp.Regexp
}

// Detector finds and redacts rule matches.
type Detector struct {
	rules []compiledRule
}

// NewDetector compiles builtin detectors (by name) followed by custom
// rules. Invalid configuration is an error, never a silent fallback.
func NewDetector(builtin []string, custom []Rule) (*Detector, error) {
	d := &Detector{}
	seen := map[string]bool{}
	for _, name := range builtin {
		pattern, ok := builtins[name]
		if !ok {
			return nil, fmt.Errorf("unknown builtin detector %q (supported: email, phone)", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate sensitive rule name %q", name)
		}
		seen[name] = true
		d.rules = append(d.rules, compiledRule{name: name, re: regexp.MustCompile(pattern)})
	}
	for _, r := range custom {
		if !ruleName.MatchString(r.Name) {
			return nil, fmt.Errorf("sensitive rule name %q must match [a-z0-9_]+", r.Name)
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("duplicate sensitive rule name %q", r.Name)
		}
		if strings.TrimSpace(r.Pattern) == "" {
			return nil, fmt.Errorf("sensitive rule %q: pattern must not be empty", r.Name)
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("sensitive rule %q: invalid pattern: %w", r.Name, err)
		}
		if re.MatchString("") {
			return nil, fmt.Errorf("sensitive rule %q: pattern must not match the empty string", r.Name)
		}
		seen[r.Name] = true
		d.rules = append(d.rules, compiledRule{name: r.Name, re: re})
	}
	return d, nil
}

// Redact replaces every match with [REDACTED:<rule>] and returns the
// number of matches per rule (an empty map when nothing matched).
func (d *Detector) Redact(text string) (string, map[string]int) {
	found := map[string]int{}
	for _, r := range d.rules {
		n := 0
		text = r.re.ReplaceAllStringFunc(text, func(string) string {
			n++
			return "[REDACTED:" + r.name + "]"
		})
		if n > 0 {
			found[r.name] += n
		}
	}
	return text, found
}
