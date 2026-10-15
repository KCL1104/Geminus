package engine

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
)

var limits = simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 10_000, MaxTraceEvents: 0}

func config(horizon float64, unit model.TimeUnit) simulation.RunConfig {
	return simulation.RunConfig{Horizon: simulation.Duration{Value: horizon, Unit: unit}, Warmup: simulation.Duration{Value: 0, Unit: unit},
		Replications: 1, Seed: 7, TraceLevel: simulation.TraceNone}
}

func golden(t *testing.T, name string) model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/golden-models", name)), &ir); err != nil {
		t.Fatal(err)
	}
	return ir
}

var reviewDuration = simulation.ResolvedDataset{
	Binding: model.DatasetBinding{LogicalRef: "dataset:review_duration", DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40",
		ContentHash: "sha256:" + strings.Repeat("0", 64), Unit: model.Minute, SampleCount: 3},
	Samples: []float64{120, 240, 360},
}

func input(ir model.ModelIR) simulation.RunInput {
	return simulation.RunInput{Model: ir, Config: config(1000, model.Minute), Limits: limits,
		Datasets: map[string]simulation.ResolvedDataset{"dataset:review_duration": reviewDuration}}
}

func compile(t *testing.T, in simulation.RunInput) *Program {
	t.Helper()
	p, err := Compile(in)
	if err != nil {
		var ie *InputError
		errors.As(err, &ie)
		t.Fatalf("Compile: %v %v", err, ie)
	}
	return p
}

// Every golden model compiles; the program is in ID order and the window is
// in ticks.
func TestCompileGoldenModels(t *testing.T) {
	for _, name := range []string{"deterministic-queue.json", "congested-queue.json", "default-delivery.json", "report-example.json"} {
		t.Run(name, func(t *testing.T) {
			ir := golden(t, name)
			p := compile(t, input(ir))
			ids := p.NodeIDs()
			if !slices.IsSorted(ids) || len(ids) != len(ir.Nodes) {
				t.Errorf("node order %v", ids)
			}
			if p.HorizonTicks() != 60_000_000 || p.WarmupTicks() != 0 || p.Replications() != 1 {
				t.Errorf("window %d/%d", p.WarmupTicks(), p.HorizonTicks())
			}
			for i, n := range p.nodes {
				if p.index[n.id] != i {
					t.Errorf("index of %s", n.id)
				}
				switch n.typ {
				case model.NodeSink:
					if len(n.routes) != 0 {
						t.Errorf("sink %s has routes", n.id)
					}
				case model.NodeDecision:
					if len(n.routes) == 0 || n.routes[len(n.routes)-1].cumulative < 1-1e-9 {
						t.Errorf("decision %s routes %v", n.id, n.routes)
					}
				default:
					if len(n.routes) != 1 || n.routes[0].cumulative != 1 {
						t.Errorf("%s routes %v", n.id, n.routes)
					}
				}
			}
		})
	}
}

// Decision routes are the p > 0 flows in flow-ID order with cumulative
// probabilities; a 0% flow is not a route (so the float fallback can never
// pick it); an explicit queue names its process's buffer; resources and
// requirements are in ID order.
func TestCompileStructure(t *testing.T) {
	ir := golden(t, "default-delivery.json")
	p := compile(t, input(ir))
	for _, n := range p.nodes {
		if n.typ != model.NodeDecision {
			continue
		}
		var want []string
		for _, f := range ir.Flows {
			if f.From == n.id && f.Routing.Probability > 0 {
				want = append(want, f.ID)
			}
		}
		slices.Sort(want)
		var got []string
		prev := 0.0
		for _, r := range n.routes {
			got = append(got, r.flow)
			if r.cumulative <= prev {
				t.Errorf("%s: cumulative %v not increasing", n.id, n.routes)
			}
			prev = r.cumulative
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s routes %v, want %v", n.id, got, want)
		}
	}
	for i, n := range p.nodes {
		switch n.typ {
		case model.NodeQueue:
			if n.buffer < 0 || p.nodes[n.buffer].typ != model.NodeProcess || n.buffer != n.routes[0].to {
				t.Errorf("queue %s buffer %d", n.id, n.buffer)
			}
		case model.NodeProcess:
			if n.buffer != i || n.service == nil {
				t.Errorf("process %s", n.id)
			}
			if !slices.IsSortedFunc(n.requires, func(a, b requirement) int { return a.resource - b.resource }) {
				t.Errorf("%s requirements %v", n.id, n.requires)
			}
		default:
			if n.buffer != -1 {
				t.Errorf("%s has a buffer", n.id)
			}
		}
	}
	// start -> q (queue) -> work -> end: the queue names work's buffer.
	withQueue := minimal(t)
	withQueue.Nodes = append(withQueue.Nodes, model.Node{ID: "q", Type: model.NodeQueue})
	withQueue.Flows[0].To = "q"
	withQueue.Flows = append(withQueue.Flows, model.Flow{ID: "q_work", From: "q", To: "work", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}})
	pq := compile(t, input(withQueue))
	if q := pq.nodes[pq.index["q"]]; q.buffer != pq.index["work"] || pq.nodes[q.buffer].buffer != q.buffer {
		t.Errorf("queue buffer %d, work is %d", q.buffer, pq.index["work"])
	}
	if !slices.IsSortedFunc(p.resources, func(a, b cresource) int { return strings.Compare(a.id, b.id) }) {
		t.Errorf("resource order %v", p.resources)
	}

	// A 0% flow out of a decision is dropped: start -> d -> {work 100%, end 0%}.
	min := minimal(t)
	min.Nodes = append(min.Nodes, model.Node{ID: "d", Type: model.NodeDecision})
	min.Flows[0].To = "d"
	min.Flows = append(min.Flows,
		model.Flow{ID: "d_end", From: "d", To: "end", Routing: model.Routing{Type: model.RoutingProbability, Probability: 0}},
		model.Flow{ID: "d_work", From: "d", To: "work", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}})
	pz := compile(t, input(min))
	d := pz.nodes[pz.index["d"]]
	if len(d.routes) != 1 || d.routes[0].flow != "d_work" || d.routes[0].cumulative != 1 {
		t.Errorf("0%% flow kept: %v", d.routes)
	}
}

