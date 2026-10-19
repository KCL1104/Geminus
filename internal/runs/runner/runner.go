// Package runner is the simulation job handler (T-023, ADR-024): the
// adapter between the worker loop and the pure engine. For one claimed job
// it reads the run's frozen input under the lease, runs
// internal/simulation/des outside any transaction, and commits the result
// (or the cancellation) with the job's end, under the lease
// (handler.Run.Finish); after a lost lease nothing it does commits. It reads
// and writes the database, so it lives outside the generic kernel; only
// cmd/worker registers it.
//
// Budgets are the logical run's, across retries (D-007, API_DATABASE §7):
//   - the wall clock runs from the run's first start (execution_deadline_at,
//     never reset); a retry gets what is left;
//   - events are reserved before they are spent: each attempt reserves a
//     first batch when it starts and the engine asks for more as it needs
//     them (engine.Credit), one durable reservation per batch. What earlier
//     attempts reserved counts as spent, so a crash consumes at most the
//     unspent part of its last batch, and the run never spends more than
//     max_events in all.
//
// Progress is written at checkpoints (every Poll), which also read whether
// cancellation was requested; a requested cancellation stops the engine at
// its next event and ends the job cancelled. Failures are typed
// (internal/storage/runs codes): a limit or an input the engine refuses is
// never retried.
package runner

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sync"
	"time"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
	"github.com/kcl1104/geminus/internal/simulation/engine"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	models "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/objects"
	store "github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/storage/snapshots"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// PackLookup finds a model's domain pack and lists the domains it serves;
// *domainpack.Registry is one.
type PackLookup interface {
	Lookup(domain string) (domainpack.DomainPack, error)
	Domains() []string
}

// MaxTraceBytes bounds a stored trace artifact (migration 0009).
const MaxTraceBytes = 64 << 20

// Config is the handler's setup.
type Config struct {
	Packs PackLookup
	// Objects holds dataset snapshots and trace artifacts (D-044); nil
	// when object storage is disabled.
	Objects objects.Store
	// Actor names the worker in audit events (WORKER_ID): a letter, then
	// letters, digits, '_', '.', ':' or '-', at most 128 characters.
	Actor string
	// EngineVersion is this worker's engine (des.Version when ""); a run
	// frozen for another one fails (ENGINE_VERSION_MISMATCH).
	EngineVersion string
	// Workers bounds the replications run at once (0: GOMAXPROCS).
	Workers int
	// Poll is how often progress is written and cancellation read (1 s).
	Poll time.Duration
	// Batch is the event credit one reservation grants for a run of
	// maxEvents (a tenth of it, at least 1, when nil).
	Batch func(maxEvents int64) int64
	Log   *slog.Logger
}

// Handler runs simulation jobs (handler.Handler).
type Handler struct {
	cfg Config

	beforeFinish func(ctx context.Context) // tests only: runs just before the result's Finish
}

var _ handler.Handler = (*Handler)(nil)

var (
	actorRef = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
	runIDs   = regexp.MustCompile(`^sim_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	reqIDs   = regexp.MustCompile(`^req_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// New checks cfg.
func New(cfg Config) (*Handler, error) {
	switch {
	case cfg.Packs == nil || cfg.Log == nil:
		return nil, errors.New("runner: packs and log are required")
	case !actorRef.MatchString(cfg.Actor):
		return nil, errors.New("runner: the worker's name must start with a letter and use only letters, digits, '_', '.', ':' and '-' (at most 128) to name it in audit events")
	case cfg.Workers < 0 || cfg.Poll < 0:
		return nil, errors.New("runner: workers and poll must not be negative")
	}
	if cfg.EngineVersion == "" {
		cfg.EngineVersion = des.Version
	}
	if cfg.Poll == 0 {
		cfg.Poll = time.Second
	}
	if cfg.Batch == nil {
		cfg.Batch = func(maxEvents int64) int64 { return max(1, (maxEvents+9)/10) }
	}
	return &Handler{cfg: cfg}, nil
}

// Domains lists the domains whose runs h can run, in sorted order.
func (h *Handler) Domains() []string { return h.cfg.Packs.Domains() }

// payload is the job's frozen reference (internal/runs Create).
type payload struct {
	RunID     string `json:"run_id"`
	RequestID string `json:"request_id"`
}

func failure(code, message string, retryable bool) error {
	return &jobs.Failure{Code: code, Message: message, Retryable: retryable}
}

// errCancelRequested stops the engine when the run's cancellation was
// requested.
var errCancelRequested = errors.New("runner: the run's cancellation was requested")

// started is what an attempt read and reserved when it started.
type started struct {
	run     store.Run
	attempt store.Attempt
	version models.Version
	keys    map[string]string
	cancel  bool
}

// Handle runs one attempt of a simulation job.
func (h *Handler) Handle(ctx context.Context, hr handler.Run) error {
	job := hr.Job()
	var p payload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.RunID != job.Resource.SimulationRunID || !runIDs.MatchString(p.RunID) || !reqIDs.MatchString(p.RequestID) {
		return failure(store.RunInputInvalid, "the job does not name its run and request", false)
	}
	org := job.OrganizationID
	audit := func(ctx context.Context, tx tenant.Tx, action string, meta map[string]any) error {
		meta["attempt"] = job.Attempt
		return auditlog.AppendWorker(ctx, tx, org, h.cfg.Actor, auditlog.Event{Action: action, TargetType: "simulation",
			TargetID: p.RunID, RequestID: p.RequestID, Metadata: meta})
	}
	// Read before the transaction begins: the database measures the time
	// left from a moment no earlier than this.
	begun := time.Now()
	var st started
	if err := hr.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		st, err = h.start(ctx, tx, org, p.RunID)
		return err
	}); err != nil {
		return err
	}
	if st.cancel {
		return h.cancelled(ctx, hr, audit)
	}
	in, pack, err := h.input(ctx, org, st)
	if err != nil {
		return err
	}
	return h.execute(ctx, hr, org, st, in, pack, begun, audit)
}

