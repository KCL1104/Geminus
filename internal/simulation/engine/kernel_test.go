package engine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// recorder keeps every observation and checks the kernel's invariants as
// they happen: an entity is in exactly one state, resources stay within
// [0, capacity], time never goes back (seq can, across priorities in a
// tick), and a service starts
// only after its entity queued.
type recorder struct {
	t      *testing.T
	p      *Program
	all    []Observation
	state  map[uint64]ObservationKind
	inUse  map[int]int64
	queued map[uint64]int
	byNode map[int]int64 // arrivals per source
	last   Observation
}

func newRecorder(t *testing.T, p *Program) *recorder {
	return &recorder{t: t, p: p, state: map[uint64]ObservationKind{}, inUse: map[int]int64{}, queued: map[uint64]int{}, byNode: map[int]int64{}}
}

func (r *recorder) Observe(o Observation) {
	r.t.Helper()
	if len(r.all) > 0 && o.Tick < r.last.Tick {
		r.t.Fatalf("time went back: %+v after %+v", o, r.last)
	}
	r.last = o
	r.all = append(r.all, o)
	switch o.Kind {
	case ObsResource:
		if o.Value < 0 || o.Value > int64(r.p.resources[o.Resource].capacity) {
			r.t.Fatalf("resource %s in use %d of %d", r.p.resources[o.Resource].id, o.Value, r.p.resources[o.Resource].capacity)
		}
		r.inUse[o.Resource] = o.Value
		return
	case ObsResourceDown, ObsResourceUp, ObsBreakdownPending:
		return // resource events (D-051), not an entity's
	case ObsArrival:
		if _, seen := r.state[o.Entity]; seen {
			r.t.Fatalf("entity %d arrived twice", o.Entity)
		}
		if r.byNode[o.Node]++; o.Value != r.byNode[o.Node] {
			r.t.Fatalf("arrival %d at %s reported as number %d", r.byNode[o.Node], r.p.nodes[o.Node].id, o.Value)
		}
	case ObsQueueEnter:
		r.queued[o.Entity] = o.Node
	case ObsServiceStart:
		if r.state[o.Entity] != ObsQueueEnter || r.queued[o.Entity] != o.Node {
			r.t.Fatalf("entity %d started service at %d without queueing there (state %d)", o.Entity, o.Node, r.state[o.Entity])
		}
	case ObsServiceEnd:
		if r.state[o.Entity] != ObsServiceStart {
			r.t.Fatalf("entity %d ended a service it was not in", o.Entity)
		}
	case ObsDelayEnd:
		if r.state[o.Entity] != ObsDelayStart {
			r.t.Fatalf("entity %d left a delay it was not in", o.Entity)
		}
	}
	if r.state[o.Entity] == ObsComplete {
		r.t.Fatalf("entity %d acted after completing: %+v", o.Entity, o)
	}
	r.state[o.Entity] = o.Kind
}

func (r *recorder) at(kind ObservationKind, node string) []Observation {
	var out []Observation
	for _, o := range r.all {
		if o.Kind == kind && (node == "" || r.p.nodes[o.Node].id == node) {
			out = append(out, o)
		}
	}
	return out
}

func run(t *testing.T, in simulation.RunInput, index uint32) (*recorder, ReplicationOutcome) {
	t.Helper()
	p := compile(t, in)
	rec := newRecorder(t, p)
	out, err := RunReplication(context.Background(), p, index, NewBudget(10_000_000), rec)
	if err != nil {
		t.Fatalf("RunReplication: %v", err)
	}
	if out.Created != out.Exited+out.Censored {
		t.Fatalf("created %d != exited %d + censored %d", out.Created, out.Exited, out.Censored)
	}
	return rec, out
}

// quantile7 is the Type 7 sample quantile (SIMULATION_SPEC §5).
func quantile7(x []float64, p float64) float64 {
	s := slices.Clone(x)
	slices.Sort(s)
	h := float64(len(s)-1) * p
	lo, hi := math.Floor(h), math.Ceil(h)
	return s[int(lo)] + (h-lo)*(s[int(hi)]-s[int(lo)])
}

func mean(x []float64) float64 {
	sum := 0.0
	for _, v := range x {
		sum += v
	}
	return sum / float64(len(x))
}

// utilization integrates in-use units over [0, horizon] / (capacity*horizon).
func (r *recorder) utilization(resource string, horizon int64) float64 {
	idx := slices.IndexFunc(r.p.resources, func(c cresource) bool { return c.id == resource })
	var area, last, level int64
	for _, o := range r.all {
		if o.Kind == ObsResource && o.Resource == idx {
			area += level * (o.Tick - last)
			last, level = o.Tick, o.Value
		}
	}
	area += level * (horizon - last)
	return float64(area) / float64(int64(r.p.resources[idx].capacity)*horizon)
}

// GOLDEN-001 and GOLDEN-002 against their hand-derived expectations
// (testdata/simulation/expected-cases.json, HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN).
func TestGoldenCases(t *testing.T) {
	var cases struct {
		Cases []struct {
			ID       string               `json:"id"`
			Model    string               `json:"model"`
			Config   simulation.RunConfig `json:"config"`
			Expected map[string]float64   `json:"expected"`
		} `json:"cases"`
	}
	root := contracttest.Root(t)
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(root, "testdata/simulation/expected-cases.json")), &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases.Cases {
		t.Run(c.ID, func(t *testing.T) {
			in := input(golden(t, filepath.Base(c.Model)))
			in.Config = c.Config
			rec, out := run(t, in, 0)
			arrivedAt := map[uint64]int64{}
			for _, o := range rec.at(ObsArrival, "") {
				arrivedAt[o.Entity] = o.Tick
			}
			queuedAt := map[uint64]int64{}
			for _, o := range rec.at(ObsQueueEnter, "") {
				queuedAt[o.Entity] = o.Tick
			}
			var waits, leads []float64
			for _, o := range rec.at(ObsServiceStart, "") {
				waits = append(waits, float64(o.Tick-queuedAt[o.Entity])/60_000)
			}
			done := rec.at(ObsComplete, "")
			for _, o := range done {
				leads = append(leads, float64(o.Tick-arrivedAt[o.Entity])/60_000)
			}
			got := map[string]float64{
				"completed_count":         float64(out.Exited),
				"queue_wait_mean_minutes": mean(waits), "queue_wait_p50_minutes": quantile7(waits, 0.5), "queue_wait_p95_minutes": quantile7(waits, 0.95),
				"lead_time_mean_minutes": mean(leads), "lead_time_p50_minutes": quantile7(leads, 0.5), "lead_time_p95_minutes": quantile7(leads, 0.95),
				"utilization":       rec.utilization("server", compile(t, in).HorizonTicks()),
				"censored_entities": float64(out.Censored),
			}
			if len(done) > 0 {
				got["first_arrival_minutes"] = float64(rec.at(ObsArrival, "")[0].Tick) / 60_000
				got["last_completion_minutes"] = float64(done[len(done)-1].Tick) / 60_000
			}
			for k, want := range c.Expected {
				if g, ok := got[k]; !ok || math.Abs(g-want) > 1e-12*math.Max(1, math.Abs(want)) {
					t.Errorf("%s = %v, hand-derived %v", k, got[k], want)
				}
			}
		})
	}
}

