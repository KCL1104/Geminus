// Package worker is the job loop (T-019; ARCHITECTURE §6, ADR-024): it serves
// the organizations the deployment assigns to it (internal/tenant/workerscope),
// one transaction per organization; in each it takes back expired leases,
// then claims one job of a type it has a handler for (internal/worker/handler)
// and runs it, renewing the lease meanwhile and stopping the handler before
// the lease could end. Every database access of the handler is fenced by the
// lease, so once it is lost nothing the handler does is written.
package worker

import (
	"context"
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
	// ID names this worker in the lease (twin.jobs.locked_by).
	ID     string
	Policy jobs.Policy
	// Heartbeat renews the lease; at most a third of Policy.Lease. The
	// handler is stopped half a Heartbeat before the lease could end (see
	// heartbeat), so one renewal may fail without stopping it.
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
	case cfg.ID == "" || len(cfg.ID) > 200:
		return nil, errors.New("worker: the worker ID must be 1-200 characters")
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
		if err := w.scope.Run(ctx, org, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
			requeued, failed, err := jobs.Reap(ctx, tx, org, w.cfg.ReapLimit, w.cfg.Policy)
			if requeued+failed > 0 {
				log.Warn("expired leases taken back", "requeued", requeued, "failed", failed)
			}
			return err
		}); err != nil {
			log.Error("reaping failed", "error", err.Error())
			continue
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
			w.run(ctx, job, lease, claimed, log.With("job_id", job.ID, "type", string(job.Type), "attempt", job.Attempt))
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
	lost  func()

	mu       sync.Mutex
	finished bool
	ended    jobs.Status
}

func (a *attempt) Job() jobs.Job { return a.job }

// locked runs fn, then end (if any), in one transaction after locking the
// job under the lease.
func (a *attempt) locked(ctx context.Context, fn, end func(ctx context.Context, tx tenant.Tx) error) error {
	err := a.w.scope.Run(ctx, a.lease.OrganizationID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
		if _, err := jobs.Lock(ctx, tx, a.lease); err != nil {
			return err
		}
		if err := fn(ctx, tx); err != nil {
			return err
		}
		if end == nil {
			return nil
		}
		return end(ctx, tx)
	})
	if errors.Is(err, jobs.ErrLeaseLost) {
		a.lost()
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
	hctx, stop := context.WithCancel(ctx)
	defer stop()
	var mu sync.Mutex
	lost := false
	setLost := func() { mu.Lock(); lost = true; mu.Unlock(); stop() }
	a := &attempt{w: w, job: job, lease: lease, lost: setLost}

	var beats sync.WaitGroup
	beats.Go(func() { w.heartbeat(hctx, lease, claimed, setLost, log) })
	err := w.handle(hctx, a)
	stop()
	beats.Wait()
	mu.Lock()
	isLost := lost
	mu.Unlock()
	a.mu.Lock()
	isFinished, end := a.finished, a.ended
	a.mu.Unlock()

	switch {
	case isFinished:
		log.Info("job ended", "status", string(end))
		return
	case isLost || errors.Is(err, jobs.ErrLeaseLost):
		log.Warn("lease lost; the attempt ends without writing")
		return
	}
	var failure jobs.Failure
	var f *jobs.Failure
	var p panicked
	switch {
	// The handler's own verdict counts even when the worker is stopping:
	// a failure that may not be retried must not become a retry.
	case errors.As(err, &f) && f != nil:
		failure = checked(*f, log)
	case errors.As(err, &p):
		failure = jobs.Failure{Code: Internal, Message: "the job's handler failed"}
		log.Error("job handler panicked", "panic", p.Error(), "stack", p.stack)
	case ctx.Err() != nil:
		failure = jobs.Failure{Code: WorkerStopped, Message: "the worker stopped during the attempt", Retryable: true}
	case err == nil:
		failure = jobs.Failure{Code: NoResult, Message: "the handler returned without a result"}
	default:
		failure = jobs.Failure{Code: Internal, Message: "the job's handler failed", Retryable: true}
	}
	if err != nil && !errors.As(err, &p) {
		log.Error("job attempt failed", "code", failure.Code, "error", logText(err))
	}
	// The worker may be stopping: record the attempt's end on a context of
	// its own, bounded by the lease the record needs.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.Heartbeat)
	defer cancel()
	var retried bool
	ferr := w.scope.Run(fctx, lease.OrganizationID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		retried, err = jobs.Fail(ctx, tx, lease, failure, w.cfg.Policy)
		return err
	})
	if ferr != nil {
		// The lease ends on its own; the reaper takes the job back.
		log.Error("recording the failure failed", "code", failure.Code, "error", ferr.Error())
		return
	}
	log.Info("job attempt ended", "code", failure.Code, "retried", retried)
}

var failureCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// checked is the handler's failure as it can be stored: an invalid code is
// recorded as INTERNAL (its retry decision kept), and a missing or overlong
// message is replaced, so the attempt always ends with a record.
func checked(f jobs.Failure, log *slog.Logger) jobs.Failure {
	if !failureCode.MatchString(f.Code) {
		log.Error("the job's handler returned a failure with an invalid code")
		return jobs.Failure{Code: Internal, Message: "the job's handler failed", Retryable: f.Retryable}
	}
	if n := len([]rune(f.Message)); n == 0 || n > 1000 {
		f.Message = "the job failed"
	}
	return f
}

// logText is what the log keeps of a handler's error: its text without
// control characters, at most 500 characters. Handlers keep customer data
// out of their errors (SECURITY §7, handler.Handler); this bounds the damage
// of one that does not.
func logText(err error) string {
	s := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, err.Error()))
	if len(s) > 500 {
		return string(s[:500]) + "…"
	}
	return string(s)
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
//     earlier than the worker's clock read before that transaction began.
//     The worker counts from there, the claim's at first and then each
//     successful renewal's, and stops the handler half a Heartbeat before
//     that start plus the lease. With a lease of three heartbeats, one
//     renewal may fail and the next still keeps the job; the margin covers
//     stopping and a worker clock a little behind the database's (a larger
//     skew is the deployment's to prevent, NTP).
//   - A renewal that finds the lease gone (expired, reaped, claimed again)
//     stops the handler at once.
//   - A renewal that fails or hangs changes nothing: it is bounded by the
//     deadline, and the deadline stops the handler whether or not a renewal
//     is still in flight.
func (w *Worker) heartbeat(ctx context.Context, lease jobs.Lease, start time.Time, setLost func(), log *slog.Logger) {
	keep := w.cfg.Policy.Lease - w.cfg.Heartbeat/2
	deadline := start.Add(keep)
	expire := time.NewTimer(time.Until(deadline))
	defer expire.Stop()
	tick := time.NewTicker(w.cfg.Heartbeat)
	defer tick.Stop()
	type renewal struct {
		start time.Time
		err   error
	}
	done := make(chan renewal, 1)
	inFlight := false
	// Every way out ends ctx first (setLost or the caller), so a renewal
	// still in flight returns promptly.
	defer func() {
		if inFlight {
			<-done
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-expire.C:
			log.Warn("the lease could not be renewed in time; the attempt stops")
			setLost()
			return
		case <-tick.C:
			if inFlight {
				continue
			}
			inFlight = true
			go func(started, until time.Time) {
				bctx, cancel := context.WithDeadline(ctx, until)
				defer cancel()
				done <- renewal{started, w.scope.Run(bctx, lease.OrganizationID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
					return jobs.Heartbeat(ctx, tx, lease, w.cfg.Policy)
				})}
			}(time.Now(), deadline)
		case r := <-done:
			inFlight = false
			switch {
			case r.err == nil:
				deadline = r.start.Add(keep)
				expire.Reset(time.Until(deadline))
			case ctx.Err() != nil:
				return
			case errors.Is(r.err, jobs.ErrLeaseLost):
				setLost()
				return
			default:
				log.Warn("renewing the lease failed", "error", r.err.Error())
			}
		}
	}
}
