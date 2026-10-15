package des

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
	"github.com/kcl1104/geminus/internal/simulation/trace"
)

var limits = simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 10_000, MaxTraceEvents: 100_000}

const pinned = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func golden(t *testing.T, name string) model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/golden-models", name)), &ir); err != nil {
		t.Fatal(err)
	}
	return ir
}

// input freezes ir with its effective model hash, as the run API will.
func input(t *testing.T, ir model.ModelIR, horizon, warmup float64, replications int, level simulation.TraceLevel) simulation.RunInput {
	t.Helper()
	data, err := model.Encode(&ir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hashing.Model(data)
	if err != nil {
		t.Fatal(err)
	}
	return simulation.RunInput{Model: ir, ModelHash: pinned, EffectiveModelHash: h, Limits: limits,
		Config: simulation.RunConfig{Horizon: simulation.Duration{Value: horizon, Unit: model.Minute}, Warmup: simulation.Duration{Value: warmup, Unit: model.Minute},
			Replications: replications, Seed: 7, TraceLevel: level}}
}

func run(t *testing.T, e *Engine, in simulation.RunInput) *Output {
	t.Helper()
	out, err := e.RunWithTrace(context.Background(), in, nil)
	if err != nil {
		var ie *engine.InputError
		errors.As(err, &ie)
		t.Fatalf("run: %v %v", err, ie)
	}
	return out
}

// dora stands in for the software-delivery pack (the generic engine and its
// tests do not import a domain): every DORA metric unavailable, as in P0.
func dora(*simulation.Result) map[string]simulation.MetricSummary {
	reason := "MISSING_DORA_EVENT_MAPPING"
	return map[string]simulation.MetricSummary{"change_lead_time": {Status: simulation.Unavailable, Population: simulation.PopulationEntity, Unit: "minute", Reason: &reason}}
}

func validResult(t *testing.T, r *simulation.Result) {
	t.Helper()
	c := *r
	c.RunID = "sim_01M4YDQK00FGZZWH0PP15XPNFA" // the worker sets run identity
	schemaValid(t, &c, "simulation.schema.json#/$defs/Result")
}

func schemaValid(t *testing.T, v any, ref string) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	issues, err := validate.Document(data, ref, validate.IRSchemaInvalid)
	if err != nil || len(issues) > 0 {
		t.Fatalf("%s: %v %v", ref, err, issues)
	}
}

func asJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// GOLDEN-001 end to end: a schema-valid Result whose metrics, summaries and
// window are exactly testdata/contracts/valid-result.json's (hand-derived;
// the fixture's additional_metrics is empty, the engine adds the
// replication-mean lead time), whose hashes are the T-005 functions over the
// input, and whose digest is hashing.Result over the rest.
func TestGoldenOneResult(t *testing.T) {
	e := &Engine{DomainMetrics: dora}
	in := input(t, golden(t, "deterministic-queue.json"), 1010, 0, 1, simulation.TraceSampled)
	res := run(t, e, in).Result
	validResult(t, res)
	var fixture map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-result.json")), &fixture); err != nil {
		t.Fatal(err)
	}
	got := asJSON(t, res).(map[string]any)
	gm, fm := got["metrics"].(map[string]any), fixture["metrics"].(map[string]any)
	for _, k := range []string{"throughput", "lead_time_minutes", "resources", "queues", "failure_count", "rework_count", "dora"} {
		if !reflect.DeepEqual(gm[k], fm[k]) {
			t.Errorf("metrics.%s:\n got %v\nwant %v", k, gm[k], fm[k])
		}
	}
	for _, k := range []string{"replication_summaries", "measurement_window"} {
		if !reflect.DeepEqual(got[k], fixture[k]) {
			t.Errorf("%s:\n got %v\nwant %v", k, got[k], fixture[k])
		}
	}
	config, _ := hashing.Config(in.Config)
	manifest, _ := hashing.Manifest(nil)
	r := res.Reproducibility
	if r.Seed != 7 || r.Replications != 1 || r.ModelHash != pinned || r.EffectiveModelHash != in.EffectiveModelHash || r.ConfigHash != config || r.DatasetManifestHash != manifest {
		t.Errorf("reproducibility %+v", r)
	}
	c := *res
	c.Reproducibility.ResultDigest = ""
	if d, err := hashing.Result(c); err != nil || d != r.ResultDigest {
		t.Errorf("digest %s, recomputed %s (%v)", r.ResultDigest, d, err)
	}
	if res.EngineVersion != Version || res.Status != "completed" || res.RunID != "" {
		t.Errorf("identity %q %q %q", res.EngineVersion, res.Status, res.RunID)
	}
}