// start checks the run as its attempt begins and marks it running,
// reserving the attempt's first grant of events.
func (h *Handler) start(ctx context.Context, tx tenant.Tx, org, id string) (started, error) {
	r, err := store.Lock(ctx, tx, org, id)
	if errors.Is(err, store.ErrNotFound) {
		return started{}, failure(store.RunInputInvalid, "the job's run is not visible to its organization", false)
	}
	if err != nil {
		return started{}, err
	}
	switch {
	case r.Status != store.Queued:
		// A running job's run is queued until its attempt starts (a retry
		// puts it back, 0009); anything else is not a run this job can run.
		return started{}, failure("INTERNAL", fmt.Sprintf("the run is %s while its job runs", r.Status), false)
	case r.CancellationRequested:
		return started{run: r, cancel: true}, nil
	case r.EngineVersion != h.cfg.EngineVersion:
		return started{}, failure(store.EngineVersionMismatch, "the run was frozen for engine "+r.EngineVersion+", this worker runs "+h.cfg.EngineVersion, false)
	}
	left := r.Limits.MaxEvents - r.EventBudgetReserved
	if left <= 0 {
		return started{}, failure(store.EventBudgetExceeded, "earlier attempts reserved the run's whole event budget", false)
	}
	a, err := store.Start(ctx, tx, org, id, min(left, h.cfg.Batch(r.Limits.MaxEvents)))
	if err != nil {
		return started{}, err
	}
	if a.Remaining <= 0 {
		// Returning the failure rolls the start back: the run fails as it was.
		return started{}, failure(store.WallTimeoutExceeded, "the run's wall time ended before this attempt", false)
	}
	v, err := models.GetVersion(ctx, tx, org, r.ModelVersionID)
	if err != nil {
		return started{}, err
	}
	keys, err := snapshots.Keys(ctx, tx, org, r.DatasetBindings)
	if err != nil {
		return started{}, err
	}
	return started{run: r, attempt: a, version: v, keys: keys}, nil
}

// input rebuilds the run's frozen input outside any transaction: the
// version with the frozen patch applied, the datasets fetched and checked
// against their hashes, and every hash compared with the frozen one.
func (h *Handler) input(ctx context.Context, org string, st started) (simulation.RunInput, domainpack.DomainPack, error) {
	r := st.run
	invalid := func(what string) (simulation.RunInput, domainpack.DomainPack, error) {
		return simulation.RunInput{}, nil, failure(store.RunInputInvalid, what, false)
	}
	res, err := patch.Apply(st.version.IR, r.ScenarioPatch, patch.Scenario)
	if err != nil {
		return simulation.RunInput{}, nil, err
	}
	if !res.Valid() {
		return invalid("the frozen scenario patch no longer applies to its version")
	}
	if effective, err := hashing.Model(res.Candidate); err != nil || effective != r.EffectiveModelHash {
		return invalid("the effective model's hash is not the frozen one")
	}
	if st.version.Hash != r.ModelHash {
		return invalid("the version's hash is not the frozen one")
	}
	if config, err := hashing.Config(r.Config); err != nil || config != r.ConfigHash {
		return invalid("the configuration's hash is not the frozen one")
	}
	ir := res.Validation.IR
	pack, err := h.cfg.Packs.Lookup(ir.Domain)
	if err != nil {
		return invalid("the model's domain is not served by this worker")
	}
	if len(r.DatasetBindings) > 0 && h.cfg.Objects == nil {
		return simulation.RunInput{}, nil, failure(store.ArtifactStorageFailed, "the run binds datasets and this worker has no object storage", false)
	}
	resolution, err := datasets.Resolve(ctx, snapshots.Store{Keys: st.keys, Objects: h.cfg.Objects}, org, ir, r.DatasetBindings)
	if err != nil {
		return simulation.RunInput{}, nil, err
	}
	if !resolution.Valid() {
		first := validate.Sort(resolution.Issues)[0]
		return invalid(fmt.Sprintf("the frozen datasets no longer verify: %s at %s", first.Code, first.Path))
	}
	if resolution.ManifestHash != r.DatasetManifestHash {
		return invalid("the dataset manifest's hash is not the frozen one")
	}
	limits := r.Limits
	limits.MaxEvents -= st.attempt.Base
	limits.WallTimeoutSeconds = min(r.Limits.WallTimeoutSeconds, max(1, int(math.Ceil(st.attempt.Remaining.Seconds()))))
	return simulation.RunInput{Model: *ir, ModelHash: r.ModelHash, EffectiveModelHash: r.EffectiveModelHash, Config: r.Config,
		Limits: limits, Datasets: resolution.Datasets, Manifest: resolution.Manifest}, pack, nil
}

