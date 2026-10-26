package conformance

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// modelMetrics is queuePack with model metrics (D-058's
// ModelMetricsProvider): one metric from the IR (the number of process
// nodes), one from the Result (replication 0's completed count) and one
// unavailable, as a real pack must when its data is missing.
type modelMetrics struct{ queuePack }

var _ domainpack.ModelMetricsProvider = modelMetrics{}

func (modelMetrics) ComputeModelMetrics(ir *model.ModelIR, r *simulation.Result) map[string]simulation.MetricSummary {
	if ir == nil || r == nil || len(r.ReplicationSummaries) == 0 {
		return nil
	}
	processes := 0.0
	for _, n := range ir.Nodes {
		if n.Type == model.NodeProcess {
			processes++
		}
	}
	done := float64(r.ReplicationSummaries[0].CompletedCount)
	none := "NO_CLERK_SCHEDULE"
	one := func(v *float64) simulation.MetricSummary {
		return simulation.MetricSummary{Status: simulation.Available, Mean: v, P50: v, P95: v, SampleCount: 1, Population: simulation.PopulationReplication, Unit: "count"}
	}
	return map[string]simulation.MetricSummary{"counters": one(&processes), "served_in_first_replication": one(&done),
		"scheduled_hours": {Status: simulation.Unavailable, Population: simulation.PopulationReplication, Unit: "hour", Reason: &none}}
}

type modelMetricsMutateIR struct{ modelMetrics }

func (p modelMetricsMutateIR) ComputeModelMetrics(ir *model.ModelIR, r *simulation.Result) map[string]simulation.MetricSummary {
	if ir != nil {
		ir.Name += "!"
	}
	return p.modelMetrics.ComputeModelMetrics(ir, r)
}

type modelMetricsMutateResult struct{ modelMetrics }

func (p modelMetricsMutateResult) ComputeModelMetrics(ir *model.ModelIR, r *simulation.Result) map[string]simulation.MetricSummary {
	if r != nil {
		r.Metrics.FailureCount.SampleCount++
	}
	return p.modelMetrics.ComputeModelMetrics(ir, r)
}

type modelMetricInvalid struct{ modelMetrics }

func (modelMetricInvalid) ComputeModelMetrics(*model.ModelIR, *simulation.Result) map[string]simulation.MetricSummary {
	return map[string]simulation.MetricSummary{"broken": {Status: simulation.Available, Population: simulation.PopulationReplication, Unit: "x", SampleCount: 1}}
}

type modelMetricEmptyKey struct{ modelMetrics }

func (p modelMetricEmptyKey) ComputeModelMetrics(ir *model.ModelIR, r *simulation.Result) map[string]simulation.MetricSummary {
	out := p.modelMetrics.ComputeModelMetrics(ir, r)
	out[""] = out["counters"]
	return out
}

type modelMetricsCount struct {
	modelMetrics
	n *atomic.Int64
}

func (p modelMetricsCount) ComputeModelMetrics(*model.ModelIR, *simulation.Result) map[string]simulation.MetricSummary {
	v := float64(p.n.Add(1))
	return map[string]simulation.MetricSummary{"calls": {Status: simulation.Available, Mean: &v, P50: &v, P95: &v, SampleCount: 1,
		Population: simulation.PopulationReplication, Unit: "call"}}
}

type modelMetricsPanic struct{ modelMetrics }

func (modelMetricsPanic) ComputeModelMetrics(*model.ModelIR, *simulation.Result) map[string]simulation.MetricSummary {
	panic("pack bug")
}

// modelMetricsReadDigest answers from the Result's digest, which the engine
// takes only after the pack's metrics: through the engine the pack sees
// another Result than the one it is given directly.
type modelMetricsReadDigest struct{ modelMetrics }

func (p modelMetricsReadDigest) ComputeModelMetrics(ir *model.ModelIR, r *simulation.Result) map[string]simulation.MetricSummary {
	out := p.modelMetrics.ComputeModelMetrics(ir, r)
	if r != nil && r.Reproducibility.ResultDigest != "" {
		delete(out, "counters")
	}
	return out
}

// remembersModel answers with the name of the model it saw before.
type remembersModel struct {
	modelMetrics
	last *atomic.Pointer[string]
}

func (p remembersModel) ComputeModelMetrics(ir *model.ModelIR, r *simulation.Result) map[string]simulation.MetricSummary {
	out := p.modelMetrics.ComputeModelMetrics(ir, r)
	if prev := p.last.Load(); prev != nil {
		reason := *prev
		out["previous"] = simulation.MetricSummary{Status: simulation.Unavailable, Population: simulation.PopulationReplication, Unit: "x", Reason: &reason}
	}
	if ir != nil {
		name := ir.Name
		p.last.Store(&name)
	}
	return out
}

// The model-metrics checks prove what they claim: each broken provider
// produces its finding; the correct one none.
func TestModelMetricsChecks(t *testing.T) {
	m := queueModel(t)
	for _, c := range []struct {
		pack   domainpack.DomainPack
		check  string
		detail string
	}{
		{modelMetricsMutateIR{}, "model-metrics/read-only", "model"},
		{modelMetricsMutateResult{}, "model-metrics/read-only", "result"},
		{modelMetricsMutateResult{}, "model-metrics/generic-unchanged", ""},
		{modelMetricInvalid{}, "model-metrics/schema", ""},
		{modelMetricEmptyKey{}, "model-metrics/keys", ""},
		{modelMetricsCount{n: new(atomic.Int64)}, "model-metrics/deterministic", ""},
		{modelMetricsCount{n: new(atomic.Int64)}, "concurrent", "ComputeModelMetrics"},
		{modelMetricsPanic{}, "model-metrics/compute", "panicked"},
		{modelMetricsReadDigest{}, "model-metrics/engine-agrees", ""},
		{remembersModel{last: new(atomic.Pointer[string])}, "stateless", "returns text"},
	} {
		t.Run(typeName(c.pack)+"/"+c.check, func(t *testing.T) {
			findings, err := Check(context.Background(), Suite{Pack: c.pack, Models: []model.ModelIR{m}, Horizon: simulation.Duration{Value: 4, Unit: model.Hour}})
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range findings {
				if f.Check == c.check && strings.Contains(f.Detail, c.detail) {
					return
				}
			}
			t.Errorf("no %s finding (%q); got %v", c.check, c.detail, findings)
		})
	}
	if findings, err := Check(context.Background(), Suite{Pack: modelMetrics{}, Models: []model.ModelIR{m}}); err != nil || len(findings) > 0 {
		t.Errorf("a correct provider: %v %v", err, findings)
	}
}