// GOLDEN-001's trace, hand-derived. Entity k arrives at 10k minutes, queues
// and starts at once, and leaves at 10k+5. Event sequence numbers follow
// from the schedule: arrival 1 is event 1; arrival k (k >= 2) is event
// 4k-6, scheduled while arrival k-1 ran; the dispatch that starts it is
// 4k-1 and its completion 4k (each completion also schedules one dispatch).
// The sampled k come from the independent reference (engine-vectors.json).
func TestGoldenOneTrace(t *testing.T) {
	out := run(t, &Engine{}, input(t, golden(t, "deterministic-queue.json"), 1010, 0, 1, simulation.TraceSampled))
	var vectors struct {
		TraceSelection struct {
			Cases []struct {
				Prefix   string `json:"prefix"`
				Selected []int  `json:"selected"`
			} `json:"cases"`
		} `json:"trace_selection"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/engine-vectors.json")), &vectors); err != nil {
		t.Fatal(err)
	}
	selected := vectors.TraceSelection.Cases[0].Selected
	var want []simulation.TraceEvent
	for _, k := range selected {
		id := fmt.Sprintf("r0/arrival/%d", k)
		at, done := int64(k)*600_000, int64(k)*600_000+300_000
		arrival := uint64(1)
		if k >= 2 {
			arrival = uint64(4*k - 6)
		}
		want = append(want,
			simulation.TraceEvent{TimeTick: at, Sequence: arrival, Type: simulation.TraceArrival, EntityID: id, NodeID: "arrival"},
			simulation.TraceEvent{TimeTick: at, Sequence: arrival, Type: simulation.TraceQueueEnter, EntityID: id, NodeID: "service"},
			simulation.TraceEvent{TimeTick: at, Sequence: uint64(4*k - 1), Type: simulation.TraceServiceStart, EntityID: id, NodeID: "service"},
			simulation.TraceEvent{TimeTick: done, Sequence: uint64(4 * k), Type: simulation.TraceServiceEnd, EntityID: id, NodeID: "service"},
			simulation.TraceEvent{TimeTick: done, Sequence: uint64(4 * k), Type: simulation.TraceComplete, EntityID: id, NodeID: "done"})
	}
	if out.Trace == nil || !reflect.DeepEqual(out.Trace.Events, want) {
		t.Fatalf("trace\n got %+v\nwant %+v", out.Trace, want)
	}
	tr := out.Trace
	if tr.Format != simulation.TraceFormat || tr.ReplicationIndex != 0 || tr.Truncated || tr.Sampling.Rate != 0.125 || tr.Sampling.MaxEvents != 100_000 || tr.EngineVersion != Version {
		t.Errorf("artifact header %+v", tr)
	}
	if r := out.Result.Trace; !r.Available || r.SampledEvents != len(want) || r.ArtifactID != nil {
		t.Errorf("result trace %+v", r)
	}
	schemaValid(t, tr, "simulation.schema.json#/$defs/TraceArtifact")
}

// TEST-019: the same frozen input gives the same Result bit for bit, run
// after run and whatever the worker count; another seed gives another
// digest.
func TestDeterminism(t *testing.T) {
	in := input(t, golden(t, "default-delivery.json"), 14*24*60, 24*60, 12, simulation.TraceSampled)
	first := run(t, &Engine{Workers: 1}, in)
	validResult(t, first.Result)
	for _, workers := range []int{1, 3, 8, 0} {
		got := run(t, &Engine{Workers: workers}, in)
		if !reflect.DeepEqual(got.Result, first.Result) || !reflect.DeepEqual(got.Trace, first.Trace) {
			t.Fatalf("workers %d: a different result", workers)
		}
	}
	other := in
	other.Config.Seed = 8
	if run(t, &Engine{}, other).Result.Reproducibility.ResultDigest == first.Result.Reproducibility.ResultDigest {
		t.Error("another seed, the same digest")
	}
}

// trace_level and the trace cap never change a number: only config_hash and
// with it the digest change (SIMULATION_SPEC §9). A full trace stops
// recording, not the simulation.
func TestTraceDoesNotChangeMetrics(t *testing.T) {
	ir := golden(t, "default-delivery.json")
	sampled := run(t, &Engine{}, input(t, ir, 7*24*60, 0, 3, simulation.TraceSampled))
	none := run(t, &Engine{}, input(t, ir, 7*24*60, 0, 3, simulation.TraceNone))
	capped := input(t, ir, 7*24*60, 0, 3, simulation.TraceSampled)
	capped.Limits.MaxTraceEvents = 7
	small := run(t, &Engine{}, capped)
	for name, o := range map[string]*Output{"none": none, "capped": small} {
		if !reflect.DeepEqual(o.Result.Metrics, sampled.Result.Metrics) || !reflect.DeepEqual(o.Result.ReplicationSummaries, sampled.Result.ReplicationSummaries) {
			t.Errorf("%s: metrics differ from the sampled run", name)
		}
	}
	if none.Trace != nil || none.Result.Trace != (simulation.Trace{}) {
		t.Errorf("trace_level none: %+v %+v", none.Trace, none.Result.Trace)
	}
	if none.Result.Reproducibility.ConfigHash == sampled.Result.Reproducibility.ConfigHash ||
		none.Result.Reproducibility.ResultDigest == sampled.Result.Reproducibility.ResultDigest {
		t.Error("trace_level is not in config_hash")
	}
	if small.Trace == nil || !small.Trace.Truncated || len(small.Trace.Events) != 7 || !reflect.DeepEqual(small.Trace.Events, sampled.Trace.Events[:7]) {
		t.Errorf("capped trace %+v", small.Trace)
	}
	if sampled.Trace.Truncated || len(sampled.Trace.Events) == 0 {
		t.Errorf("full trace truncated=%v, %d events", sampled.Trace.Truncated, len(sampled.Trace.Events))
	}
	for _, ev := range sampled.Trace.Events {
		if !strings.HasPrefix(ev.EntityID, "r0/") {
			t.Fatalf("event of another replication: %+v", ev)
		}
	}
	schemaValid(t, small.Trace, "simulation.schema.json#/$defs/TraceArtifact")
}

// A run that hits a limit, is cancelled or times out fails with a typed
// error and returns nothing.
func TestRunFailures(t *testing.T) {
	// A fresh model per case: edits must not leak through shared slices.
	congested := func() model.ModelIR { return golden(t, "congested-queue.json") }
	for name, c := range map[string]struct {
		edit func(*simulation.RunInput)
		e    *Engine
		want error
	}{
		"event budget":    {func(in *simulation.RunInput) { in.Limits.MaxEvents = 50 }, &Engine{}, engine.ErrEventBudget},
		"active entities": {func(in *simulation.RunInput) { in.Limits.MaxActiveEntities = 3 }, &Engine{}, engine.ErrActiveEntities},
		"invalid model":   {func(in *simulation.RunInput) { in.Model.Resources[0].Capacity = 0 }, &Engine{}, engine.ErrInvalidInput},
		"no model hash":   {func(in *simulation.RunInput) { in.ModelHash = "" }, &Engine{}, engine.ErrInvalidInput},
		"stale effective hash": {func(in *simulation.RunInput) {
			in.Model.Resources[0].Capacity = 2 // the patch changed after the hash was frozen
		}, &Engine{}, engine.ErrInvalidInput},
		// A deadline already past when the pool starts: deterministic.
		"wall timeout": {func(in *simulation.RunInput) { in.Config.Replications = 50 },
			&Engine{Workers: 2, timeout: func(simulation.ExecutionLimits) time.Duration { return 0 }}, ErrWallTimeout},
	} {
		t.Run(name, func(t *testing.T) {
			in := input(t, congested(), 205, 0, 2, simulation.TraceSampled)
			c.edit(&in)
			out, err := c.e.RunWithTrace(context.Background(), in, nil)
			if !errors.Is(err, c.want) || out != nil {
				t.Errorf("err %v, out %v; want %v", err, out, c.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := (&Engine{}).RunWithTrace(ctx, input(t, congested(), 205, 0, 2, simulation.TraceNone), nil); !errors.Is(err, context.Canceled) || out != nil {
		t.Errorf("cancelled: %v", err)
	}
	var ie *engine.InputError
	in := input(t, congested(), 205, 0, 1, simulation.TraceNone)
	in.EffectiveModelHash = pinned
	if _, err := (&Engine{}).RunWithTrace(context.Background(), in, nil); !errors.As(err, &ie) || ie.Issues[0].Path != "/effective_model_hash" {
		t.Errorf("mismatched effective hash: %v", err)
	}
}

// A deadline that passes while replications run stops them at a checkpoint:
// 40 template years take seconds; the deadline is 20 ms. (Timers need a
// free P to fire, so a run shorter than the scheduler's latency may still
// complete; completion then decides, by design.)
func TestWallTimeoutDuringRun(t *testing.T) {
	in := input(t, golden(t, "default-delivery.json"), 365*24*60, 0, 40, simulation.TraceNone)
	e := &Engine{Workers: 2, timeout: func(simulation.ExecutionLimits) time.Duration { return 20 * time.Millisecond }}
	start := time.Now()
	out, err := e.RunWithTrace(context.Background(), in, nil)
	if !errors.Is(err, ErrWallTimeout) || out != nil {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

// Cancelling while replications run stops them at a checkpoint.
func TestCancelDuringRun(t *testing.T) {
	in := input(t, golden(t, "default-delivery.json"), 365*24*60, 0, 40, simulation.TraceNone)
	ctx, cancel := context.WithCancel(context.Background())
	p := &progressLog{onReport: func(simulation.Progress) { cancel() }}
	out, err := (&Engine{Workers: 2}).RunWithTrace(ctx, in, p)
	if !errors.Is(err, context.Canceled) || out != nil {
		t.Fatalf("err %v", err)
	}
	if n := len(p.reports()); n == 0 || n >= 40 {
		t.Errorf("%d replications reported before the cancel took effect", n)
	}
}

type progressLog struct {
	mu       sync.Mutex
	all      []simulation.Progress
	onReport func(simulation.Progress)
}

// waitFor waits up to a second for a report with completed replications.
func (p *progressLog) waitFor(completed int) bool {
	for range 100 {
		if all := p.reports(); len(all) > 0 && all[len(all)-1].CompletedReplications == completed {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (p *progressLog) Report(pr simulation.Progress) {
	p.mu.Lock()
	p.all = append(p.all, pr)
	p.mu.Unlock()
	if p.onReport != nil {
		p.onReport(pr)
	}
}

func (p *progressLog) reports() []simulation.Progress {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]simulation.Progress(nil), p.all...)
}

// Progress counts finished replications and never goes back; a slow sink
// may skip snapshots, but the last one (all replications) is delivered; it
// is not a correctness input.
func TestProgress(t *testing.T) {
	p := &progressLog{}
	in := input(t, golden(t, "default-delivery.json"), 3*24*60, 0, 10, simulation.TraceNone)
	res, err := (&Engine{Workers: 4}).Run(context.Background(), in, p)
	if err != nil {
		t.Fatal(err)
	}
	if !p.waitFor(10) {
		t.Fatalf("the final snapshot never arrived: %+v", p.reports())
	}
	all := p.reports()
	if len(all) == 0 || len(all) > 10 {
		t.Fatalf("%d reports", len(all))
	}
	for i, r := range all {
		if r.TotalReplications != 10 || r.CompletedReplications < 1 || (i > 0 && (r.CompletedReplications <= all[i-1].CompletedReplications || r.EventsProcessed < all[i-1].EventsProcessed)) || r.EventsProcessed > limits.MaxEvents {
			t.Fatalf("report %d: %+v", i, r)
		}
	}
	quiet, err := (&Engine{Workers: 4}).Run(context.Background(), in, nil)
	if err != nil || !reflect.DeepEqual(quiet, res) {
		t.Errorf("progress changed the result: %v", err)
	}
}

// Every golden model, with the empirical dataset where one is used, gives a
// schema-valid Result.
func TestResultsValidate(t *testing.T) {
	review := simulation.ResolvedDataset{
		Binding: model.DatasetBinding{LogicalRef: "dataset:review_duration", DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40",
			ContentHash: "sha256:" + strings.Repeat("0", 64), Unit: model.Minute, SampleCount: 3},
		Samples: []float64{120, 240, 360},
	}
	for _, name := range []string{"deterministic-queue.json", "congested-queue.json", "default-delivery.json", "report-example.json"} {
		t.Run(name, func(t *testing.T) {
			in := input(t, golden(t, name), 5*24*60, 24*60, 4, simulation.TraceSampled)
			in.Datasets = map[string]simulation.ResolvedDataset{review.Binding.LogicalRef: review}
			in.Manifest = []model.DatasetBinding{review.Binding}
			out := run(t, &Engine{DomainMetrics: dora}, in)
			validResult(t, out.Result)
			if out.Trace != nil {
				schemaValid(t, out.Trace, "simulation.schema.json#/$defs/TraceArtifact")
			}
		})
	}
}

// The engine stays generic and pure: no domain pack, HTTP, DB, object
// storage or LLM code.
func TestDependencies(t *testing.T) {
	contracttest.OnlyDeps(t,
		"github.com/kcl1104/geminus/internal/model",
		"github.com/kcl1104/geminus/internal/model/hashing",
		"github.com/kcl1104/geminus/internal/model/validate",
		"github.com/kcl1104/geminus/internal/results/metrics",
		"github.com/kcl1104/geminus/internal/simulation",
		"github.com/kcl1104/geminus/internal/simulation/engine",
		"github.com/kcl1104/geminus/internal/simulation/trace",
		"github.com/kcl1104/geminus/schemas",
		"github.com/gowebpki/jcs",
		"github.com/santhosh-tekuri/jsonschema/v6",
		"github.com/santhosh-tekuri/jsonschema/v6/kind",
		"golang.org/x/text/feature/plural",
		"golang.org/x/text/internal",
		"golang.org/x/text/internal/catmsg",
		"golang.org/x/text/internal/format",
		"golang.org/x/text/internal/language",
		"golang.org/x/text/internal/language/compact",
		"golang.org/x/text/internal/number",
		"golang.org/x/text/internal/stringset",
		"golang.org/x/text/internal/tag",
		"golang.org/x/text/language",
		"golang.org/x/text/message",
		"golang.org/x/text/message/catalog",
	)
}

// The golden cases' digests are pinned (testdata/simulation/
// engine-golden-digests.json, a regression lock taken from this engine, not
// an oracle): any numerical change shows up here and needs a new
// engine_version.
func TestGoldenDigests(t *testing.T) {
	root := contracttest.Root(t)
	var cases struct {
		Cases []struct {
			ID     string               `json:"id"`
			Model  string               `json:"model"`
			Config simulation.RunConfig `json:"config"`
		} `json:"cases"`
	}
	var lock struct {
		EngineVersion string            `json:"engine_version"`
		Digests       map[string]string `json:"digests"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(root, "testdata/simulation/expected-cases.json")), &cases); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(root, "testdata/simulation/engine-golden-digests.json")), &lock); err != nil {
		t.Fatal(err)
	}
	if lock.EngineVersion != Version || len(lock.Digests) != len(cases.Cases) {
		t.Fatalf("lock for %s with %d digests; engine %s, %d cases", lock.EngineVersion, len(lock.Digests), Version, len(cases.Cases))
	}
	for _, c := range cases.Cases {
		in := input(t, golden(t, filepath.Base(c.Model)), 0, 0, 1, simulation.TraceNone)
		in.Config = c.Config
		if got := run(t, &Engine{DomainMetrics: dora}, in).Result.Reproducibility.ResultDigest; got != lock.Digests[c.ID] {
			t.Errorf("%s: digest %s, locked %s", c.ID, got, lock.Digests[c.ID])
		}
	}
}