// execute runs the engine with its governors and ends the attempt.
func (h *Handler) execute(ctx context.Context, hr handler.Run, org string, st started, in simulation.RunInput, pack domainpack.DomainPack,
	begun time.Time, audit func(context.Context, tenant.Tx, string, map[string]any) error) error {
	r := st.run
	run, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	run, cancelDeadline := context.WithDeadlineCause(run, begun.Add(st.attempt.Remaining), des.ErrWallTimeout)
	defer cancelDeadline()

	sink := &latest{}
	var polls sync.WaitGroup
	polls.Go(func() { h.poll(run, hr, org, r.ID, sink, stop) })
	credit := &credit{hr: hr, org: org, run: r.ID, base: st.attempt.Base, limit: in.Limits.MaxEvents, batch: h.cfg.Batch(r.Limits.MaxEvents)}
	e := &des.Engine{Version: h.cfg.EngineVersion, Workers: h.cfg.Workers, DomainMetrics: pack.ComputeMetrics}
	out, err := e.RunWith(run, in, des.Options{Progress: sink, Credit: credit, Granted: st.attempt.Granted})
	cause := context.Cause(run)
	stop(nil)
	polls.Wait()

	switch {
	case err == nil:
	case errors.Is(cause, errCancelRequested) && ctx.Err() == nil:
		return h.cancelled(ctx, hr, audit)
	case ctx.Err() != nil:
		return err // the attempt stopped: the worker records it, or nothing
	default:
		return h.failed(err)
	}
	result := *out.Result
	result.RunID = r.ID
	var trace *store.Trace
	if out.Trace != nil {
		if trace, err = h.storeTrace(ctx, org, r.ID, out.Trace); err != nil {
			return err
		}
		result.Trace.ArtifactID = &trace.ArtifactID
	}
	data, err := json.Marshal(result)
	if err != nil {
		return failure(store.EngineInternal, "the result cannot be encoded", false)
	}
	if issues, err := validate.Document(data, "simulation.schema.json#/$defs/Result", store.EngineInternal); err != nil || len(issues) > 0 {
		return failure(store.EngineInternal, "the engine's result does not match the contract", false)
	}
	if h.beforeFinish != nil {
		h.beforeFinish(ctx)
	}
	err = hr.Finish(ctx, jobs.Completed, func(ctx context.Context, tx tenant.Tx) error {
		now, err := store.Lock(ctx, tx, org, r.ID)
		if err != nil {
			return err
		}
		if now.CancellationRequested {
			// Requested after the engine's last checkpoint: the request
			// wins, and the result is not stored (cancel and complete are
			// exclusive, TEST-021).
			return errCancelRequested
		}
		if err := store.Complete(ctx, tx, store.Result{RunID: r.ID, OrganizationID: org, Result: data,
			Digest: result.Reproducibility.ResultDigest, Trace: trace}, r.Config.Replications, out.Events); err != nil {
			return err
		}
		return audit(ctx, tx, "simulation.complete", map[string]any{"result_digest": result.Reproducibility.ResultDigest, "events": out.Events})
	})
	if errors.Is(err, errCancelRequested) {
		return h.cancelled(ctx, hr, audit)
	}
	return err
}

// cancelled ends the job cancelled; the run follows it (0009).
func (h *Handler) cancelled(ctx context.Context, hr handler.Run, audit func(context.Context, tenant.Tx, string, map[string]any) error) error {
	return hr.Finish(ctx, jobs.Cancelled, func(ctx context.Context, tx tenant.Tx) error {
		return audit(ctx, tx, "simulation.cancelled", map[string]any{})
	})
}

