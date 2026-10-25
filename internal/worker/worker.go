// Package worker is the job loop (T-019; ARCHITECTURE §6, ADR-024): it serves
// the organizations the deployment assigns to it (internal/tenant/workerscope),
// one transaction per organization; in each it takes back expired leases,
// then claims one job of a type it has a handler for (internal/worker/handler)
// and runs it, renewing the lease meanwhile and stopping the handler before
// the lease could end. Every database access of the handler is fenced by the
// lease and ends with the attempt: once the handler is stopped nothing it
// does commits, except a COMMIT the database had already carried out, which
// the handler is told may have committed (tenant.ErrOutcomeUnknown).
package worker

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/audit"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// Failure codes the loop records itself.
const (
	Internal      = "INTERNAL"       // the handler failed (retried) or panicked (not retried)
	NoResult      = "NO_RESULT"      // the handler returned without finishing
	WorkerStopped = "WORKER_STOPPED" // the worker stopped during the attempt (retried)
)

// Config is the worker's identity, timing and handlers.
type Config struct {
	// ID names this worker in the lease (twin.jobs.locked_by) and in the
	// audit events it writes: an actor reference (audit.ValidActor, D-092).
	ID     string
	Policy jobs.Policy
	// Heartbeat renews the lease; at most a third of Policy.Lease. Each
	// renewal may take at most one Heartbeat, and the handler is stopped half
	// a Heartbeat before the lease could end (see heartbeat), so one renewal
	// may fail without stopping it.
	Heartbeat time.Duration
	// Idle is the pause after a pass that found no job.
	Idle time.Duration
	// ReapLimit is how many expired leases one pass takes back per
	// organization.
	ReapLimit int
	Handlers  map[jobs.Type]handler.Handler
	Log       *slog.Logger
}

// Worker is the loop.
type Worker struct {
	scope *workerscope.Worker
	cfg   Config
	types []jobs.Type
}

// New checks cfg and returns the loop on scope.
func New(scope *workerscope.Worker, cfg Config) (*Worker, error) {
	switch {
	case scope == nil || len(scope.Assigned()) == 0:
		return nil, errors.New("worker: no assigned organization")
	case !audit.ValidActor(cfg.ID):
		return nil, errors.New("worker: the worker ID must start with a letter and use only letters, digits, '_', '.', ':' and '-' (at most 128): it names the worker in audit events (D-092)")
	case len(cfg.Handlers) == 0:
		return nil, errors.New("worker: no job handler")
	case cfg.Policy.Lease <= 0 || cfg.Heartbeat <= 0 || cfg.Heartbeat*3 > cfg.Policy.Lease:
		return nil, errors.New("worker: the heartbeat must be positive and at most a third of the lease")
	case cfg.Idle <= 0 || cfg.ReapLimit < 1 || cfg.Log == nil:
		return nil, errors.New("worker: idle, reap limit and log are required")
	}
	w := &Worker{scope: scope, cfg: cfg}
	for t, h := range cfg.Handlers {
		if h == nil {
			return nil, fmt.Errorf("worker: no handler for %s", t)
		}
		w.types = append(w.types, t)
	}
	slices.Sort(w.types)
	return w, nil
}

// Run passes over the assigned organizations until ctx ends, pausing for
// Idle after a pass that found no job.
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if w.Pass(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(w.cfg.Idle):
		}
	}
}