// trace_level sampled, but no selected entity arrives (GOLDEN-002's first
// six, by 30 minutes, are all unselected, engine-vectors.json): no
// artifact, and the Result says the trace is unavailable.
func TestSampledButEmptyTrace(t *testing.T) {
	out := run(t, &Engine{}, input(t, golden(t, "congested-queue.json"), 30, 0, 1, simulation.TraceSampled))
	if out.Trace != nil || out.Result.Trace != (simulation.Trace{}) {
		t.Errorf("trace %+v, result trace %+v", out.Trace, out.Result.Trace)
	}
	validResult(t, out.Result)
}

// A deployment's engine_version (build identity appended) is recorded in the
// Result and the trace, and is part of the digest.
func TestDeploymentVersion(t *testing.T) {
	in := input(t, golden(t, "deterministic-queue.json"), 1010, 0, 1, simulation.TraceSampled)
	plain := run(t, &Engine{}, in)
	built := run(t, &Engine{Version: Version + "+0123abc"}, in)
	if built.Result.EngineVersion != Version+"+0123abc" || built.Trace.EngineVersion != Version+"+0123abc" {
		t.Errorf("versions %q %q", built.Result.EngineVersion, built.Trace.EngineVersion)
	}
	if built.Result.Reproducibility.ResultDigest == plain.Result.Reproducibility.ResultDigest {
		t.Error("engine_version is not in the digest")
	}
}