// Decision routes follow flow-ID order, not document order, so routing draws
// map to the same flow on every run whatever order an editor saved.
func TestDecisionRoutesInFlowIDOrder(t *testing.T) {
	ir := minimal(t)
	ir.Nodes = append(ir.Nodes, model.Node{ID: "d", Type: model.NodeDecision})
	ir.Flows[0].To = "d"
	ir.Flows = append(ir.Flows,
		model.Flow{ID: "d_z_work", From: "d", To: "work", Routing: model.Routing{Type: model.RoutingProbability, Probability: 0.25}},
		model.Flow{ID: "d_a_end", From: "d", To: "end", Routing: model.Routing{Type: model.RoutingProbability, Probability: 0.75}})
	p := compile(t, input(ir))
	got := p.nodes[p.index["d"]].routes
	want := []route{{flow: "d_a_end", to: p.index["end"], cumulative: 0.75}, {flow: "d_z_work", to: p.index["work"], cumulative: 1}}
	if !slices.Equal(got, want) {
		t.Errorf("routes %v, want %v", got, want)
	}
}

func minimal(t *testing.T) model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-minimal-ir.json")), &ir); err != nil {
		t.Fatal(err)
	}
	return ir
}

func issueAt(t *testing.T, err error, path, code string) {
	t.Helper()
	var ie *InputError
	if !errors.As(err, &ie) || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want an InputError, got %v", err)
	}
	if !slices.ContainsFunc(ie.Issues, func(i model.ValidationIssue) bool {
		return strings.HasPrefix(i.Path, path) && (code == "" || i.Code == code)
	}) {
		t.Fatalf("no %s issue at %s: %v", code, path, ie.Issues)
	}
}

