package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

var validEnv = map[string]string{
	"DATABASE_URL":            "postgresql://twin_worker:db-pass-123@/twin?host=/nonexistent-geminus-socket",
	"WORKER_ID":               "worker-0",
	"WORKER_ORGANIZATION_IDS": ids.New("org"),
}

func with(kv ...string) func(string) string {
	m := map[string]string{}
	for k, v := range validEnv {
		m[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// someHandler stands for the handlers later tasks register.
var someHandler = map[jobs.Type]handler.Handler{jobs.ImportProfile: handler.Func(func(context.Context, handler.Run) error { return nil })}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), func(string) string { return "" }, &stderr, someHandler); code != exitConfig {
		t.Fatalf("exit = %d, want %d", code, exitConfig)
	}
	for _, v := range []string{"DATABASE_URL", "WORKER_ID", "WORKER_ORGANIZATION_IDS"} {
		if !strings.Contains(stderr.String(), v) {
			t.Errorf("stderr does not name %s: %s", v, stderr.String())
		}
	}
}

// This build has no job handler, so the worker refuses to start before it
// connects anywhere.
func TestRunRefusesToStartWithoutHandlers(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), with(), &stderr, handlers()); code != exitNotImplemented {
		t.Fatalf("exit = %d, want %d", code, exitNotImplemented)
	}
	out := stderr.String()
	if !strings.Contains(out, "NOT IMPLEMENTED") || !strings.Contains(out, "T-016") {
		t.Fatalf("stderr does not explain what is missing: %s", out)
	}
	if strings.Contains(out, "db-pass-123") || strings.Contains(out, "database") {
		t.Fatalf("stderr leaks the password or shows a connection attempt: %s", out)
	}
}

// The allowlist is checked with the rest of the configuration, before the
// handlers and before any connection (T-019 review L4).
func TestRunRefusesABadAllowlist(t *testing.T) {
	for _, orgs := range []string{"org_bad", ids.New("usr"), validEnv["WORKER_ORGANIZATION_IDS"] + "," + validEnv["WORKER_ORGANIZATION_IDS"], ids.New("org") + ",", "not-an-id,,,"} {
		for name, hs := range map[string]map[jobs.Type]handler.Handler{"with a handler": someHandler, "without one": handlers()} {
			var stderr bytes.Buffer
			if code := run(context.Background(), with("WORKER_ORGANIZATION_IDS", orgs), &stderr, hs); code != exitConfig || !strings.Contains(stderr.String(), "WORKER_ORGANIZATION_IDS") {
				t.Errorf("%q %s: exit %d: %s", orgs, name, code, stderr.String())
			}
		}
	}
}

// Every connection the pool hands back is checked for leftover tenant
// scope (tenant.Guard).
func TestPoolGuardsReleasedConnections(t *testing.T) {
	cfg, err := poolConfig(validEnv["DATABASE_URL"])
	if err != nil || cfg.AfterRelease == nil {
		t.Fatalf("the pool has no release guard (%v)", err)
	}
	if _, err := poolConfig("not a url"); err == nil {
		t.Error("an invalid DATABASE_URL was accepted")
	}
}

func TestRunNeedsTheDatabase(t *testing.T) {
	var stderr bytes.Buffer
	// Bounded, so a worker that wrongly started fails the test instead of
	// hanging it (T-019 review I1).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if code := run(ctx, with(), &stderr, someHandler); code != exitDatabase {
		t.Fatalf("exit = %d, want %d: %s", code, exitDatabase, stderr.String())
	}
	if out := stderr.String(); !strings.Contains(out, "database unavailable") || strings.Contains(out, "db-pass-123") {
		t.Fatalf("stderr: %s", out)
	}
}

// A signal before the loop began (the context already ended) is a stop,
// not a database failure.
func TestRunStopsBeforeStarting(t *testing.T) {
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := run(ctx, with(), &stderr, someHandler); code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, stderr.String())
	}
	if out := stderr.String(); !strings.Contains(out, "stopped before starting") || strings.Contains(out, "database unavailable") {
		t.Fatalf("stderr: %s", out)
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

// Against the disposable database: the worker refuses a superuser login,
// runs a job of an assigned organization as the worker login, and stops
// when signalled.
func TestRunWorksAndStops(t *testing.T) {
	admin := dbtest.Admin(t) // skips unless the disposable database is configured; creates the worker login
	o := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner)
	var stderr syncBuffer
	// Bounded, so a worker that wrongly accepted the login stops and the
	// test fails instead of hanging.
	refuse, cancelRefuse := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRefuse()
	if code := run(refuse, with("DATABASE_URL", dbtest.AdminURL(t), "WORKER_ORGANIZATION_IDS", o.ID), &stderr, someHandler); code != exitDatabase ||
		!strings.Contains(stderr.String(), "refusing the database login") {
		t.Fatalf("a superuser login: exit %d: %s", code, stderr.String())
	}
	imp := dbtest.NewImport(t, admin, o)
	var job jobs.Job
	if err := postgres.OpenPool(admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		job, err = jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: o.ID, Type: jobs.ImportProfile,
			Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{"import_id": imp}}, jobs.DefaultPolicy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{}, 1)
	handlers := map[jobs.Type]handler.Handler{jobs.ImportProfile: handler.Func(func(ctx context.Context, run handler.Run) error {
		finish := run.Finish
		if err := finish(ctx, jobs.Completed, func(context.Context, tenant.Tx) error { return nil }); err != nil {
			return err
		}
		ran <- struct{}{}
		return nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, with("DATABASE_URL", dbtest.LoginURL(t, dbtest.WorkerLogin), "WORKER_ORGANIZATION_IDS", o.ID), &stderr, handlers)
	}()
	select {
	case <-ran:
	case code := <-done:
		t.Fatalf("exited %d: %s", code, stderr.String())
	case <-time.After(20 * time.Second):
		t.Fatalf("the job did not run: %s", stderr.String())
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d: %s", code, stderr.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the worker did not stop")
	}
	var status jobs.Status
	if err := admin.QueryRow(context.Background(), `SELECT status FROM twin.jobs WHERE id=$1`, job.ID).Scan(&status); err != nil || status != jobs.Completed {
		t.Errorf("the job is %s (%v)", status, err)
	}
	if out := stderr.String(); !strings.Contains(out, `"msg":"running"`) || !strings.Contains(out, `"msg":"stopped"`) {
		t.Errorf("stderr: %s", out)
	}
}
