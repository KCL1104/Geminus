package domainpack_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

type fakePack struct{ id string }

func (p fakePack) ID() string                                     { return p.id }
func (fakePack) NodeCatalog() []domainpack.NodeDefinition         { return nil }
func (fakePack) ResourceCatalog() []domainpack.ResourceDefinition { return nil }
func (fakePack) Validate(*model.ModelIR) []model.ValidationIssue  { return nil }
func (fakePack) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return nil, domainpack.ErrUnavailable
}
func (fakePack) ComputeMetrics(*simulation.Result) map[string]simulation.MetricSummary { return nil }
func (fakePack) PromptContext() domainpack.PromptContext                               { return domainpack.PromptContext{} }
func (fakePack) DefaultViews(*model.ModelIR) domainpack.ViewDefinitions {
	return domainpack.ViewDefinitions{}
}

// REQ-003: an unregistered domain fails closed.
func TestRegistry(t *testing.T) {
	r, err := domainpack.NewRegistry(fakePack{"b"}, fakePack{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Domains(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("domains %v", got)
	}
	if p, err := r.Lookup("a"); err != nil || p.ID() != "a" {
		t.Errorf("lookup a: %v %v", p, err)
	}
	if _, err := r.Lookup("manufacturing"); !errors.Is(err, domainpack.ErrUnsupportedDomain) {
		t.Errorf("unknown domain: %v", err)
	}
	if _, err := domainpack.NewRegistry(fakePack{"a"}, fakePack{"a"}); err == nil {
		t.Error("duplicate ID accepted")
	}
	if _, err := domainpack.NewRegistry(fakePack{""}); err == nil {
		t.Error("empty ID accepted")
	}
}

func minimal(t *testing.T) *model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-minimal-ir.json")), &ir); err != nil {
		t.Fatal(err)
	}
	return &ir
}

func TestLayeredLayout(t *testing.T) {
	ir := minimal(t)
	got := domainpack.LayeredLayout(ir).Positions
	want := map[string]model.Position{"start": {X: 0, Y: 0}, "work": {X: 240, Y: 0}, "end": {X: 480, Y: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layout %v, want %v", got, want)
	}

	// Existing positions stay; an unreachable node goes past the deepest
	// layer; a rework loop terminates; nothing depends on map order.
	ir.Views.TwoD.Positions = map[string]model.Position{"work": {X: 7, Y: 9}}
	ir.Nodes = append(ir.Nodes, model.Node{ID: "orphan", Type: model.NodeSink}, model.Node{ID: "check", Type: model.NodeDecision})
	ir.Flows = append(ir.Flows, model.Flow{ID: "loop", From: "end", To: "start"}, model.Flow{ID: "wire", From: "work", To: "check"})
	first := domainpack.LayeredLayout(ir).Positions
	for range 20 {
		if again := domainpack.LayeredLayout(ir).Positions; !reflect.DeepEqual(again, first) {
			t.Fatal("layout is not deterministic")
		}
	}
	if first["work"] != (model.Position{X: 7, Y: 9}) {
		t.Errorf("existing position replaced: %v", first["work"])
	}
	if first["orphan"] != (model.Position{X: 3 * domainpack.LayerSpacing, Y: 0}) {
		t.Errorf("unreachable node at %v", first["orphan"])
	}
	if first["check"] != (model.Position{X: 2 * domainpack.LayerSpacing, Y: domainpack.RowSpacing}) {
		t.Errorf("second node of layer 2 at %v", first["check"])
	}
	if len(first) != len(ir.Nodes) {
		t.Errorf("%d positions for %d nodes", len(first), len(ir.Nodes))
	}
}

// TEST-001: the canonical event type matches its schema and fixture.
func TestCanonicalEventContract(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	s.Parity(t, reflect.TypeFor[domainpack.CanonicalEvent](), "canonical-event.schema.json#")
	contracttest.RoundTrip[domainpack.CanonicalEvent](t, "valid-canonical-event",
		contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-canonical-event.json")))
}

// The generic boundary knows no domain, HTTP, database or LLM.
func TestDependencies(t *testing.T) {
	contracttest.OnlyDeps(t, "github.com/kcl1104/geminus/internal/model", "github.com/kcl1104/geminus/internal/simulation")
}

// Review of T-011: nil inputs are errors or empty results, never panics.
func TestNilInputs(t *testing.T) {
	if _, err := domainpack.NewRegistry(fakePack{"a"}, nil); err == nil {
		t.Error("nil pack accepted")
	}
	var r *domainpack.Registry
	if _, err := r.Lookup("a"); !errors.Is(err, domainpack.ErrUnsupportedDomain) {
		t.Errorf("nil registry: %v", err)
	}
	if got := r.Domains(); len(got) != 0 {
		t.Errorf("nil registry domains %v", got)
	}
	if got := domainpack.LayeredLayout(nil).Positions; len(got) != 0 {
		t.Errorf("nil IR layout %v", got)
	}
}

// A node added to a saved layout is never placed on top of a kept node.
func TestLayoutAvoidsKeptPositions(t *testing.T) {
	ir := minimal(t)
	ir.Views.TwoD.Positions = domainpack.LayeredLayout(ir).Positions // start (0,0), work (240,0), end (480,0)
	ir.Nodes = append(ir.Nodes, model.Node{ID: "review", Type: model.NodeQueue}, model.Node{ID: "audit", Type: model.NodeQueue})
	ir.Flows[0].To = "review"
	ir.Flows = append(ir.Flows, model.Flow{ID: "review_work", From: "review", To: "work"}, model.Flow{ID: "start_audit", From: "start", To: "audit"})
	got := domainpack.LayeredLayout(ir).Positions
	seen := map[model.Position]string{}
	for _, n := range ir.Nodes {
		if other, dup := seen[got[n.ID]]; dup {
			t.Errorf("%s and %s share %v", n.ID, other, got[n.ID])
		}
		seen[got[n.ID]] = n.ID
	}
	if got["review"].X != domainpack.LayerSpacing {
		t.Errorf("review is in layer 1 but at %v", got["review"])
	}
}

// Flows to unknown nodes are ignored and a duplicated ID (an invalid IR) is
// placed once, by its first occurrence.
func TestLayoutTolerance(t *testing.T) {
	ir := minimal(t)
	ir.Flows = append(ir.Flows, model.Flow{ID: "ghost", From: "start", To: "nowhere"}, model.Flow{ID: "ghost2", From: "nowhere", To: "end"})
	base := domainpack.LayeredLayout(minimal(t)).Positions
	if got := domainpack.LayeredLayout(ir).Positions; !reflect.DeepEqual(got, base) {
		t.Errorf("flows to unknown nodes changed the layout: %v", got)
	}
	ir = minimal(t)
	ir.Nodes = append(ir.Nodes, model.Node{ID: "start", Type: model.NodeSink})
	if got := domainpack.LayeredLayout(ir).Positions; got["start"] != base["start"] || len(got) != 3 {
		t.Errorf("duplicate ID: %v", got)
	}
}
