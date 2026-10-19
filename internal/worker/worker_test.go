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
	"github.com/kcl1104/geminus/internal/worker/handler"
)

const issuer = "https://test.invalid"

// policy is the tests' queue timing: a lease no test outlives unless it
// sets a short one, and a backoff long enough that a retried job never
// becomes due while a test runs.
var policy = jobs.Policy{Lease: time.Minute, MaxAttempts: 3, BackoffBase: time.Hour, BackoffMax: 4 * time.Hour}

// wait bounds every wait on the worker, so a broken loop fails the test
// instead of hanging it.
const wait = 20 * time.Second

func config(h handler.Handler, log io.Writer) worker.Config {
	return worker.Config{
		ID: "worker-1", Policy: policy, Heartbeat: 20 * time.Second, Idle: 20 * time.Millisecond, ReapLimit: 10,
		Handlers: map[jobs.Type]handler.Handler{jobs.ImportProfile: h},
		Log:      slog.New(slog.NewTextHandler(log, nil)),
	}
}

var noop = handler.Func(func(context.Context, handler.Run) error { return nil })

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
	w := e.worker(t, config(handler.Func(func(ctx context.Context, run handler.Run) error {
		job, finish := run.Job(), run.Finish
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
	w := e.worker(t, config(handler.Func(func(ctx context.Context, run handler.Run) error {
		job, finish := run.Job(), run.Finish
		for _, end := range []jobs.Status{jobs.Failed, jobs.Queued, jobs.Running, ""} {
			if err := finish(ctx, end, func(context.Context, tenant.Tx) error { ran = true; return nil }); !errors.Is(err, handler.ErrInvalidEnd) {
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

// A checkpoint reads and writes under the lease without ending the job:
// its writes commit while the lease holds, and once it is lost nothing runs.
func TestCheckpointsUnderTheLease(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	var first, second error
	ranAfterLoss, stopped := false, false
	statusBetween := ""
	w := e.worker(t, config(handler.Func(func(ctx context.Context, run handler.Run) error {
		first = run.Checkpoint(ctx, profile(run.Job()))
		statusBetween = e.importStatus(t, run.Job())
		e.exec(t, `UPDATE twin.jobs SET locked_by='worker-2', lease_token=lease_token+1 WHERE id=$1`, run.Job().ID)
		second = run.Checkpoint(context.WithoutCancel(ctx), func(context.Context, tenant.Tx) error { ranAfterLoss = true; return nil })
		// The handler is stopped at once, not at the next heartbeat.
		stopped = ctx.Err() != nil
		return second
	}), e.log))
	w.Pass(context.Background())
	if first != nil || statusBetween != "PROFILING" {
		t.Errorf("the checkpoint under the lease: %v; the import was %q before the end", first, statusBetween)
	}
	if !errors.Is(second, jobs.ErrLeaseLost) || ranAfterLoss || !stopped {
		t.Errorf("a checkpoint after the lease was lost: %v (its function ran: %v; the handler was stopped: %v)", second, ranAfterLoss, stopped)
	}
	if got := e.job(t, j); got.Status != jobs.Running || got.LockedBy != "worker-2" || got.Error != nil {
		t.Errorf("the worker wrote to a job it lost: %+v", got)
	}
}

// An attempt that ends without a result records why: the handler's
// Failure as given, anything else as a code of the loop's own; the error
// text is logged, never stored.
func TestFailedAttempts(t *testing.T) {
	e := setup(t, 1)
	// A value a handler panics with may hold the data it worked on: it is
	// neither stored nor logged. Error text is logged (handlers keep data
	// out of it), never stored.
	const secret = "row 3: customer@example.com"
	const errText = "reading s3://bucket/x: not found"
	// What the log keeps of an error: no control characters, 500 at most.
	long := "line 1\nline 2\x1b[2J" + strings.Repeat("x", 600)
	errBoom := errors.New("boom")
	for _, c := range []struct {
		name     string
		attempts int
		handle   func(ctx context.Context, run handler.Run) error
		status   jobs.Status
		failure  jobs.Failure
	}{
		{"a failure that is not retried", 0, func(context.Context, handler.Run) error {
			return &jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}},
		{"a wrapped retryable failure", 0, func(context.Context, handler.Run) error {
			return fmt.Errorf("profiling: %w", &jobs.Failure{Code: "STORAGE_UNAVAILABLE", Message: "the object store did not answer", Retryable: true})
		}, jobs.Queued, jobs.Failure{Code: "STORAGE_UNAVAILABLE", Message: "the object store did not answer", Retryable: true}},
		{"an error", 0, func(context.Context, handler.Run) error {
			return errors.New(errText)
		}, jobs.Queued, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		{"an error with control characters, overlong", 0, func(context.Context, handler.Run) error {
			return errors.New(long)
		}, jobs.Queued, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		{"an error on the last attempt", 1, func(context.Context, handler.Run) error {
			return errors.New(errText)
		}, jobs.Failed, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		// A handler's bug: a nil *Failure in a non-nil error.
		{"a typed nil failure", 0, func(context.Context, handler.Run) error {
			var f *jobs.Failure
			return f
		}, jobs.Queued, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		{"a failure without a code", 0, func(context.Context, handler.Run) error {
			return &jobs.Failure{Message: "no code", Retryable: false}
		}, jobs.Failed, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed"}},
		{"a failure with an invalid code", 0, func(context.Context, handler.Run) error {
			return &jobs.Failure{Code: "bad code", Message: "x", Retryable: true}
		}, jobs.Queued, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		{"a failure without a message", 0, func(context.Context, handler.Run) error {
			return &jobs.Failure{Code: "INVALID_INPUT"}
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: "the job failed"}},
		{"a failure with an overlong message", 0, func(context.Context, handler.Run) error {
			return &jobs.Failure{Code: "INVALID_INPUT", Message: strings.Repeat("字", 1001)}
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: "the job failed"}},
		{"the result's transaction fails", 0, func(ctx context.Context, run handler.Run) error {
			job, finish := run.Job(), run.Finish
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
		{"a panic with a value", 0, func(context.Context, handler.Run) error {
			panic(secret)
		}, jobs.Failed, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed"}},
		{"a runtime panic", 0, func(context.Context, handler.Run) error {
			var rows []int
			return fmt.Errorf("%d", rows[len(rows)])
		}, jobs.Failed, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed"}},
		{"no result", 0, func(context.Context, handler.Run) error {
			return nil
		}, jobs.Failed, jobs.Failure{Code: worker.NoResult, Message: "the handler returned without a result"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, c.attempts)
			if !e.worker(t, config(handler.Func(c.handle), e.log)).Pass(context.Background()) {
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
	out := e.log.String()
	if !strings.Contains(out, errText) || !strings.Contains(out, "index out of range [0] with length 0") || !strings.Contains(out, "handler panicked with a string") {
		t.Errorf("the handlers' errors were not logged as they may be: %s", out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("a panic's value was logged: %s", out)
	}
	const kept = "line 1 line 2 [2J"
	if !strings.Contains(out, kept+strings.Repeat("x", 500-len(kept))+"…") || strings.Contains(out, strings.Repeat("x", 501-len(kept))) {
		t.Errorf("an error's text was not logged without control characters and bounded: %s", out)
	}
	if !strings.Contains(out, "stack=\"goroutine ") {
		t.Errorf("a panic's stack was not logged: %s", out)
	}
}

// The heartbeat renews the lease, so a job may run for longer than one.
func TestTheHeartbeatKeepsALongJob(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	cfg := config(handler.Func(func(ctx context.Context, run handler.Run) error {
		job, finish := run.Job(), run.Finish
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
	cfg := config(handler.Func(func(ctx context.Context, run handler.Run) error {
		job, finish := run.Job(), run.Finish
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
	handler := handler.Func(func(ctx context.Context, run handler.Run) error {
		job, finish := run.Job(), run.Finish
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

// Timing tests use a lease of three seconds renewed every second: the
// handler must be stopped half a heartbeat before the lease could end, so
// at 2.5 s after the claim when nothing renews it, with the lease still
// valid in the database (checked there, with its clock) when it stops.
const (
	tLease = 3 * time.Second
	tBeat  = time.Second
)

func timed(cfg worker.Config) worker.Config {
	cfg.Policy.Lease, cfg.Heartbeat = tLease, tBeat
	return cfg
}

// leaseHolds reports whether job's lease is still unexpired by the
// database's clock.
func (e *env) leaseHolds(t *testing.T, id string) bool {
	t.Helper()
	var ok bool
	if err := e.admin.QueryRow(context.Background(), `SELECT status='running' AND lease_expires_at>clock_timestamp() FROM twin.jobs WHERE id=$1`, id).Scan(&ok); err != nil {
		t.Error(err)
	}
	return ok
}

// flaky is a worker scope whose database stops answering while down is set
// (every transaction fails at its start) and that counts transactions.
func (e *env) flaky(t *testing.T) (scope *workerscope.Worker, down *atomic.Bool, txs *atomic.Int64) {
	t.Helper()
	down, txs = new(atomic.Bool), new(atomic.Int64)
	pc, err := pgxpool.ParseConfig(dbtest.LoginURL(t, dbtest.WorkerLogin))
	if err != nil {
		t.Fatal(err)
	}
	pc.AfterRelease = tenant.Guard
	pc.PrepareConn = func(context.Context, *pgx.Conn) (bool, error) {
		txs.Add(1)
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
	var assigned []string
	for _, o := range e.orgs {
		assigned = append(assigned, o.ID)
	}
	scope, err = workerscope.New(pool, assigned)
	if err != nil {
		t.Fatal(err)
	}
	return scope, down, txs
}

// stopsInTime checks a handler that was stopped after running for ran:
// not before the deadline (no renewal succeeded since the claim), and while
// the database still held the lease.
func stopsInTime(t *testing.T, ran time.Duration, held bool) {
	t.Helper()
	if want := tLease - tBeat/2; ran < want-300*time.Millisecond || ran > want+300*time.Millisecond {
		t.Errorf("the handler stopped after %v, want about %v", ran, want)
	}
	if !held {
		t.Error("the handler was stopped after its lease had ended in the database")
	}
}

// A renewal that hangs (here on a row lock, as a database that does not
// answer would) cannot keep the handler running past its lease: the
// deadline stops it whether or not the renewal returns.
func TestAHungRenewalCannotOutliveTheLease(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	var ran time.Duration
	var held bool
	cfg := timed(config(handler.Func(func(ctx context.Context, run handler.Run) error {
		start := time.Now()
		tx, err := e.admin.Begin(context.Background())
		if err != nil {
			return err
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(context.Background(), `SELECT 1 FROM twin.jobs WHERE id=$1 FOR UPDATE`, run.Job().ID); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			ran, held = time.Since(start), e.leaseHolds(t, run.Job().ID)
		case <-time.After(wait):
		}
		return ctx.Err()
	}), e.log))
	e.worker(t, cfg).Pass(context.Background())
	stopsInTime(t, ran, held)
	if got := e.job(t, j); got.Status != jobs.Running || got.Error != nil {
		t.Errorf("the worker wrote to a job it lost: %+v", got)
	}
	if !strings.Contains(e.log.String(), "the lease could not be renewed in time") {
		t.Errorf("log: %s", e.log.String())
	}
}

// A database that does not answer at all: every renewal fails at once, and
// the handler is stopped at the deadline, while the lease still holds. The
// worker writes nothing; the lease ends on its own.
func TestTheHeartbeatGivesUpWithoutTheDatabase(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	scope, down, _ := e.flaky(t)
	var ran time.Duration
	var held bool
	cfg := timed(config(handler.Func(func(ctx context.Context, run handler.Run) error {
		down.Store(true)
		start := time.Now()
		select {
		case <-ctx.Done():
			ran, held = time.Since(start), e.leaseHolds(t, run.Job().ID)
		case <-time.After(wait):
		}
		return ctx.Err()
	}), e.log))
	w, err := worker.New(scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.Pass(context.Background())
	down.Store(false)
	stopsInTime(t, ran, held)
	if got := e.job(t, j); got.Status != jobs.Running || got.Error != nil || got.LockedBy != "worker-1" {
		t.Errorf("the worker wrote to a job it lost: %+v", got)
	}
	if out := e.log.String(); !strings.Contains(out, "the database does not answer") || !strings.Contains(out, "the attempt ends without writing") {
		t.Errorf("log: %s", out)
	}
}

// One failed renewal in a long job does not stop it: the deadline counts
// from the last renewal that succeeded, and the next one keeps the job.
func TestOneFailedRenewalIsSurvived(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	scope, down, _ := e.flaky(t)
	cfg := timed(config(handler.Func(func(ctx context.Context, run handler.Run) error {
		// Renewals at about 1 s and 2 s succeed, the one at 3 s fails, the
		// one at 4 s succeeds again.
		steps := []struct {
			at   time.Duration
			down bool
		}{{2500 * time.Millisecond, true}, {3500 * time.Millisecond, false}, {5500 * time.Millisecond, false}}
		start := time.Now()
		for _, st := range steps {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Until(start.Add(st.at))):
			}
			down.Store(st.down)
		}
		return run.Finish(ctx, jobs.Completed, profile(run.Job()))
	}), e.log))
	w, err := worker.New(scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.Pass(context.Background())
	if got := e.job(t, j); got.Status != jobs.Completed {
		t.Errorf("a job that outlived one failed renewal: %s %s", got.Status, got.Error)
	}
	if !strings.Contains(e.log.String(), "renewing the lease failed") {
		t.Errorf("the failed renewal was not logged: %s", e.log.String())
	}
}

// A renewal that succeeds late renews the lease from its transaction's
// start, which is where the worker counts from too: here the renewal at
// about 1 s waits on a row lock until 2.2 s (the tick at 2 s finds it still
// in flight and starts none), and the database stops answering after it.
// The lease then lasts until 1 s + 3 s, and the handler is stopped half a
// heartbeat before, at 3.5 s, while it holds.
func TestASlowRenewalCountsFromItsStart(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	scope, down, _ := e.flaky(t)
	var ran time.Duration
	var held bool
	cfg := timed(config(handler.Func(func(ctx context.Context, run handler.Run) error {
		start := time.Now()
		tx, err := e.admin.Begin(context.Background())
		if err != nil {
			return err
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(context.Background(), `SELECT 1 FROM twin.jobs WHERE id=$1 FOR UPDATE`, run.Job().ID); err != nil {
			return err
		}
		for _, st := range []struct {
			at time.Duration
			do func()
		}{
			{2200 * time.Millisecond, func() { tx.Rollback(context.Background()) }},
			{2500 * time.Millisecond, func() { down.Store(true) }},
			{wait, func() {}},
		} {
			select {
			case <-ctx.Done():
				ran, held = time.Since(start), e.leaseHolds(t, run.Job().ID)
				return ctx.Err()
			case <-time.After(time.Until(start.Add(st.at))):
			}
			st.do()
		}
		return ctx.Err()
	}), e.log))
	w, err := worker.New(scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.Pass(context.Background())
	down.Store(false)
	if want := time.Second + tLease - tBeat/2; ran < want-300*time.Millisecond || ran > want+300*time.Millisecond {
		t.Errorf("the handler stopped after %v, want about %v", ran, want)
	}
	if !held {
		t.Error("the handler was stopped after its lease had ended in the database")
	}
	if got := e.job(t, j); got.Status != jobs.Running || got.Error != nil || got.LockedBy != "worker-1" {
		t.Errorf("the worker wrote to a job it lost: %+v", got)
	}
}

// A worker that stops during an attempt puts the job back for a retry,
// unless the handler's own failure says it may not be retried.
func TestStoppingRequeuesTheJob(t *testing.T) {
	e := setup(t, 1)
	budget := &jobs.Failure{Code: "BUDGET_EXCEEDED", Message: "the run used its event budget"}
	for _, c := range []struct {
		name   string
		ret    func(ctx context.Context) error
		status jobs.Status
		want   jobs.Failure
	}{
		{"the handler returns the context's error", func(ctx context.Context) error { return ctx.Err() }, jobs.Queued,
			jobs.Failure{Code: worker.WorkerStopped, Message: "the worker stopped during the attempt", Retryable: true}},
		{"the handler returns a failure that may not be retried", func(context.Context) error { return budget }, jobs.Failed, *budget},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := e.worker(t, config(handler.Func(func(hctx context.Context, run handler.Run) error {
				cancel()
				select {
				case <-hctx.Done():
				case <-time.After(wait):
				}
				return c.ret(hctx)
			}), e.log))
			w.Pass(ctx)
			if got := e.job(t, j); got.Status != c.status || stored(t, got) != c.want || got.Attempt != 1 {
				t.Errorf("the job is %s, attempt %d, %s", got.Status, got.Attempt, got.Error)
			}
		})
	}
}

// Recording the attempt's end waits at most one heartbeat: a database that
// does not answer cannot hold the worker (the reaper takes the job back).
func TestTheFailureRecordIsBounded(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	release := make(chan struct{})
	var held sync.WaitGroup
	cfg := config(handler.Func(func(ctx context.Context, run handler.Run) error {
		// The job's row stays locked after the handler returns, so the
		// failure record waits for it.
		locked := make(chan struct{})
		held.Go(func() {
			tx, err := e.admin.Begin(context.Background())
			if err != nil {
				t.Error(err)
				close(locked)
				return
			}
			defer tx.Rollback(context.Background())
			if _, err := tx.Exec(context.Background(), `SELECT 1 FROM twin.jobs WHERE id=$1 FOR UPDATE`, run.Job().ID); err != nil {
				t.Error(err)
			}
			close(locked)
			select {
			case <-release:
			case <-time.After(wait):
			}
		})
		<-locked
		return errors.New("the object store did not answer")
	}), e.log)
	cfg.Policy.Lease, cfg.Heartbeat = 1500*time.Millisecond, 500*time.Millisecond
	start := time.Now()
	e.worker(t, cfg).Pass(context.Background())
	took := time.Since(start)
	close(release)
	held.Wait()
	if took > 2*time.Second {
		t.Errorf("the pass took %v with the record blocked, want about one heartbeat", took)
	}
	if !strings.Contains(e.log.String(), "recording the failure failed") {
		t.Errorf("log: %s", e.log.String())
	}
	if got := e.job(t, j); got.Status != jobs.Running {
		t.Errorf("the job is %s; the reaper is to take it back", got.Status)
	}
}

// Without jobs, a running worker pauses between passes: an idle worker
// makes two transactions per organization per Idle, not a busy loop.
func TestAnIdleWorkerPauses(t *testing.T) {
	e := setup(t, 1)
	scope, _, txs := e.flaky(t)
	cfg := config(handler.Func(func(context.Context, handler.Run) error { return nil }), e.log)
	cfg.Idle = 200 * time.Millisecond
	w, err := worker.New(scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w.Run(ctx)
	if n := txs.Load(); n < 4 || n > 14 {
		t.Errorf("%d transactions in a second of idling with a 200 ms pause, want about 10", n)
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
	w := e.worker(t, config(handler.Func(func(ctx context.Context, run handler.Run) error {
		job, finish := run.Job(), run.Finish
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
	w := e.worker(t, config(handler.Func(func(context.Context, handler.Run) error { ran = true; return nil }), e.log))
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
	handler := handler.Func(func(ctx context.Context, run handler.Run) error {
		job, finish := run.Job(), run.Finish
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