// Pass visits each assigned organization once, in deployment order: it
// reaps expired leases, then claims and runs at most one job. It reports
// whether a job ran. Errors are logged; the next pass tries again.
func (w *Worker) Pass(ctx context.Context) bool {
	worked := false
	for _, org := range w.scope.Assigned() {
		if ctx.Err() != nil {
			return worked
		}
		log := w.cfg.Log.With("organization_id", org)
		// Each job taken back is audited in the reap's own transaction
		// (D-091 (b)): when an event cannot be written, nothing is reaped
		// and the next pass tries again.
		var requeued, failed int
		if err := w.scope.Run(ctx, org, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
			requeued, failed = 0, 0
			reaped, err := jobs.ReapJobs(ctx, tx, org, w.cfg.ReapLimit, w.cfg.Policy)
			if err != nil {
				return err
			}
			for _, r := range reaped {
				if err := w.audit(ctx, tx, r.Job, jobs.ReapFailure, r.Requeued); err != nil {
					return err
				}
				if r.Requeued {
					requeued++
				} else {
					failed++
				}
			}
			return nil
		}); err != nil {
			log.Error("reaping failed", "error", err.Error())
			continue
		}
		if requeued+failed > 0 {
			log.Warn("expired leases taken back", "requeued", requeued, "failed", failed)
		}
		var job jobs.Job
		var lease jobs.Lease
		var ok bool
		// Read before the claim's transaction begins: the lease the database
		// grants starts at that transaction's now(), no earlier.
		claimed := time.Now()
		if err := w.scope.Run(ctx, org, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
			var err error
			job, lease, ok, err = jobs.Claim(ctx, tx, org, w.cfg.ID, w.types, w.cfg.Policy)
			return err
		}); err != nil {
			log.Error("claiming failed", "error", err.Error())
			continue
		}
		if ok {
			w.run(ctx, job, lease, claimed, log.With(correlation(job)...))
			worked = true
		}
	}
	return worked
}

// attempt is handler.Run for one claimed job.
type attempt struct {
	w     *Worker
	job   jobs.Job
	lease jobs.Lease
	// alive ends when the attempt stops: its lease lost or its deadline
	// reached, the worker stopping, or the handler returned.
	alive  context.Context
	lost   func()
	isLost func() bool

	mu       sync.Mutex
	finished bool
	ended    jobs.Status
}

func (a *attempt) Job() jobs.Job { return a.job }

// stopped is why a stopped attempt writes nothing.
func (a *attempt) stopped() error {
	if a.isLost() {
		return jobs.ErrLeaseLost
	}
	return handler.ErrStopped
}

// locked runs fn, then end (if any), in one transaction after locking the
// job under the lease. Nothing starts once the attempt has stopped, and the
// transaction ends with the attempt as well as with ctx: a handler's own
// context (even a detached one) cannot carry a write past the deadline.
//
// What it returns says what the transaction did (round 3 M1):
//   - fn's error (as the transaction classified it), the transaction rolled
//     back; wrapped in the stop if the attempt stopped meanwhile, so a
//     *jobs.Failure fn returned still counts;
//   - tenant.ErrOutcomeUnknown, even if the attempt has stopped: the COMMIT
//     was sent and its answer did not come, so it may have committed;
//   - the stop (ErrStopped or jobs.ErrLeaseLost), wrapping the cause, when
//     the attempt stopped before the transaction committed.
func (a *attempt) locked(ctx context.Context, fn, end func(ctx context.Context, tx tenant.Tx) error) error {
	if a.alive.Err() != nil {
		return a.stopped()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(a.alive, cancel)()
	var failed bool
	err := a.w.scope.Run(ctx, a.lease.OrganizationID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
		if _, err := jobs.Lock(ctx, tx, a.lease); err != nil {
			return err
		}
		if err := fn(ctx, tx); err != nil {
			// The transaction classifies fn's error with unbounded walks
			// of its Unwrap tree, so readable bounds the tree first
			// (round 3 L1). Only the tree's shape is checked: an As
			// method that panics still panics there (round 4 I1).
			failed = true
			return readable(err)
		}
		if end == nil {
			return nil
		}
		return end(ctx, tx)
	})
	switch {
	case err == nil:
		return nil
	case failed:
		if a.alive.Err() != nil {
			return fmt.Errorf("%w: %w", a.stopped(), err)
		}
		return err
	case errors.Is(err, tenant.ErrOutcomeUnknown):
		return err
	case errors.Is(err, jobs.ErrLeaseLost):
		a.lost()
		return err
	case a.alive.Err() != nil:
		return fmt.Errorf("%w: %w", a.stopped(), err)
	}
	return err
}