// failed is the typed failure of an engine error.
func (h *Handler) failed(err error) error {
	var ie *engine.InputError
	var f *jobs.Failure
	switch {
	case errors.As(err, &f):
		return f
	case errors.Is(err, des.ErrWallTimeout):
		return failure(store.WallTimeoutExceeded, "the run exceeded its wall time (wall_timeout_seconds)", false)
	case errors.Is(err, engine.ErrEventBudget):
		return failure(store.EventBudgetExceeded, "the run exceeded its event budget (max_events)", false)
	case errors.Is(err, engine.ErrActiveEntities):
		return failure(store.ActiveEntitiesExceeded, "more entities were in the system at once than max_active_entities", false)
	case errors.Is(err, engine.ErrEntityVisits):
		return failure(store.EntityVisitsExceeded, "an entity entered more nodes than max_entity_visits", false)
	case errors.As(err, &ie):
		first := ie.Issues[0]
		return failure(store.RunInputInvalid, fmt.Sprintf("the engine refused the frozen input: %s at %s", first.Code, first.Path), false)
	case errors.Is(err, engine.ErrInternal):
		return failure(store.EngineInternal, "the engine failed an internal check", false)
	}
	return err // a checkpoint's error (the database, a lost lease): the worker decides
}

// storeTrace writes the trace artifact under a key of its own (a retry
// writes another, so a stored object is never replaced) and returns how
// the result names it.
func (h *Handler) storeTrace(ctx context.Context, org, runID string, t *simulation.TraceArtifact) (*store.Trace, error) {
	if h.cfg.Objects == nil {
		return nil, failure(store.ArtifactStorageFailed, "the run records a sampled trace and this worker has no object storage", false)
	}
	data, err := json.Marshal(t)
	if err != nil {
		return nil, failure(store.EngineInternal, "the trace cannot be encoded", false)
	}
	art := ids.New("art")
	key, err := objects.NewKey(org, objects.SimulationResults, runID, art+".json")
	if err != nil {
		return nil, err
	}
	obj, err := h.cfg.Objects.Put(ctx, key, bytes.NewReader(data), MaxTraceBytes)
	switch {
	case errors.Is(err, objects.ErrTooLarge):
		return nil, failure(store.EngineInternal, "the trace is larger than a trace may be", false)
	case err != nil && ctx.Err() != nil:
		return nil, err
	case err != nil:
		h.cfg.Log.Warn("storing a trace failed", "organization_id", org, "run_id", runID, "error", err.Error())
		return nil, failure(store.ArtifactStorageFailed, "the trace could not be stored", true)
	}
	return &store.Trace{ArtifactID: art, ObjectKey: key, SHA256: obj.SHA256, Size: obj.Size}, nil
}

// latest keeps the engine's last progress report (simulation.ProgressSink:
// it never blocks the engine).
type latest struct {
	mu sync.Mutex
	p  simulation.Progress
}

func (l *latest) Report(p simulation.Progress) { l.mu.Lock(); l.p = p; l.mu.Unlock() }

func (l *latest) get() simulation.Progress { l.mu.Lock(); defer l.mu.Unlock(); return l.p }

// poll writes progress every Poll and stops the engine (stop) once the
// run's cancellation was requested, until ctx ends. A write that fails is
// tried at the next tick; one the attempt can no longer make ends polling
// (the worker stops the attempt).
func (h *Handler) poll(ctx context.Context, hr handler.Run, org, id string, sink *latest, stop context.CancelCauseFunc) {
	t := time.NewTicker(h.cfg.Poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		p := sink.get()
		var cancel bool
		err := hr.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
			var err error
			cancel, err = store.Progress(ctx, tx, org, id, p.CompletedReplications, p.EventsProcessed)
			return err
		})
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, jobs.ErrLeaseLost), errors.Is(err, handler.ErrStopped):
			return
		case err != nil:
			h.cfg.Log.Warn("writing a run's progress failed", "organization_id", org, "run_id", id, "error", err.Error())
		case cancel:
			stop(errCancelRequested)
			return
		}
	}
}

// credit grants the engine its event budget in batches, each reserved in
// the run's row under the lease before the engine may spend it.
type credit struct {
	hr       handler.Run
	org, run string
	base     int64 // reserved before this attempt
	limit    int64 // this attempt's budget: max_events - base
	batch    int64
}

func (c *credit) Grant(ctx context.Context, granted int64) (int64, error) {
	next := min(c.limit, granted+c.batch)
	err := c.hr.Checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		return store.Reserve(ctx, tx, c.org, c.run, c.base+granted, c.base+next)
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}
