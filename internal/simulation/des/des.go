// Package des is the native Go discrete-event SimulationEngine
// (docs/ARCHITECTURE.md §3, ADR-006, ADR-012): it compiles a frozen run
// input, runs its replications in a bounded pool against one shared event
// budget and wall timeout, and turns their observations into the Result
// (metrics, replication summaries, reproducibility hashes, result digest)
// and the sampled trace (T-022).
//
// Like the engine it is pure: no DB, network, object storage, LLM or HTTP.
// Domain metrics (DORA) come from a function the process injects, so the
// generic engine never imports a domain pack.
package des

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/results/metrics"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
	"github.com/kcl1104/geminus/internal/simulation/trace"
)

// Version is the engine_version of this engine and its numerical profile
// (SIMULATION_SPEC §4, D-038). A change to any sampled, aggregated or hashed
// number needs a new version.
const Version = "des-go-0.1.0"

// ErrWallTimeout: the run exceeded wall_timeout_seconds. Like the engine's
// limit errors it fails the run; nothing partial is returned.
var ErrWallTimeout = errors.New("des: wall timeout exceeded")

// Engine runs simulations. The zero value is ready to use.
type Engine struct {
	// Version is the engine_version recorded in results; "" means Version.
	// A deployment may append build identity (SIMULATION_SPEC §4).
	Version string
	// Workers bounds how many replications run at once; 0 means GOMAXPROCS.
	Workers int
	// DomainMetrics, if set, fills Result.metrics.dora from the generic
	// result before the digest is taken (DomainPack.ComputeMetrics).
	DomainMetrics func(*simulation.Result) map[string]simulation.MetricSummary

	timeout       func(simulation.ExecutionLimits) time.Duration // tests only
	afterPool     func()                                         // tests only: runs once every worker has stopped
	onReplication func(index int)                                // tests only: runs as a worker starts a replication
}

var _ simulation.Engine = (*Engine)(nil)

// Output is a completed run: the Result and, when trace_level is sampled
// and max_trace_events > 0, the trace artifact to store.
type Output struct {
	Result *simulation.Result
	Trace  *simulation.TraceArtifact
}

func (e *Engine) version() string {
	if e.Version != "" {
		return e.Version
	}
	return Version
}

// Validate reports the IR's schema and semantic issues, with paths relative
// to the IR (Compile reports the same issues under /model). Dataset bindings,
// the manifest, the config and the limits are checked by Run.
func (e *Engine) Validate(ctx context.Context, ir model.ModelIR) ([]model.ValidationIssue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := model.Encode(&ir)
	if err != nil {
		return nil, fmt.Errorf("des: encoding the model: %w", err)
	}
	res, err := validate.IR(data)
	if err != nil {
		return nil, err
	}
	return res.Issues, nil
}

// Run executes a run and returns its Result; RunWithTrace also returns the
// trace. Result.RunID is left for the caller, which owns run identity.
func (e *Engine) Run(ctx context.Context, in simulation.RunInput, progress simulation.ProgressSink) (*simulation.Result, error) {
	out, err := e.RunWithTrace(ctx, in, progress)
	if err != nil {
		return nil, err
	}
	return out.Result, nil
}

// RunWithTrace executes every replication of in and builds the Result. It
// fails, returning nothing partial, on invalid input (*engine.InputError),
// cancellation (the context's error), the wall timeout (ErrWallTimeout) or a
// limit (engine.ErrEventBudget, ErrActiveEntities, ErrEntityVisits). When
// several replications fail, which limit is reported can vary between runs;
// each reported limit was really exceeded.
func (e *Engine) RunWithTrace(ctx context.Context, in simulation.RunInput, progress simulation.ProgressSink) (*Output, error) {
	return e.RunWith(ctx, in, Options{Progress: progress})
}

// Options govern one run without changing its result: where progress goes
// and how the event budget is granted.
type Options struct {
	Progress simulation.ProgressSink
	// Credit, if set, grants the run's event budget (in.Limits.MaxEvents) in
	// batches: Granted events at once, the rest as Credit grants them
	// (engine.NewCreditBudget). The worker reserves each grant durably, so a
	// logical run's budget holds across retries (D-007). Without it the
	// whole budget is granted at once.
	Credit  engine.Credit
	Granted int64
}