// Compile refuses what it cannot run, with typed issues, and never panics.
func TestCompileRejects(t *testing.T) {
	t.Run("invalid IR", func(t *testing.T) {
		ir := golden(t, "deterministic-queue.json")
		ir.Resources[0].Capacity = 0
		_, err := Compile(input(ir))
		issueAt(t, err, "/model/resources/0/capacity", validate.IRSchemaInvalid)
	})
	t.Run("missing dataset", func(t *testing.T) {
		in := input(golden(t, "report-example.json"))
		in.Datasets = nil
		_, err := Compile(in)
		issueAt(t, err, "/datasets/dataset:review_duration", validate.DatasetBindingInvalid)
	})
	t.Run("dataset for another ref", func(t *testing.T) {
		in := input(golden(t, "report-example.json"))
		ds := reviewDuration
		ds.Binding.LogicalRef = "dataset:other"
		in.Datasets = map[string]simulation.ResolvedDataset{"dataset:review_duration": ds}
		_, err := Compile(in)
		issueAt(t, err, "/datasets/dataset:review_duration", validate.DatasetBindingInvalid)
	})
	t.Run("sample count", func(t *testing.T) {
		in := input(golden(t, "report-example.json"))
		ds := reviewDuration
		ds.Samples = []float64{1, 2}
		in.Datasets = map[string]simulation.ResolvedDataset{"dataset:review_duration": ds}
		_, err := Compile(in)
		issueAt(t, err, "/datasets/dataset:review_duration", validate.DatasetBindingInvalid)
	})
	t.Run("sample over the cap", func(t *testing.T) {
		in := input(golden(t, "report-example.json"))
		ds := reviewDuration
		ds.Samples = []float64{1, 2, 366 * 24 * 60}
		in.Datasets = map[string]simulation.ResolvedDataset{"dataset:review_duration": ds}
		_, err := Compile(in)
		issueAt(t, err, "/datasets/dataset:review_duration", validate.DatasetBindingInvalid)
	})
	t.Run("zero sample as interarrival", func(t *testing.T) {
		ir := golden(t, "deterministic-queue.json")
		ir.Nodes[0].Arrival.Distribution = model.Distribution{Type: model.DistEmpirical, SamplesRef: "dataset:review_duration"}
		in := input(ir)
		ds := reviewDuration
		ds.Samples = []float64{0, 5, 10}
		in.Datasets = map[string]simulation.ResolvedDataset{"dataset:review_duration": ds}
		_, err := Compile(in)
		issueAt(t, err, "/model/nodes/0/arrival/distribution", validate.DistributionInvalid)
	})
	for name, c := range map[string]struct {
		edit       func(*simulation.RunInput)
		path, code string
	}{
		"warmup = horizon":     {func(in *simulation.RunInput) { in.Config.Warmup = in.Config.Horizon }, "/config/warmup", validate.LimitExceeded},
		"no replications":      {func(in *simulation.RunInput) { in.Config.Replications = 0 }, "/config/replications", validate.IRSchemaInvalid},
		"horizon over 365 d":   {func(in *simulation.RunInput) { in.Config.Horizon = simulation.Duration{Value: 366, Unit: model.Day} }, "/config", ""},
		"negative seed":        {func(in *simulation.RunInput) { in.Config.Seed = -1 }, "/config/seed", validate.IRSchemaInvalid},
		"unknown trace level":  {func(in *simulation.RunInput) { in.Config.TraceLevel = "full" }, "/config/trace_level", validate.IRSchemaInvalid},
		"event cap over limit": {func(in *simulation.RunInput) { in.Limits.MaxEvents = 10_000_001 }, "/limits/max_events", validate.IRSchemaInvalid},
		"no limits":            {func(in *simulation.RunInput) { in.Limits = simulation.ExecutionLimits{} }, "/limits", validate.IRSchemaInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			in := input(golden(t, "deterministic-queue.json"))
			c.edit(&in)
			_, err := Compile(in)
			issueAt(t, err, c.path, c.code)
		})
	}
}

// The program shares nothing with the caller's IR: editing the input after
// Compile changes nothing the replications read.
func TestCompileDoesNotAlias(t *testing.T) {
	in := input(golden(t, "deterministic-queue.json"))
	p := compile(t, in)
	before := p.nodes[p.index["arrival"]].arrival.ticks
	*in.Model.Nodes[0].Arrival.Distribution.Value = 999
	in.Model.Nodes[0].ID = "renamed"
	in.Model.Flows[0].Routing.Probability = 0
	if p.nodes[p.index["arrival"]].arrival.ticks != before || before != 600_000 || p.nodes[p.index["arrival"]].routes[0].cumulative != 1 {
		t.Error("the program changed with its input")
	}
}

// The engine stays pure: no DB, network, process, domain pack or LLM code.
func TestEngineDependencies(t *testing.T) {
	contracttest.OnlyDeps(t,
		"github.com/kcl1104/geminus/internal/model",
		"github.com/kcl1104/geminus/internal/model/hashing",
		"github.com/kcl1104/geminus/internal/model/validate",
		"github.com/kcl1104/geminus/internal/simulation",
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

// D-033 at compile time: an empirical service whose dataset has a 0 sample,
// inside a rework loop, is a zero-time cycle. The IR alone is valid; only
// the actual samples reveal it.
func TestCompileZeroTimeCycleFromDataset(t *testing.T) {
	ir := minimal(t)
	ir.Nodes[1].ServiceTime.Distribution = model.Distribution{Type: model.DistEmpirical, SamplesRef: "dataset:review_duration"}
	ir.Nodes = append(ir.Nodes, model.Node{ID: "d", Type: model.NodeDecision})
	ir.Flows[1].To = "d"
	ir.Flows = append(ir.Flows,
		model.Flow{ID: "d_back", From: "d", To: "work", Routing: model.Routing{Type: model.RoutingProbability, Probability: 0.5}},
		model.Flow{ID: "d_end", From: "d", To: "end", Routing: model.Routing{Type: model.RoutingProbability, Probability: 0.5}})
	in := input(ir)
	compile(t, in) // samples 120, 240, 360: fine
	ds := reviewDuration
	ds.Samples = []float64{0, 240, 360}
	in.Datasets = map[string]simulation.ResolvedDataset{"dataset:review_duration": ds}
	_, err := Compile(in)
	issueAt(t, err, "/model/nodes/1", validate.ZeroTimeCycle)
}