func (a *attempt) Checkpoint(ctx context.Context, fn func(ctx context.Context, tx tenant.Tx) error) error {
	return a.locked(ctx, fn, nil)
}

func (a *attempt) Finish(ctx context.Context, end jobs.Status, fn func(ctx context.Context, tx tenant.Tx) error) error {
	var ends func(context.Context, tenant.Tx, jobs.Lease) error
	switch end {
	case jobs.Completed:
		ends = jobs.Complete
	case jobs.Cancelled:
		ends = jobs.Cancel
	default:
		return handler.ErrInvalidEnd
	}
	err := a.locked(ctx, fn, func(ctx context.Context, tx tenant.Tx) error { return ends(ctx, tx, a.lease) })
	if err == nil {
		a.mu.Lock()
		a.finished, a.ended = true, end
		a.mu.Unlock()
	}
	return err
}

// run runs one claimed job and ends its attempt.
func (w *Worker) run(ctx context.Context, job jobs.Job, lease jobs.Lease, claimed time.Time, log *slog.Logger) {
	// The handler's lines carry the job's correlation as the loop's do
	// (D-093 (b)): observability adds them to every line logged with hctx,
	// and the organization, which the loop's logger has from Pass.
	hctx, stop := context.WithCancel(observability.With(ctx,
		attrs(append([]any{"organization_id", job.OrganizationID}, correlation(job)...))...))
	defer stop()
	var mu sync.Mutex
	lost := false
	setLost := func() { mu.Lock(); lost = true; mu.Unlock(); stop() }
	isLost := func() bool { mu.Lock(); defer mu.Unlock(); return lost }
	a := &attempt{w: w, job: job, lease: lease, alive: hctx, lost: setLost, isLost: isLost}

	var beats sync.WaitGroup
	beats.Go(func() { w.heartbeat(hctx, lease, claimed, setLost, log) })
	err := w.handle(hctx, a)
	stop()
	beats.Wait()
	a.mu.Lock()
	isFinished, end := a.finished, a.ended
	a.mu.Unlock()
	v := read(err)

	took := time.Since(claimed).Milliseconds()
	switch {
	case isFinished:
		log.Info("job ended", "status", string(end), "duration_ms", took)
		return
	case isLost():
		log.Warn("lease lost; the attempt ends without writing")
		return
	}
	var failure jobs.Failure
	switch {
	// The handler's own verdict counts even when the worker is stopping:
	// a failure that may not be retried must not become a retry.
	case v.failure != nil:
		failure = checked(*v.failure, log)
	case v.panicked != nil:
		failure = jobs.Failure{Code: Internal, Message: "the job's handler failed"}
		log.Error("job handler panicked", "panic", v.panicked.Error(), "stack", v.panicked.stack)
	case v.broken:
		// A bug like a panic: not retried.
		failure = jobs.Failure{Code: Internal, Message: "the job's handler failed"}
		log.Error("the job's handler returned an error that cannot be read", "type", v.typ)
	case v.large:
		failure = jobs.Failure{Code: Internal, Message: "the job's handler failed", Retryable: true}
		log.Error("the job's handler returned an error too large to read", "type", v.typ)
	case ctx.Err() != nil:
		failure = jobs.Failure{Code: WorkerStopped, Message: "the worker stopped during the attempt", Retryable: true}
	case err == nil:
		failure = jobs.Failure{Code: NoResult, Message: "the handler returned without a result"}
	default:
		failure = jobs.Failure{Code: Internal, Message: "the job's handler failed", Retryable: true}
	}
	if err != nil && v.panicked == nil && !v.broken && !v.large {
		log.Error("job attempt failed", "code", failure.Code, "error", v.text)
	}
	// The worker may be stopping: record the attempt's end on a context of
	// its own, bounded by the lease the record needs.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.Heartbeat)
	defer cancel()
	var retried bool
	// The failure and its audit event commit together (D-091 (b)): when the
	// event cannot be written, the failure is not recorded either, and the
	// reaper takes the job back once its lease ends.
	ferr := w.scope.Run(fctx, lease.OrganizationID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		if retried, err = jobs.Fail(ctx, tx, lease, failure, w.cfg.Policy); err != nil {
			return err
		}
		return w.audit(ctx, tx, job, failure, retried)
	})
	switch {
	case errors.Is(ferr, jobs.ErrLeaseLost):
		// The job is no longer this attempt's: reaped, or ended by a COMMIT
		// whose answer was lost (tenant.ErrOutcomeUnknown).
		log.Warn("the job is no longer this attempt's; its end is not recorded", "code", failure.Code)
		return
	case ferr != nil:
		// The lease ends on its own; the reaper takes the job back.
		log.Error("recording the failure failed", "code", failure.Code, "error", ferr.Error())
		return
	}
	log.Info("job attempt ended", "code", failure.Code, "retried", retried, "duration_ms", took)
}