// constant returns a constant distribution.
func constant(v float64) model.Distribution {
	return model.Distribution{Type: model.DistConstant, Value: &v}
}

func flow(id, from, to string, p float64) model.Flow {
	return model.Flow{ID: id, From: from, To: to, Routing: model.Routing{Type: model.RoutingProbability, Probability: p}}
}

func process(id string, service float64, resources ...string) model.Node {
	n := model.Node{ID: id, Type: model.NodeProcess, ServiceTime: &model.TimedDistribution{Distribution: constant(service)}, Requires: []model.ResourceRequirement{}}
	for _, r := range resources {
		n.Requires = append(n.Requires, model.ResourceRequirement{ResourceID: r, Units: 1})
	}
	return n
}

func source(id string, every float64, max int) model.Node {
	return model.Node{ID: id, Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: constant(every), MaxEntities: max}}
}

func build(t *testing.T, nodes []model.Node, flows []model.Flow, resources ...string) model.ModelIR {
	t.Helper()
	ir := minimal(t)
	ir.Nodes, ir.Flows = nodes, flows
	ir.Resources = nil
	for _, r := range resources {
		ir.Resources = append(ir.Resources, model.Resource{ID: r, Type: model.ResourceMachine, Name: r, Capacity: 1, QueueDiscipline: model.FIFO})
	}
	return ir
}

func starts(rec *recorder) map[string]int64 {
	out := map[string]int64{}
	for _, o := range rec.at(ObsServiceStart, "") {
		out[rec.p.nodes[o.Node].id] = o.Tick / 60_000
	}
	return out
}

// Bundles are all-or-nothing and FIFO is per resource: P waits for R1+R2
// without taking R1; Q (R1 only) queued after P is held back because it
// shares R1 with the earlier waiting P; Z (R3) shares nothing and proceeds.
func TestBundleAndResourceWiseFIFO(t *testing.T) {
	sink := model.Node{ID: "done", Type: model.NodeSink}
	ir := build(t, []model.Node{
		source("s_hold", 1, 1), process("H", 100, "R2"),
		source("s_p", 2, 1), process("P", 5, "R1", "R2"),
		source("s_q", 3, 1), process("Q", 5, "R1"),
		source("s_z", 4, 1), process("Z", 5, "R3"), sink,
	}, []model.Flow{
		flow("f1", "s_hold", "H", 1), flow("f2", "H", "done", 1), flow("f3", "s_p", "P", 1), flow("f4", "P", "done", 1),
		flow("f5", "s_q", "Q", 1), flow("f6", "Q", "done", 1), flow("f7", "s_z", "Z", 1), flow("f8", "Z", "done", 1),
	}, "R1", "R2", "R3")
	rec, _ := run(t, input(ir), 0)
	if got, want := starts(rec), map[string]int64{"H": 1, "P": 101, "Q": 106, "Z": 4}; !mapsEqual(got, want) {
		t.Errorf("service starts %v, want %v", got, want)
	}
	r1 := slices.IndexFunc(rec.p.resources, func(c cresource) bool { return c.id == "R1" })
	for _, o := range rec.at(ObsResource, "") {
		if o.Resource == r1 && o.Tick < 101*60_000 {
			t.Fatalf("R1 taken at %d while P was waiting: a partial acquire", o.Tick/60_000)
		}
	}
}

func mapsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// D-032: a service completion (priority 0) releases its resource and routes
// the entity into the next queue before a same-tick arrival (priority 10)
// queues, so the earlier request wins the resource at the dispatch.
func TestSameTickReleaseBeforeArrival(t *testing.T) {
	ir := build(t, []model.Node{
		source("s_first", 10, 1), process("A", 10, "R"), process("B", 50, "R"),
		source("s_second", 20, 1), process("C", 5, "R"),
		{ID: "done", Type: model.NodeSink},
	}, []model.Flow{
		flow("f1", "s_first", "A", 1), flow("f2", "A", "B", 1), flow("f3", "B", "done", 1),
		flow("f4", "s_second", "C", 1), flow("f5", "C", "done", 1),
	}, "R")
	rec, _ := run(t, input(ir), 0)
	// A: 10–20. At 20 the A entity enters B's buffer (in the completion
	// event) before the s_second arrival enters C's: B starts at 20, C waits.
	if got, want := starts(rec), map[string]int64{"A": 10, "B": 20, "C": 70}; !mapsEqual(got, want) {
		t.Errorf("service starts %v, want %v", got, want)
	}
}

