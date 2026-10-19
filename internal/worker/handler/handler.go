// Package handler is how a job handler plugs into the worker loop (T-019,
// ADR-024). It is a leaf: it does not depend on the worker scope, so a
// handler package implements Handler without reaching
// internal/tenant/workerscope (the architecture rule
// worker-scope-only-in-the-worker), internal/worker runs it, and cmd/worker
// registers it.
//
// A handler reads and writes the database, so it is an adapter: it lives
// where database access is allowed (T-016's internal/import/upload, for
// one), never inside the generic kernel (internal/model, internal/simulation,
// internal/results) or internal/ai, whose own rules forbid internal/storage
// and internal/tenant. The simulation (T-023) and AI (T-025) handlers call
// those packages from an adapter package of their own (ADR-024).
package handler

import (
	"context"
	"errors"

	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Run is one attempt of a job, as its handler sees it. Every database
// access goes through Checkpoint or Finish: each runs in one transaction in
// the job's organization after locking the job under the worker's lease.
// Once the attempt has stopped (the lease lost or its deadline reached, the
// worker stopping) neither starts: jobs.ErrLeaseLost when the lease is lost
// or its deadline reached, ErrStopped otherwise. A transaction in progress
// when the attempt stops is cancelled, whatever context the handler gave it:
//   - if fn returned an error, the transaction rolls back and the call
//     returns that error wrapped in the stop. While the worker is stopping
//     (ErrStopped) a *jobs.Failure in it still counts when the handler
//     returns it; once the lease is lost (jobs.ErrLeaseLost) the job is no
//     longer the attempt's, and nothing is recorded;
//   - a COMMIT already sent returns tenant.ErrOutcomeUnknown, never the
//     stop, whether the database then commits it or rolls it back;
//   - otherwise the transaction rolls back and the call returns the stop.
//
// fn's error reaches the caller as the transaction classified it, unless
// the worker cannot read it (an Unwrap tree of more than 64 errors, or an
// Unwrap that panics): the call then returns a stand-in that names only the
// error's type, so the handler cannot inspect it, and the worker records
// the stand-in as it would the error (INTERNAL; retried for a large tree,
// not for a broken one). Keep fn's errors small, and return a *jobs.Failure
// itself.
//
// Such a transaction holds the job's row lock, which the lease's renewal
// waits for: it must end well within one heartbeat, or the renewal is given
// up, and within the lease, or the worker stops the attempt and the
// transaction rolls back. Long work belongs outside the transaction.
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

var (
	// ErrInvalidEnd: Finish was asked to end a job other than as completed
	// or cancelled; nothing was written.
	ErrInvalidEnd = errors.New("handler: a job ends as completed or cancelled")
	// ErrStopped: the attempt has stopped (the worker is stopping): the
	// call's transaction, if one ran, did not commit, and nothing more is
	// written; the worker records the attempt's end.
	ErrStopped = errors.New("handler: the attempt has stopped")
)

// Handler runs one attempt of a job. It returns nil only after Finish
// succeeded. Otherwise its error ends the attempt: a *jobs.Failure says
// whether it may be retried; any other error is retried while attempts
// remain and is recorded only as INTERNAL. The worker logs the error's text
// (bounded), so it must not carry customer data, prompts, provider output
// or secrets (SECURITY §7). The worker reads at most 64 errors of an
// error's Unwrap tree: a larger one (a join of many row errors, say) is not
// read, so a *jobs.Failure in it does not count, and it is retried as
// INTERNAL: return the Failure itself (it has no Unwrap), not a tree holding
// it. A handler's error types must be safe to call on any value. The worker
// survives most that are not: one whose method panics is recorded as
// INTERNAL and not retried. It cannot survive every one: a method that
// blocks stalls the worker, one that recurses without end overflows the
// stack and ends the process, and an As method that panics while a
// transaction classifies fn's error panics out of Checkpoint or Finish,
// which ends the process if they were called from a goroutine the handler
// started. ctx ends when the attempt stops; the handler then returns
// promptly.
type Handler interface {
	Handle(ctx context.Context, run Run) error
}

// Func is a function as a Handler.
type Func func(ctx context.Context, run Run) error

func (f Func) Handle(ctx context.Context, run Run) error { return f(ctx, run) }
