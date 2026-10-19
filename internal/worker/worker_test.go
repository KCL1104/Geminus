package worker_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker"
)

const issuer = "https://test.invalid"

// policy is the tests' queue timing: a lease no test outlives unless it
// sets a short one, and a backoff long enough that a retried job never
// becomes due while a test runs.
var policy = jobs.Policy{Lease: time.Minute, MaxAttempts: 3, BackoffBase: time.Hour, BackoffMax: 4 * time.Hour}

// wait bounds every wait on the worker, so a broken loop fails the test
// instead of hanging it.
const wait = 20 * time.Second

func config(h worker.Handler, log io.Writer) worker.Config {
	return worker.Config{
		ID: "worker-1", Policy: policy, Heartbeat: 20 * time.Second, Idle: 20 * time.Millisecond, ReapLimit: 10,
		Handlers: map[jobs.Type]worker.Handler{jobs.ImportProfile: h},
		Log:      slog.New(slog.NewTextHandler(log, nil)),
	}
}

var noop = worker.HandlerFunc(func(context.Context, jobs.Job, worker.Finish) error { return nil })

func TestNewChecksTheConfig(t *testing.T) {
	scope, err := workerscope.New(nil, []string{ids.New("org")})
	if err != nil {
		t.Fatal(err)
	}
	none, err := workerscope.New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		scope *workerscope.Worker
		edit  func(*worker.Config)
	}{
		"no scope":                  {nil, func(*worker.Config) {}},
		"no assigned organization":  {none, func(*worker.Config) {}},
		"no ID":                     {scope, func(c *worker.Config) { c.ID = "" }},
		"an ID over 200 characters": {scope, func(c *worker.Config) { c.ID = strings.Repeat("w", 201) }},
		"no handler":                {scope, func(c *worker.Config) { c.Handlers = nil }},
		"a nil handler":             {scope, func(c *worker.Config) { c.Handlers[jobs.Simulation] = nil }},
		"no lease":                  {scope, func(c *worker.Config) { c.Policy.Lease = 0 }},
		"no heartbeat":              {scope, func(c *worker.Config) { c.Heartbeat = 0 }},
		"a heartbeat over a third":  {scope, func(c *worker.Config) { c.Heartbeat = c.Policy.Lease/3 + time.Millisecond }},
		"no idle pause":             {scope, func(c *worker.Config) { c.Idle = 0 }},
		"no reaping":                {scope, func(c *worker.Config) { c.ReapLimit = 0 }},
		"no log":                    {scope, func(c *worker.Config) { c.Log = nil }},
	} {
		cfg := config(noop, io.Discard)
		c.edit(&cfg)
		if w, err := worker.New(c.scope, cfg); err == nil || w != nil {
			t.Errorf("%s: accepted", name)
		}
	}
	cfg := config(noop, io.Discard)
	cfg.Heartbeat = cfg.Policy.Lease / 3
	cfg.ID = strings.Repeat("w", 200)
	if _, err := worker.New(scope, cfg); err != nil {
		t.Errorf("a valid config: %v", err)
	}
}

// logs is a log sink the worker's goroutines share.
type logs struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type env struct {
	admin *pgxpool.Pool
	scope *workerscope.Worker
	orgs  []dbtest.Org
	log   *logs
}

// setup provisions n organizations, all assigned to the worker, which
// connects as the worker login (no superuser, no BYPASSRLS).
func setup(t *testing.T, n int) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	e := &env{admin: admin, log: &logs{}}
	var assigned []string
	for range n {
		o := dbtest.NewOrg(t, admin, issuer, tenant.Owner)
		e.orgs = append(e.orgs, o)
		assigned = append(assigned, o.ID)
	}
	pool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 8, true)
	if err := tenant.CheckRuntimeRole(context.Background(), pool, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	scope, err := workerscope.New(pool, assigned)
	if err != nil {
		t.Fatal(err)
	}
	e.scope = scope
	return e
}