// 100%/0% routing, a fixed-seed split, delay, failure/rework observations,
// abandoned sinks and a process without resources.
func TestRoutingDelayObservations(t *testing.T) {
	split := 0.3
	ir := build(t, []model.Node{
		{ID: "src", Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: model.Distribution{Type: model.DistExponential, Mean: ptr(1.0)}, MaxEntities: 20000}},
		{ID: "d", Type: model.NodeDecision},
		{ID: "wait", Type: model.NodeDelay, DelayTime: &model.TimedDistribution{Distribution: constant(7)}},
		{ID: "fail", Type: model.NodeFailure},
		{ID: "redo", Type: model.NodeRework},
		process("free", 0.5),
		{ID: "never", Type: model.NodeSink, Outcome: model.SinkCompleted},
		{ID: "gone", Type: model.NodeSink, Outcome: model.SinkAbandoned},
		{ID: "ok", Type: model.NodeSink},
	}, []model.Flow{
		flow("a_src", "src", "d", 1),
		flow("b_never", "d", "never", 0), flow("c_fail", "d", "fail", split), flow("d_wait", "d", "wait", 1-split),
		flow("e_fail", "fail", "redo", 1), flow("f_redo", "redo", "free", 1), flow("g_free", "free", "gone", 1),
		flow("h_wait", "wait", "ok", 1),
	})
	in := input(ir)
	in.Config = config(365, model.Day)
	rec, out := run(t, in, 0)
	if n := len(rec.at(ObsComplete, "never")); n != 0 {
		t.Errorf("%d entities took the 0%% flow", n)
	}
	failed, delayed := len(rec.at(ObsFailure, "fail")), len(rec.at(ObsDelayStart, "wait"))
	if failed+delayed != 20000 || len(rec.at(ObsRework, "redo")) != failed || out.Exited != 20000 {
		t.Fatalf("failed %d, delayed %d, completed %d", failed, delayed, out.Exited)
	}
	if share := float64(failed) / 20000; math.Abs(share-split) > 0.015 {
		t.Errorf("failure share %.4f, configured %.2f", share, split)
	}
	for _, o := range rec.at(ObsComplete, "gone") {
		if o.Value != 0 {
			t.Fatal("abandoned sink reported as completed")
		}
	}
	for _, o := range rec.at(ObsComplete, "ok") {
		if o.Value != 1 {
			t.Fatal("completed sink reported as abandoned")
		}
	}
	// The delay takes exactly 7 minutes; the resource-free process starts in
	// the tick its entity queues.
	delayStart := map[uint64]int64{}
	for _, o := range rec.at(ObsDelayStart, "wait") {
		delayStart[o.Entity] = o.Tick
	}
	for _, o := range rec.at(ObsDelayEnd, "wait") {
		if o.Tick-delayStart[o.Entity] != 7*60_000 {
			t.Fatalf("delay of %d ticks", o.Tick-delayStart[o.Entity])
		}
	}
	queued := map[uint64]int64{}
	for _, o := range rec.at(ObsQueueEnter, "free") {
		queued[o.Entity] = o.Tick
	}
	for _, o := range rec.at(ObsServiceStart, "free") {
		if o.Tick != queued[o.Entity] {
			t.Fatal("a process without resources waited")
		}
	}
}

// Entities still in the system at the horizon are censored, not completed.
func TestCensoring(t *testing.T) {
	in := input(golden(t, "congested-queue.json"))
	in.Config = config(100, model.Minute)
	_, out := run(t, in, 0)
	// Arrivals at 5..100 (20), service 10 each from t=5: completions at 15, 25, ..., 95 (9).
	if out.Created != 20 || out.Exited != 9 || out.Censored != 11 {
		t.Errorf("created %d completed %d censored %d", out.Created, out.Exited, out.Censored)
	}
}

// The active-entity and visit limits fail the replication with typed errors.
func TestKernelLimits(t *testing.T) {
	in := input(golden(t, "congested-queue.json"))
	in.Limits.MaxActiveEntities = 5
	p := compile(t, in)
	if _, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), nil); !errors.Is(err, ErrActiveEntities) {
		t.Errorf("active entities: %v", err)
	}
	ir := build(t, []model.Node{
		source("src", 1, 1), process("work", 1), {ID: "d", Type: model.NodeDecision}, {ID: "end", Type: model.NodeSink},
	}, []model.Flow{flow("f1", "src", "work", 1), flow("f2", "work", "d", 1), flow("f3", "d", "work", 0.999999), flow("f4", "d", "end", 0.000001)}) // the first 30 draws of this stream all loop (scripts/engine_reference.py)
	in = input(ir)
	in.Config = config(365, model.Day)
	in.Limits.MaxEntityVisits = 50
	p = compile(t, in)
	if _, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), nil); !errors.Is(err, ErrEntityVisits) {
		t.Errorf("entity visits: %v", err)
	}
}

// The default template runs under the invariant checks, and the same
// replication gives the same observations every time, also in parallel.
func TestKernelDeterminism(t *testing.T) {
	in := input(golden(t, "default-delivery.json"))
	in.Config = config(14, model.Day)
	in.Config.Replications = 3
	first, out := run(t, in, 2)
	if out.Created < 300 || out.Exited == 0 {
		t.Fatalf("template run: %+v", out)
	}
	p := compile(t, in)
	var wg sync.WaitGroup
	results := make([][]Observation, 6)
	for i := range results {
		wg.Go(func() {
			rec := &collect{}
			if _, err := RunReplication(context.Background(), p, 2, NewBudget(10_000_000), rec); err != nil {
				t.Error(err)
			}
			results[i] = rec.all
		})
	}
	wg.Wait()
	for i, r := range results {
		if !slices.Equal(r, first.all) {
			t.Fatalf("parallel run %d differs", i)
		}
	}
}

type collect struct{ all []Observation }

func (c *collect) Observe(o Observation) { c.all = append(c.all, o) }

