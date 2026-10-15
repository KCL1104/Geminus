package trace

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// Selection matches the independent reference (scripts/engine_reference.py)
// on every listed entity ID, selected or not.
func TestSelectedMatchesReference(t *testing.T) {
	var v struct {
		TraceSelection struct {
			Rate  float64 `json:"rate"`
			Cases []struct {
				Prefix   string `json:"prefix"`
				Count    int    `json:"count"`
				Selected []int  `json:"selected"`
			} `json:"cases"`
		} `json:"trace_selection"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/engine-vectors.json")), &v); err != nil {
		t.Fatal(err)
	}
	if v.TraceSelection.Rate != Rate || len(v.TraceSelection.Cases) < 2 {
		t.Fatalf("reference %+v", v.TraceSelection)
	}
	for _, c := range v.TraceSelection.Cases {
		var got []int
		for k := 1; k <= c.Count; k++ {
			if Selected(fmt.Sprintf("%s%d", c.Prefix, k)) {
				got = append(got, k)
			}
		}
		if !slices.Equal(got, c.Selected) {
			t.Errorf("%s1..%d: selected %v, reference %v", c.Prefix, c.Count, got, c.Selected)
		}
	}
}

var limits = simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 10_000, MaxTraceEvents: 100_000}

// program is src (every minute, n entities) -> inbox (queue) -> work ->
// d (decision) -> 50% done / 50% gone (abandoned), with a delay before d.
func program(t *testing.T, n int) *engine.Program {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/golden-models/deterministic-queue.json")), &ir); err != nil {
		t.Fatal(err)
	}
	one, half := 1.0, 0.5
	ir.Nodes[0].Arrival = &model.Arrival{Distribution: model.Distribution{Type: model.DistConstant, Value: &one}, MaxEntities: n}
	ir.Nodes[1].ServiceTime.Distribution = model.Distribution{Type: model.DistConstant, Value: &half}
	ir.Nodes = append(ir.Nodes, model.Node{ID: "inbox", Type: model.NodeQueue},
		model.Node{ID: "wait", Type: model.NodeDelay, DelayTime: &model.TimedDistribution{Distribution: model.Distribution{Type: model.DistConstant, Value: &one}}},
		model.Node{ID: "d", Type: model.NodeDecision}, model.Node{ID: "gone", Type: model.NodeSink, Outcome: model.SinkAbandoned})
	f := func(id, from, to string, p float64) model.Flow {
		return model.Flow{ID: id, From: from, To: to, Routing: model.Routing{Type: model.RoutingProbability, Probability: p}}
	}
	ir.Flows = []model.Flow{f("a", "arrival", "inbox", 1), f("b", "inbox", "service", 1), f("c", "service", "wait", 1), f("e", "wait", "d", 1),
		f("g", "d", "done", 0.5), f("h", "d", "gone", 0.5)}
	in := simulation.RunInput{Model: ir, Limits: limits, Config: simulation.RunConfig{
		Horizon: simulation.Duration{Value: 10_000, Unit: model.Minute}, Warmup: simulation.Duration{Value: 0, Unit: model.Minute},
		Replications: 3, Seed: 7, TraceLevel: simulation.TraceSampled}}
	p, err := engine.Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func record(t *testing.T, p *engine.Program, index uint32, max int) *simulation.TraceArtifact {
	t.Helper()
	r := NewRecorder(p, index, max)
	if _, err := engine.RunReplication(context.Background(), p, index, engine.NewBudget(10_000_000), r); err != nil {
		t.Fatal(err)
	}
	return r.Artifact("des-test")
}

// Every sampled entity's trajectory is complete and only sampled entities
// appear: arrival at the source, queue_enter at the explicit queue (where
// it waits), service at the process, delay, then complete or abandon at a
// sink. Events are in engine order.
func TestTrajectories(t *testing.T) {
	p := program(t, 400)
	a := record(t, p, 2, 100_000)
	byEntity := map[string][]simulation.TraceEvent{}
	var order []string
	// Array order is the engine's processing order: by tick, then event
	// priority, then sequence, so sequence alone can go back within a tick
	// (a service completion, priority 0, runs before an earlier-scheduled
	// delay completion, priority 10).
	sawPriorityOrder := false
	for i, ev := range a.Events {
		if i > 0 && ev.TimeTick < a.Events[i-1].TimeTick {
			t.Fatalf("event %d goes back in time: %+v after %+v", i, ev, a.Events[i-1])
		}
		if i > 0 && ev.TimeTick == a.Events[i-1].TimeTick && ev.Sequence < a.Events[i-1].Sequence {
			sawPriorityOrder = true
			if ev.Type != simulation.TraceDelayEnd || a.Events[i-1].Type != simulation.TraceDelayStart {
				t.Fatalf("event %d: sequence went back outside a priority tie: %+v after %+v", i, ev, a.Events[i-1])
			}
		}
		if !Selected(ev.EntityID) {
			t.Fatalf("unselected entity traced: %+v", ev)
		}
		if _, ok := byEntity[ev.EntityID]; !ok {
			order = append(order, ev.EntityID)
		}
		byEntity[ev.EntityID] = append(byEntity[ev.EntityID], ev)
	}
	want := 0
	for k := 1; k <= 400; k++ {
		if Selected(fmt.Sprintf("r2/arrival/%d", k)) {
			want++
		}
	}
	if len(byEntity) != want || want < 20 {
		t.Fatalf("%d traced entities, %d selected", len(byEntity), want)
	}
	outcomes := map[simulation.TraceEventType]int{}
	for _, id := range order {
		evs := byEntity[id]
		var steps []string
		for _, ev := range evs {
			steps = append(steps, string(ev.Type)+"@"+ev.NodeID)
		}
		last := evs[len(evs)-1].Type
		outcomes[last]++
		end := "complete@done"
		if last == simulation.TraceAbandon {
			end = "abandon@gone"
		}
		if wantSteps := []string{"arrival@arrival", "queue_enter@inbox", "service_start@service", "service_end@service", "delay_start@wait", "delay_end@wait", end}; !slices.Equal(steps, wantSteps) {
			t.Fatalf("%s: %v", id, steps)
		}
	}
	if !sawPriorityOrder {
		t.Error("the fixture no longer exercises a same-tick priority tie")
	}
	if outcomes[simulation.TraceComplete] == 0 || outcomes[simulation.TraceAbandon] == 0 {
		t.Errorf("outcomes %v", outcomes)
	}
	if a.Truncated || a.ReplicationIndex != 2 || a.Format != simulation.TraceFormat {
		t.Errorf("header %+v", a)
	}
}

// The cap is exact: max events are kept and the trace says it is truncated;
// a cap the trace fits in is not truncated.
func TestCap(t *testing.T) {
	p := program(t, 400)
	full := record(t, p, 0, 100_000)
	n := len(full.Events)
	for _, c := range []struct {
		max       int
		truncated bool
	}{{n, false}, {n - 1, true}, {1, true}} {
		a := record(t, p, 0, c.max)
		if want := min(c.max, n); len(a.Events) != want || a.Truncated != c.truncated || !slices.Equal(a.Events, full.Events[:want]) {
			t.Errorf("max %d: %d events, truncated %v", c.max, len(a.Events), a.Truncated)
		}
		if a.Sampling.MaxEvents != c.max {
			t.Errorf("max_events %d", a.Sampling.MaxEvents)
		}
	}
}

// No entity selected: an empty, not truncated, trace with an empty array.
func TestEmpty(t *testing.T) {
	p := program(t, 3)
	for _, k := range []int{1, 2, 3} {
		if Selected(fmt.Sprintf("r1/arrival/%d", k)) {
			t.Fatalf("fixture assumption broken: r1/arrival/%d is selected", k)
		}
	}
	a := record(t, p, 1, 10)
	if a.Events == nil || len(a.Events) != 0 || a.Truncated {
		t.Errorf("%+v", a)
	}
}