func (e *env) worker(t *testing.T, cfg worker.Config) *worker.Worker {
	t.Helper()
	w, err := worker.New(e.scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// enqueue adds a job of type typ on a new import in o; attempts 0 means
// the policy's.
func (e *env) enqueue(t *testing.T, o dbtest.Org, typ jobs.Type, attempts int) jobs.Job {
	t.Helper()
	imp := dbtest.NewImport(t, e.admin, o)
	var j jobs.Job
	if err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		j, err = jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: o.ID, Type: typ,
			Resource: jobs.Resource{ImportID: imp}, Payload: map[string]any{"import_id": imp}, MaxAttempts: attempts}, policy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return j
}

func (e *env) job(t *testing.T, j jobs.Job) jobs.Job {
	t.Helper()
	var got jobs.Job
	if err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		got, err = jobs.Get(ctx, tx, j.OrganizationID, j.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func (e *env) importStatus(t *testing.T, j jobs.Job) string {
	t.Helper()
	var s string
	if err := e.admin.QueryRow(context.Background(), `SELECT status FROM twin.imports WHERE id=$1`, j.Resource.ImportID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// exec runs one statement as the provisioning login: what another worker,
// a crash or the passing time does to a job.
func (e *env) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.admin.Exec(context.Background(), sql, args...); err != nil {
		t.Error(err)
	}
}

// stored decodes the failure recorded on j (zero if none).
func stored(t *testing.T, j jobs.Job) jobs.Failure {
	t.Helper()
	var f jobs.Failure
	if j.Error != nil {
		if err := json.Unmarshal(j.Error, &f); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// profile is the result the tests' handlers commit: the import moves on.
func profile(j jobs.Job) func(ctx context.Context, tx tenant.Tx) error {
	return func(ctx context.Context, tx tenant.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE twin.imports SET status='PROFILING' WHERE id=$1`, j.Resource.ImportID)
		return err
	}
}

// A claimed job runs once; its result and its completion commit together,
// written as the worker role.
func TestAJobRunsOnceWithItsResult(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	var seen []jobs.Job
	w := e.worker(t, config(worker.HandlerFunc(func(ctx context.Context, job jobs.Job, finish worker.Finish) error {
		seen = append(seen, job)
		return finish(ctx, jobs.Completed, profile(job))
	}), e.log))
	if !w.Pass(context.Background()) {
		t.Fatal("the pass ran no job")
	}
	if w.Pass(context.Background()) {
		t.Error("a second pass ran a job")
	}
	if len(seen) != 1 {
		t.Fatalf("the handler ran %d times", len(seen))
	}
	if h := seen[0]; h.ID != j.ID || h.Type != jobs.ImportProfile || h.Status != jobs.Running || h.Attempt != 1 ||
		h.LockedBy != "worker-1" || h.Resource.ImportID != j.Resource.ImportID || !strings.Contains(string(h.Payload), j.Resource.ImportID) {
		t.Errorf("the handler got %+v", h)
	}
	if got := e.job(t, j); got.Status != jobs.Completed || got.Error != nil || got.LockedBy != "" || got.LeaseExpiresAt != nil {
		t.Errorf("the job is %+v", got)
	}
	if s := e.importStatus(t, j); s != "PROFILING" {
		t.Errorf("the result was not committed: the import is %s", s)
	}
	if out := e.log.String(); !strings.Contains(out, `msg="job ended"`) || !strings.Contains(out, "status=completed") || strings.Contains(out, "level=ERROR") {
		t.Errorf("log: %s", out)
	}
}

// A handler that stops at a checkpoint because cancellation was requested
// ends the job as cancelled, with its own writes, in one transaction; a job
// ends in no other way through Finish.
func TestAJobEndsCancelled(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	var invalid error
	ran := false
	w := e.worker(t, config(worker.HandlerFunc(func(ctx context.Context, job jobs.Job, finish worker.Finish) error {
		for _, end := range []jobs.Status{jobs.Failed, jobs.Queued, jobs.Running, ""} {
			if err := finish(ctx, end, func(context.Context, tenant.Tx) error { ran = true; return nil }); !errors.Is(err, worker.ErrInvalidEnd) {
				invalid = fmt.Errorf("ending as %q: %v", end, err)
			}
		}
		return finish(ctx, jobs.Cancelled, profile(job))
	}), e.log))
	if !w.Pass(context.Background()) {
		t.Fatal("the pass ran no job")
	}
	if invalid != nil || ran {
		t.Errorf("%v (the result function ran: %v)", invalid, ran)
	}
	if got := e.job(t, j); got.Status != jobs.Cancelled || got.Error != nil || got.LockedBy != "" {
		t.Errorf("the job is %+v", got)
	}
	if s := e.importStatus(t, j); s != "PROFILING" {
		t.Errorf("the handler's writes were not committed with the end: the import is %s", s)
	}
	if !strings.Contains(e.log.String(), "status=cancelled") {
		t.Errorf("log: %s", e.log.String())
	}
}

// An attempt that ends without a result records why: the handler's
// Failure as given, anything else as a code of the loop's own; the error
// text is logged, never stored.
func TestFailedAttempts(t *testing.T) {
	e := setup(t, 1)
	const secret = "s3://bucket/tenant-private-key"
	errBoom := errors.New("boom")
	for _, c := range []struct {
		name     string
		attempts int
		handle   func(ctx context.Context, job jobs.Job, finish worker.Finish) error
		status   jobs.Status
		failure  jobs.Failure
	}{
		{"a failure that is not retried", 0, func(context.Context, jobs.Job, worker.Finish) error {
			return &jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}},
		{"a wrapped retryable failure", 0, func(context.Context, jobs.Job, worker.Finish) error {
			return fmt.Errorf("profiling: %w", &jobs.Failure{Code: "STORAGE_UNAVAILABLE", Message: "the object store did not answer", Retryable: true})
		}, jobs.Queued, jobs.Failure{Code: "STORAGE_UNAVAILABLE", Message: "the object store did not answer", Retryable: true}},
		{"an error", 0, func(context.Context, jobs.Job, worker.Finish) error {
			return errors.New("reading " + secret + ": not found")
		}, jobs.Queued, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		{"an error on the last attempt", 1, func(context.Context, jobs.Job, worker.Finish) error {
			return errors.New("reading " + secret + ": not found")
		}, jobs.Failed, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		{"the result's transaction fails", 0, func(ctx context.Context, job jobs.Job, finish worker.Finish) error {
			err := finish(ctx, jobs.Completed, func(ctx context.Context, tx tenant.Tx) error {
				if err := profile(job)(ctx, tx); err != nil {
					return err
				}
				return errBoom
			})
			if !errors.Is(err, errBoom) {
				return fmt.Errorf("finish returned %v", err)
			}
			return err
		}, jobs.Queued, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		{"a panic", 0, func(context.Context, jobs.Job, worker.Finish) error {
			panic("index out of range")
		}, jobs.Failed, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed"}},
		{"no result", 0, func(context.Context, jobs.Job, worker.Finish) error {
			return nil
		}, jobs.Failed, jobs.Failure{Code: worker.NoResult, Message: "the handler returned without a result"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, c.attempts)
			if !e.worker(t, config(worker.HandlerFunc(c.handle), e.log)).Pass(context.Background()) {
				t.Fatal("the pass ran no job")
			}
			got := e.job(t, j)
			if got.Status != c.status || got.Attempt != 1 || stored(t, got) != c.failure || got.LockedBy != "" {
				t.Errorf("the job is %s, attempt %d, %s, held by %q; want %s with %+v", got.Status, got.Attempt, got.Error, got.LockedBy, c.status, c.failure)
			}
			if c.status == jobs.Queued {
				if wait := time.Until(got.AvailableAt); wait < 59*time.Minute || wait > 61*time.Minute {
					t.Errorf("retried in %v, want the policy's backoff", wait)
				}
			}
			if strings.Contains(string(got.Error), secret) {
				t.Errorf("the error text was stored: %s", got.Error)
			}
			if s := e.importStatus(t, j); s != "UPLOADED" {
				t.Errorf("a failed attempt wrote its result: the import is %s", s)
			}
		})
	}
	if !strings.Contains(e.log.String(), secret) || !strings.Contains(e.log.String(), "index out of range") {
		t.Error("the handler's errors were not logged")
	}
}

// The heartbeat renews the lease, so a job may run for longer than one.
func TestTheHeartbeatKeepsALongJob(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	cfg := config(worker.HandlerFunc(func(ctx context.Context, job jobs.Job, finish worker.Finish) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
		return finish(ctx, jobs.Completed, profile(job))
	}), e.log)
	cfg.Policy.Lease, cfg.Heartbeat = 1500*time.Millisecond, 100*time.Millisecond
	if !e.worker(t, cfg).Pass(context.Background()) {
		t.Fatal("the pass ran no job")
	}
	if got := e.job(t, j); got.Status != jobs.Completed {
		t.Errorf("a job twice as long as its lease: %s %s", got.Status, got.Error)
	}
}

// A lease taken by another worker stops the handler at the next heartbeat,
// and nothing the handler does afterwards is written.
func TestALostLeaseStopsTheHandler(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	const lease = 1500 * time.Millisecond
	var stopped time.Duration
	var wrote bool
	var ferr error
	cfg := config(worker.HandlerFunc(func(ctx context.Context, job jobs.Job, finish worker.Finish) error {
		e.exec(t, `UPDATE twin.jobs SET locked_by='worker-2', lease_token=lease_token+1 WHERE id=$1`, job.ID)
		start := time.Now()
		select {
		case <-ctx.Done():
			stopped = time.Since(start)
		case <-time.After(wait):
		}
		ferr = finish(context.WithoutCancel(ctx), jobs.Completed, func(ctx context.Context, tx tenant.Tx) error {
			wrote = true
			return profile(job)(ctx, tx)
		})
		return ferr
	}), e.log)
	cfg.Policy.Lease, cfg.Heartbeat = lease, 100*time.Millisecond
	e.worker(t, cfg).Pass(context.Background())
	// The next renewal finds the lease gone: well before the lease would
	// have ended.
	if stopped == 0 || stopped > lease-300*time.Millisecond || !errors.Is(ferr, jobs.ErrLeaseLost) || wrote {
		t.Errorf("stopped after %v; finish: %v; the result function ran: %v", stopped, ferr, wrote)
	}
	if got := e.job(t, j); got.Status != jobs.Running || got.LockedBy != "worker-2" || got.Error != nil || got.Attempt != 1 {
		t.Errorf("the worker wrote to a job it lost: %+v", got)
	}
	if s := e.importStatus(t, j); s != "UPLOADED" {
		t.Errorf("the import is %s", s)
	}
	if out := e.log.String(); !strings.Contains(out, "the attempt ends without writing") || strings.Contains(out, "recording the failure failed") {
		t.Errorf("the lost lease was not logged as such: %s", out)
	}
}

// A lease that expired before the heartbeat noticed refuses Finish; the
// worker writes nothing, and a later pass takes the job back.
func TestFinishAfterTheLeaseExpired(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	var ferr error
	var wrote bool
	handler := worker.HandlerFunc(func(ctx context.Context, job jobs.Job, finish worker.Finish) error {
		e.exec(t, `UPDATE twin.jobs SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, job.ID)
		ferr = finish(ctx, jobs.Completed, func(ctx context.Context, tx tenant.Tx) error {
			wrote = true
			return profile(job)(ctx, tx)
		})
		return ferr
	})
	w := e.worker(t, config(handler, e.log))
	if !w.Pass(context.Background()) {
		t.Fatal("the pass ran no job")
	}
	if !errors.Is(ferr, jobs.ErrLeaseLost) || wrote {
		t.Errorf("finish: %v; the result function ran: %v", ferr, wrote)
	}
	if got := e.job(t, j); got.Status != jobs.Running || got.Error != nil || got.LockedBy != "worker-1" {
		t.Errorf("the worker wrote to a job it lost: %+v", got)
	}
	if w.Pass(context.Background()) {
		t.Error("the reaped job ran before its backoff")
	}
	if got := e.job(t, j); got.Status != jobs.Queued || stored(t, got).Code != jobs.LeaseExpired || got.LockedBy != "" {
		t.Errorf("the pass did not take the job back: %s %s", got.Status, got.Error)
	}
	if s := e.importStatus(t, j); s != "UPLOADED" {
		t.Errorf("the import is %s", s)
	}
	if !strings.Contains(e.log.String(), "expired leases taken back") {
		t.Error("the reaping was not logged")
	}
}

// A renewal that hangs (here on a row lock) times out after one heartbeat,
// so it cannot keep a handler running past its lease: once the lease has
// ended the next renewal finds it lost, and the handler stops then, not
// before.
func TestAHungRenewalCannotOutliveTheLease(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	const lease = 1500 * time.Millisecond
	var held, stopped time.Duration
	cfg := config(worker.HandlerFunc(func(ctx context.Context, job jobs.Job, finish worker.Finish) error {
		// The row lock makes every renewal wait, as a hung database would.
		tx, err := e.admin.Begin(context.Background())
		if err != nil {
			return err
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(context.Background(), `SELECT 1 FROM twin.jobs WHERE id=$1 FOR UPDATE`, job.ID); err != nil {
			return err
		}
		start := time.Now()
		select {
		case <-ctx.Done():
			stopped = time.Since(start)
		case <-time.After(wait):
			held = time.Since(start)
		}
		return ctx.Err()
	}), e.log)
	cfg.Policy.Lease, cfg.Heartbeat = lease, 100*time.Millisecond
	e.worker(t, cfg).Pass(context.Background())
	if stopped == 0 || stopped < lease-200*time.Millisecond {
		t.Errorf("the handler stopped after %v (held %v), want about the lease, %v", stopped, held, lease)
	}
	if got := e.job(t, j); got.Status != jobs.Running || got.Error != nil {
		t.Errorf("the worker wrote to a job it lost: %+v", got)
	}
	if !strings.Contains(e.log.String(), "renewing the lease failed") {
		t.Error("the failed renewals were not logged")
	}
}

// A database that does not answer at all: every renewal fails, and after a
// whole lease without one the handler is stopped (not before: two renewals
// may fail meanwhile). The worker writes nothing; the lease ends on its own.
func TestTheHeartbeatGivesUpWithoutTheDatabase(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	var down atomic.Bool
	pc, err := pgxpool.ParseConfig(dbtest.LoginURL(t, dbtest.WorkerLogin))
	if err != nil {
		t.Fatal(err)
	}
	pc.AfterRelease = tenant.Guard
	pc.PrepareConn = func(context.Context, *pgx.Conn) (bool, error) {
		if down.Load() {
			return true, errors.New("the database does not answer")
		}
		return true, nil
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	scope, err := workerscope.New(pool, []string{e.orgs[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	const lease = 1500 * time.Millisecond
	var held, stopped time.Duration
	cfg := config(worker.HandlerFunc(func(ctx context.Context, job jobs.Job, finish worker.Finish) error {
		down.Store(true)
		start := time.Now()
		select {
		case <-ctx.Done():
			stopped = time.Since(start)
		case <-time.After(wait):
			held = time.Since(start)
		}
		return ctx.Err()
	}), e.log)
	cfg.Policy.Lease, cfg.Heartbeat = lease, 100*time.Millisecond
	w, err := worker.New(scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.Pass(context.Background())
	down.Store(false)
	if stopped == 0 || stopped < lease-200*time.Millisecond {
		t.Errorf("the handler stopped after %v (held %v), want about the lease, %v", stopped, held, lease)
	}
	if got := e.job(t, j); got.Status != jobs.Running || got.Error != nil || got.LockedBy != "worker-1" {
		t.Errorf("the worker wrote to a job it lost: %+v", got)
	}
	if out := e.log.String(); !strings.Contains(out, "the database does not answer") || !strings.Contains(out, "the attempt ends without writing") {
		t.Errorf("log: %s", out)
	}
}

// A worker that stops during an attempt puts the job back for a retry.
func TestStoppingRequeuesTheJob(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := e.worker(t, config(worker.HandlerFunc(func(hctx context.Context, job jobs.Job, finish worker.Finish) error {
		cancel()
		select {
		case <-hctx.Done():
		case <-time.After(wait):
		}
		return hctx.Err()
	}), e.log))
	w.Pass(ctx)
	got := e.job(t, j)
	if want := (jobs.Failure{Code: worker.WorkerStopped, Message: "the worker stopped during the attempt", Retryable: true}); got.Status != jobs.Queued || stored(t, got) != want || got.Attempt != 1 {
		t.Errorf("the job is %s, attempt %d, %s", got.Status, got.Attempt, got.Error)
	}
}

// A pass serves every assigned organization, one job each, in deployment
// order, so one organization's queue cannot starve another's.
func TestOrganizationsTakeTurns(t *testing.T) {
	e := setup(t, 2)
	a, b := e.orgs[0], e.orgs[1]
	queued := []jobs.Job{e.enqueue(t, a, jobs.ImportProfile, 0), e.enqueue(t, a, jobs.ImportProfile, 0),
		e.enqueue(t, b, jobs.ImportProfile, 0), e.enqueue(t, b, jobs.ImportProfile, 0)}
	var order []string
	w := e.worker(t, config(worker.HandlerFunc(func(ctx context.Context, job jobs.Job, finish worker.Finish) error {
		order = append(order, job.OrganizationID)
		return finish(ctx, jobs.Completed, profile(job))
	}), e.log))
	for pass := 1; pass <= 2; pass++ {
		if !w.Pass(context.Background()) {
			t.Fatalf("pass %d ran no job", pass)
		}
		if want := slicesRepeat([]string{a.ID, b.ID}, pass); fmt.Sprint(order) != fmt.Sprint(want) {
			t.Fatalf("after pass %d the organizations ran %v, want %v", pass, order, want)
		}
	}
	if w.Pass(context.Background()) {
		t.Error("a third pass ran a job")
	}
	for _, j := range queued {
		if got := e.job(t, j); got.Status != jobs.Completed {
			t.Errorf("%s is %s", j.ID, got.Status)
		}
	}
}

func slicesRepeat(s []string, n int) []string {
	var out []string
	for range n {
		out = append(out, s...)
	}
	return out
}

// A job type the worker has no handler for stays queued for a worker that
// has one.
func TestAnUnhandledTypeStaysQueued(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportValidate, 0)
	ran := false
	w := e.worker(t, config(worker.HandlerFunc(func(context.Context, jobs.Job, worker.Finish) error { ran = true; return nil }), e.log))
	if w.Pass(context.Background()) || ran {
		t.Error("the worker ran a job it has no handler for")
	}
	if got := e.job(t, j); got.Status != jobs.Queued || got.Attempt != 0 || got.LockedBy != "" {
		t.Errorf("the job is %+v", got)
	}
}

// Two workers on one queue run each job exactly once, and stop when told.
func TestWorkersShareTheQueue(t *testing.T) {
	e := setup(t, 1)
	var queued []jobs.Job
	for range 6 {
		queued = append(queued, e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0))
	}
	var mu sync.Mutex
	runs := map[string]int{}
	done := make(chan struct{}, len(queued))
	handler := worker.HandlerFunc(func(ctx context.Context, job jobs.Job, finish worker.Finish) error {
		mu.Lock()
		runs[job.ID]++
		mu.Unlock()
		if err := finish(ctx, jobs.Completed, profile(job)); err != nil {
			return err
		}
		done <- struct{}{}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var loops sync.WaitGroup
	for _, id := range []string{"worker-1", "worker-2"} {
		cfg := config(handler, e.log)
		cfg.ID = id
		w := e.worker(t, cfg)
		loops.Go(func() { w.Run(ctx) })
	}
	for range queued {
		select {
		case <-done:
		case <-time.After(wait):
			t.Fatal("the workers did not run every job")
		}
	}
	cancel()
	stopped := make(chan struct{})
	go func() { loops.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(wait):
		t.Fatal("Run did not return after its context ended")
	}
	for _, j := range queued {
		if runs[j.ID] != 1 {
			t.Errorf("%s ran %d times", j.ID, runs[j.ID])
		}
		if got := e.job(t, j); got.Status != jobs.Completed {
			t.Errorf("%s is %s", j.ID, got.Status)
		}
	}
}