// FuzzKernel: any IR that compiles runs without panics or broken invariants.
func FuzzKernel(f *testing.F) {
	for _, name := range []string{"deterministic-queue.json", "congested-queue.json", "default-delivery.json", "report-example.json"} {
		f.Add(contracttest.ReadFile(f, filepath.Join(contracttest.Root(f), "testdata/golden-models", name)), uint32(0))
	}
	// T-047: models with finite buffers (blocking-goldens.json).
	for _, m := range blockingSeeds(f) {
		f.Add([]byte(m), uint32(0))
	}
	// T-048: models with breakdowns (breakdown-goldens.json).
	for _, m := range breakdownSeeds(f) {
		f.Add([]byte(m), uint32(0))
	}
	// T-049: models with calendars and release schedules (calendar-goldens.json).
	for _, m := range calendarSeeds(f) {
		f.Add([]byte(m), uint32(0))
	}
	f.Fuzz(func(t *testing.T, data []byte, index uint32) {
		var ir model.ModelIR
		if model.Decode(data, &ir) != nil {
			return
		}
		in := input(ir)
		in.Limits.MaxActiveEntities = 2000
		in.Config.Replications = 1000
		p, err := Compile(in)
		if err != nil {
			return
		}
		rec := newRecorder(t, p)
		out, err := RunReplication(context.Background(), p, index%1000, NewBudget(20000), rec)
		if err != nil && !errors.Is(err, ErrEventBudget) && !errors.Is(err, ErrActiveEntities) && !errors.Is(err, ErrEntityVisits) {
			t.Fatalf("run: %v", err)
		}
		if err == nil && out.Created != out.Exited+out.Censored {
			t.Fatalf("entity accounting %+v", out)
		}
	})
}

// D-032: a delay completion (priority 10) comes after a same-tick service
// completion (priority 0), even though the delay was scheduled first.
func TestDelayCompletesAfterServiceInATick(t *testing.T) {
	delay := model.Node{ID: "wait", Type: model.NodeDelay, DelayTime: &model.TimedDistribution{Distribution: constant(10)}}
	ir := build(t, []model.Node{
		source("a", 10, 1), process("pre", 10), source("b", 10, 1), delay, process("X", 30, "R"),
		{ID: "done", Type: model.NodeSink},
	}, []model.Flow{
		flow("f1", "a", "pre", 1), flow("f2", "pre", "X", 1), flow("f3", "b", "wait", 1), flow("f4", "wait", "X", 1), flow("f5", "X", "done", 1),
	}, "R")
	rec, _ := run(t, input(ir), 0)
	var order []string
	for _, o := range rec.at(ObsServiceStart, "X") {
		order = append(order, rec.r(o.Entity))
	}
	if !slices.Equal(order, []string{"a", "b"}) {
		t.Errorf("X served %v; the service completion must queue before the delay completion", order)
	}
}

// r names the source of an entity (test helper).
func (r *recorder) r(e uint64) string {
	for _, o := range r.all {
		if o.Kind == ObsArrival && o.Entity == e {
			return r.p.nodes[o.Node].id
		}
	}
	return ""
}

// Each kind of draw comes from its own (node, purpose) stream, in event
// order: recomputing service, delay and routing draws from fresh streams
// gives exactly what the kernel did.
func TestStreamsUsedByPurpose(t *testing.T) {
	ir := build(t, []model.Node{
		{ID: "src", Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: model.Distribution{Type: model.DistExponential, Mean: ptr(5.0)}, MaxEntities: 300}},
		{ID: "work", Type: model.NodeProcess, ServiceTime: &model.TimedDistribution{Distribution: model.Distribution{Type: model.DistTriangular, Min: ptr(1.0), Mode: ptr(3.0), Max: ptr(9.0)}},
			Requires: []model.ResourceRequirement{{ResourceID: "R", Units: 1}}},
		{ID: "d", Type: model.NodeDecision},
		{ID: "wait", Type: model.NodeDelay, DelayTime: &model.TimedDistribution{Distribution: model.Distribution{Type: model.DistExponential, Mean: ptr(2.0)}}},
		{ID: "done", Type: model.NodeSink},
	}, []model.Flow{flow("f1", "src", "work", 1), flow("f2", "work", "d", 1), flow("f3", "d", "wait", 0.4), flow("f4", "d", "done", 0.6), flow("f5", "wait", "done", 1)}, "R")
	in := input(ir)
	in.Config = config(365, model.Day)
	in.Config.Replications = 6
	rec, _ := run(t, in, 5)
	p := rec.p
	rep := ReplicationSeed(p.seed, 5)
	check := func(node, purpose string, s *sampler, got []int64) {
		t.Helper()
		st := Stream(rep, node, purpose)
		for i, g := range got {
			if want := s.draw(st); g != want {
				t.Fatalf("%s %s draw %d: %d, stream gives %d", node, purpose, i, g, want)
			}
		}
	}
	durations := func(start, end ObservationKind, node string) []int64 {
		began := map[uint64]int64{}
		for _, o := range rec.at(start, node) {
			began[o.Entity] = o.Tick
		}
		var out []int64
		for _, o := range rec.at(start, node) {
			for _, e := range rec.at(end, node) {
				if e.Entity == o.Entity {
					out = append(out, e.Tick-began[o.Entity])
				}
			}
		}
		return out
	}
	check("work", PurposeService, p.nodes[p.index["work"]].service, durations(ObsServiceStart, ObsServiceEnd, "work"))
	check("wait", PurposeDelay, p.nodes[p.index["wait"]].delay, durations(ObsDelayStart, ObsDelayEnd, "wait"))
	// Routing: entities leave work in service-end order; each took the
	// first route with U < cumulative.
	st, d := Stream(rep, "d", PurposeRouting), p.nodes[p.index["d"]]
	delayed := map[uint64]bool{}
	for _, o := range rec.at(ObsDelayStart, "wait") {
		delayed[o.Entity] = true
	}
	for i, o := range rec.at(ObsServiceEnd, "work") {
		toWait := st.Uniform() < d.routes[0].cumulative && p.nodes[d.routes[0].to].id == "wait"
		if toWait != delayed[o.Entity] {
			t.Fatalf("routing draw %d disagrees with the kernel", i)
		}
	}
	if len(rec.at(ObsDelayStart, "wait")) == 0 || len(rec.at(ObsServiceEnd, "work")) < 250 {
		t.Fatal("fixture too small")
	}
}