// audit records that an attempt of j ended without a result
// (job.attempt_failed, D-091 (b)) in tx, the transaction that records the
// failure: on the job's resource, under the request that queued the job
// (its payload's request_id; a request ID of the worker's own, marked
// uncorrelated, when the payload names none).
func (w *Worker) audit(ctx context.Context, tx tenant.Tx, j jobs.Job, f jobs.Failure, requeued bool) error {
	target, id := resource(j)
	req, ok := requestID(j)
	outcome := "failed"
	if requeued {
		outcome = "requeued"
	}
	meta := map[string]any{"job_id": j.ID, "job_type": string(j.Type), "attempt": j.Attempt, "max_attempts": j.MaxAttempts,
		"code": f.Code, "retryable": f.Retryable, "outcome": outcome}
	if !ok {
		req = ids.New("req")
		meta["uncorrelated"] = true
	}
	return auditlog.AppendWorker(ctx, tx, j.OrganizationID, w.cfg.ID, auditlog.Event{Action: "job.attempt_failed", TargetType: target,
		TargetID: id, RequestID: req, Metadata: meta})
}

// resource is the audit target of j: the one row it works on.
func resource(j jobs.Job) (targetType, id string) {
	r := j.Resource
	switch {
	case r.SimulationRunID != "":
		return "simulation", r.SimulationRunID
	case r.AIProposalID != "":
		return "ai_proposal", r.AIProposalID
	case r.ImportID != "":
		return "import", r.ImportID
	}
	return "ai_analysis", r.AIAnalysisID
}

// resourceKey is the log key of j's resource ID.
func resourceKey(j jobs.Job) string {
	switch t, _ := resource(j); t {
	case "simulation":
		return "run_id"
	case "ai_proposal":
		return "proposal_id"
	case "import":
		return "import_id"
	}
	return "analysis_id"
}

