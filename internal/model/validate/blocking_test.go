package validate_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// D-050 R1 BLOCKING_CYCLE (graph stage, TWIN_IR_SPEC §4; T-047): a process
// that needs a resource (a holder) keeps its bundle while blocked; it has a
// blocking edge to every process whose finite buffer its route can reach
// over p > 0 flows, through decisions, failure and rework nodes and at most
// one queue. Processes needing a common resource are one component; a
// cycle of the edges between components is refused at every finite queue
// on it.

type lineModel struct {
	resources []model.Resource
	nodes     []model.Node
	flows     []model.Flow
}

func (m *lineModel) res(ids ...string) *lineModel {
	for _, id := range ids {
		m.resources = append(m.resources, model.Resource{ID: id, Type: model.ResourceMachine, Name: id, Capacity: 1, QueueDiscipline: model.FIFO})
	}
	return m
}

func (m *lineModel) source(id string) *lineModel {
	v := 10.0
	m.nodes = append(m.nodes, model.Node{ID: id, Type: model.NodeSource, EntityType: "change",
		Arrival: &model.Arrival{Distribution: model.Distribution{Type: model.DistConstant, Value: &v}}})
	return m
}

func (m *lineModel) process(id string, resources ...string) *lineModel {
	v := 1.0
	n := model.Node{ID: id, Type: model.NodeProcess, ServiceTime: &model.TimedDistribution{Distribution: model.Distribution{Type: model.DistConstant, Value: &v}},
		Requires: []model.ResourceRequirement{}}
	for _, r := range resources {
		n.Requires = append(n.Requires, model.ResourceRequirement{ResourceID: r, Units: 1})
	}
	m.nodes = append(m.nodes, n)
	return m
}

func (m *lineModel) queue(id string, capacity int) *lineModel {
	m.nodes = append(m.nodes, model.Node{ID: id, Type: model.NodeQueue, Capacity: capacity})
	return m
}

func (m *lineModel) node(id string, typ model.NodeType) *lineModel {
	n := model.Node{ID: id, Type: typ}
	if typ == model.NodeDelay {
		v := 2.0
		n.DelayTime = &model.TimedDistribution{Distribution: model.Distribution{Type: model.DistConstant, Value: &v}}
	}
	m.nodes = append(m.nodes, n)
	return m
}

// to adds flows from -> each target with the probabilities given after it
// (all 1 without any).
func (m *lineModel) to(from string, targets ...any) *lineModel {
	for i := 0; i < len(targets); i++ {
		to := targets[i].(string)
		p := 1.0
		if i+1 < len(targets) {
			if f, ok := targets[i+1].(float64); ok {
				p = f
				i++
			}
		}
		m.flows = append(m.flows, model.Flow{ID: from + "__" + to, From: from, To: to, Routing: model.Routing{Type: model.RoutingProbability, Probability: p}})
	}
	return m
}

func (m *lineModel) ir() model.ModelIR {
	return model.ModelIR{SchemaVersion: "0.1", ModelID: "mdl_01M4YDQK00R942EDGA45T538HS", Domain: "software_delivery", Name: "R1 case",
		TimeUnit: model.ModelTimeUnit(model.Minute), EntityTypes: []model.EntityType{{ID: "change", Name: "Change"}},
		Resources: m.resources, Nodes: m.nodes, Flows: m.flows, Constraints: []model.Constraint{}, Metrics: []model.Metric{}}
}

func validateModel(t *testing.T, ir model.ModelIR) validate.Result {
	t.Helper()
	data, err := model.Encode(&ir)
	if err != nil {
		t.Fatal(err)
	}
	return run(t, data)
}

func cycles(r validate.Result) []string {
	var out []string
	for _, is := range r.Issues {
		if is.Code == validate.BlockingCycle {
			out = append(out, is.Path)
		}
	}
	return out
}

