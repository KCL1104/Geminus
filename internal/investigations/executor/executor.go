// Package executor is the API's background executor of AI investigations
// (H-061 §6; D-008 as amended (b); slice A7; behind GEMINUS_INVESTIGATIONS,
// off by default): it carries out each tool call the worker's agent
// recorded in the ledger, one call per transaction, in the requester's own
// scope, for as long as the requester holds owner or editor.
//
// Every 500 ms, in each organization the deployment assigns, it asks
// internal/tenant/actingfor for the pending calls of running
// investigations and for the running investigations whose requester lost
// owner or editor (the API's only work without a user: two fixed
// statements), ends the latter (role_lost), and carries out each pending
// call in tenant.DB.Run(Scope{organization, requester, ActingFor:
// investigation}): the requester's membership FOR SHARE first (owner or
// editor, otherwise the call is not carried out and the investigation ends
// with role_lost), the call FOR UPDATE SKIP LOCKED (another executor or
// pass skips it), a second strict decode, the investigation running and
// before its deadline, the tool's effect in a SAVEPOINT through the code a
// person's own requests use (proposals.Queue, runs.Queue, analyses.Queue,
// effect.SaveScenario, effect.WriteDraft), a typed refusal rolled back to
// the savepoint and recorded, the call's result in the ledger and its
// investigation.tool_call event, one commit. The database labels what it
// makes and refuses anything outside the tool effects while
// app.investigation_id is set (0019).
//
// It holds no Eino, no provider and no model call: it is plain Go over the
// services. A transient failure is retried with backoff and recorded as
// failed after 5 tries or 120 s; a panic is recovered and recorded as
// failed (INTERNAL), its text never logged. Logs and spans carry IDs and
// codes only, never the AI's idea, note or arguments.
package executor

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/analyses"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/observability/telemetry"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/proposals/effect"
	"github.com/kcl1104/geminus/internal/runs"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	store "github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/actingfor"
)

// Defaults of Config (H-061 §6.1, §6.2, §6.5).
const (
	DefaultInterval    = 500 * time.Millisecond
	DefaultMaxTries    = 5
	DefaultRetryFor    = 120 * time.Second
	DefaultBackoff     = 500 * time.Millisecond
	DefaultMaxBackoff  = 10 * time.Second
	DefaultDrain       = 10 * time.Second
	DefaultCallTimeout = 60 * time.Second
)

// Config is an executor's setting.
type Config struct {
	// Scope is the discovery scope (the assigned organizations); DB runs the
	// acting-for transactions, on the same pool.
	Scope *actingfor.Scope
	DB    *tenant.DB
	// The services whose in-transaction bodies are the tools' effects,
	// configured as the API's own (AI availability, models, objects); Runs
	// keeps D-007's limits (a labelled run must have exactly them, 0019).
	Proposals *proposals.Service
	Runs      *runs.Service
	Analyses  *analyses.Service
	Effects   *effect.Service
	Log       *slog.Logger
	// Interval is the time between discovery passes; MaxTries and RetryFor
	// bound a transient failure's retries (then the call is failed);
	// Backoff and MaxBackoff the wait between them; Drain how long a call
	// under way may finish once the executor is stopped; CallTimeout bounds
	// one call's transaction. Zero is the default.
	Interval, RetryFor, Backoff, MaxBackoff, Drain, CallTimeout time.Duration
	MaxTries                                                    int
}

// Executor carries out recorded tool calls.
type Executor struct {
	cfg Config
	mu  sync.Mutex
	// retries are the transient failures of calls still pending, by call.
	retries map[callKey]*retry
}

type callKey struct {
	org, inv string
	seq      int
}

type retry struct {
	tries       int
	first, next time.Time
	code        investigator.Code
}