// Any IR the engine accepts gives either a typed failure or a schema-valid
// Result (no NaN, no utilization above 1, no unavailable value 0) whose
// digest recomputes, and a schema-valid trace.
func FuzzRun(f *testing.F) {
	for _, name := range []string{"deterministic-queue.json", "congested-queue.json", "default-delivery.json"} {
		f.Add(contracttest.ReadFile(f, filepath.Join(contracttest.Root(f), "testdata/golden-models", name)), uint8(2), uint16(300), uint16(30))
	}
	f.Fuzz(func(t *testing.T, data []byte, reps uint8, horizon, warmup uint16) {
		var ir model.ModelIR
		if model.Decode(data, &ir) != nil {
			return
		}
		in := input(t, ir, float64(horizon%2000)+1, float64(warmup%2000), int(reps%4)+1, simulation.TraceSampled)
		in.Limits.MaxEvents, in.Limits.MaxActiveEntities, in.Limits.MaxTraceEvents = 30_000, 2_000, 500
		out, err := (&Engine{Workers: 2, DomainMetrics: dora}).RunWithTrace(context.Background(), in, nil)
		if err != nil {
			if out != nil || !(errors.Is(err, engine.ErrInvalidInput) || errors.Is(err, engine.ErrEventBudget) ||
				errors.Is(err, engine.ErrActiveEntities) || errors.Is(err, engine.ErrEntityVisits)) {
				t.Fatalf("untyped failure %v (output %v)", err, out)
			}
			return
		}
		validResult(t, out.Result)
		c := *out.Result
		c.Reproducibility.ResultDigest = ""
		if d, err := hashing.Result(c); err != nil || d != out.Result.Reproducibility.ResultDigest {
			t.Fatalf("digest does not recompute: %v", err)
		}
		if out.Trace != nil {
			schemaValid(t, out.Trace, "simulation.schema.json#/$defs/TraceArtifact")
		}
	})
}

