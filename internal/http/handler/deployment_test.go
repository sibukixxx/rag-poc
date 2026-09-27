package handler

import "testing"

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		ok     bool
	}{
		{name: "valid", header: "Bearer fai_secret", want: "fai_secret", ok: true},
		{name: "case insensitive", header: "bearer token", want: "token", ok: true},
		{name: "missing", header: "", ok: false},
		{name: "wrong scheme", header: "Basic abc", ok: false},
		{name: "missing token", header: "Bearer", ok: false},
		{name: "too many fields", header: "Bearer a b", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := bearerToken(tt.header)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("bearerToken(%q) = (%q, %v), want (%q, %v)", tt.header, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRuntimeTokenLimiter(t *testing.T) {
	limiter := newRuntimeTokenLimiter(2)
	if !limiter.Allow("token-a") {
		t.Fatal("first request should be allowed")
	}
	if !limiter.Allow("token-a") {
		t.Fatal("second request should be allowed")
	}
	if limiter.Allow("token-a") {
		t.Fatal("third request should be rate limited")
	}
	if !limiter.Allow("token-b") {
		t.Fatal("a different token should have its own window")
	}
}