func TestBlockingCycles(t *testing.T) {
	cases := []struct {
		name  string
		model *lineModel
		want  []string // BLOCKING_CYCLE paths; nil: the model is valid
	}{
		{"a line of finite buffers", new(lineModel).res("ra", "rb", "rc").source("src").process("a", "ra").queue("qb", 2).process("b", "rb").
			queue("qc", 1).process("c", "rc").node("end", model.NodeSink).to("src", "a").to("a", "qb").to("qb", "b").to("b", "qc").to("qc", "c").to("c", "end"), nil},
		{"a retest loop into the process's own finite buffer", new(lineModel).res("r").source("src").queue("q", 3).process("p", "r").node("d", model.NodeDecision).
			node("end", model.NodeSink).to("src", "q").to("q", "p").to("p", "d").to("d", "end", 0.8, "q", 0.2), []string{"/nodes/1/capacity"}},
		{"a retest loop through an unlimited queue before a finite one", new(lineModel).res("rt", "rp").source("src").queue("q1", 0).process("test", "rt").
			node("d", model.NodeDecision).queue("qp", 2).process("pack", "rp").node("end", model.NodeSink).
			to("src", "q1").to("q1", "test").to("test", "d").to("d", "qp", 0.9, "q1", 0.1).to("qp", "pack").to("pack", "end"), nil},
		{"two finite buffers in a loop", new(lineModel).res("ra", "rb").source("src").queue("qa", 1).process("a", "ra").queue("qb", 1).process("b", "rb").
			node("d", model.NodeDecision).node("end", model.NodeSink).
			to("src", "qa").to("qa", "a").to("a", "qb").to("qb", "b").to("b", "d").to("d", "end", 0.5, "qa", 0.5), []string{"/nodes/1/capacity", "/nodes/3/capacity"}},
		{"a loop through one finite and one unlimited buffer", new(lineModel).res("ra", "rb").source("src").queue("qa", 0).process("a", "ra").queue("qb", 1).process("b", "rb").
			node("d", model.NodeDecision).node("end", model.NodeSink).
			to("src", "qa").to("qa", "a").to("a", "qb").to("qb", "b").to("b", "d").to("d", "end", 0.5, "qa", 0.5), nil},
		{"a shared resource, no flow cycle", new(lineModel).res("worker").source("src").process("a", "worker").queue("qp", 2).process("p", "worker").
			node("end", model.NodeSink).to("src", "a").to("a", "qp").to("qp", "p").to("p", "end"), []string{"/nodes/2/capacity"}},
		{"components merged through a chain of resources", new(lineModel).res("r1", "r2").source("src").process("a", "r1").process("b", "r1", "r2").
			queue("qc", 1).process("c", "r2").node("end", model.NodeSink).
			to("src", "a").to("a", "qc").to("qc", "c").to("c", "b").to("b", "end"), []string{"/nodes/3/capacity"}},
		{"a route straight into the process, bypassing its finite queue", new(lineModel).res("ra", "rp").source("src").queue("qp", 1).process("a", "ra").
			process("p", "rp").node("d", model.NodeDecision).node("end", model.NodeSink).
			to("src", "qp").to("qp", "p").to("p", "a").to("a", "d").to("d", "p", 0.5, "end", 0.5),
			nil}, // a -> p is a blocking edge, p -> a is not (a's buffer is unlimited): no cycle
		{"a route straight into the process of a shared resource", new(lineModel).res("r").source("src").queue("qp", 1).process("a", "r").
			process("p", "r").node("d", model.NodeDecision).node("end", model.NodeSink).
			to("src", "qp").to("qp", "p").to("p", "a").to("a", "d").to("d", "p", 0.5, "end", 0.5),
			[]string{"/nodes/1/capacity"}},
		{"through failure and rework nodes", new(lineModel).res("r").source("src").queue("q", 2).process("p", "r").node("f", model.NodeFailure).
			node("w", model.NodeRework).node("d", model.NodeDecision).node("end", model.NodeSink).
			to("src", "q").to("q", "p").to("p", "d").to("d", "end", 0.5, "f", 0.5).to("f", "w").to("w", "q"), []string{"/nodes/1/capacity"}},
		{"a delay releases the bundle first", new(lineModel).res("r").source("src").queue("q", 2).process("p", "r").node("d", model.NodeDecision).
			node("wait", model.NodeDelay).node("end", model.NodeSink).
			to("src", "q").to("q", "p").to("p", "d").to("d", "end", 0.5, "wait", 0.5).to("wait", "q"), nil},
		{"a process without resources holds nothing", new(lineModel).source("src").queue("q", 1).process("p").node("d", model.NodeDecision).
			node("end", model.NodeSink).to("src", "q").to("q", "p").to("p", "d").to("d", "end", 0.5, "q", 0.5), nil},
		{"a 0% flow is not a route", new(lineModel).res("r").source("src").queue("q", 1).process("p", "r").node("d", model.NodeDecision).
			node("end", model.NodeSink).to("src", "q").to("q", "p").to("p", "d").to("d", "end", 1.0, "q", 0.0), nil},
		{"an unreachable cycle is refused too", new(lineModel).res("r", "s").source("src").process("x", "s").node("end", model.NodeSink).
			queue("q", 1).process("p", "r").node("d", model.NodeDecision).
			to("src", "x").to("x", "end").to("q", "p").to("p", "d").to("d", "end", 0.5, "q", 0.5), []string{"/nodes/3/capacity"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := validateModel(t, c.model.ir())
			got := cycles(r)
			if c.want == nil {
				if !r.Valid() {
					t.Errorf("want valid, got %v", r.Issues)
				}
				return
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("BLOCKING_CYCLE at %v, want %v (issues %v)", got, c.want, r.Issues)
			}
			for _, is := range r.Issues {
				if is.Code == validate.BlockingCycle && is.Severity != model.SeverityError {
					t.Errorf("not an error: %+v", is)
				}
			}
		})
	}
}