// The event budget is shared by all replications (SIMULATION_SPEC §8):
// three replications that each fit fail together.
func TestSharedBudget(t *testing.T) {
	one := input(t, golden(t, "congested-queue.json"), 205, 0, 1, simulation.TraceNone)
	p, err := engine.Compile(one)
	if err != nil {
		t.Fatal(err)
	}
	out, err := engine.RunReplication(context.Background(), p, 0, engine.NewBudget(1<<40), nil)
	if err != nil {
		t.Fatal(err)
	}
	three := input(t, golden(t, "congested-queue.json"), 205, 0, 3, simulation.TraceNone)
	three.Limits.MaxEvents = out.Events*2 + out.Events/2
	if _, err := (&Engine{Workers: 1}).RunWithTrace(context.Background(), three, nil); !errors.Is(err, engine.ErrEventBudget) {
		t.Errorf("%d events per replication, budget %d for three: %v", out.Events, three.Limits.MaxEvents, err)
	}
	three.Limits.MaxEvents = out.Events * 3
	run(t, &Engine{Workers: 1}, three)
}

// The trace is replication 0's whatever the worker count: equal to a
// recorder on replication 0 alone, and not replication 1's.
func TestTraceIsReplicationZero(t *testing.T) {
	in := input(t, golden(t, "default-delivery.json"), 7*24*60, 0, 5, simulation.TraceSampled)
	p, err := engine.Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	record := func(index uint32) *simulation.TraceArtifact {
		rec := trace.NewRecorder(p, index, in.Limits.MaxTraceEvents)
		if _, err := engine.RunReplication(context.Background(), p, index, engine.NewBudget(1<<40), rec); err != nil {
			t.Fatal(err)
		}
		return rec.Artifact(Version)
	}
	zero, one := record(0), record(1)
	if reflect.DeepEqual(zero.Events, one.Events) {
		t.Fatal("replications 0 and 1 trace the same: the test would be vacuous")
	}
	for _, w := range []int{1, 2, 5} {
		if out := run(t, &Engine{Workers: w}, in); !reflect.DeepEqual(out.Trace, zero) {
			t.Errorf("workers %d: the trace is not replication 0's", w)
		}
	}
}

