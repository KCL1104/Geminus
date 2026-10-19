// Package handler is how a job handler plugs into the worker loop (T-019,
// ADR-024). It is a leaf: it does not depend on the worker scope, so the
// packages that do the work (import profiling T-016, simulation T-023, AI
// T-025) implement Handler without reaching internal/tenant/workerscope (the
// architecture rule worker-scope-only-in-the-worker), internal/worker runs
// them, and cmd/worker registers them.
package handler

import (
	"context"
	"errors"

	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Run is one attempt of a job, as its handler sees it. Every database
// access goes through Checkpoint or Finish: each runs in one transaction in
// the job's organization after locking the job under the worker's lease, so
// once the lease is lost (expired, reaped, claimed again) nothing is read
// as the job's and nothing is written: jobs.ErrLeaseLost.
//
// Such a transaction holds the job's row lock, which the lease's renewal
// waits for: it must end well within one lease minus one heartbeat, or the
// worker stops the handler and the transaction rolls back.
type Run interface {
	// Job is the claimed job: running, in this attempt, under this lease.
	Job() jobs.Job
	// Checkpoint runs fn under the lease without ending the job: reading
	// the job's inputs, writing progress, checking whether cancellation was
	// requested.
	Checkpoint(ctx context.Context, fn func(ctx context.Context, tx tenant.Tx) error) error
	// Finish runs fn under the lease and ends the job as end in the same
	// transaction: jobs.Completed with its result, or jobs.Cancelled when
	// the handler stopped at a checkpoint because cancellation was
	// requested. Any other end is ErrInvalidEnd and writes nothing.
	Finish(ctx context.Context, end jobs.Status, fn func(ctx context.Context, tx tenant.Tx) error) error
}

// ErrInvalidEnd: Finish was asked to end a job other than as completed or
// cancelled; nothing was written.
var ErrInvalidEnd = errors.New("handler: a job ends as completed or cancelled")

// Handler runs one attempt of a job. It returns nil only after Finish
// succeeded. Otherwise its error ends the attempt: a *jobs.Failure says
// whether it may be retried; any other error is retried while attempts
// remain and is recorded only as INTERNAL. The worker logs the error's text
// (bounded), so it must not carry customer data, prompts, provider output
// or secrets (SECURITY §7). ctx ends when the lease is lost or the worker
// stops; the handler then returns promptly.
type Handler interface {
	Handle(ctx context.Context, run Run) error
}

// Func is a function as a Handler.
type Func func(ctx context.Context, run Run) error

func (f Func) Handle(ctx context.Context, run Run) error { return f(ctx, run) }
