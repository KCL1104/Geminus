package conformance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// Each broken pack differs from queuePack in one method and must produce a
// finding of the named check: the suite proves the properties it claims.

type badID struct{ queuePack }

func (badID) ID() string { return "" }

type otherID struct{ queuePack }

func (otherID) ID() string { return "generic_queue_v2" }

type dupKind struct{ queuePack }

func (p dupKind) NodeCatalog() []domainpack.NodeDefinition {
	n := p.queuePack.NodeCatalog()
	return append(n, n[0])
}

type nonGenericType struct{ queuePack }

func (p nonGenericType) NodeCatalog() []domainpack.NodeDefinition {
	n := p.queuePack.NodeCatalog()
	n[2].Type = "teller"
	return n
}

type badRequirement struct{ queuePack }

func (p badRequirement) NodeCatalog() []domainpack.NodeDefinition {
	n := p.queuePack.NodeCatalog()
	n[2].ResourceTypes = []string{"robot"}
	return n
}

var sharedNodes = queuePack{}.NodeCatalog()

type sharedCatalog struct{ queuePack }

func (sharedCatalog) NodeCatalog() []domainpack.NodeDefinition { return sharedNodes }
func (p sharedCatalog) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Nodes = p.NodeCatalog()
	return c
}

type badResourceType struct{ queuePack }

func (badResourceType) ResourceCatalog() []domainpack.ResourceDefinition {
	return []domainpack.ResourceDefinition{{Kind: "clerk", Type: "robot", Label: "Clerks"}}
}
func (p badResourceType) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Resources = p.ResourceCatalog()
	return c
}

type acceptsAnything struct{ queuePack }

func (acceptsAnything) Validate(*model.ModelIR) []model.ValidationIssue { return nil }

type rejectsValid struct{ queuePack }

func (rejectsValid) Validate(*model.ModelIR) []model.ValidationIssue {
	return []model.ValidationIssue{model.NewIssue("NOPE", "/nodes", "no", model.SeverityError)}
}

type mutatingValidate struct{ queuePack }

func (p mutatingValidate) Validate(ir *model.ModelIR) []model.ValidationIssue {
	if ir != nil {
		ir.Name += "!"
	}
	return p.queuePack.Validate(ir)
}

type flakyValidate struct {
	queuePack
	n *atomic.Int64
}

func (p flakyValidate) Validate(ir *model.ModelIR) []model.ValidationIssue {
	out := p.queuePack.Validate(ir)
	if p.n.Add(1)%2 == 0 {
		out = append(out, model.NewIssue("HINT", "/name", "a hint", model.SeverityWarning))
	}
	return out
}

type panicsOnNil struct{ queuePack }

func (p panicsOnNil) Validate(ir *model.ModelIR) []model.ValidationIssue {
	_ = ir.Domain
	return p.queuePack.Validate(ir)
}

type missingNode struct{ queuePack }

func (p missingNode) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	delete(v.Positions, ir.Nodes[0].ID)
	return v
}

type ghostNode struct{ queuePack }

func (p ghostNode) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	v.Positions["ghost"] = model.Position{X: 1e6, Y: 1e6}
	return v
}

type movesSaved struct{ queuePack }

func (p movesSaved) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	c := *ir
	c.Views.TwoD.Positions = nil
	return p.queuePack.DefaultViews(&c)
}

type overlapping struct{ queuePack }

func (overlapping) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := domainpack.ViewDefinitions{Positions: map[string]model.Position{}}
	for _, n := range ir.Nodes {
		v.Positions[n.ID] = model.Position{}
	}
	for id, p := range ir.Views.TwoD.Positions {
		v.Positions[id] = p
	}
	return v
}

type mutatingViews struct{ queuePack }

func (p mutatingViews) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	ir.Views.TwoD.Positions = v.Positions
	return v
}

type mutatingMetrics struct{ queuePack }

func (p mutatingMetrics) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	r.Metrics.FailureCount.SampleCount++
	return p.queuePack.ComputeMetrics(r)
}

type invalidMetric struct{ queuePack }

func (invalidMetric) ComputeMetrics(*simulation.Result) map[string]simulation.MetricSummary {
	return map[string]simulation.MetricSummary{"broken": {Status: simulation.Available, Population: simulation.PopulationReplication, Unit: "x", SampleCount: 1}}
}

type emptyKey struct{ queuePack }

func (p emptyKey) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	out := p.queuePack.ComputeMetrics(r)
	out[""] = out["abandon_rate"]
	return out
}

type flakyMetrics struct {
	queuePack
	n *atomic.Int64
}

func (p flakyMetrics) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	v := float64(p.n.Add(1))
	return map[string]simulation.MetricSummary{"calls": {Status: simulation.Available, Mean: &v, P50: &v, P95: &v, SampleCount: 1,
		Population: simulation.PopulationReplication, Unit: "call"}}
}

type nilNil struct{ queuePack }

func (nilNil) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return nil, nil
}

type ignoresCancel struct {
	queuePack
	m model.ModelIR
}