// Completion decides: a cancellation that lands after every replication has
// finished does not fail the run.
func TestCompletionDecides(t *testing.T) {
	in := input(t, golden(t, "congested-queue.json"), 205, 0, 4, simulation.TraceNone)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{Workers: 2, afterPool: cancel}
	out, err := e.RunWithTrace(ctx, in, nil)
	if err != nil || out == nil || ctx.Err() == nil {
		t.Errorf("cancel after the pool: %v (context %v)", err, ctx.Err())
	}
}

// gatedSink holds its first Report until release is closed; later reports
// return at once. It records concurrent calls and the latest snapshot.
type gatedSink struct {
	release chan struct{}
	held    chan struct{} // closed once the first call is blocked
	once    sync.Once
	inside  atomic.Int32
	calls   atomic.Int32
	last    atomic.Int64 // completed replications in the latest report
	t       *testing.T
}

func newGatedSink(t *testing.T) *gatedSink {
	return &gatedSink{release: make(chan struct{}), held: make(chan struct{}), t: t}
}

func (s *gatedSink) Report(p simulation.Progress) {
	if s.inside.Add(1) > 1 {
		s.t.Error("Report called concurrently")
	}
	if s.calls.Add(1) == 1 {
		s.once.Do(func() { close(s.held) })
		<-s.release
	}
	s.last.Store(int64(p.CompletedReplications))
	s.inside.Add(-1)
}