// The message names the cycle's processes, its finite buffers and the
// resources that merged its components, in ID order, and says how to fix
// it (ADR-026 D-050 R1); the order of the IR's arrays does not matter.
func TestBlockingCycleMessage(t *testing.T) {
	m := new(lineModel).res("r2", "r1").source("src").process("b", "r1", "r2").process("a", "r1").queue("qc", 1).process("c", "r2").
		node("end", model.NodeSink).to("src", "a").to("a", "qc").to("qc", "c").to("c", "b").to("b", "end")
	r := validateModel(t, m.ir())
	if len(r.Issues) != 1 || r.Issues[0].Code != validate.BlockingCycle || r.Issues[0].Path != "/nodes/3/capacity" {
		t.Fatalf("issues %v", r.Issues)
	}
	msg := r.Issues[0].Message
	for _, want := range []string{"qc", "a, b, c", "r1, r2", "unlimited", "share"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not say %q", msg, want)
		}
	}
	// Reversed arrays: the same issue at the queue's new index, the same message.
	rev := m.ir()
	slices.Reverse(rev.Nodes)
	slices.Reverse(rev.Flows)
	slices.Reverse(rev.Resources)
	r2 := validateModel(t, rev)
	if len(r2.Issues) != 1 || r2.Issues[0].Message != msg || r2.Issues[0].Path != fmt.Sprintf("/nodes/%d/capacity", len(rev.Nodes)-1-3) {
		t.Errorf("reversed: %v", r2.Issues)
	}
}

// R1 is part of the graph stage (D-023): an earlier stage's error hides
// it, and it hides the later stages' errors.
func TestBlockingCycleStage(t *testing.T) {
	cyclic := func() model.ModelIR {
		return new(lineModel).res("r").source("src").queue("q", 3).process("p", "r").node("d", model.NodeDecision).
			node("end", model.NodeSink).to("src", "q").to("q", "p").to("p", "d").to("d", "end", 0.8, "q", 0.2).ir()
	}
	later := cyclic()
	min, mode, max := 3.0, 2.0, 1.0 // a distribution error (later stage)
	later.Nodes[2].ServiceTime.Distribution = model.Distribution{Type: model.DistTriangular, Min: &min, Mode: &mode, Max: &max}
	if r := validateModel(t, later); !slices.Equal(cycles(r), []string{"/nodes/1/capacity"}) || len(r.Issues) != 1 {
		t.Errorf("with a later-stage error: %v", r.Issues)
	}
	earlier := cyclic()
	earlier.Flows[0].To = "nowhere"
	if r := validateModel(t, earlier); len(cycles(r)) != 0 || r.Valid() {
		t.Errorf("with an earlier-stage error: %v", r.Issues)
	}
	same := cyclic()
	same.Nodes = append(same.Nodes, model.Node{ID: "lost", Type: model.NodeSink}) // a warning of the same stage
	if r := validateModel(t, same); len(cycles(r)) != 1 || !slices.ContainsFunc(r.Issues, func(i model.ValidationIssue) bool { return i.Code == validate.UnreachableNode }) {
		t.Errorf("with a same-stage warning: %v", r.Issues)
	}
}
