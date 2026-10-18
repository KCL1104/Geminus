package conformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
)

// queuePack is a test-only generic queue domain (T-042): a service counter
// built from the same generic primitives as software delivery. It proves
// that a second domain needs no change to the IR, the validator, the engine
// or the renderers' data — and it is not a product domain: it exists only in
// this test file.
type queuePack struct{}

const queueDomain = "generic_queue"

var _ domainpack.DomainPack = queuePack{}

func (queuePack) ID() string { return queueDomain }

func (queuePack) NodeCatalog() []domainpack.NodeDefinition {
	return []domainpack.NodeDefinition{
		{Kind: "arrivals", Type: model.NodeSource, Label: "Arrivals", Description: "Customers arriving."},
		{Kind: "waiting_line", Type: model.NodeQueue, Label: "Waiting line", Description: "Customers waiting for a counter."},
		{Kind: "service_counter", Type: model.NodeProcess, Label: "Counter", Description: "Service by a clerk.", ResourceTypes: []string{"generic", "human"}},
		{Kind: "follow_up", Type: model.NodeDecision, Label: "Follow-up", Description: "Some customers queue again."},
		{Kind: "exit", Type: model.NodeSink, Label: "Exit", Description: "Customers leaving."},
	}
}

func (queuePack) ResourceCatalog() []domainpack.ResourceDefinition {
	return []domainpack.ResourceDefinition{{Kind: "clerk", Type: "generic", Label: "Clerks", Description: "People at the counters."}}
}

func (queuePack) Validate(ir *model.ModelIR) []model.ValidationIssue {
	if ir == nil {
		return []model.ValidationIssue{model.NewIssue(DomainMismatch, "", "no model", model.SeverityError)}
	}
	if ir.Domain != queueDomain {
		return []model.ValidationIssue{model.NewIssue(DomainMismatch, "/domain", fmt.Sprintf("domain %q is not %q", ir.Domain, queueDomain), model.SeverityError)}
	}
	return nil
}

func (queuePack) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return nil, domainpack.ErrUnavailable
}

// ComputeMetrics derives one metric from the generic queue statistics (the
// longest mean wait of any queue) and marks another unavailable, as a real
// pack must when its data is missing.
func (queuePack) ComputeMetrics(r *simulation.Result) map[string]simulation.MetricSummary {
	none := "NO_ABANDON_SINK"
	out := map[string]simulation.MetricSummary{
		"abandon_rate": {Status: simulation.Unavailable, Population: simulation.PopulationReplication, Unit: "percent", Reason: &none},
	}
	names := make([]string, 0, len(r.Metrics.Queues))
	for name := range r.Metrics.Queues {
		names = append(names, name)
	}
	slices.Sort(names)
	var best *simulation.QueueStats
	for _, name := range names {
		q := r.Metrics.Queues[name]
		if q.Status == simulation.Available && (best == nil || *q.WaitMeanMinutes > *best.WaitMeanMinutes) {
			best = &q
		}
	}
	if best == nil {
		reason := "NO_QUEUE_VISIT"
		out["longest_queue_wait_minutes"] = simulation.MetricSummary{Status: simulation.Unavailable, Population: simulation.PopulationStageVisit, Unit: "minute", Reason: &reason}
	} else {
		out["longest_queue_wait_minutes"] = simulation.MetricSummary{Status: simulation.Available, Mean: best.WaitMeanMinutes, P50: best.WaitP50Minutes,
			P95: best.WaitP95Minutes, SampleCount: best.SampleCount, Population: simulation.PopulationStageVisit, Unit: "minute"}
	}
	return out
}

func (p queuePack) PromptContext() domainpack.PromptContext {
	return domainpack.PromptContext{Domain: queueDomain, Summary: "Test-only: customers queue for clerks at a service counter.",
		Nodes: p.NodeCatalog(), Resources: p.ResourceCatalog(), Rules: []string{"Test-only domain; not a product."}}
}

func (queuePack) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	return domainpack.LayeredLayout(ir)
}

func runWithPack(in simulation.RunInput, p domainpack.DomainPack) (*simulation.Result, error) {
	return (&des.Engine{DomainMetrics: p.ComputeMetrics}).Run(context.Background(), in, nil)
}

func queueModel(t *testing.T) model.ModelIR {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "generic-queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ir model.ModelIR
	if err := model.Decode(data, &ir); err != nil {
		t.Fatal(err)
	}
	return ir
}