// within fails the test if f does not return in d: a run that waits for a
// sink held until after Run would never return.
func within[T any](t *testing.T, d time.Duration, f func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- f() }()
	select {
	case v := <-done:
		return v
	case <-time.After(d):
		t.Fatalf("no return within %v: the run waits for its progress sink", d)
		panic("unreachable")
	}
}

// waitUntil polls cond for up to d.
func waitUntil(d time.Duration, cond func() bool) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

type result struct {
	res *simulation.Result
	err error
}

// Progress is never a correctness input (SIMULATION_SPEC §8, T-022 review):
// a sink held for as long as the test likes neither keeps a run past its
// deadline nor changes whether, or how, the run completes; the latest
// snapshot still arrives afterwards, and the reporter goroutine then exits.
// Deterministic: the sink is released only after Run has returned.
func TestSlowProgressSinkChangesNothing(t *testing.T) {
	in := input(t, golden(t, "congested-queue.json"), 205, 0, 4, simulation.TraceNone)
	expired := &Engine{Workers: 4, timeout: func(simulation.ExecutionLimits) time.Duration { return 0 }}
	sink := newGatedSink(t)
	r := within(t, 30*time.Second, func() result {
		res, err := expired.Run(context.Background(), in, sink)
		return result{res, err}
	})
	close(sink.release)
	if !errors.Is(r.err, ErrWallTimeout) || r.res != nil {
		t.Errorf("expired deadline with a held sink: %v", r.err)
	}

	in = input(t, golden(t, "default-delivery.json"), 30*24*60, 0, 8, simulation.TraceNone)
	quiet := run(t, &Engine{Workers: 4}, in)
	before := runtime.NumGoroutine()
	sink = newGatedSink(t)
	r = within(t, 30*time.Second, func() result {
		res, err := (&Engine{Workers: 4}).Run(context.Background(), in, sink)
		return result{res, err}
	})
	if r.err != nil || !reflect.DeepEqual(r.res, quiet.Result) {
		t.Fatalf("a held sink changed the run: %v", r.err)
	}
	<-sink.held
	if n := sink.calls.Load(); n != 1 {
		t.Fatalf("%d reports reached a sink that was still held", n)
	}
	close(sink.release)
	// Every report made while the sink was held collapses into the latest
	// one: at most one more call (none if the held call already carried the
	// final snapshot), and it has all 8 replications.
	if !waitUntil(10*time.Second, func() bool { return sink.last.Load() == 8 }) || sink.calls.Load() > 2 {
		t.Errorf("latest report %d after %d calls; want 8 after at most 2", sink.last.Load(), sink.calls.Load())
	}
	if !waitUntil(10*time.Second, func() bool { return runtime.NumGoroutine() <= before }) {
		t.Errorf("goroutines %d after the run, %d before: the reporter did not exit", runtime.NumGoroutine(), before)
	}
}

type panickingSink struct{ calls atomic.Int32 }

func (s *panickingSink) Report(simulation.Progress) {
	s.calls.Add(1)
	panic("sink bug")
}

// A sink that panics stops receiving reports; the run and the process are
// unaffected (T-022 re-review). A DomainMetrics panic fails the run with
// ErrInternal instead of killing the worker.
func TestPanicsOutsideTheReplications(t *testing.T) {
	in := input(t, golden(t, "congested-queue.json"), 205, 0, 6, simulation.TraceNone)
	quiet := run(t, &Engine{Workers: 2}, in)
	before := runtime.NumGoroutine()
	sink := &panickingSink{}
	res, err := (&Engine{Workers: 2}).Run(context.Background(), in, sink)
	if err != nil || !reflect.DeepEqual(res, quiet.Result) {
		t.Fatalf("a panicking sink changed the run: %v", err)
	}
	if !waitUntil(10*time.Second, func() bool { return runtime.NumGoroutine() <= before }) || sink.calls.Load() != 1 {
		t.Errorf("after a sink panic: %d calls, goroutines %d (before %d)", sink.calls.Load(), runtime.NumGoroutine(), before)
	}
	bad := &Engine{DomainMetrics: func(*simulation.Result) map[string]simulation.MetricSummary { panic("pack bug") }}
	if res, err := bad.Run(context.Background(), in, nil); !errors.Is(err, engine.ErrInternal) || res != nil {
		t.Errorf("DomainMetrics panic: %v", err)
	}
}