// New checks cfg and returns its executor.
func New(cfg Config) (*Executor, error) {
	if cfg.Scope == nil || cfg.DB == nil || cfg.Proposals == nil || cfg.Runs == nil || cfg.Analyses == nil || cfg.Effects == nil || cfg.Log == nil {
		return nil, errors.New("executor: a scope, a database, the four services and a log are required")
	}
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&cfg.Interval, DefaultInterval)
	def(&cfg.RetryFor, DefaultRetryFor)
	def(&cfg.Backoff, DefaultBackoff)
	def(&cfg.MaxBackoff, DefaultMaxBackoff)
	def(&cfg.Drain, DefaultDrain)
	def(&cfg.CallTimeout, DefaultCallTimeout)
	if cfg.MaxTries <= 0 {
		cfg.MaxTries = DefaultMaxTries
	}
	return &Executor{cfg: cfg, retries: map[callKey]*retry{}}, nil
}

// Run passes every Interval until ctx ends: then it stops claiming, lets a
// call under way finish for at most Drain (it is rolled back after that,
// and its row stays pending for the next executor), and returns.
func (e *Executor) Run(ctx context.Context) {
	work, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stop := context.AfterFunc(ctx, func() { time.AfterFunc(e.cfg.Drain, cancel) })
	defer stop()
	t := time.NewTicker(e.cfg.Interval)
	defer t.Stop()
	for {
		e.pass(ctx, work)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Pass runs one discovery pass now, carrying out what it finds (tests, and
// Run's loop).
func (e *Executor) Pass(ctx context.Context) { e.pass(ctx, ctx) }

// pass discovers in each assigned organization, one transaction each, then
// ends the investigations whose requester lost the role and carries out
// the others' pending calls; stop says when to stop claiming, work bounds
// the work under way.
func (e *Executor) pass(stop, work context.Context) {
	for _, org := range e.cfg.Scope.Assigned() {
		if stop.Err() != nil {
			return
		}
		d, err := e.cfg.Scope.Pending(work, org)
		if err != nil {
			e.cfg.Log.Warn("investigation discovery failed", "organization_id", org, "error", err.Error())
			continue
		}
		lost := map[string]bool{}
		for _, l := range d.Lost {
			lost[l.InvestigationID] = true
			e.endLost(work, org, l.InvestigationID)
		}
		seen := map[callKey]bool{}
		for _, c := range d.Calls {
			if stop.Err() != nil {
				return
			}
			k := callKey{org, c.InvestigationID, c.Seq}
			seen[k] = true
			if lost[c.InvestigationID] {
				continue
			}
			e.carry(work, org, c, k)
		}
		e.forget(org, seen)
	}
}

// endLost ends investigation inv with role_lost (the database refuses it
// while the requester still holds owner or editor).
func (e *Executor) endLost(ctx context.Context, org, inv string) {
	ended, err := e.cfg.Scope.EndLost(ctx, org, inv)
	switch {
	case err != nil:
		e.cfg.Log.Warn("ending an investigation whose requester lost the role failed", "organization_id", org, "investigation_id", inv,
			"error", err.Error())
	case ended:
		e.cfg.Log.Info("investigation stopped: its requester no longer holds owner or editor", "organization_id", org,
			"investigation_id", inv, "outcome", string(store.RoleLost))
	}
}

// forget drops the retries of org's calls this pass did not find pending.
func (e *Executor) forget(org string, seen map[callKey]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k := range e.retries {
		if k.org == org && !seen[k] {
			delete(e.retries, k)
		}
	}
}

// outcome is how one attempt at a call ended.
type outcome int

const (
	carried   outcome = iota // done, refused or failed, recorded with its event
	skipped                  // not pending any more, or held by another transaction
	ended                    // the investigation ended (or its deadline passed) first: nothing written
	lost                     // the requester holds no owner or editor membership
	transient                // retried later (code: what it is recorded as at the end)
	broken                   // not retried: recorded failed with code
)

// result is an attempt's outcome, with what was recorded or is to be.
type result struct {
	how    outcome
	status store.CallStatus
	tool   string
	code   investigator.Code
	err    error
}

// carry carries out call c of org (or waits for its backoff), and records
// it failed once its transient failures are used up or for a broken one.
func (e *Executor) carry(ctx context.Context, org string, c actingfor.Call, k callKey) {
	e.mu.Lock()
	r := e.retries[k]
	e.mu.Unlock()
	if r != nil && time.Now().Before(r.next) {
		return
	}
	start := time.Now()
	res := e.attempt(ctx, org, c)
	switch res.how {
	case carried:
		e.drop(k)
		e.report(ctx, start, org, c, res)
	case skipped:
	case ended:
		e.drop(k)
	case lost:
		e.drop(k)
		e.endLost(ctx, org, c.InvestigationID)
	case transient:
		if e.later(k, res.code) {
			return
		}
		e.fail(ctx, start, org, c, k, res.code)
	case broken:
		if res.err != nil {
			e.cfg.Log.Error("an investigation call failed", "organization_id", org, "investigation_id", c.InvestigationID,
				"seq", c.Seq, "code", string(res.code), "error", res.err.Error())
		}
		e.fail(ctx, start, org, c, k, res.code)
	}
}

func (e *Executor) drop(k callKey) {
	e.mu.Lock()
	delete(e.retries, k)
	e.mu.Unlock()
}

// later records a transient failure of call k and reports whether it is to
// be tried again (fewer than MaxTries, within RetryFor of the first).
func (e *Executor) later(k callKey, code investigator.Code) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	r := e.retries[k]
	if r == nil {
		r = &retry{first: now}
		e.retries[k] = r
	}
	r.tries++
	r.code = code
	if r.tries >= e.cfg.MaxTries || now.Sub(r.first) >= e.cfg.RetryFor {
		delete(e.retries, k)
		return false
	}
	wait := e.cfg.Backoff << (r.tries - 1)
	if wait <= 0 || wait > e.cfg.MaxBackoff {
		wait = e.cfg.MaxBackoff
	}
	r.next = now.Add(wait)
	return true
}

// fail records call c failed with code, in its own acting-for transaction
// (nothing else is written); an investigation that ended first, or a call
// no longer pending, is left as it is.
func (e *Executor) fail(ctx context.Context, start time.Time, org string, c actingfor.Call, k callKey, code investigator.Code) {
	e.drop(k)
	res := e.record(ctx, org, c, func(ctx context.Context, tx tenant.Tx, m tenant.Membership, inv store.Investigation, call store.Call) (store.Resolution, error) {
		return store.Resolution{Status: store.Failed, Error: &store.ToolError{Code: string(code), Path: ""}}, nil
	})
	switch res.how {
	case carried:
		e.report(ctx, start, org, c, res)
	case lost:
		e.endLost(ctx, org, c.InvestigationID)
	case transient, broken:
		e.cfg.Log.Warn("recording an investigation call as failed failed; it stays pending", "organization_id", org,
			"investigation_id", c.InvestigationID, "seq", c.Seq, "code", string(res.code))
	}
}

// report logs and traces a recorded call by its IDs and codes.
func (e *Executor) report(ctx context.Context, start time.Time, org string, c actingfor.Call, res result) {
	telemetry.Current().ToolCall(ctx, start, res.tool, string(res.status), string(res.code))
	e.cfg.Log.Info("investigation call carried out", "organization_id", org, "investigation_id", c.InvestigationID, "seq", c.Seq,
		"tool", res.tool, "outcome", string(res.status), "code", string(res.code), "duration_ms", time.Since(start).Milliseconds())
}

// attempt is one try at call c: its effect, through effects.
func (e *Executor) attempt(ctx context.Context, org string, c actingfor.Call) result {
	return e.record(ctx, org, c, e.effect)
}

// effectFunc is a call's effect in its transaction: the call's resolution
// (done with what it made, refused with a typed refusal), or an error.
type effectFunc func(ctx context.Context, tx tenant.Tx, m tenant.Membership, inv store.Investigation, call store.Call) (store.Resolution, error)

// errSkipped: the call is not pending any more, or another transaction
// holds it; nothing was written.
var errSkipped = errors.New("executor: the call is not pending or is held elsewhere")

// record runs fn for call c in the investigation's acting-for transaction
// for its requester, then records its resolution and its event, in one
// commit; a panic is recovered as a broken attempt.
func (e *Executor) record(ctx context.Context, org string, c actingfor.Call, fn effectFunc) (res result) {
	ctx, cancel := context.WithTimeout(ctx, e.cfg.CallTimeout)
	defer cancel()
	defer func() {
		if p := recover(); p != nil {
			// Never the panic's text: it may hold what the call carried.
			res = result{how: broken, code: investigator.Internal, err: errors.New("the call's effect panicked")}
		}
	}()
	requestID := ids.New("req")
	var tool string
	var status store.CallStatus
	var code investigator.Code
	err := e.cfg.DB.Run(ctx, tenant.Scope{OrganizationID: org, UserID: c.RequestedBy, ActingFor: c.InvestigationID}, tenant.ReadWrite,
		func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
			if err := m.Require(tenant.Writers...); err != nil {
				return err
			}
			call, ok, err := store.ClaimCall(ctx, tx, org, c.InvestigationID, c.Seq)
			if err != nil {
				return err
			}
			if !ok {
				return errSkipped
			}
			tool = call.Tool
			inv, err := store.Get(ctx, tx, org, c.InvestigationID)
			if err != nil {
				return err
			}
			if inv.Status != store.Running || inv.CreatedBy != m.UserID {
				return store.ErrEnded
			}
			r, err := fn(ctx, tx, m, inv, call)
			if err != nil {
				return err
			}
			if err := store.Resolve(ctx, tx, org, inv.ID, call.Seq, r); err != nil {
				return err
			}
			status = r.Status
			meta := map[string]any{"seq": call.Seq, "tool": call.Tool, "outcome": string(r.Status)}
			if r.Error != nil {
				code = investigator.Code(r.Error.Code)
				meta["code"] = r.Error.Code
			}
			return auditlog.AppendUser(ctx, tx, org, m.UserID, auditlog.Event{Action: "investigation.tool_call", TargetType: "investigation",
				TargetID: inv.ID, RequestID: requestID, Metadata: auditlog.MarkAI(meta, m.ActingFor)})
		})
	if err == nil {
		return result{how: carried, status: status, tool: tool, code: code}
	}
	return classify(err, tool)
}

// classify is how a failed attempt ended.
func classify(err error, tool string) result {
	var rerr *datasets.ReadError
	switch {
	case errors.Is(err, errSkipped):
		return result{how: skipped, tool: tool}
	case errors.Is(err, tenant.ErrNoMembership), errors.Is(err, tenant.ErrForbidden):
		return result{how: lost, tool: tool}
	case errors.Is(err, store.ErrEnded), errors.Is(err, effect.ErrEnded), errors.Is(err, store.ErrResolved):
		return result{how: ended, tool: tool}
	case errors.As(err, &rerr):
		return result{how: transient, tool: tool, code: investigator.DatasetStorageFailed}
	case errors.Is(err, tenant.ErrRetryable), errors.Is(err, tenant.ErrOutcomeUnknown), errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled), isImmutable(err):
		// 55000 is the database's end: a stop or the deadline that
		// committed first (the next discovery no longer finds the call),
		// or a guard this code did not expect (failed after the tries).
		return result{how: transient, tool: tool, code: transientCode(err)}
	}
	return result{how: broken, tool: tool, code: investigator.Internal, err: err}
}

func transientCode(err error) investigator.Code {
	if isImmutable(err) {
		return investigator.Internal
	}
	return investigator.ServiceUnavailable
}

// state is the readable form of an outcome, for tests.
func (o outcome) String() string {
	return [...]string{"carried", "skipped", "ended", "lost", "transient", "broken"}[o]
}