func (p ignoresCancel) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	m := clone(&p.m)
	return &domainpack.InferenceResult{Model: &m}, nil
}

type proposesForeign struct {
	queuePack
	m model.ModelIR
}

func (p proposesForeign) InferFromEvents(ctx context.Context, _ domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := clone(&p.m)
	m.Domain = "software_delivery"
	return &domainpack.InferenceResult{Model: &m}, nil
}

type proposesInvalid struct {
	queuePack
	m model.ModelIR
}

func (p proposesInvalid) InferFromEvents(ctx context.Context, _ domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := clone(&p.m)
	m.Flows[0].To = "nowhere"
	return &domainpack.InferenceResult{Model: &m}, nil
}

type unavailableWithResult struct{ queuePack }

func (unavailableWithResult) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return &domainpack.InferenceResult{}, domainpack.ErrUnavailable
}

type promptOtherDomain struct{ queuePack }

func (p promptOtherDomain) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Domain = "software_delivery"
	return c
}

type promptDropsNodes struct{ queuePack }

func (p promptDropsNodes) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Nodes = c.Nodes[1:]
	return c
}

type promptChanges struct {
	queuePack
	n *atomic.Int64
}

func (p promptChanges) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	if p.n.Add(1)%2 == 0 {
		c.Rules = append(c.Rules, "and another")
	}
	return c
}

type flakyID struct{ n *atomic.Int64 }

func (p flakyID) ID() string {
	if p.n.Add(1) > 1 {
		return queueDomain + "_changed"
	}
	return queueDomain
}

type flakyIDPack struct {
	queuePack
	flakyID
}

func (p flakyIDPack) ID() string { return p.flakyID.ID() }

type emptyCatalog struct{ queuePack }

func (emptyCatalog) NodeCatalog() []domainpack.NodeDefinition { return nil }
func (emptyCatalog) PromptContext() domainpack.PromptContext {
	c := queuePack{}.PromptContext()
	c.Nodes = nil
	return c
}

type noLabel struct{ queuePack }

func (p noLabel) NodeCatalog() []domainpack.NodeDefinition {
	n := p.queuePack.NodeCatalog()
	n[0].Label = ""
	return n
}

type dupResource struct{ queuePack }

func (p dupResource) ResourceCatalog() []domainpack.ResourceDefinition {
	r := p.queuePack.ResourceCatalog()
	return append(r, r[0])
}

type flakyViews struct {
	queuePack
	n *atomic.Int64
}

func (p flakyViews) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	if p.n.Add(1)%2 == 0 {
		for id, pos := range v.Positions {
			if _, saved := ir.Views.TwoD.Positions[id]; !saved {
				v.Positions[id] = model.Position{X: pos.X + 0.5, Y: pos.Y}
			}
		}
	}
	return v
}

type unencodableViews struct{ queuePack }

func (p unencodableViews) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	v := p.queuePack.DefaultViews(ir)
	for id := range v.Positions {
		if _, saved := ir.Views.TwoD.Positions[id]; !saved {
			v.Positions[id] = model.Position{X: math.Inf(1), Y: float64(len(id))}
		}
	}
	return v
}

type emptySummary struct{ queuePack }

func (p emptySummary) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Summary = ""
	return c
}

type emptyRule struct{ queuePack }

func (p emptyRule) PromptContext() domainpack.PromptContext {
	c := p.queuePack.PromptContext()
	c.Rules = append(c.Rules, "")
	return c
}

type panicsEverywhere struct{ queuePack }

func (panicsEverywhere) NodeCatalog() []domainpack.NodeDefinition { panic("boom") }