// Several requests granted in one dispatch start in request order, so the
// i-th grant takes the i-th service draw.
func TestGrantsStartInRequestOrder(t *testing.T) {
	work := model.Node{ID: "work", Type: model.NodeProcess, ServiceTime: &model.TimedDistribution{Distribution: model.Distribution{Type: model.DistExponential, Mean: ptr(4.0)}},
		Requires: []model.ResourceRequirement{{ResourceID: "R", Units: 1}}}
	ir := build(t, []model.Node{source("a", 10, 1), source("b", 10, 1), source("c", 10, 1), work, {ID: "done", Type: model.NodeSink}},
		[]model.Flow{flow("f1", "a", "work", 1), flow("f2", "b", "work", 1), flow("f3", "c", "work", 1), flow("f4", "work", "done", 1)}, "R")
	ir.Resources[0].Capacity = 3
	rec, _ := run(t, input(ir), 0)
	st := Stream(ReplicationSeed(rec.p.seed, 0), "work", PurposeService)
	s := rec.p.nodes[rec.p.index["work"]].service
	starts := rec.at(ObsServiceStart, "work")
	if len(starts) != 3 || starts[0].Tick != starts[2].Tick {
		t.Fatalf("starts %v", starts)
	}
	for i, o := range starts {
		var end int64
		for _, e := range rec.at(ObsServiceEnd, "work") {
			if e.Entity == o.Entity {
				end = e.Tick
			}
		}
		if src, want := rec.r(o.Entity), []string{"a", "b", "c"}[i]; src != want || end-o.Tick != s.draw(st) {
			t.Fatalf("grant %d went to %s (want %s) or took the wrong draw", i, src, want)
		}
	}
}

