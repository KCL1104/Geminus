package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

type arrival struct {
	tick int64
	id   string
}

// arrivals runs replication index of p with only sources active (each
// entity's onward route is T-021) and returns what arrived, in order.
func arrivals(t *testing.T, p *Program, index uint32, b *Budget) ([]arrival, error) {
	t.Helper()
	out, _, err := runArrivals(t, p, index, b)
	return out, err
}

// runArrivals is arrivals that also returns the finished replication.
func runArrivals(t *testing.T, p *Program, index uint32, b *Budget) ([]arrival, *replication, error) {
	t.Helper()
	r := newReplication(p, index)
	var out []arrival
	r.onArrival = func(r *replication, e uint64) error {
		out = append(out, arrival{r.now, r.EntityID(e)})
		return nil
	}
	if b == nil {
		b = NewBudget(10_000_000)
	}
	return out, r, r.run(context.Background(), b)
}

// sourcesModel is a valid IR with the given sources, each flowing to one sink.
func sourcesModel(t *testing.T, sources []model.Node) model.ModelIR {
	t.Helper()
	ir := minimal(t)
	ir.Nodes = []model.Node{{ID: "done", Type: model.NodeSink}}
	ir.Flows = nil
	ir.Resources = ir.Resources[:0]
	for _, s := range sources {
		s.Type, s.EntityType = model.NodeSource, "change"
		ir.Nodes = append(ir.Nodes, s)
		ir.Flows = append(ir.Flows, model.Flow{ID: s.ID + "_done", From: s.ID, To: "done", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}})
	}
	return ir
}

// The arrival process reproduces the independent reference: three sources
// listed out of ID order, exponential/constant/triangular interarrivals,
// max_entities, the first arrival one interarrival after 0, and entity IDs.
// Each source's arrival stream ends in the reference's state, so a source
// draws exactly once per scheduled arrival: none after max_entities, and
// one (discarded) for the arrival that falls after the horizon.
func TestArrivalVector(t *testing.T) {
	v := loadVectors(t).Arrivals
	var sources []model.Node
	for _, s := range v.Sources {
		d := s.Distribution
		sources = append(sources, model.Node{ID: s.ID, Arrival: &model.Arrival{Distribution: d, MaxEntities: s.MaxEntities}})
	}
	in := input(sourcesModel(t, sources))
	in.Model.TimeUnit = model.ModelTimeUnit(v.Run.Unit)
	in.Config = simulation.RunConfig{Horizon: simulation.Duration{Value: v.Run.Horizon.Value, Unit: v.Run.Horizon.Unit},
		Warmup: simulation.Duration{Value: 0, Unit: v.Run.Horizon.Unit}, Replications: int(v.Run.ReplicationIndex) + 1,
		Seed: int64(v.Run.BaseSeed), TraceLevel: simulation.TraceNone}
	p := compile(t, in)
	got, r, err := runArrivals(t, p, v.Run.ReplicationIndex, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.StreamStates) != len(v.Sources) {
		t.Fatalf("%d stream states for %d sources", len(v.StreamStates), len(v.Sources))
	}
	for id, want := range v.StreamStates {
		s, ok := r.streams[streamKey{p.index[id], PurposeArrival}]
		if !ok {
			t.Fatalf("source %s never drew", id)
		}
		if got := fmt.Sprintf("%016x", s.state); got != want {
			t.Errorf("source %s: arrival stream state %s, reference %s (wrong number of draws)", id, got, want)
		}
	}
	var want []arrival
	for _, e := range v.Expected {
		want = append(want, arrival{int64(e[0].(float64)), e[1].(string)})
	}
	if !slices.Equal(got, want) {
		t.Errorf("arrivals\n got %v\nwant %v", got, want)
	}
}