func TestBrokenPacksAreCaught(t *testing.T) {
	m := queueModel(t)
	cases := []struct {
		pack  domainpack.DomainPack
		check string
	}{
		{badID{}, "identity/id"},
		{otherID{}, "identity/models"},
		{dupKind{}, "catalog/nodes"},
		{nonGenericType{}, "catalog/nodes"},
		{badRequirement{}, "catalog/nodes"},
		{sharedCatalog{}, "catalog/copies"},
		{badResourceType{}, "catalog/resources"},
		{acceptsAnything{}, "validate/fail-closed"},
		{acceptsAnything{}, "validate/nil"},
		{rejectsValid{}, "validate/valid-models"},
		{mutatingValidate{}, "validate/read-only"},
		{flakyValidate{n: new(atomic.Int64)}, "validate/deterministic"},
		{panicsOnNil{}, "validate/nil"},
		{missingNode{}, "views/covers-nodes"},
		{ghostNode{}, "views/same-ids"},
		{movesSaved{}, "views/keeps-saved"},
		{overlapping{}, "views/no-overlap"},
		{mutatingViews{}, "views/read-only"},
		{mutatingMetrics{}, "metrics/read-only"},
		{mutatingMetrics{}, "metrics/generic-unchanged"},
		{invalidMetric{}, "metrics/schema"},
		{emptyKey{}, "metrics/keys"},
		{flakyMetrics{n: new(atomic.Int64)}, "metrics/deterministic"},
		{nilNil{}, "inference/answer"},
		{ignoresCancel{m: m}, "inference/cancel"},
		{proposesForeign{m: m}, "inference/answer"},
		{unavailableWithResult{}, "inference/answer"},
		{proposesInvalid{m: m}, "inference/answer"},
		{promptOtherDomain{}, "prompt/context"},
		{promptDropsNodes{}, "prompt/catalogs"},
		{promptChanges{n: new(atomic.Int64)}, "prompt/static"},
		{panicsEverywhere{}, "catalog/nodes"},
		{flakyIDPack{flakyID: flakyID{n: new(atomic.Int64)}}, "identity/id"},
		{emptyCatalog{}, "catalog/nodes"},
		{noLabel{}, "catalog/nodes"},
		{dupResource{}, "catalog/resources"},
		{flakyViews{n: new(atomic.Int64)}, "views/deterministic"},
		{unencodableViews{}, "views/valid-ir"},
		{emptySummary{}, "prompt/context"},
		{emptyRule{}, "prompt/context"},
	}
	for _, c := range cases {
		name := typeName(c.pack)
		t.Run(name+"/"+c.check, func(t *testing.T) {
			findings, err := Check(context.Background(), Suite{Pack: c.pack, Models: []model.ModelIR{m}, Horizon: simulation.Duration{Value: 4, Unit: model.Hour}})
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range findings {
				if f.Check == c.check {
					return
				}
			}
			t.Errorf("no %s finding; got %v", c.check, findings)
		})
	}
	// The correct pack has none.
	if findings, err := Check(context.Background(), Suite{Pack: queuePack{}, Models: []model.ModelIR{m}}); err != nil || len(findings) > 0 {
		t.Errorf("queuePack: %v %v", err, findings)
	}
}

// A model the generic validator accepts but the engine cannot run (an
// empirical distribution without its dataset) is a finding with the
// engine's issues, not a pass.
func TestUnrunnableModel(t *testing.T) {
	m := queueModel(t)
	m.Nodes[2].ServiceTime.Distribution = model.Distribution{Type: model.DistEmpirical, SamplesRef: "dataset:missing"}
	findings, err := Check(context.Background(), Suite{Pack: queuePack{}, Models: []model.ModelIR{m}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 || findings[0].Check != "metrics/run" || !strings.Contains(findings[0].Detail, "dataset:missing") {
		t.Errorf("findings %v", findings)
	}
}

// Run turns findings into test failures: the helper below runs a broken
// pack through Run in a child test process, which must fail and name the
// finding.
func TestRunReportsFindings(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperRunBrokenPack$", "-test.count=1")
	cmd.Env = append(os.Environ(), "CONFORMANCE_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "does not conform: validate/fail-closed") {
		t.Errorf("Run passed a broken pack (err %v):\n%s", err, out)
	}
}

func TestHelperRunBrokenPack(t *testing.T) {
	if os.Getenv("CONFORMANCE_HELPER") != "1" {
		return // only meaningful in the child process of TestRunReportsFindings
	}
	Run(t, Suite{Pack: acceptsAnything{}, Models: []model.ModelIR{queueModel(t)}})
}

// A suite without models, or with a model the generic validator rejects,
// is an error rather than a pass.
func TestInvalidSuites(t *testing.T) {
	bad := queueModel(t)
	bad.Flows[0].To = "nowhere"
	for _, s := range []Suite{{Pack: queuePack{}}, {Models: []model.ModelIR{queueModel(t)}}, {Pack: queuePack{}, Models: []model.ModelIR{bad}}} {
		if _, err := Check(context.Background(), s); !errors.Is(err, ErrSuite) {
			t.Errorf("%+v: %v", s.Pack, err)
		}
	}
}

// The concurrency check reports goroutines that disagree with a sequential
// call (data races themselves are for -race).
func TestConcurrentDisagreement(t *testing.T) {
	m := queueModel(t)
	c := &checker{s: Suite{Pack: promptChanges{n: new(atomic.Int64)}, Models: []model.ModelIR{m}}}
	c.concurrent()
	if len(c.findings) == 0 || c.findings[0].Check != "concurrent" {
		t.Errorf("findings %v", c.findings)
	}
	c = &checker{s: Suite{Pack: panicsLater{n: new(atomic.Int64)}, Models: []model.ModelIR{m}}}
	c.concurrent()
	if len(c.findings) == 0 || !strings.Contains(c.findings[0].Detail, "panicked") {
		t.Errorf("panicking goroutines: %v", c.findings)
	}
	c = &checker{s: Suite{Pack: queuePack{}, Models: []model.ModelIR{m}}}
	c.concurrent()
	if len(c.findings) != 0 {
		t.Errorf("queuePack: %v", c.findings)
	}
}

type panicsLater struct {
	queuePack
	n *atomic.Int64
}

func (p panicsLater) PromptContext() domainpack.PromptContext {
	if p.n.Add(1) > 1 {
		panic("second call")
	}
	return p.queuePack.PromptContext()
}

func typeName(v any) string { return strings.TrimPrefix(fmt.Sprintf("%T", v), "conformance.") }