// Entity records are released at the sink, so a long run keeps only its
// active entities (the T-020 review measured 604 MB for 10M records kept
// forever). 300,000 entities, one a second, each through a 1 ms delay, are
// never more than one at a time, and RunReplication checks that no record
// outlives its entity.
func TestEntityRecordsAreReleased(t *testing.T) {
	ir := build(t, []model.Node{
		{ID: "src", Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: constant(1)}},
		{ID: "hold", Type: model.NodeDelay, DelayTime: &model.TimedDistribution{Distribution: constant(0.001)}},
		{ID: "done", Type: model.NodeSink},
	}, []model.Flow{flow("a", "src", "hold", 1), flow("b", "hold", "done", 1)})
	ir.TimeUnit = model.ModelTimeUnit(model.Second)
	in := input(ir)
	in.Config = config(300_000, model.Second)
	p := compile(t, in)
	out, err := RunReplication(context.Background(), p, 0, NewBudget(10_000_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	// The last arrival, at the horizon, is still in its delay: censored.
	if out.Created != 300_000 || out.Exited != 299_999 || out.Censored != 1 || out.PeakActive != 1 {
		t.Errorf("created %d exited %d censored %d peak %d", out.Created, out.Exited, out.Censored, out.PeakActive)
	}
}

// A decision whose cumulative probabilities stop short of 1 (the validator
// allows a small tolerance) sends the remaining U to its last route; a
// non-decision node never draws, so a p = 1-1e-9 flow always carries its
// entity (T-020 review).
func TestRoutingTolerance(t *testing.T) {
	ir := build(t, []model.Node{
		source("src", 1, 4000),
		{ID: "d", Type: model.NodeDecision},
		{ID: "x", Type: model.NodeSink}, {ID: "y", Type: model.NodeSink},
	}, []model.Flow{flow("a", "src", "d", 1-1e-9), flow("dx", "d", "x", 0.5), flow("dy", "d", "y", 0.5)})
	in := input(ir)
	in.Config = config(10_000, model.Minute)
	p := compile(t, in)
	if c := p.nodes[p.index["src"]].routes[0].cumulative; c != 1-1e-9 {
		t.Fatalf("source route cumulative %v", c)
	}
	d := &p.nodes[p.index["d"]]
	d.routes[0].cumulative, d.routes[1].cumulative = 0.25, 0.5 // as if the probabilities summed to 0.5
	rec := newRecorder(t, p)
	out, err := RunReplication(context.Background(), p, 0, NewBudget(10_000_000), rec)
	if err != nil || out.Exited != 4000 {
		t.Fatalf("%v, exited %d", err, out.Exited)
	}
	if x := len(rec.at(ObsComplete, "x")); math.Abs(float64(x)/4000-0.25) > 0.03 {
		t.Errorf("%d of 4000 to x; the first route covers U < 0.25 only", x)
	}
}

// The limits are exact. The source counts as the first visit, so
// src -> a -> b -> end is 4 visits and passes max_entity_visits = 4 but not
// 3; 5 entities in the system at once pass max_active_entities = 5, not 4.
func TestKernelLimitsAreExact(t *testing.T) {
	ir := build(t, []model.Node{
		source("src", 1, 3), {ID: "a", Type: model.NodeDecision}, {ID: "b", Type: model.NodeDecision}, {ID: "end", Type: model.NodeSink},
	}, []model.Flow{flow("f1", "src", "a", 1), flow("f2", "a", "b", 1), flow("f3", "b", "end", 1)})
	for limit, want := range map[int]error{4: nil, 3: ErrEntityVisits} {
		in := input(ir)
		in.Limits.MaxEntityVisits = limit
		if _, err := RunReplication(context.Background(), compile(t, in), 0, NewBudget(1_000), nil); !errors.Is(err, want) {
			t.Errorf("max_entity_visits %d: %v, want %v", limit, err, want)
		}
	}
	// Five arrivals one minute apart, each held 10 minutes: five at once.
	held := build(t, []model.Node{
		source("src", 1, 5), {ID: "hold", Type: model.NodeDelay, DelayTime: &model.TimedDistribution{Distribution: constant(10)}}, {ID: "end", Type: model.NodeSink},
	}, []model.Flow{flow("f1", "src", "hold", 1), flow("f2", "hold", "end", 1)})
	for limit, want := range map[int]error{5: nil, 4: ErrActiveEntities} {
		in := input(held)
		in.Limits.MaxActiveEntities = limit
		out, err := RunReplication(context.Background(), compile(t, in), 0, NewBudget(1_000), nil)
		if !errors.Is(err, want) || (want == nil && out.PeakActive != 5) {
			t.Errorf("max_active_entities %d: %v (peak %d), want %v", limit, err, out.PeakActive, want)
		}
	}
}

// Multi-unit resource-wise FIFO (T-021 review): A holds 1 of R's 2 units;
// B needs both and waits; C needs 1 and queued after B, so it waits behind B
// even though a unit is free: A 1-11, B 11-16, C 16.
func TestMultiUnitFIFO(t *testing.T) {
	two := process("B", 5, "R")
	two.Requires[0].Units = 2
	ir := build(t, []model.Node{
		source("sa", 1, 1), process("A", 10, "R"),
		source("sb", 2, 1), two,
		source("sc", 3, 1), process("C", 5, "R"),
		{ID: "done", Type: model.NodeSink},
	}, []model.Flow{flow("f1", "sa", "A", 1), flow("f2", "A", "done", 1), flow("f3", "sb", "B", 1), flow("f4", "B", "done", 1), flow("f5", "sc", "C", 1), flow("f6", "C", "done", 1)}, "R")
	ir.Resources[0].Capacity = 2
	rec, _ := run(t, input(ir), 0)
	if got, want := starts(rec), map[string]int64{"A": 1, "B": 11, "C": 16}; !mapsEqual(got, want) {
		t.Fatalf("starts %v, want %v", got, want)
	}
	peak := int64(0)
	for _, o := range rec.at(ObsResource, "") {
		peak = max(peak, o.Value)
	}
	if peak != 2 {
		t.Errorf("R peaked at %d of 2", peak)
	}
}

// Every grant reports the bundle's new in-use level before its service
// start, step by step (T-021 review D3): three grants of a capacity-3
// resource at t=10 report 1, 2, 3; the releases at 14 report 2, 1, 0.
func TestGrantObservationsStepwise(t *testing.T) {
	ir := build(t, []model.Node{source("a", 10, 1), source("b", 10, 1), source("c", 10, 1), process("work", 4, "R"), {ID: "done", Type: model.NodeSink}},
		[]model.Flow{flow("f1", "a", "work", 1), flow("f2", "b", "work", 1), flow("f3", "c", "work", 1), flow("f4", "work", "done", 1)}, "R")
	ir.Resources[0].Capacity = 3
	rec, _ := run(t, input(ir), 0)
	var got []string
	for _, o := range rec.all {
		switch o.Kind {
		case ObsResource:
			got = append(got, fmt.Sprintf("%d:R=%d", o.Tick/60_000, o.Value))
		case ObsServiceStart:
			got = append(got, fmt.Sprintf("%d:start", o.Tick/60_000))
		}
	}
	want := []string{"10:R=1", "10:start", "10:R=2", "10:start", "10:R=3", "10:start", "14:R=2", "14:R=1", "14:R=0"}
	if !slices.Equal(got, want) {
		t.Errorf("observations %v, want %v", got, want)
	}
}

// D-032 with zero-time steps (T-021 review): a and b arrive at t=10. b's
// zero delay completes (priority 10) before the tick's dispatch (priority
// 20), so b queues at P1 first; a's zero-time P0 service is granted at that
// dispatch and a reaches P1 only after it. P1 serves b, then a.
func TestZeroTimeStepsKeepD032Order(t *testing.T) {
	ir := build(t, []model.Node{
		source("a", 10, 1), source("b", 10, 1),
		process("P0", 0, "R0"), process("P1", 5, "R"),
		{ID: "D", Type: model.NodeDelay, DelayTime: &model.TimedDistribution{Distribution: constant(0)}},
		{ID: "end", Type: model.NodeSink},
	}, []model.Flow{flow("fa", "a", "P0", 1), flow("fb", "b", "D", 1), flow("f0", "P0", "P1", 1), flow("fd", "D", "P1", 1), flow("f1", "P1", "end", 1)}, "R", "R0")
	rec, _ := run(t, input(ir), 0)
	var order []string
	for _, o := range rec.at(ObsServiceStart, "P1") {
		order = append(order, rec.r(o.Entity))
	}
	if !slices.Equal(order, []string{"b", "a"}) {
		t.Fatalf("P1 served %v, want [b a]", order)
	}
}

// A zero-time service granted at a dispatch completes in the same tick,
// after that dispatch; its release schedules another dispatch in the tick,
// so the next waiting request is not stranded: both start at t=10.
func TestSameTickRedispatch(t *testing.T) {
	ir := build(t, []model.Node{source("a", 10, 1), source("b", 10, 1), process("Z", 0, "R"), {ID: "end", Type: model.NodeSink}},
		[]model.Flow{flow("fa", "a", "Z", 1), flow("fb", "b", "Z", 1), flow("fz", "Z", "end", 1)}, "R")
	p := compile(t, input(ir))
	c := newChecker(t, p)
	rec := newRecorder(t, p)
	if _, err := RunReplication(context.Background(), p, 0, NewBudget(1000), multi{c, rec}); err != nil {
		t.Fatal(err)
	}
	c.finish()
	if got := starts(rec); len(got) != 1 || got["Z"] != 10 || len(rec.at(ObsServiceStart, "Z")) != 2 || len(c.errs) > 0 {
		t.Errorf("starts %v (%d), problems %v", got, len(rec.at(ObsServiceStart, "Z")), c.errs)
	}
	for _, o := range rec.at(ObsServiceStart, "Z") {
		if o.Tick != 10*60_000 {
			t.Errorf("a Z service started at %d", o.Tick/60_000)
		}
	}
}

type multi []Observer

func (m multi) Observe(o Observation) {
	for _, x := range m {
		x.Observe(o)
	}
}

// An explicit queue routes into its process's buffer (the queue-enter
// observation names the process), and both nodes count as visits:
// src -> q -> work -> end is 4 visits, the direct src -> work -> end is 3.
func TestQueueNodeRoutingAndVisits(t *testing.T) {
	mk := func(withQueue bool) model.ModelIR {
		nodes := []model.Node{source("src", 1, 3), process("work", 2, "R"), {ID: "end", Type: model.NodeSink}}
		flows := []model.Flow{flow("f2", "work", "end", 1)}
		if withQueue {
			nodes = append(nodes, model.Node{ID: "q", Type: model.NodeQueue})
			flows = append(flows, flow("f0", "src", "q", 1), flow("f1", "q", "work", 1))
		} else {
			flows = append(flows, flow("f1", "src", "work", 1))
		}
		return build(t, nodes, flows, "R")
	}
	for _, c := range []struct {
		queue bool
		limit int
		want  error
	}{{false, 3, nil}, {false, 2, ErrEntityVisits}, {true, 4, nil}, {true, 3, ErrEntityVisits}} {
		in := input(mk(c.queue))
		in.Limits.MaxEntityVisits = c.limit
		_, err := RunReplication(context.Background(), compile(t, in), 0, NewBudget(10_000), nil)
		if !errors.Is(err, c.want) {
			t.Errorf("queue %v, max_entity_visits %d: %v, want %v", c.queue, c.limit, err, c.want)
		}
	}
	rec, out := run(t, input(mk(true)), 0)
	if out.Exited != 3 || len(rec.at(ObsQueueEnter, "work")) != 3 || len(rec.at(ObsServiceStart, "work")) != 3 {
		t.Errorf("exited %d, queued %d", out.Exited, len(rec.at(ObsQueueEnter, "work")))
	}
}

// Failure and rework nodes count as visits like any other node.
func TestFailureAndReworkAreVisits(t *testing.T) {
	ir := build(t, []model.Node{source("src", 1, 1), {ID: "fail", Type: model.NodeFailure}, {ID: "redo", Type: model.NodeRework}, {ID: "end", Type: model.NodeSink}},
		[]model.Flow{flow("f1", "src", "fail", 1), flow("f2", "fail", "redo", 1), flow("f3", "redo", "end", 1)})
	for limit, want := range map[int]error{4: nil, 3: ErrEntityVisits} {
		in := input(ir)
		in.Limits.MaxEntityVisits = limit
		if _, err := RunReplication(context.Background(), compile(t, in), 0, NewBudget(100), nil); !errors.Is(err, want) {
			t.Errorf("max_entity_visits %d: %v, want %v", limit, err, want)
		}
	}
}

// RunReplication honours its context and the shared budget at every event
// (TEST-021): a cancel from inside the run stops it with the context's
// error; a small budget fails it; Events counts what was processed.
func TestRunReplicationCheckpoints(t *testing.T) {
	p := compile(t, input(golden(t, "default-delivery.json")))
	ctx, cancel := context.WithCancel(context.Background())
	seen := 0
	stopper := observerFunc(func(Observation) {
		if seen++; seen == 50 {
			cancel()
		}
	})
	out, err := RunReplication(ctx, p, 0, NewBudget(10_000_000), stopper)
	if !errors.Is(err, context.Canceled) || out.Events == 0 || out.Events > 50 {
		t.Errorf("cancelled run: %v after %d events", err, out.Events)
	}
	b := NewBudget(25)
	if out, err := RunReplication(context.Background(), p, 0, b, nil); !errors.Is(err, ErrEventBudget) || out.Events != 25 || b.Used() != 25 {
		t.Errorf("budget: %v, %d events, used %d", err, out.Events, b.Used())
	}
	b = NewBudget(10_000_000)
	if out, err := RunReplication(context.Background(), p, 0, b, nil); err != nil || out.Events != b.Used() || out.Events <= 25 {
		t.Errorf("full run: %v, %d events, used %d", err, out.Events, b.Used())
	}
}

type observerFunc func(Observation)

func (f observerFunc) Observe(o Observation) { f(o) }

// Kernel-level horizon equality (TEST-017): GOLDEN-001's last completion is
// at 1005; a horizon of exactly 1005 processes it, one millisecond less
// leaves that entity censored.
func TestHorizonEqualityInKernel(t *testing.T) {
	for _, c := range []struct {
		horizon          float64
		exited, censored int64
	}{{1005, 100, 0}, {1005 - 1.0/60_000, 99, 1}} {
		in := input(golden(t, "deterministic-queue.json"))
		in.Config = config(c.horizon, model.Minute)
		_, out := run(t, in, 0)
		if out.Exited != c.exited || out.Censored != c.censored {
			t.Errorf("horizon %v: exited %d censored %d", c.horizon, out.Exited, out.Censored)
		}
	}
}

// The dispatcher's work is bounded by the resources, not by how many
// requests wait (T-021 review D1): 3,000 requests wait on a 3-resource
// bundle while 30,000 unrelated arrivals each trigger a dispatch. Scanning
// every waiting request per dispatch would look at ~10^8 of them.
func TestDispatchWorkIsBounded(t *testing.T) {
	res := []string{"R0", "R1", "R2"}
	long := process("long", 300_000, res...)
	ir := build(t, []model.Node{
		{ID: "a", Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: constant(0.001), MaxEntities: 3_000}},
		long,
		{ID: "b", Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: constant(0.001)}},
		process("fast", 0.001),
		{ID: "end", Type: model.NodeSink},
	}, []model.Flow{flow("fa", "a", "long", 1), flow("fl", "long", "end", 1), flow("fb", "b", "fast", 1), flow("ff", "fast", "end", 1)}, res...)
	ir.TimeUnit = model.ModelTimeUnit(model.Second)
	in := input(ir)
	in.Config = config(36, model.Second)
	k, out, err := runKernel(context.Background(), compile(t, in), 0, NewBudget(10_000_000), nil)
	if err != nil || out.Created < 30_000 {
		t.Fatalf("%v, %+v", err, out)
	}
	if limit := 4 * out.Events; k.examined > limit {
		t.Errorf("the dispatcher examined %d requests in %d events (limit %d)", k.examined, out.Events, limit)
	}
}

