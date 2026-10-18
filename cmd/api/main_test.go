package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/storage/dbtest"
)

var validEnv = map[string]string{
	"DATABASE_URL":       "postgresql://twin_api:db-pass-123@/twin?host=/nonexistent-geminus-socket",
	"AUTH_OIDC_ISSUER":   "https://id.example/realms/geminus",
	"AUTH_OIDC_AUDIENCE": "https://api.geminus.example",
	"AUTH_OIDC_JWKS_URL": "https://id.example/realms/geminus/certs",
	"API_LISTEN_ADDR":    "127.0.0.1:0",
}

func with(kv ...string) func(string) string {
	m := map[string]string{}
	for k, v := range validEnv {
		m[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), func(string) string { return "" }, &stderr, nil); code != exitConfig {
		t.Fatalf("exit = %d, want %d", code, exitConfig)
	}
	for _, v := range []string{"DATABASE_URL", "AUTH_OIDC_ISSUER", "AUTH_OIDC_AUDIENCE", "AUTH_OIDC_JWKS_URL"} {
		if !strings.Contains(stderr.String(), v) {
			t.Fatalf("stderr does not name the missing %s: %s", v, stderr.String())
		}
	}
	for name, getenv := range map[string]func(string) string{
		"insecure algorithm": with("AUTH_OIDC_ALGORITHMS", "HS256"),
		"listen address":     with("API_LISTEN_ADDR", "localhost"),
		"port out of range":  with("API_LISTEN_ADDR", ":70000"),
	} {
		stderr.Reset()
		if code := run(context.Background(), getenv, &stderr, nil); code != exitConfig {
			t.Errorf("%s: exit %d: %s", name, code, stderr.String())
		}
	}
}

// Without a usable database the API does not start, and says so without
// the password.
func TestRunNeedsTheDatabase(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), with(), &stderr, nil); code != exitDatabase {
		t.Fatalf("exit = %d, want %d: %s", code, exitDatabase, stderr.String())
	}
	if strings.Contains(stderr.String(), "db-pass-123") {
		t.Fatalf("stderr leaks the database password: %s", stderr.String())
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// Against the disposable database: an unsafe login (the provisioning
// superuser) is refused; the API login serves the routes and stops cleanly
// when its context ends.
func TestRunServesAndStops(t *testing.T) {
	dbtest.Admin(t) // skips unless the disposable database is configured; creates the API login
	var stderr syncBuffer
	if code := run(context.Background(), with("DATABASE_URL", dbtest.AdminURL(t)), &stderr, nil); code != exitDatabase {
		t.Errorf("the superuser login: exit %d: %s", code, stderr.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan int, 1)
	go func() { done <- run(ctx, with("DATABASE_URL", dbtest.LoginURL(t, dbtest.APILogin)), &stderr, ready) }()
	var addr string
	select {
	case addr = <-ready:
	case code := <-done:
		t.Fatalf("exit %d before serving: %s", code, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatal("not serving after 30 s")
	}
	res, err := http.Get("http://" + addr + "/v1/projects")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 401 || !strings.Contains(string(body), `"UNAUTHENTICATED"`) || res.Header.Get("X-Request-ID") == "" {
		t.Errorf("unauthenticated request: %d %s", res.StatusCode, body)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d after shutdown: %s", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("did not stop after its context ended")
	}
}