var reqIDs = regexp.MustCompile(`^req_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// requestID is the request_id j's payload names: the request that queued
// it (every enqueue writes one: T-016, T-017, T-023, T-025).
func requestID(j jobs.Job) (string, bool) {
	var p struct {
		RequestID string `json:"request_id"`
	}
	if json.Unmarshal(j.Payload, &p) != nil || !reqIDs.MatchString(p.RequestID) {
		return "", false
	}
	return p.RequestID, true
}

// correlation is what every line about j carries besides its organization
// (D-093 (b)): the job's ID, type and attempt, its resource's ID, and the
// request that queued it.
func correlation(j jobs.Job) []any {
	_, id := resource(j)
	out := []any{"job_id", j.ID, "job_type", string(j.Type), "attempt", j.Attempt, resourceKey(j), id}
	if req, ok := requestID(j); ok {
		out = append(out, "request_id", req)
	}
	return out
}

// attrs is kv (key, value pairs) as attributes.
func attrs(kv []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, slog.Any(kv[i].(string), kv[i+1]))
	}
	return out
}

// verdict is what the loop reads of a handler's error.
type verdict struct {
	failure  *jobs.Failure
	panicked *panicked
	// broken: a method of the error panicked, a bug like a panic (not
	// retried); large: its Unwrap tree holds more than maxErrors errors, so
	// it is not read further (retried). typ names its type for the log.
	broken, large bool
	typ           string
	// text is the error's text for the log (logText).
	text string
}

// maxErrors bounds the errors read in one error's Unwrap tree.
const maxErrors = 64

// unreadable stands in for an error of fn (Checkpoint, Finish) the worker
// could not read (see readable); run records it as read would the error.
type unreadable struct {
	typ   string
	large bool
}

func (u unreadable) Error() string {
	if u.large {
		return "the handler's error (" + u.typ + ") is too large to read"
	}
	return "the handler's error (" + u.typ + ") cannot be read"
}

// readable is err if the worker can read it (as read does), else an
// unreadable in its place.
func readable(err error) (out error) {
	typ := fmt.Sprintf("%T", err)
	defer func() {
		if recover() != nil {
			out = unreadable{typ: typ}
		}
	}()
	if !finite(err) {
		return unreadable{typ: typ, large: true}
	}
	return err
}

// read reads err defensively: a handler's error type may be broken, a nil
// receiver whose methods dereference it or an Unwrap chain that never ends,
// and must neither crash nor stall the worker that serves every assigned
// organization. A panic in its methods makes it broken; a tree of more than
// maxErrors errors is large and read no further. (A method that blocks
// forever cannot be guarded against in-process.)
func read(err error) (v verdict) {
	if err == nil {
		return verdict{}
	}
	typ := fmt.Sprintf("%T", err)
	defer func() {
		if recover() != nil {
			v = verdict{broken: true, typ: typ}
		}
	}()
	if !finite(err) {
		return verdict{large: true, typ: typ}
	}
	var u unreadable
	if errors.As(err, &u) {
		return verdict{broken: !u.large, large: u.large, typ: u.typ}
	}
	// A typed nil *jobs.Failure stays nil here, so run treats it as an
	// ordinary error (review M2).
	var f *jobs.Failure
	if errors.As(err, &f) {
		v.failure = f
	}
	var p panicked
	if errors.As(err, &p) {
		v.panicked = &p
	}
	v.text = logText(err)
	return v
}

// finite reports whether err's Unwrap tree holds at most maxErrors errors.
func finite(err error) bool {
	queue := []error{err}
	for seen := 0; len(queue) > 0; seen++ {
		if seen == maxErrors || len(queue) > maxErrors {
			return false
		}
		e := queue[0]
		queue = queue[1:]
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			if next := u.Unwrap(); next != nil {
				queue = append(queue, next)
			}
		case interface{ Unwrap() []error }:
			for _, next := range u.Unwrap() {
				if next != nil {
					queue = append(queue, next)
				}
			}
		}
	}
	return true
}

var failureCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// checked is the handler's failure as it can be stored: an invalid code is
// recorded as INTERNAL (its retry decision kept), and a message that is
// missing, over 1000 characters, not UTF-8 or holding NUL (which JSON or
// PostgreSQL cannot store) is replaced, so the attempt always ends with a
// record.
func checked(f jobs.Failure, log *slog.Logger) jobs.Failure {
	if !failureCode.MatchString(f.Code) {
		log.Error("the job's handler returned a failure with an invalid code")
		return jobs.Failure{Code: Internal, Message: "the job's handler failed", Retryable: f.Retryable}
	}
	if n := utf8.RuneCountInString(f.Message); n == 0 || n > 1000 || !utf8.ValidString(f.Message) || strings.ContainsRune(f.Message, 0) {
		f.Message = "the job failed"
	}
	return f
}

// logText is what the log keeps of a handler's error: its text without
// control characters, at most 500 characters. Handlers keep customer data
// out of their errors (SECURITY §7, handler.Handler); this bounds the damage
// of one that does not.
func logText(err error) string {
	var b strings.Builder
	n := 0
	for _, r := range err.Error() {
		if n == 500 {
			b.WriteString("…")
			break
		}
		if unicode.IsControl(r) {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// panicked is a handler's panic. Only a runtime error's message is kept: a
// value the handler panicked with may hold the data it was working on.
type panicked struct {
	value any
	stack string
}

func (p panicked) Error() string {
	if re, ok := p.value.(runtime.Error); ok {
		return "handler panicked: " + re.Error()
	}
	return fmt.Sprintf("handler panicked with a %T", p.value)
}

// handle runs the handler, turning a panic into an error that is not
// retried (a bug, not a passing fault).
func (w *Worker) handle(ctx context.Context, a *attempt) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = panicked{value: v, stack: string(debug.Stack())}
		}
	}()
	return w.cfg.Handlers[a.job.Type].Handle(ctx, a)
}

// heartbeat renews the lease every Heartbeat until ctx ends, and stops the
// handler (setLost) before the lease could end in the database:
//
//   - The database's lease runs from its transaction's now(), which is no
//     earlier than when the worker started that transaction. The worker
//     counts from there, the claim's at first and then each successful
//     renewal's, and stops the handler half a Heartbeat before that start
//     plus the lease, on a timer of its own that does not wait for a
//     renewal in flight. With a lease of three heartbeats one renewal may
//     fail and the next still keeps the job.
//   - Renewals are due every Heartbeat from the claim's start (not from
//     when the handler began, so a slow claim does not push them late), and
//     run one at a time, each for at most one Heartbeat (and never past the
//     deadline). One that hangs is given up after a Heartbeat and the next,
//     due by then, starts at once, so a connection that hangs cannot use up
//     the margin for a retry.
//   - A renewal that finds the lease gone (expired, reaped, claimed again)
//     stops the handler at once.
//   - The worker measures with Go's monotonic clock, so an offset between
//     its wall clock and the database's does not matter. What the half
//     Heartbeat of margin covers is stopping the handler, a forward step of
//     the database's clock, and a pause of the worker process shorter than
//     it; longer ones are the deployment's to prevent (NTP slewing, no
//     suspended VMs).
func (w *Worker) heartbeat(ctx context.Context, lease jobs.Lease, start time.Time, setLost func(), log *slog.Logger) {
	keep := w.cfg.Policy.Lease - w.cfg.Heartbeat/2
	deadline := start.Add(keep)
	deadlines := make(chan time.Time, 1)
	// The watcher gets the deadline only through its timer and deadlines:
	// the loop below owns deadline.
	expire := time.NewTimer(time.Until(deadline))
	var watch sync.WaitGroup
	defer watch.Wait()
	watch.Go(func() {
		defer expire.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-expire.C:
				log.Warn("the lease could not be renewed in time; the attempt stops")
				setLost()
				return
			case d := <-deadlines:
				expire.Reset(time.Until(d))
			}
		}
	})
	due := start.Add(w.cfg.Heartbeat)
	next := time.NewTimer(time.Until(due))
	defer next.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-next.C:
		}
		started := time.Now()
		bctx, cancel := context.WithDeadline(ctx, earliest(deadline, started.Add(w.cfg.Heartbeat)))
		err := w.scope.Run(bctx, lease.OrganizationID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
			return jobs.Heartbeat(ctx, tx, lease, w.cfg.Policy)
		})
		cancel()
		// The next renewal is due a Heartbeat after this one was (at once
		// if that has passed: this one hung).
		due = due.Add(w.cfg.Heartbeat)
		next.Reset(time.Until(due))
		switch {
		case err == nil:
			deadline = started.Add(keep)
			select {
			case deadlines <- deadline:
			case <-ctx.Done():
				return
			}
		case ctx.Err() != nil:
			return
		case errors.Is(err, jobs.ErrLeaseLost):
			setLost()
			return
		default:
			log.Warn("renewing the lease failed", "error", err.Error())
		}
	}
}

func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