// EntityID of an entity that already left is "", not a panic (review P5);
// a replication index outside the program is an internal error (P6); limit
// errors say where they happened (P4).
func TestKernelEdges(t *testing.T) {
	p := compile(t, input(golden(t, "deterministic-queue.json")))
	r := newReplication(p, 0)
	if id := r.EntityID(42); id != "" {
		t.Errorf("unknown entity ID %q", id)
	}
	if _, err := RunReplication(context.Background(), p, 1, NewBudget(100), nil); !errors.Is(err, ErrInternal) {
		t.Errorf("index 1 of a 1-replication program: %v", err)
	}
	in := input(golden(t, "congested-queue.json"))
	in.Limits.MaxActiveEntities = 3
	if _, err := RunReplication(context.Background(), compile(t, in), 0, NewBudget(1000), nil); !errors.Is(err, ErrActiveEntities) || !strings.Contains(err.Error(), "r0/arrival/") {
		t.Errorf("active limit: %v", err)
	}
	in = input(golden(t, "deterministic-queue.json"))
	in.Limits.MaxEntityVisits = 2
	if _, err := RunReplication(context.Background(), compile(t, in), 0, NewBudget(1000), nil); !errors.Is(err, ErrEntityVisits) || !strings.Contains(err.Error(), "r0/arrival/1") || !strings.Contains(err.Error(), "done") {
		t.Errorf("visit limit: %v", err)
	}
}

