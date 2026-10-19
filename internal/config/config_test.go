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

func TestLoadServer(t *testing.T) {
	for addr, want := range map[string]string{"": ":8080", ":9090": ":9090", "127.0.0.1:0": "127.0.0.1:0", "[::1]:443": "[::1]:443", ":65535": ":65535", ":0": ":0"} {
		s, err := LoadServer(env(map[string]string{"API_LISTEN_ADDR": addr}))
		if err != nil || s.Addr != want {
			t.Errorf("%q: %+v %v", addr, s, err)
		}
	}
	for _, addr := range []string{"localhost", ":70000", ":65536", ":-1", ":http", "host:"} {
		if _, err := LoadServer(env(map[string]string{"API_LISTEN_ADDR": addr})); err == nil || !strings.Contains(err.Error(), "API_LISTEN_ADDR") {
			t.Errorf("%q accepted: %v", addr, err)
		}
	}
}

func TestLoadWorker(t *testing.T) {
	w, err := LoadWorker(env(map[string]string{"WORKER_ID": "worker-0.pod_a:1", "WORKER_ORGANIZATION_IDS": " org_a , org_b"}))
	if err != nil || w.ID != "worker-0.pod_a:1" || !slices.Equal(w.Organizations, []string{"org_a", "org_b"}) {
		t.Errorf("%+v %v", w, err)
	}
	// An empty entry is kept, for workerscope to refuse: the list is never
	// silently shortened.
	if w, err := LoadWorker(env(map[string]string{"WORKER_ID": "w", "WORKER_ORGANIZATION_IDS": "org_a,,org_b,"})); err != nil || len(w.Organizations) != 4 {
		t.Errorf("%+v %v", w, err)
	}
	if _, err := LoadWorker(env(nil)); err == nil || !slices.Equal(codes(t, err), []string{"WORKER_ID:required", "WORKER_ORGANIZATION_IDS:required"}) {
		t.Errorf("nothing set: %v", err)
	}
	for _, id := range []string{"has space", "tab\t", "naïve", strings.Repeat("w", 201), "\x7f"} {
		if _, err := LoadWorker(env(map[string]string{"WORKER_ID": id, "WORKER_ORGANIZATION_IDS": " , "})); err == nil || !slices.Equal(codes(t, err), []string{"WORKER_ID:invalid"}) {
			t.Errorf("%q: %v", id, err)
		}
	}
	if _, err := LoadWorker(env(map[string]string{"WORKER_ID": strings.Repeat("w", 200), "WORKER_ORGANIZATION_IDS": " \t"})); err == nil || !slices.Equal(codes(t, err), []string{"WORKER_ORGANIZATION_IDS:required"}) {
		t.Errorf("a blank allowlist: %v", err)
	}
}
