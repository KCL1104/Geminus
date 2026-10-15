package config

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const validDB = "postgresql://twin_api@db.internal:5432/twin"

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

// codes flattens the joined error into "VAR:code" pairs.
func codes(t *testing.T, err error) []string {
	t.Helper()
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("want joined error, got %T: %v", err, err)
	}
	var out []string
	for _, e := range joined.Unwrap() {
		var ce *Error
		if !errors.As(e, &ce) {
			t.Fatalf("want *Error, got %T", e)
		}
		out = append(out, ce.Var+":"+ce.Code)
	}
	return out
}

func TestLoadValid(t *testing.T) {
	cases := map[string]map[string]string{
		"defaults to disabled AI": {"DATABASE_URL": validDB},
		"explicitly disabled AI":  {"DATABASE_URL": validDB, "AI_PROVIDER": "disabled"},
		"unix socket via host=":   {"DATABASE_URL": "postgresql:///dt_test_x?host=/var/run/postgresql"},
		"postgres scheme":         {"DATABASE_URL": "postgres://u@h/twin"},
		"object storage disabled": {"DATABASE_URL": validDB, "OBJECT_STORAGE_PROVIDER": "disabled"},
	}
	for name, kv := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(env(kv))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.AIProvider != AIProviderDisabled {
				t.Fatalf("AIProvider = %q, want disabled", cfg.AIProvider)
			}
			if cfg.DatabaseURL != kv["DATABASE_URL"] {
				t.Fatalf("DatabaseURL not preserved")
			}
		})
	}
}

func TestLoadRejects(t *testing.T) {
	cases := []struct {
		name string
		kv   map[string]string
		want []string
	}{
		{"missing database", map[string]string{}, []string{"DATABASE_URL:required"}},
		{"wrong scheme", map[string]string{"DATABASE_URL": "mysql://u@h/twin"}, []string{"DATABASE_URL:invalid"}},
		{"no database name", map[string]string{"DATABASE_URL": "postgresql://u@h/"}, []string{"DATABASE_URL:invalid"}},
		{"no host", map[string]string{"DATABASE_URL": "postgresql:///twin"}, []string{"DATABASE_URL:invalid"}},
		{"unparseable", map[string]string{"DATABASE_URL": "postgresql://%zz"}, []string{"DATABASE_URL:invalid"}},
		{"anthropic not yet implemented", map[string]string{"DATABASE_URL": validDB, "AI_PROVIDER": "anthropic"},
			[]string{"AI_PROVIDER:not_implemented"}},
		{"unknown provider", map[string]string{"DATABASE_URL": validDB, "AI_PROVIDER": "openai"}, []string{"AI_PROVIDER:invalid"}},
		{"provider is case-sensitive", map[string]string{"DATABASE_URL": validDB, "AI_PROVIDER": "Disabled"}, []string{"AI_PROVIDER:invalid"}},
		{"key while disabled", map[string]string{"DATABASE_URL": validDB, "AI_API_KEY": "k"}, []string{"AI_API_KEY:invalid"}},
		{"object storage TBD", map[string]string{"DATABASE_URL": validDB, "OBJECT_STORAGE_PROVIDER": "s3"},
			[]string{"OBJECT_STORAGE_PROVIDER:not_implemented"}},
		{"all problems reported together",
			map[string]string{"AI_PROVIDER": "openai", "OBJECT_STORAGE_PROVIDER": "gcs"},
			[]string{"DATABASE_URL:required", "AI_PROVIDER:invalid", "OBJECT_STORAGE_PROVIDER:not_implemented"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(env(tc.kv))
			if err == nil {
				t.Fatalf("want error, got %+v", cfg)
			}
			if got := codes(t, err); !slices.Equal(got, tc.want) {
				t.Fatalf("codes = %v, want %v", got, tc.want)
			}
			if cfg != (Config{}) {
				t.Fatalf("rejected config must be zero, got %+v", cfg)
			}
		})
	}
}

func TestErrorsNeverEchoSecrets(t *testing.T) {
	_, err := Load(env(map[string]string{
		"DATABASE_URL": "postgresql://u:db-pass-123@h/",
		"AI_API_KEY":   "sk-test-should-not-leak",
	}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, secret := range []string{"db-pass-123", "sk-test-should-not-leak"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error message leaks %q: %v", secret, err)
		}
	}
}
