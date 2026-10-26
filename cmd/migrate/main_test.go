package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/storage/dbtest"
)

var repoMigrations = filepath.Join("..", "..", "migrations")

func getenv(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// The command refuses what it cannot run before it connects: no or an
// unknown subcommand, extra arguments, no GEMINUS_MIGRATE_DATABASE_URL
// (it never falls back to the runtime DATABASE_URL), a directory without
// valid migrations; an unreachable database is reported without its
// password.
func TestRunRefusesBeforeConnecting(t *testing.T) {
	url := "postgresql://geminus_migrator:mig-pass-SENTINEL@/twin?host=/nonexistent-geminus-socket"
	empty := t.TempDir()
	for name, c := range map[string]struct {
		args []string
		env  func(string) string
		code int
	}{
		"no subcommand":       {nil, getenv("GEMINUS_MIGRATE_DATABASE_URL", url), exitUsage},
		"unknown subcommand":  {[]string{"down"}, getenv("GEMINUS_MIGRATE_DATABASE_URL", url), exitUsage},
		"extra arguments":     {[]string{"-dir", repoMigrations, "up", "x"}, getenv("GEMINUS_MIGRATE_DATABASE_URL", url), exitUsage},
		"no URL":              {[]string{"-dir", repoMigrations, "up"}, getenv("DATABASE_URL", url), exitUsage},
		"no migrations":       {[]string{"-dir", empty, "up"}, getenv("GEMINUS_MIGRATE_DATABASE_URL", url), exitUsage},
		"no such directory":   {[]string{"-dir", filepath.Join(empty, "x"), "status"}, getenv("GEMINUS_MIGRATE_DATABASE_URL", url), exitUsage},
		"an unreachable host": {[]string{"-dir", repoMigrations, "up"}, getenv("GEMINUS_MIGRATE_DATABASE_URL", url), exitDatabase},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), c.args, c.env, &stdout, &stderr); code != c.code {
			t.Errorf("%s: exit %d, want %d: %s", name, code, c.code, stderr.String())
		}
		if strings.Contains(stderr.String(), "SENTINEL") || stdout.Len() != 0 {
			t.Errorf("%s: %q %q", name, stdout.String(), stderr.String())
		}
	}
	if err := os.WriteFile(filepath.Join(empty, "0001_a.up.sql"), []byte("BEGIN;\nSELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"-dir", empty, "up"}, getenv("GEMINUS_MIGRATE_DATABASE_URL", url), new(bytes.Buffer), &stderr); code != exitUsage ||
		!strings.Contains(stderr.String(), "0001_a.up.sql") {
		t.Errorf("an invalid migration: exit %d: %s", code, stderr.String())
	}
}

// Against the disposable cluster: status reports what is pending (exit
// 6), up applies it and prints the versions as JSON, a second up applies
// nothing, status is then 0; a superuser login and a runtime login are
// refused (exit 4) and change nothing.
func TestRunMigrates(t *testing.T) {
	urlFor := dbtest.EmptyDatabase(t)
	entries, _ := filepath.Glob(filepath.Join(repoMigrations, "*.up.sql"))
	n := len(entries)
	type summary struct {
		Applied []int `json:"applied"`
		Current int   `json:"current"`
		Pending []int `json:"pending"`
	}
	call := func(args []string, login string) (int, summary, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), append([]string{"-dir", repoMigrations}, args...), getenv("GEMINUS_MIGRATE_DATABASE_URL", urlFor(login)), &stdout, &stderr)
		var s summary
		if stdout.Len() > 0 {
			if err := json.Unmarshal(stdout.Bytes(), &s); err != nil {
				t.Fatalf("stdout %q: %v", stdout.String(), err)
			}
		}
		return code, s, stderr.String()
	}
	for _, login := range []string{"", dbtest.APILogin, dbtest.WorkerLogin} {
		if code, _, stderr := call([]string{"up"}, login); code != exitRefused || !strings.Contains(stderr, "refusing the database login") {
			t.Errorf("login %q: exit %d: %s", login, code, stderr)
		}
	}
	if code, s, stderr := call([]string{"status"}, dbtest.MigratorLogin); code != exitPending || s.Current != 0 || len(s.Pending) != n || len(s.Applied) != 0 {
		t.Errorf("status before: exit %d %+v %s", code, s, stderr)
	}
	if code, s, stderr := call([]string{"up"}, dbtest.MigratorLogin); code != 0 || len(s.Applied) != n || s.Current != n || len(s.Pending) != 0 {
		t.Fatalf("up: exit %d %+v %s", code, s, stderr)
	}
	if code, s, stderr := call([]string{"up"}, dbtest.MigratorLogin); code != 0 || len(s.Applied) != 0 || s.Current != n {
		t.Errorf("up again: exit %d %+v %s", code, s, stderr)
	}
	if code, s, stderr := call([]string{"status"}, dbtest.MigratorLogin); code != 0 || s.Current != n || len(s.Pending) != 0 {
		t.Errorf("status after: exit %d %+v %s", code, s, stderr)
	}
}