// TEST-001/TEST-002 and the T-042 boundary: a second, test-only domain runs
// on the one Generic Twin IR, generic validator, engine and view data.
func TestGenericQueuePackConforms(t *testing.T) {
	Run(t, Suite{Pack: queuePack{}, Models: []model.ModelIR{queueModel(t)}, Horizon: simulation.Duration{Value: 8, Unit: model.Hour}, Replications: 3})
}

// The pack's metric is derived from the engine's numbers, so the run above
// exercised an available domain metric, not only unavailable ones.
func TestGenericQueuePackHasAnAvailableMetric(t *testing.T) {
	c := &checker{s: Suite{Pack: queuePack{}, Models: []model.ModelIR{queueModel(t)}}}
	in, err := c.freeze(queueModel(t))
	if err != nil {
		t.Fatal(err)
	}
	res, err := runWithPack(in, queuePack{})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics.DORA["longest_queue_wait_minutes"]
	if m.Status != simulation.Available || m.Mean == nil || *m.Mean <= 0 || m.SampleCount == 0 {
		t.Fatalf("longest_queue_wait_minutes %+v", m)
	}
}

// Each invalid variant breaks the rule it is named for, on the queue model,
// so the domain-invariance check reaches every validator stage (T-042
// review N6): a validator that skipped a stage for an unknown domain would
// give the renamed copy other issues.
func TestInvalidVariantsBreakTheirRule(t *testing.T) {
	ir := queueModel(t)
	variants := invalidVariants(&ir)
	if len(variants) != len(variantCodes) {
		t.Errorf("%d variants for %d expected codes", len(variants), len(variantCodes))
	}
	for name, want := range variantCodes {
		v, ok := variants[name]
		if !ok {
			t.Errorf("%s: no variant", name)
			continue
		}
		if !slices.ContainsFunc(genericIssues(&v), func(is model.ValidationIssue) bool { return is.Code == want }) {
			t.Errorf("%s: no %s error, got %v", name, want, genericIssues(&v))
		}
	}
}

// The sentinel model is the suite model under other names: every ID and
// every reference carries the sentinel, and it is still a valid model
// (T-042 third review L3).
func TestSentinelModel(t *testing.T) {
	m := queueModel(t)
	m.Constraints = []model.Constraint{{Type: model.ConstraintOutgoingProbabilitySum, NodeID: "follow_up", Equals: 1}}
	m.Metrics = append(m.Metrics,
		model.Metric{ID: "busy", Type: model.MetricResourceUtilization, ResourceID: "clerk"},
		model.Metric{ID: "waiting", Type: model.MetricQueueWait, NodeID: "line"},
		model.Metric{ID: "left", Type: model.MetricSinkThroughput, SinkID: "leave"})
	errorsOf := func(ir *model.ModelIR) []model.ValidationIssue {
		var out []model.ValidationIssue
		for _, is := range allIssues(ir) {
			if is.Severity == model.SeverityError {
				out = append(out, is)
			}
		}
		return out
	}
	if errs := errorsOf(&m); len(errs) > 0 {
		t.Fatalf("the test model is invalid: %v", errs)
	}
	s := sentinelModel(&m)
	if errs := errorsOf(&s); len(errs) > 0 {
		t.Errorf("the sentinel model is invalid: %v", errs)
	}
	var ids []string
	for _, e := range s.EntityTypes {
		ids = append(ids, e.ID)
	}
	for _, r := range s.Resources {
		ids = append(ids, r.ID)
	}
	for _, n := range s.Nodes {
		ids = append(ids, n.ID)
		if n.EntityType != "" {
			ids = append(ids, n.EntityType)
		}
		for _, r := range n.Requires {
			ids = append(ids, r.ResourceID)
		}
	}
	for _, f := range s.Flows {
		ids = append(ids, f.ID, f.From, f.To)
	}
	for _, c := range s.Constraints {
		ids = append(ids, c.NodeID)
	}
	for _, mt := range s.Metrics {
		ids = append(ids, mt.ID)
		for _, ref := range []string{mt.SinkID, mt.ResourceID, mt.NodeID} {
			if ref != "" {
				ids = append(ids, ref)
			}
		}
	}
	for k := range s.Views.TwoD.Positions {
		ids = append(ids, k)
	}
	for _, id := range ids {
		if !strings.HasPrefix(id, sentinelID) {
			t.Errorf("%q does not carry the sentinel", id)
		}
	}
	if s.Name != sentinel || m.Nodes[0].ID != "arrivals" {
		t.Errorf("name %q; the original changed: %q", s.Name, m.Nodes[0].ID)
	}
}
