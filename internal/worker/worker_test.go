package worker_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
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

// profile is the result the tests' handlers commit: the import moves on,
// marked as the handler's (one row counted), so that a result is told
// apart from the import following its failed job (0010).
func profile(j jobs.Job) func(ctx context.Context, tx tenant.Tx) error {
	return func(ctx context.Context, tx tenant.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE twin.imports SET status='PROFILING', total_rows=1 WHERE id=$1`, j.Resource.ImportID)
		return err
	}
}

// untouched is the import as a job that wrote no result leaves it: still
// UPLOADED, or FAILED with the job's error when the job failed (the import
// follows its profile job, 0010), and never with the handler's mark.
func (e *env) untouched(t *testing.T, j jobs.Job) string {
	t.Helper()
	var s string
	var rows int
	var followed bool
	if err := e.admin.QueryRow(context.Background(), `SELECT i.status, i.total_rows, i.error IS NOT DISTINCT FROM j.error
		FROM twin.imports i JOIN twin.jobs j ON j.import_id=i.id WHERE j.id=$1`, j.ID).Scan(&s, &rows, &followed); err != nil {
		t.Fatal(err)
	}
	got := e.job(t, j)
	want := "UPLOADED"
	if got.Status == jobs.Failed || got.Status == jobs.Cancelled {
		want = "FAILED"
	}
	if s != want || rows != 0 || (want == "FAILED" && !followed) {
		return fmt.Sprintf("%s with %d rows (the job %s; its error recorded: %v)", s, rows, got.Status, followed)
	}
	return ""
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
	// The attempt ends when its handler returns, not when its lease (a
	// minute) would (round 3 L2).
	if !passWithin(t, w, 5*time.Second) {
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
		// The limit is in characters, not bytes.
		{"a failure with a long message within the limit", 0, func(context.Context, handler.Run) error {
			return &jobs.Failure{Code: "INVALID_INPUT", Message: strings.Repeat("字", 400)}
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: strings.Repeat("字", 400)}},
		// What JSON or PostgreSQL cannot store is replaced, so the attempt
		// still ends with the handler's verdict (round 2 L4).
		{"a failure whose message holds NUL", 0, func(context.Context, handler.Run) error {
			return &jobs.Failure{Code: "INVALID_INPUT", Message: "a\x00b"}
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: "the job failed"}},
		{"a failure whose message is not UTF-8", 0, func(context.Context, handler.Run) error {
			return &jobs.Failure{Code: "INVALID_INPUT", Message: "a\xffb"}
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: "the job failed"}},
		{"a failure with a code over 64 characters", 0, func(context.Context, handler.Run) error {
			return &jobs.Failure{Code: "A" + strings.Repeat("B", 64), Message: "x"}
		}, jobs.Failed, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed"}},
		// Error types whose methods fail on their own value (round 2 M1): the
		// worker survives them and records a bug, not a retry.
		{"a typed nil *os.PathError", 0, func(context.Context, handler.Run) error {
			var p *os.PathError
			return p
		}, jobs.Failed, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed"}},
		{"a typed nil error whose Error reads its value", 0, func(context.Context, handler.Run) error {
			var r *rowError
			return r
		}, jobs.Failed, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed"}},
		// A tree of more than 64 errors is not read further: an ordinary
		// failure, retried (round 3 I1), whether it never ends or is just
		// large; one within the bound is read to its end (round 3 L2).
		{"an error that wraps itself", 0, func(context.Context, handler.Run) error {
			return selfWrapping{}
		}, jobs.Queued, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		{"a join of many errors", 0, func(context.Context, handler.Run) error {
			return errors.Join(slices.Repeat([]error{errBoom}, 70)...)
		}, jobs.Queued, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		{"a failure deep in a chain", 0, func(context.Context, handler.Run) error {
			var err error = &jobs.Failure{Code: "INVALID_INPUT", Message: "deep"}
			for i := range 40 {
				err = fmt.Errorf("step %d: %w", i, err)
			}
			return err
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: "deep"}},
		{"a failure among many errors", 0, func(context.Context, handler.Run) error {
			return errors.Join(append(slices.Repeat([]error{errBoom}, 40), &jobs.Failure{Code: "INVALID_INPUT", Message: "joined"})...)
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: "joined"}},
		// The same errors from a transaction's function (round 3 L1): the
		// transaction never reads one the worker could not, so the worker
		// neither stalls nor loses the handler's verdict.
		{"a checkpoint's error that wraps itself", 0, func(ctx context.Context, run handler.Run) error {
			return run.Checkpoint(ctx, func(context.Context, tenant.Tx) error { return selfWrapping{} })
		}, jobs.Queued, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed", Retryable: true}},
		{"a checkpoint's typed nil *os.PathError", 0, func(ctx context.Context, run handler.Run) error {
			return run.Checkpoint(ctx, func(context.Context, tenant.Tx) error {
				var p *os.PathError
				return p
			})
		}, jobs.Failed, jobs.Failure{Code: worker.Internal, Message: "the job's handler failed"}},
		{"a checkpoint's failure", 0, func(ctx context.Context, run handler.Run) error {
			return run.Checkpoint(ctx, func(context.Context, tenant.Tx) error {
				return &jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}
			})
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}},
		{"a result's failure deep in a chain", 0, func(ctx context.Context, run handler.Run) error {
			return run.Finish(ctx, jobs.Completed, func(context.Context, tenant.Tx) error {
				var err error = &jobs.Failure{Code: "INVALID_INPUT", Message: "deep"}
				for i := range 40 {
					err = fmt.Errorf("step %d: %w", i, err)
				}
				return err
			})
		}, jobs.Failed, jobs.Failure{Code: "INVALID_INPUT", Message: "deep"}},
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
		// The worker reads its own outcome of the transaction, never with
		// fn's error's Is methods: one that panics is an ordinary error,
		// retried (round 4 L2).
		{"a checkpoint's error whose Is panics", 0, func(ctx context.Context, run handler.Run) error {
			return run.Checkpoint(ctx, func(context.Context, tenant.Tx) error { return isPanics{} })
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
			if !passWithin(t, e.worker(t, config(handler.Func(c.handle), e.log)), 5*time.Second) {
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
			if s := e.untouched(t, j); s != "" {
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
	if !strings.Contains(out, "cannot be read") || !strings.Contains(out, "type=*fs.PathError") || !strings.Contains(out, "type=*worker_test.rowError") {
		t.Errorf("the unreadable errors were not logged by type: %s", out)
	}
	if !strings.Contains(out, "too large to read") || !strings.Contains(out, "type=worker_test.selfWrapping") || !strings.Contains(out, "type=*errors.joinError") {
		t.Errorf("the errors too large to read were not logged by type: %s", out)
	}
	// Read inside a recover: no unreadable error panics the handler.
	if strings.Contains(out, "nil pointer dereference") {
		t.Errorf("reading an error panicked: %s", out)
	}
}

// passWithin runs one pass of w, failing the test if it does not return
// within d: a worker that stalls fails here instead of at the test binary's
// timeout (round 3 L2).
func passWithin(t *testing.T, w *worker.Worker, d time.Duration) bool {
	t.Helper()
	done := make(chan bool, 1)
	go func() { done <- w.Pass(context.Background()) }()
	select {
	case ran := <-done:
		return ran
	case <-time.After(d):
		t.Fatalf("the pass did not return within %v", d)
		return false
	}
}

// rowError reads its own value in Error, as a careless handler's might.
type rowError struct{ row int }

func (r *rowError) Error() string { return fmt.Sprintf("row %d", r.row) }

// isPanics is an error whose Is method panics.
type isPanics struct{}

func (isPanics) Error() string { return "is panics" }
func (isPanics) Is(error) bool { panic("Is called") }

// selfWrapping is an Unwrap chain that never ends.
type selfWrapping struct{}

func (selfWrapping) Error() string   { return "again" }
func (e selfWrapping) Unwrap() error { return e }

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

// flakyDB is a worker scope on its own pool whose database can be made to
// misbehave, and that counts transactions.
type flakyDB struct {
	scope *workerscope.Worker
	// down: every transaction fails at its start.
	down atomic.Bool
	txs  atomic.Int64
	// hangNext: the next transaction hangs this long (or until its context
	// ends) at its start, then fails.
	hangNext atomic.Int64
	// claimDelay: the claim's statement waits this long after its
	// transaction began.
	claimDelay atomic.Int64
	// holdCommit: the database's answer to the next COMMIT is held back this
	// long, as on a slow network; the COMMIT itself reaches the database,
	// which commits at once.
	holdCommit atomic.Int64
}

// heldConn is a connection to the database that holds back the answer to a
// COMMIT when its flakyDB asks, until the hold ends or the client gives up
// waiting (the read deadline pgx sets when its context ends).
type heldConn struct {
	net.Conn
	f        *flakyDB
	mu       sync.Mutex
	hold     time.Duration
	deadline time.Time
	changed  chan struct{}
}

func (c *heldConn) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("commit\x00")) {
		if h := c.f.holdCommit.Swap(0); h > 0 {
			c.mu.Lock()
			c.hold = time.Duration(h)
			c.mu.Unlock()
		}
	}
	return c.Conn.Write(p)
}

// Read holds back the first answer after a held COMMIT (a read may have
// begun before the COMMIT was written, so the answer is held once read).
func (c *heldConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	h := c.hold
	if n > 0 {
		c.hold = 0
	}
	c.mu.Unlock()
	if h > 0 && n > 0 && !c.waitOut(h) {
		return 0, os.ErrDeadlineExceeded
	}
	return n, err
}

// waitOut waits for h, or until the read deadline passes (false).
func (c *heldConn) waitOut(h time.Duration) bool {
	end := time.Now().Add(h)
	for {
		c.mu.Lock()
		d, changed := c.deadline, c.changed
		c.mu.Unlock()
		now := time.Now()
		if !d.IsZero() && !now.Before(d) {
			return false
		}
		if !now.Before(end) {
			return true
		}
		until := end
		if !d.IsZero() && d.Before(until) {
			until = d
		}
		select {
		case <-time.After(time.Until(until)):
		case <-changed:
		}
	}
}

func (c *heldConn) SetDeadline(t time.Time) error {
	c.setReadDeadline(t)
	return c.Conn.SetDeadline(t)
}

func (c *heldConn) SetReadDeadline(t time.Time) error {
	c.setReadDeadline(t)
	return c.Conn.SetReadDeadline(t)
}

func (c *heldConn) setReadDeadline(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	close(c.changed)
	c.changed = make(chan struct{})
}

// slowClaim delays the claim's statement (TraceQueryStart runs after the
// transaction began, so its now() is already fixed).
type slowClaim struct{ f *flakyDB }

func (s slowClaim) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if d := s.f.claimDelay.Load(); d > 0 && strings.Contains(data.SQL, "next_job") {
		time.Sleep(time.Duration(d))
	}
	return ctx
}

func (slowClaim) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (e *env) flakyDB(t *testing.T) *flakyDB {
	t.Helper()
	f := &flakyDB{}
	pc, err := pgxpool.ParseConfig(dbtest.LoginURL(t, dbtest.WorkerLogin))
	if err != nil {
		t.Fatal(err)
	}
	pc.AfterRelease = tenant.Guard
	pc.ConnConfig.Tracer = slowClaim{f}
	pc.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &heldConn{Conn: c, f: f, changed: make(chan struct{})}, nil
	}
	pc.PrepareConn = func(ctx context.Context, _ *pgx.Conn) (bool, error) {
		f.txs.Add(1)
		if f.down.Load() {
			return true, errors.New("the database does not answer")
		}
		if h := f.hangNext.Swap(0); h > 0 {
			select {
			case <-time.After(time.Duration(h)):
			case <-ctx.Done():
			}
			return true, errors.New("the database hung, then failed")
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
	if f.scope, err = workerscope.New(pool, assigned); err != nil {
		t.Fatal(err)
	}
	return f
}

// flaky is flakyDB for the tests that only need it down.
func (e *env) flaky(t *testing.T) (*workerscope.Worker, *atomic.Bool, *atomic.Int64) {
	f := e.flakyDB(t)
	return f.scope, &f.down, &f.txs
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
// about 1 s waits on a row lock until 1.8 s (within its one heartbeat), and
// the database stops answering after it. The lease then lasts until 1 s +
// 3 s, and the handler is stopped half a heartbeat before, at 3.5 s, while
// it holds.
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
			{1800 * time.Millisecond, func() { tx.Rollback(context.Background()) }},
			{1900 * time.Millisecond, func() { down.Store(true) }},
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

// A renewal that hangs is given up after one heartbeat and the next starts
// at once, so a connection that hangs and then fails (a failover) does not
// use up the lease: here the renewal at about 1 s hangs for 1.5 s; it is
// given up at 2 s, the retry succeeds, and the job outlives its lease.
func TestAHungRenewalIsRetried(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	f := e.flakyDB(t)
	cfg := timed(config(handler.Func(func(ctx context.Context, run handler.Run) error {
		start := time.Now()
		f.hangNext.Store(int64(1500 * time.Millisecond))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5500*time.Millisecond - time.Since(start)):
		}
		return run.Finish(ctx, jobs.Completed, profile(run.Job()))
	}), e.log))
	w, err := worker.New(f.scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.Pass(context.Background())
	if got := e.job(t, j); got.Status != jobs.Completed {
		t.Errorf("a job whose renewal hung once: %s %s", got.Status, got.Error)
	}
	if !strings.Contains(e.log.String(), "the database hung, then failed") {
		t.Errorf("the hung renewal was not logged: %s", e.log.String())
	}
}

// The deadline counts from before the claim's transaction began, where the
// database's lease starts: a claim whose statement takes 0.8 s (after its
// transaction's now() was fixed) leaves the handler 0.8 s less.
func TestASlowClaimCountsFromBeforeIt(t *testing.T) {
	e := setup(t, 1)
	e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	f := e.flakyDB(t)
	const slow = 800 * time.Millisecond
	f.claimDelay.Store(int64(slow))
	var ran time.Duration
	var held bool
	cfg := timed(config(handler.Func(func(ctx context.Context, run handler.Run) error {
		f.claimDelay.Store(0)
		f.down.Store(true)
		start := time.Now()
		select {
		case <-ctx.Done():
			ran, held = time.Since(start), e.leaseHolds(t, run.Job().ID)
		case <-time.After(wait):
		}
		return ctx.Err()
	}), e.log))
	w, err := worker.New(f.scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.Pass(context.Background())
	f.down.Store(false)
	if want := tLease - tBeat/2 - slow; ran < want-300*time.Millisecond || ran > want+300*time.Millisecond {
		t.Errorf("the handler stopped after %v, want about %v", ran, want)
	}
	if !held {
		t.Error("the handler was stopped after its lease had ended in the database")
	}
}

// Renewals are due every heartbeat from the claim's start, not from when
// the handler began (round 3 I2): after a claim of 0.6 s, the renewal due
// at 1 s fails and the one at 2 s keeps the job. Counted from the handler's
// start they would come at 1.6 s and 2.6 s, after the deadline at 2.5 s.
func TestRenewalsAreDueFromTheClaim(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	f := e.flakyDB(t)
	f.claimDelay.Store(int64(600 * time.Millisecond))
	cfg := timed(config(handler.Func(func(ctx context.Context, run handler.Run) error {
		f.claimDelay.Store(0)
		f.down.Store(true)
		start := time.Now()
		for _, st := range []struct {
			at time.Duration
			do func()
		}{
			{1200 * time.Millisecond, func() { f.down.Store(false) }},
			{2500 * time.Millisecond, func() {}},
		} {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Until(start.Add(st.at))):
			}
			st.do()
		}
		return run.Finish(ctx, jobs.Completed, profile(run.Job()))
	}), e.log))
	w, err := worker.New(f.scope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.Pass(context.Background())
	f.down.Store(false)
	if got := e.job(t, j); got.Status != jobs.Completed {
		t.Errorf("the job is %s %s", got.Status, got.Error)
	}
	if !strings.Contains(e.log.String(), "renewing the lease failed") {
		t.Errorf("the failed renewal was not logged: %s", e.log.String())
	}
}

// Once the attempt has stopped, Checkpoint and Finish write nothing, even
// on a context the handler detached: the lease's deadline gives
// jobs.ErrLeaseLost, the worker stopping handler.ErrStopped.
func TestNothingIsWrittenAfterTheAttemptStops(t *testing.T) {
	e := setup(t, 1)
	for _, c := range []struct {
		name   string
		stop   func(f *flakyDB, cancel func())
		want   error
		status jobs.Status
	}{
		{"at the lease's deadline", func(f *flakyDB, _ func()) { f.down.Store(true) }, jobs.ErrLeaseLost, jobs.Running},
		{"when the worker stops", func(_ *flakyDB, cancel func()) { cancel() }, handler.ErrStopped, jobs.Queued},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
			f := e.flakyDB(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var cp, fin error
			ran := false
			cfg := timed(config(handler.Func(func(hctx context.Context, run handler.Run) error {
				c.stop(f, cancel)
				select {
				case <-hctx.Done():
				case <-time.After(wait):
				}
				f.down.Store(false)
				write := func(ctx context.Context, tx tenant.Tx) error { ran = true; return profile(run.Job())(ctx, tx) }
				cp = run.Checkpoint(context.WithoutCancel(hctx), write)
				fin = run.Finish(context.WithoutCancel(hctx), jobs.Completed, write)
				return hctx.Err()
			}), e.log))
			w, err := worker.New(f.scope, cfg)
			if err != nil {
				t.Fatal(err)
			}
			w.Pass(ctx)
			if !errors.Is(cp, c.want) || !errors.Is(fin, c.want) || ran {
				t.Errorf("after the stop: checkpoint %v, finish %v, the function ran: %v", cp, fin, ran)
			}
			if got := e.job(t, j); got.Status != c.status {
				t.Errorf("the job is %s %s, want %s", got.Status, got.Error, c.status)
			}
			if s := e.importStatus(t, j); s != "UPLOADED" {
				t.Errorf("the import is %s", s)
			}
		})
	}
}

// A transaction still open when the attempt stops is cancelled and rolls
// back, whatever context the handler gave it: here a checkpoint begun under
// the lease writes, then keeps the row lock past the deadline (its handler
// ignoring the context), and nothing of it commits.
func TestATransactionIsCancelledWhenTheAttemptStops(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	var cp error
	var took time.Duration
	cfg := timed(config(handler.Func(func(ctx context.Context, run handler.Run) error {
		start := time.Now()
		cp = run.Checkpoint(context.WithoutCancel(ctx), func(ctx context.Context, tx tenant.Tx) error {
			if err := profile(run.Job())(ctx, tx); err != nil {
				return err
			}
			<-time.After(3500*time.Millisecond - time.Since(start))
			return nil
		})
		took = time.Since(start)
		return cp
	}), e.log))
	e.worker(t, cfg).Pass(context.Background())
	if !errors.Is(cp, jobs.ErrLeaseLost) || took > 4*time.Second {
		t.Errorf("the checkpoint returned %v after %v", cp, took)
	}
	if s := e.importStatus(t, j); s != "UPLOADED" {
		t.Errorf("a transaction outlived its attempt: the import is %s", s)
	}
	if got := e.job(t, j); got.Status != jobs.Running || got.LockedBy != "worker-1" || got.Error != nil {
		t.Errorf("the worker wrote to the job: %+v", got)
	}
}

// Run returns soon after its context ends (bounded here, so a loop that
// ignores it fails the test instead of hanging it).
func TestRunReturnsWhenStopped(t *testing.T) {
	e := setup(t, 1)
	w := e.worker(t, config(noop, e.log))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5 s of its context ending")
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
		// No result because the worker stopped it: a retry, not NO_RESULT.
		{"the handler returns nil", func(context.Context) error { return nil }, jobs.Queued,
			jobs.Failure{Code: worker.WorkerStopped, Message: "the worker stopped during the attempt", Retryable: true}},
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

// A failure the transaction's function returns while the attempt stops
// still counts: the call returns the stop wrapping it, nothing commits, and
// the handler's verdict is recorded (round 3 M1).
func TestAFailureReturnedWhileStoppingCounts(t *testing.T) {
	e := setup(t, 1)
	invalid := &jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}
	for _, c := range []struct {
		name string
		call func(ctx context.Context, run handler.Run, fn func(context.Context, tenant.Tx) error) error
	}{
		{"checkpoint", func(ctx context.Context, run handler.Run, fn func(context.Context, tenant.Tx) error) error {
			return run.Checkpoint(ctx, fn)
		}},
		{"finish", func(ctx context.Context, run handler.Run, fn func(context.Context, tenant.Tx) error) error {
			return run.Finish(ctx, jobs.Completed, fn)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var err error
			w := e.worker(t, config(handler.Func(func(hctx context.Context, run handler.Run) error {
				err = c.call(hctx, run, func(ctx context.Context, tx tenant.Tx) error {
					if err := profile(run.Job())(ctx, tx); err != nil {
						return err
					}
					cancel()
					select {
					case <-ctx.Done():
					case <-time.After(wait):
					}
					return invalid
				})
				return err
			}), e.log))
			w.Pass(ctx)
			var f *jobs.Failure
			if !errors.Is(err, handler.ErrStopped) || !errors.As(err, &f) || f != invalid {
				t.Errorf("the call returned %v", err)
			}
			if got := e.job(t, j); got.Status != jobs.Failed || stored(t, got) != *invalid {
				t.Errorf("the job is %s %s, want failed with the handler's failure", got.Status, got.Error)
			}
			if s := e.untouched(t, j); s != "" {
				t.Errorf("a transaction that failed committed: the import is %s", s)
			}
		})
	}
}

// A Failure fn returns after the attempt lost its lease (here its deadline
// passed while fn held the job's row lock, so no renewal could land) reaches
// the handler wrapped in jobs.ErrLeaseLost, not ErrStopped; nothing is
// recorded, the job being no longer the attempt's (round 4 L2).
func TestAFailureReturnedAfterTheLeaseIsLost(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	invalid := &jobs.Failure{Code: "INVALID_INPUT", Message: "the upload is not CSV"}
	var err error
	cfg := config(handler.Func(func(hctx context.Context, run handler.Run) error {
		err = run.Checkpoint(hctx, func(ctx context.Context, tx tenant.Tx) error {
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
			return invalid
		})
		return err
	}), e.log)
	cfg.Policy.Lease, cfg.Heartbeat = 1500*time.Millisecond, 500*time.Millisecond
	if !passWithin(t, e.worker(t, cfg), 5*time.Second) {
		t.Fatal("the pass ran no job")
	}
	var f *jobs.Failure
	if !errors.Is(err, jobs.ErrLeaseLost) || errors.Is(err, handler.ErrStopped) || !errors.As(err, &f) || f != invalid {
		t.Errorf("the call returned %v", err)
	}
	if got := e.job(t, j); got.Status != jobs.Running || got.Error != nil || got.Attempt != 1 {
		t.Errorf("the worker recorded the end of a job it lost: %+v", got)
	}
	if s := e.importStatus(t, j); s != "UPLOADED" {
		t.Errorf("the import is %s", s)
	}
}

// A COMMIT the database carried out before the attempt stopped, its answer
// still on the way, may have committed: the call says so
// (tenant.ErrOutcomeUnknown), never that it rolled back (round 3 M1). Here
// the answer is held back 2 s and the worker stops 300 ms into the call.
// The job's end is then whatever committed: a finished job stays finished
// (the attempt's own record is fenced out), a checkpoint's job is retried.
func TestALostCommitAnswerIsNotReportedAsRolledBack(t *testing.T) {
	e := setup(t, 1)
	for _, c := range []struct {
		name   string
		call   func(ctx context.Context, run handler.Run) error
		status jobs.Status
		log    string
	}{
		{"finish", func(ctx context.Context, run handler.Run) error {
			return run.Finish(ctx, jobs.Completed, profile(run.Job()))
		}, jobs.Completed, "the job is no longer this attempt's"},
		{"checkpoint", func(ctx context.Context, run handler.Run) error {
			return run.Checkpoint(ctx, profile(run.Job()))
		}, jobs.Queued, "code=WORKER_STOPPED retried=true"},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
			f := e.flakyDB(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var err error
			var took time.Duration
			log := &logs{}
			w, werr := worker.New(f.scope, config(handler.Func(func(hctx context.Context, run handler.Run) error {
				f.holdCommit.Store(int64(2 * time.Second))
				stop := time.AfterFunc(300*time.Millisecond, cancel)
				defer stop.Stop()
				start := time.Now()
				err = c.call(hctx, run)
				took = time.Since(start)
				return err
			}), log))
			if werr != nil {
				t.Fatal(werr)
			}
			w.Pass(ctx)
			if !errors.Is(err, tenant.ErrOutcomeUnknown) || errors.Is(err, handler.ErrStopped) || errors.Is(err, jobs.ErrLeaseLost) || took > time.Second {
				t.Errorf("the call returned %v after %v", err, took)
			}
			if s := e.importStatus(t, j); s != "PROFILING" {
				t.Errorf("the committed write is missing: the import is %s", s)
			}
			if got := e.job(t, j); got.Status != c.status {
				t.Errorf("the job is %s %s, want %s", got.Status, got.Error, c.status)
			}
			if out := log.String(); !strings.Contains(out, c.log) || strings.Contains(out, "recording the failure failed") {
				t.Errorf("log: %s", out)
			}
		})
	}
}

// A checkpoint ends with the handler's own context too: here its deadline
// ends a statement that would take 5 s, the transaction rolls back, and
// the attempt goes on to finish (round 3 L2).
func TestACheckpointEndsWithItsContext(t *testing.T) {
	e := setup(t, 1)
	j := e.enqueue(t, e.orgs[0], jobs.ImportProfile, 0)
	var cp error
	var took time.Duration
	w := e.worker(t, config(handler.Func(func(ctx context.Context, run handler.Run) error {
		cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		cp = run.Checkpoint(cctx, func(ctx context.Context, tx tenant.Tx) error {
			if err := profile(run.Job())(ctx, tx); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `SELECT pg_sleep(5)`)
			return err
		})
		took = time.Since(start)
		return run.Finish(ctx, jobs.Completed, func(context.Context, tenant.Tx) error { return nil })
	}), e.log))
	passWithin(t, w, 10*time.Second)
	if !errors.Is(cp, context.DeadlineExceeded) || errors.Is(cp, handler.ErrStopped) || took > 2*time.Second {
		t.Errorf("the checkpoint returned %v after %v", cp, took)
	}
	if got := e.job(t, j); got.Status != jobs.Completed {
		t.Errorf("the job is %s %s", got.Status, got.Error)
	}
	if s := e.importStatus(t, j); s != "UPLOADED" {
		t.Errorf("the cancelled checkpoint committed: the import is %s", s)
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
	// One heartbeat, not the lease (round 3 L2).
	cfg.Policy.Lease, cfg.Heartbeat = 3*time.Second, 500*time.Millisecond
	start := time.Now()
	e.worker(t, cfg).Pass(context.Background())
	took := time.Since(start)
	close(release)
	held.Wait()
	if took > 1500*time.Millisecond {
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