// A queue with no visit in the window is unavailable with a reason and null
// waits, and the Result stays schema-valid (GOLDEN-002, window [150, 151]:
// the last arrival queues at 100).
func TestUnavailableQueue(t *testing.T) {
	out := run(t, &Engine{DomainMetrics: dora}, input(t, golden(t, "congested-queue.json"), 151, 150, 2, simulation.TraceNone))
	q := out.Result.Metrics.Queues["service"]
	if q.Status != simulation.Unavailable || q.Reason == nil || q.WaitMeanMinutes != nil || q.WaitP50Minutes != nil || q.WaitP95Minutes != nil || q.SampleCount != 0 || q.QueueLengthMean <= 0 {
		t.Errorf("queue %+v", q)
	}
	validResult(t, out.Result)
}

// A panic inside a replication (an engine bug) fails the run with
// ErrInternal instead of killing the worker process (T-022 review).
func TestReplicationPanicIsAnError(t *testing.T) {
	in := input(t, golden(t, "congested-queue.json"), 205, 0, 4, simulation.TraceNone)
	e := &Engine{Workers: 2, onReplication: func(i int) {
		if i == 2 {
			panic("injected")
		}
	}}
	out, err := e.RunWithTrace(context.Background(), in, nil)
	if !errors.Is(err, engine.ErrInternal) || out != nil || !strings.Contains(err.Error(), "injected") {
		t.Errorf("err %v, out %v", err, out)
	}
}

// queue_enter names the node the entity actually entered (T-022 review): an
// entity routed straight into a process that also has an explicit queue
// upstream is traced at the process, one that came through the queue at the
// queue; the queue metrics still belong to the explicit queue.
func TestTraceNamesTheEnteredNode(t *testing.T) {
	ir := golden(t, "deterministic-queue.json")
	ir.Nodes = append(ir.Nodes, model.Node{ID: "inbox", Type: model.NodeQueue},
		model.Node{ID: "other", Type: model.NodeSource, EntityType: ir.Nodes[0].EntityType, Arrival: &model.Arrival{Distribution: ir.Nodes[0].Arrival.Distribution, MaxEntities: 100}})
	ir.Flows = append(ir.Flows,
		model.Flow{ID: "inbox_service", From: "inbox", To: "service", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}},
		model.Flow{ID: "other_inbox", From: "other", To: "inbox", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}})
	out := run(t, &Engine{}, input(t, ir, 1010, 0, 1, simulation.TraceSampled))
	seen := map[string]bool{}
	for _, ev := range out.Trace.Events {
		if ev.Type != simulation.TraceQueueEnter {
			continue
		}
		source, _, _ := strings.Cut(strings.TrimPrefix(ev.EntityID, "r0/"), "/")
		want := map[string]string{"arrival": "service", "other": "inbox"}[source]
		if ev.NodeID != want {
			t.Errorf("%s queued at %s, entered %s", ev.EntityID, ev.NodeID, want)
		}
		seen[source] = true
	}
	if !seen["arrival"] || !seen["other"] {
		t.Fatalf("no sampled entity of both sources: %v", seen)
	}
	if _, ok := out.Result.Metrics.Queues["inbox"]; !ok || len(out.Result.Metrics.Queues) != 1 {
		t.Errorf("queues %v", out.Result.Metrics.Queues)
	}
}

// The engine recomputes effective_model_hash as hashing.Model over
// model.Encode of the decoded IR; the run API hashes the stored IR bytes.
// They must agree for every valid IR, or valid runs would be refused
// (T-022 review): checked on every golden model and valid IR fixture.
func TestEffectiveHashMatchesStoredBytes(t *testing.T) {
	root := contracttest.Root(t)
	var paths []string
	for _, dir := range []string{"testdata/golden-models/*.json", "testdata/contracts/valid-*ir.json"} {
		m, err := filepath.Glob(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, m...)
	}
	if len(paths) < 8 {
		t.Fatalf("only %d IR files", len(paths))
	}
	for _, path := range paths {
		raw := contracttest.ReadFile(t, path)
		var ir model.ModelIR
		if err := model.Decode(raw, &ir); err != nil {
			t.Fatalf("%s: %v", filepath.Base(path), err)
		}
		data, err := model.Encode(&ir)
		if err != nil {
			t.Fatal(err)
		}
		stored, err1 := hashing.Model(raw)
		engineSide, err2 := hashing.Model(data)
		if err1 != nil || err2 != nil || stored != engineSide {
			t.Errorf("%s: stored bytes %s, engine %s (%v %v)", filepath.Base(path), stored, engineSide, err1, err2)
		}
	}
}