// ErrInvalidOptions: Options.Granted is outside 0..in.Limits.MaxEvents.
var ErrInvalidOptions = errors.New("des: invalid run options")

// RunWith is RunWithTrace with opts.
func (e *Engine) RunWith(ctx context.Context, in simulation.RunInput, opts Options) (*Output, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Credit != nil && (opts.Granted < 0 || opts.Granted > in.Limits.MaxEvents) {
		return nil, ErrInvalidOptions
	}
	progress := opts.Progress
	p, err := engine.Compile(in)
	if err != nil {
		return nil, err
	}
	repro, err := reproducibility(in)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(in.Limits.WallTimeoutSeconds) * time.Second
	if e.timeout != nil {
		timeout = e.timeout(in.Limits)
	}
	runCtx, cancelTimeout := context.WithTimeoutCause(ctx, timeout, ErrWallTimeout)
	defer cancelTimeout()
	poolCtx, stop := context.WithCancelCause(runCtx)
	defer stop(nil)

	n := p.Replications()
	var rec *trace.Recorder
	if p.TraceLevel() == simulation.TraceSampled && in.Limits.MaxTraceEvents > 0 {
		rec = trace.NewRecorder(p, 0, in.Limits.MaxTraceEvents) // replication 0 only (D-039)
	}
	reps := make([]metrics.Replication, n)
	budget := engine.NewBudget(in.Limits.MaxEvents)
	if opts.Credit != nil {
		budget = engine.NewCreditBudget(in.Limits.MaxEvents, opts.Granted, opts.Credit)
	}
	workers := e.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	report, stopReports := deliver(progress)
	defer stopReports()
	var (
		next     atomic.Int64
		mu       sync.Mutex
		finished int // replications completed, under mu
		wg       sync.WaitGroup
	)
	for range min(workers, n) {
		wg.Go(func() {
			// A panic is an engine bug; it fails the run, not the process.
			defer func() {
				if v := recover(); v != nil {
					stop(fmt.Errorf("%w: panic in a replication: %v", engine.ErrInternal, v))
				}
			}()
			for {
				i := int(next.Add(1) - 1)
				if i >= n || poolCtx.Err() != nil {
					return
				}
				if e.onReplication != nil {
					e.onReplication(i)
				}
				c := metrics.NewCollector(p)
				var obs engine.Observer = c
				if i == 0 && rec != nil {
					obs = tee{c, rec}
				}
				out, err := engine.RunReplication(poolCtx, p, uint32(i), budget, obs)
				if err != nil {
					stop(err)
					return
				}
				c.Finish()
				reps[i] = metrics.Replication{Collector: c, Outcome: out}
				mu.Lock()
				finished++
				report(simulation.Progress{CompletedReplications: finished, TotalReplications: n, EventsProcessed: budget.Used()})
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if e.afterPool != nil {
		e.afterPool()
	}
	// Completion decides: a run whose every replication finished is
	// complete even if the deadline or a cancellation lands just after.
	if finished != n {
		if err := context.Cause(poolCtx); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %d of %d replications finished", engine.ErrInternal, finished, n)
	}

	m, summaries, window, err := metrics.Build(p, reps)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", engine.ErrInternal, err)
	}
	res := &simulation.Result{
		Status: "completed", EngineVersion: e.version(), Metrics: m, ReplicationSummaries: summaries,
		MeasurementWindow: window, Reproducibility: repro, Trace: simulation.Trace{},
	}
	out := &Output{Result: res}
	if rec != nil {
		out.Trace = rec.Artifact(e.version())
		res.Trace = simulation.Trace{Available: len(out.Trace.Events) > 0, SampledEvents: len(out.Trace.Events)}
		if !res.Trace.Available {
			out.Trace = nil
		}
	}
	if e.DomainMetrics != nil {
		dm, err := domainMetrics(e.DomainMetrics, res)
		if err != nil {
			return nil, err
		}
		if dm != nil {
			res.Metrics.DORA = dm
		}
	}
	digest, err := hashing.Result(*res)
	if err != nil {
		return nil, fmt.Errorf("%w: result digest: %v", engine.ErrInternal, err)
	}
	res.Reproducibility.ResultDigest = digest
	return out, nil
}

// domainMetrics runs the injected pack function; a panic is a pack bug that
// fails the run with ErrInternal rather than the worker process.
func domainMetrics(f func(*simulation.Result) map[string]simulation.MetricSummary, res *simulation.Result) (dm map[string]simulation.MetricSummary, err error) {
	defer func() {
		if v := recover(); v != nil {
			dm, err = nil, fmt.Errorf("%w: panic in DomainMetrics: %v", engine.ErrInternal, v)
		}
	}()
	return f(res), nil
}

// deliver returns a report function that never blocks and a stop function.
// Reports go to sink from one goroutine, never concurrently; when the sink is
// slower than the run, only the latest undelivered snapshot is kept, so
// snapshots can be skipped but never reordered (report is called under the
// pool's lock). The run never waits for the sink: progress is not a
// correctness input (SIMULATION_SPEC §8). stop closes the channel once the
// pool has stopped; the reporter drains it, so the last snapshot is always
// delivered, possibly just after the run returns, and then exits.
func deliver(sink simulation.ProgressSink) (func(simulation.Progress), func()) {
	if sink == nil {
		return func(simulation.Progress) {}, func() {}
	}
	latest := make(chan simulation.Progress, 1)
	go func() {
		// A sink that panics stops receiving reports; the run, which may
		// already have returned, is unaffected (T-022 re-review). report
		// never needs a reader: it replaces the unread snapshot itself.
		defer func() { _ = recover() }()
		for pr := range latest {
			sink.Report(pr)
		}
	}()
	report := func(pr simulation.Progress) {
		for {
			select {
			case latest <- pr:
				return
			default:
			}
			select { // replace the undelivered, older snapshot
			case <-latest:
			default:
			}
		}
	}
	return report, func() { close(latest) }
}

// tee sends each observation to the metrics collector and the trace recorder.
type tee struct {
	c *metrics.Collector
	r *trace.Recorder
}

func (t tee) Observe(o engine.Observation) {
	t.c.Observe(o)
	t.r.Observe(o)
}

var hashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// reproducibility computes every hash the Result records except the digest.
// The effective model hash is recomputed and must equal the one frozen with
// the run; the pinned version's model hash is recorded as given.
func reproducibility(in simulation.RunInput) (simulation.Reproducibility, error) {
	bad := func(path, message string) (simulation.Reproducibility, error) {
		return simulation.Reproducibility{}, &engine.InputError{Issues: []model.ValidationIssue{
			model.NewIssue(validate.IRSchemaInvalid, path, message, model.SeverityError)}}
	}
	if !hashPattern.MatchString(in.ModelHash) {
		return bad("/model_hash", "the pinned version's model hash is missing or malformed")
	}
	data, err := model.Encode(&in.Model)
	if err != nil {
		return simulation.Reproducibility{}, fmt.Errorf("des: encoding the model: %w", err)
	}
	effective, err := hashing.Model(data)
	if err != nil {
		return simulation.Reproducibility{}, fmt.Errorf("%w: effective model hash: %v", engine.ErrInternal, err)
	}
	if in.EffectiveModelHash != effective {
		return bad("/effective_model_hash", "does not match the model being run")
	}
	config, err := hashing.Config(in.Config)
	if err != nil {
		return simulation.Reproducibility{}, fmt.Errorf("%w: config hash: %v", engine.ErrInternal, err)
	}
	manifest, err := hashing.Manifest(in.Manifest)
	if err != nil {
		return simulation.Reproducibility{}, fmt.Errorf("%w: manifest hash: %v", engine.ErrInternal, err)
	}
	return simulation.Reproducibility{
		Seed: in.Config.Seed, Replications: in.Config.Replications, ModelHash: in.ModelHash,
		EffectiveModelHash: effective, ConfigHash: config, DatasetManifestHash: manifest,
	}, nil
}