// observationDigest hashes an observation stream: per observation, kind,
// tick, seq, entity, node, resource and value as little-endian 64-bit words.
func observationDigest(all []Observation) string {
	h := sha256.New()
	var buf [56]byte
	for _, o := range all {
		for i, v := range []uint64{uint64(o.Kind), uint64(o.Tick), o.Seq, o.Entity, uint64(int64(o.Node)), uint64(int64(o.Resource)), uint64(o.Value)} {
			binary.LittleEndian.PutUint64(buf[8*i:], v)
		}
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// A stochastic run's full observation stream is pinned (review P3):
// testdata/simulation/kernel-observation-digests.json is a regression lock
// taken from this kernel, not an independent oracle; the semantics are
// checked by the observation-only model and the hand-derived goldens. Any
// change to event order, tie-breaks or sampling shows up here.
func TestKernelObservationDigests(t *testing.T) {
	var lock struct {
		Cases []struct {
			Model        string  `json:"model"`
			HorizonDays  float64 `json:"horizon_days"`
			Replications int     `json:"replications"`
			Index        uint32  `json:"replication_index"`
			Observations int     `json:"observations"`
			Digest       string  `json:"sha256"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/kernel-observation-digests.json")), &lock); err != nil {
		t.Fatal(err)
	}
	if len(lock.Cases) == 0 {
		t.Fatal("no locked cases")
	}
	for _, c := range lock.Cases {
		in := input(golden(t, c.Model))
		in.Config = config(c.HorizonDays, model.Day)
		in.Config.Replications = c.Replications
		col := &collect{}
		if _, err := RunReplication(context.Background(), compile(t, in), c.Index, NewBudget(10_000_000), col); err != nil {
			t.Fatal(err)
		}
		if got := observationDigest(col.all); len(col.all) != c.Observations || got != c.Digest {
			t.Errorf("%s r%d: %d observations, digest %s; locked %d, %s", c.Model, c.Index, len(col.all), got, c.Observations, c.Digest)
		}
	}
}

// A resource queue that never empties stays compact (T-021 re-review L1):
// 300 requests always wait on R while a request is served every
// millisecond for 2M events; the queue's backing array must track the
// waiting requests, not every request ever pushed.
func TestResourceQueueStaysCompact(t *testing.T) {
	ir := build(t, []model.Node{
		{ID: "a", Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: constant(0.001), MaxEntities: 300}},
		{ID: "b", Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: constant(0.001)}},
		process("work", 0.001, "R"),
		{ID: "end", Type: model.NodeSink},
	}, []model.Flow{flow("fa", "a", "work", 1), flow("fb", "b", "work", 1), flow("fw", "work", "end", 1)}, "R")
	ir.TimeUnit = model.ModelTimeUnit(model.Second)
	in := input(ir)
	in.Config = config(30*86400, model.Second)
	k, out, err := runKernel(context.Background(), compile(t, in), 0, NewBudget(2_000_000), nil)
	if !errors.Is(err, ErrEventBudget) || out.PeakActive < 300 {
		t.Fatalf("%v, %+v", err, out)
	}
	q := k.queues[0]
	if live := len(q.items) - q.head; live < 250 || cap(q.items) > 4096 {
		t.Errorf("queue: %d live requests in a backing array of %d", live, cap(q.items))
	}
}

// A candidate whose bundle does not fit is not looked at again until one of
// its resources is released (T-021 re-review L2): 100 requests each need
// both units of their own resource while a long "hog" holds one unit of
// all of them, and an unrelated stream triggers a dispatch every
// millisecond. Re-checking every held candidate per dispatch would look at
// ~100 requests per event.
func TestHeldCandidatesWaitForARelease(t *testing.T) {
	const n = 100
	var res []string
	for i := range n {
		res = append(res, fmt.Sprintf("R%03d", i))
	}
	nodes := []model.Node{
		{ID: "a_hog", Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: constant(0.001), MaxEntities: 1}},
		process("hog", 300_000, res...),
		{ID: "b_src", Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: constant(0.002), MaxEntities: 2 * n}},
		{ID: "b_split", Type: model.NodeDecision},
		{ID: "c_src", Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: constant(0.001)}},
		process("fast", 0.001),
		{ID: "end", Type: model.NodeSink},
	}
	flows := []model.Flow{flow("f_hog_in", "a_hog", "hog", 1), flow("f_hog_out", "hog", "end", 1), flow("f_b", "b_src", "b_split", 1),
		flow("f_c", "c_src", "fast", 1), flow("f_fast", "fast", "end", 1)}
	for i := range n {
		w := process(fmt.Sprintf("w%03d", i), 1, res[i])
		w.Requires[0].Units = 2
		nodes = append(nodes, w)
		flows = append(flows, flow(fmt.Sprintf("g%03d", i), "b_split", w.ID, 1.0/n), flow("h_"+w.ID, w.ID, "end", 1))
	}
	ir := build(t, nodes, flows, res...)
	for i := range ir.Resources {
		ir.Resources[i].Capacity = 2
	}
	ir.TimeUnit = model.ModelTimeUnit(model.Second)
	in := input(ir)
	in.Config = config(30*86400, model.Second)
	k, out, err := runKernel(context.Background(), compile(t, in), 0, NewBudget(300_000), nil)
	if !errors.Is(err, ErrEventBudget) {
		t.Fatalf("%v, %+v", err, out)
	}
	if perEvent := float64(k.examined) / float64(out.Events); perEvent > 2 {
		t.Errorf("the dispatcher examined %.1f requests per event", perEvent)
	}
}
