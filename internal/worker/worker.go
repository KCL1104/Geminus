// Package worker is the job loop (T-019; ARCHITECTURE §6): it serves the
// organizations the deployment assigns to it (internal/tenant/workerscope),
// one transaction per organization; in each it takes back expired leases,
// then claims one job of a type it has a handler for and runs it, renewing
// the lease meanwhile. A handler commits its result with Finish, which
// locks the job under the lease and ends it (completed, or cancelled at a
// checkpoint) in the same transaction; a lease lost on the way stops the
// handler, and nothing it does after that is written (ADR-024).
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
)

// Finish runs fn in one transaction in the job's organization, after
// locking the job under the worker's lease, and then ends the job as end:
// jobs.Completed with its result, or jobs.Cancelled when the handler
// stopped at a checkpoint because cancellation was requested. The result
// and the job's end commit together. jobs.ErrLeaseLost means the job is
// not the worker's any more and nothing was written.
type Finish func(ctx context.Context, end jobs.Status, fn func(ctx context.Context, tx tenant.Tx) error) error

// ErrInvalidEnd: Finish was asked to end a job other than as completed or
// cancelled; nothing was written.
var ErrInvalidEnd = errors.New("worker: a job ends as completed or cancelled")

// Handler runs one job. It returns nil only after Finish succeeded. An
// error ends the attempt: a *jobs.Failure says whether it may be retried;
// any other error is retried while attempts remain, recorded only as
// INTERNAL (the error itself is logged, never stored). ctx ends when the
// lease is lost or the worker stops; the handler then returns.
type Handler interface {
	Handle(ctx context.Context, job jobs.Job, finish Finish) error
}

// HandlerFunc is a function as a Handler.
type HandlerFunc func(ctx context.Context, job jobs.Job, finish Finish) error

func (f HandlerFunc) Handle(ctx context.Context, job jobs.Job, finish Finish) error {
	return f(ctx, job, finish)
}

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
	// Heartbeat renews the lease; at most a third of Policy.Lease, so two
	// renewals can fail before the lease ends.
	Heartbeat time.Duration
	// Idle is the pause after a pass that found no job.
	Idle time.Duration
	// ReapLimit is how many expired leases one pass takes back per
	// organization.
	ReapLimit int
	Handlers  map[jobs.Type]Handler
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
		if err := w.scope.Run(ctx, org, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
			var err error
			job, lease, ok, err = jobs.Claim(ctx, tx, org, w.cfg.ID, w.types, w.cfg.Policy)
			return err
		}); err != nil {
			log.Error("claiming failed", "error", err.Error())
			continue
		}
		if ok {
			w.run(ctx, job, lease, log.With("job_id", job.ID, "type", string(job.Type), "attempt", job.Attempt))
			worked = true
		}
	}
	return worked
}

// run runs one claimed job and ends its attempt.
func (w *Worker) run(ctx context.Context, job jobs.Job, lease jobs.Lease, log *slog.Logger) {
	hctx, stop := context.WithCancel(ctx)
	defer stop()
	var mu sync.Mutex
	lost, finished := false, false
	var ended jobs.Status
	setLost := func() { mu.Lock(); lost = true; mu.Unlock(); stop() }

	var beats sync.WaitGroup
	beats.Go(func() { w.heartbeat(hctx, lease, setLost, log) })

	finish := func(fctx context.Context, end jobs.Status, fn func(ctx context.Context, tx tenant.Tx) error) error {
		var ends func(context.Context, tenant.Tx, jobs.Lease) error
		switch end {
		case jobs.Completed:
			ends = jobs.Complete
		case jobs.Cancelled:
			ends = jobs.Cancel
		default:
			return ErrInvalidEnd
		}
		err := w.scope.Run(fctx, lease.OrganizationID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
			if _, err := jobs.Lock(ctx, tx, lease); err != nil {
				return err
			}
			if err := fn(ctx, tx); err != nil {
				return err
			}
			return ends(ctx, tx, lease)
		})
		if errors.Is(err, jobs.ErrLeaseLost) {
			setLost()
		}
		if err == nil {
			mu.Lock()
			finished, ended = true, end
			mu.Unlock()
		}
		return err
	}
	err := w.handle(hctx, job, finish)
	stop()
	beats.Wait()
	mu.Lock()
	isLost, isFinished, end := lost, finished, ended
	mu.Unlock()

	var failure jobs.Failure
	switch {
	case isFinished:
		log.Info("job ended", "status", string(end))
		return
	case isLost || errors.Is(err, jobs.ErrLeaseLost):
		log.Warn("lease lost; the attempt ends without writing")
		return
	case ctx.Err() != nil:
		failure = jobs.Failure{Code: WorkerStopped, Message: "the worker stopped during the attempt", Retryable: true}
	case err == nil:
		failure = jobs.Failure{Code: NoResult, Message: "the handler returned without a result"}
	default:
		var f *jobs.Failure
		var p panicked
		switch {
		case errors.As(err, &f):
			failure = *f
		case errors.As(err, &p):
			failure = jobs.Failure{Code: Internal, Message: "the job's handler failed"}
		default:
			failure = jobs.Failure{Code: Internal, Message: "the job's handler failed", Retryable: true}
		}
		log.Error("job attempt failed", "code", failure.Code, "error", err.Error())
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

type panicked struct{ value any }

func (p panicked) Error() string { return fmt.Sprintf("handler panicked: %v", p.value) }

// handle runs the handler, turning a panic into an error that is not
// retried (a bug, not a passing fault).
func (w *Worker) handle(ctx context.Context, job jobs.Job, finish Finish) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = panicked{v}
		}
	}()
	return w.cfg.Handlers[job.Type].Handle(ctx, job, finish)
}

// heartbeat renews the lease every Heartbeat until ctx ends. A lease the
// worker no longer holds, or one it could not renew for a whole lease (the
// database was unreachable or did not answer), is lost: the handler is
// stopped. Each renewal may take one Heartbeat, so a hung call cannot keep
// the handler running past the lease.
func (w *Worker) heartbeat(ctx context.Context, lease jobs.Lease, setLost func(), log *slog.Logger) {
	renewed := time.Now()
	t := time.NewTicker(w.cfg.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		bctx, cancel := context.WithTimeout(ctx, w.cfg.Heartbeat)
		err := w.scope.Run(bctx, lease.OrganizationID, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx) error {
			return jobs.Heartbeat(ctx, tx, lease, w.cfg.Policy)
		})
		cancel()
		switch {
		case err == nil:
			renewed = time.Now()
		case ctx.Err() != nil:
			return
		case errors.Is(err, jobs.ErrLeaseLost):
			setLost()
			return
		default:
			log.Warn("renewing the lease failed", "error", err.Error())
			if time.Since(renewed) >= w.cfg.Policy.Lease {
				setLost()
				return
			}
		}
	}
}
