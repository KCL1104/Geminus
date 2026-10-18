package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(func(string) string { return "" }, &stderr); code != exitConfig {
		t.Fatalf("exit = %d, want %d", code, exitConfig)
	}
	for _, v := range []string{"DATABASE_URL", "AUTH_OIDC_ISSUER", "AUTH_OIDC_AUDIENCE", "AUTH_OIDC_JWKS_URL"} {
		if !strings.Contains(stderr.String(), v) {
			t.Fatalf("stderr does not name the missing %s: %s", v, stderr.String())
		}
	}
}

func TestRunRefusesToStartUntilImplemented(t *testing.T) {
	var stderr bytes.Buffer
	getenv := func(k string) string {
		return map[string]string{
			"DATABASE_URL":       "postgresql://twin_api:db-pass-123@db.internal/twin",
			"AUTH_OIDC_ISSUER":   "https://id.example/realms/geminus",
			"AUTH_OIDC_AUDIENCE": "https://api.geminus.example",
			"AUTH_OIDC_JWKS_URL": "https://id.example/realms/geminus/certs",
		}[k]
	}
	if code := run(getenv, &stderr); code != exitNotImplemented {
		t.Fatalf("exit = %d, want %d", code, exitNotImplemented)
	}
	out := stderr.String()
	if !strings.Contains(out, "NOT IMPLEMENTED") || !strings.Contains(out, "T-013") {
		t.Fatalf("stderr does not explain what is missing: %s", out)
	}
	if strings.Contains(out, "db-pass-123") {
		t.Fatalf("stderr leaks the database password: %s", out)
	}
}

// An insecure provider configuration stops the API before it serves.
func TestRunRejectsInsecureAuth(t *testing.T) {
	var stderr bytes.Buffer
	getenv := func(k string) string {
		return map[string]string{
			"DATABASE_URL":         "postgresql://twin_api@db.internal/twin",
			"AUTH_OIDC_ISSUER":     "https://id.example",
			"AUTH_OIDC_AUDIENCE":   "https://api.geminus.example",
			"AUTH_OIDC_JWKS_URL":   "https://id.example/certs",
			"AUTH_OIDC_ALGORITHMS": "HS256",
		}[k]
	}
	if code := run(getenv, &stderr); code != exitConfig || !strings.Contains(stderr.String(), "AUTH_OIDC_*") {
		t.Fatalf("exit = %d: %s", code, stderr.String())
	}
}