// GOLDEN-001's arrivals (hand-derived, testdata/simulation/expected-cases.json):
// 100 entities at 10, 20, ..., 1000 minutes; the max_entities limit, not the
// horizon, stops the source.
func TestDeterministicArrivals(t *testing.T) {
	in := input(golden(t, "deterministic-queue.json"))
	in.Config = config(1010, model.Minute)
	got, err := arrivals(t, compile(t, in), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 {
		t.Fatalf("%d arrivals", len(got))
	}
	for i, a := range got {
		if want := int64(i+1) * 600_000; a.tick != want || a.id != fmt.Sprintf("r0/arrival/%d", i+1) {
			t.Fatalf("arrival %d: %+v, want tick %d", i, a, want)
		}
	}
}

// An event exactly at the horizon is processed; one tick later is not.
func TestHorizonIsInclusive(t *testing.T) {
	for horizon, want := range map[float64]int{1000: 100, 999.99999: 99, 15: 1, 9.999 / 60 * 60: 0} {
		in := input(golden(t, "deterministic-queue.json"))
		in.Config = config(horizon, model.Minute)
		got, err := arrivals(t, compile(t, in), 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want {
			t.Errorf("horizon %g min: %d arrivals, want %d", horizon, len(got), want)
		}
	}
}

// The event budget counts every processed event across replications and
// fails the run with a typed error.
func TestEventBudget(t *testing.T) {
	in := input(golden(t, "deterministic-queue.json"))
	in.Config = config(1010, model.Minute)
	p := compile(t, in)
	b := NewBudget(150)
	if _, err := arrivals(t, p, 0, b); err != nil {
		t.Fatalf("first replication (100 events): %v", err)
	}
	if _, err := arrivals(t, p, 1, b); !errors.Is(err, ErrEventBudget) {
		t.Fatalf("second replication: %v", err)
	}
	if b.Used() != 150 {
		t.Errorf("used %d", b.Used())
	}
}

// The budget allows exactly max events: a replication of 100 events runs on
// a budget of 100 and fails on 99, having used all 99.
func TestEventBudgetIsExact(t *testing.T) {
	in := input(golden(t, "deterministic-queue.json"))
	in.Config = config(1010, model.Minute)
	p := compile(t, in)
	if _, err := arrivals(t, p, 0, NewBudget(100)); err != nil {
		t.Errorf("budget 100 for 100 events: %v", err)
	}
	b := NewBudget(99)
	if _, err := arrivals(t, p, 0, b); !errors.Is(err, ErrEventBudget) || b.Used() != 99 {
		t.Errorf("budget 99: %v, used %d", err, b.Used())
	}
}

// ARRIVAL creates the entity, then schedules the next arrival, then routes
// the entity (SIMULATION_SPEC §2). The order fixes sequence numbers: when
// routing schedules an event for the same tick and priority as the next
// arrival, the arrival comes first.
func TestArrivalSchedulesNextBeforeRouting(t *testing.T) {
	in := input(golden(t, "deterministic-queue.json"))
	in.Config = config(1010, model.Minute)
	p := compile(t, in)
	r := newReplication(p, 0)
	var order []string
	r.onArrival = func(r *replication, e uint64) error {
		order = append(order, fmt.Sprintf("%d arrive %s", r.now/60_000, r.EntityID(e)))
		if e > 2 {
			return nil
		}
		// The onward route: a 10 minute delay, the same as the interarrival.
		return r.schedule(600_000, PriorityImmediate, EventDelayComplete, 0, e)
	}
	r.onEvent = func(r *replication, ev event) error {
		order = append(order, fmt.Sprintf("%d leave %s", r.now/60_000, r.EntityID(ev.Entity)))
		return nil
	}
	if err := r.run(context.Background(), NewBudget(1000)); err != nil {
		t.Fatal(err)
	}
	want := []string{"10 arrive r0/arrival/1", "20 arrive r0/arrival/2", "20 leave r0/arrival/1", "30 arrive r0/arrival/3", "30 leave r0/arrival/2"}
	if !slices.Equal(order[:5], want) {
		t.Errorf("order %v, want %v", order[:5], want)
	}
}

// Cancellation is checked before every event and returns the context's error.
func TestCancellation(t *testing.T) {
	in := input(golden(t, "deterministic-queue.json"))
	p := compile(t, in)
	ctx, cancel := context.WithCancel(context.Background())
	r := newReplication(p, 0)
	seen := 0
	r.onArrival = func(*replication, uint64) error {
		seen++
		if seen == 3 {
			cancel()
		}
		return nil
	}
	if err := r.run(ctx, NewBudget(1000)); !errors.Is(err, context.Canceled) || seen != 3 {
		t.Errorf("err %v after %d arrivals", err, seen)
	}
	// A non-arrival event without a kernel handler is an internal error, not a silent skip.
	r = newReplication(p, 0)
	r.q.schedule(event{Tick: 1, Priority: PriorityDispatch, Kind: EventDispatch})
	if err := r.run(context.Background(), NewBudget(1000)); !errors.Is(err, ErrInternal) {
		t.Errorf("unhandled event: %v", err)
	}
}

// The same program and replication index always give the same arrivals,
// also when replications run concurrently; different indices differ; adding
// a source changes no other source's arrivals (independent streams).
func TestArrivalDeterminism(t *testing.T) {
	sources := []model.Node{
		{ID: "a_src", Arrival: &model.Arrival{Distribution: model.Distribution{Type: model.DistExponential, Mean: ptr(7.0)}}},
		{ID: "b_src", Arrival: &model.Arrival{Distribution: model.Distribution{Type: model.DistTriangular, Min: ptr(1.0), Mode: ptr(2.0), Max: ptr(9.0)}}},
	}
	p := compile(t, input(sourcesModel(t, sources)))
	first, err := arrivals(t, p, 3, nil)
	if err != nil || len(first) < 100 {
		t.Fatalf("%v, %d arrivals", err, len(first))
	}
	var wg sync.WaitGroup
	results := make([][]arrival, 8)
	for i := range results {
		wg.Go(func() {
			results[i], _ = arrivals(t, p, 3, nil)
		})
	}
	wg.Wait()
	for i, got := range results {
		if !slices.Equal(got, first) {
			t.Fatalf("concurrent run %d differs", i)
		}
	}
	other, _ := arrivals(t, p, 4, nil)
	if slices.Equal(other, first) {
		t.Error("replication 4 repeats replication 3")
	}
	onlyA := func(as []arrival) []arrival {
		return slices.DeleteFunc(slices.Clone(as), func(a arrival) bool { return a.id[3] != 'a' })
	}
	withC := compile(t, input(sourcesModel(t, append(sources,
		model.Node{ID: "c_src", Arrival: &model.Arrival{Distribution: model.Distribution{Type: model.DistExponential, Mean: ptr(3.0)}}}))))
	more, _ := arrivals(t, withC, 3, nil)
	if !slices.Equal(onlyA(more), onlyA(first)) {
		t.Error("adding a source changed another source's arrivals")
	}
}

func ptr[T any](v T) *T { return &v }

// Same-tick arrivals run in the order they were scheduled (event sequence).
// Sources are initialized in ID order, so first arrivals at the same tick
// run by source ID whatever the IR order; later ones follow when each was
// scheduled, which is not ID order in general: with a every 10 and b every
// 20, b's first arrival (scheduled at 0) runs before a's second (scheduled
// at 10) at t=20.
func TestSameTickSourcesInIDOrder(t *testing.T) {
	ten := 10.0
	sources := []model.Node{
		{ID: "b_src", Arrival: &model.Arrival{Distribution: model.Distribution{Type: model.DistConstant, Value: &ten}, MaxEntities: 3}},
		{ID: "a_src", Arrival: &model.Arrival{Distribution: model.Distribution{Type: model.DistConstant, Value: &ten}, MaxEntities: 3}},
	}
	got, err := arrivals(t, compile(t, input(sourcesModel(t, sources))), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range got {
		ids = append(ids, a.id)
	}
	want := []string{"r0/a_src/1", "r0/b_src/1", "r0/a_src/2", "r0/b_src/2", "r0/a_src/3", "r0/b_src/3"}
	if !slices.Equal(ids, want) {
		t.Errorf("order %v, want %v", ids, want)
	}
	twenty := 20.0
	sources[0].Arrival.Distribution.Value = &twenty // b every 20, a every 10
	got, err = arrivals(t, compile(t, input(sourcesModel(t, sources))), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"10 r0/a_src/1", "20 r0/b_src/1", "20 r0/a_src/2", "30 r0/a_src/3", "40 r0/b_src/2", "60 r0/b_src/3"}
	ids = ids[:0]
	for _, a := range got {
		ids = append(ids, fmt.Sprintf("%d %s", a.tick/60_000, a.id))
	}
	if !slices.Equal(ids, want) {
		t.Errorf("order %v, want %v", ids, want)
	}
}

// Streams are per (node, purpose): created once, seeded from the
// replication, and independent of each other.
func TestStreamsPerNodeAndPurpose(t *testing.T) {
	p := compile(t, input(golden(t, "deterministic-queue.json")))
	r := newReplication(p, 2)
	a := r.stream(0, PurposeArrival)
	if r.stream(0, PurposeArrival) != a {
		t.Error("stream recreated")
	}
	s := r.stream(0, PurposeService)
	o := r.stream(1, PurposeArrival)
	if s == a || o == a || a.state != StreamSeed(ReplicationSeed(7, 2), p.nodes[0].id, PurposeArrival) {
		t.Error("streams are shared or wrongly seeded")
	}
}

// Tick arithmetic cannot overflow: a delay is at most beyondCap and now is at
// most the 365 day horizon; anything else is an internal error.
func TestScheduleBounds(t *testing.T) {
	r := newReplication(compile(t, input(golden(t, "deterministic-queue.json"))), 0)
	for _, d := range []int64{-1, beyondCap + 1, 1 << 62} {
		if err := r.schedule(d, PriorityImmediate, EventDelayComplete, 0, 0); !errors.Is(err, ErrInternal) {
			t.Errorf("delay %d: %v", d, err)
		}
	}
	if err := r.schedule(beyondCap, PriorityImmediate, EventDelayComplete, 0, 0); err != nil || r.q.Len() != 0 {
		t.Errorf("a beyond-cap delay is dropped, not scheduled: %v, %d queued", err, r.q.Len())
	}
	if err := r.schedule(0, PriorityImmediate, EventDelayComplete, 0, 0); err != nil || r.q.Len() != 1 {
		t.Errorf("zero delay: %v", err)
	}
}

// FuzzCompileAndArrive: whatever IR bytes arrive, Compile never panics; a
// compiled program runs its arrivals within the budget without panicking.
func FuzzCompileAndArrive(f *testing.F) {
	for _, name := range []string{"deterministic-queue.json", "congested-queue.json", "default-delivery.json", "report-example.json"} {
		f.Add(contracttest.ReadFile(f, filepath.Join(contracttest.Root(f), "testdata/golden-models", name)), uint32(0))
	}
	f.Fuzz(func(t *testing.T, data []byte, index uint32) {
		var ir model.ModelIR
		if model.Decode(data, &ir) != nil {
			return
		}
		p, err := Compile(input(ir))
		if err != nil {
			if !errors.Is(err, ErrInvalidInput) && !strings.HasPrefix(err.Error(), "engine:") {
				t.Fatalf("unexpected error kind: %v", err)
			}
			return
		}
		r := newReplication(p, index%1000)
		r.onArrival = func(*replication, uint64) error { return nil }
		if err := r.run(context.Background(), NewBudget(5000)); err != nil && !errors.Is(err, ErrEventBudget) {
			t.Fatalf("run: %v", err)
		}
	})
}
