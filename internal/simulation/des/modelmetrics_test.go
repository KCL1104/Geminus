package des

import (
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// lineMetrics stands in for a pack's ModelMetricsProvider (the generic
// engine does not import a domain): one metric from the IR and one from
// the Result, so the test sees both reach it.
func lineMetrics(ir *model.ModelIR, r *simulation.Result) map[string]simulation.MetricSummary {
	nodes, completed := float64(len(ir.Nodes)), float64(r.ReplicationSummaries[0].CompletedCount)
	return map[string]simulation.MetricSummary{
		"nodes":     {Status: simulation.Available, Mean: &nodes, P50: &nodes, P95: &nodes, SampleCount: 1, Population: simulation.PopulationReplication, Unit: "count"},
		"completed": {Status: simulation.Available, Mean: &completed, P50: &completed, P95: &completed, SampleCount: 1, Population: simulation.PopulationReplication, Unit: "count"},
	}
}

// D-058: ModelMetrics fills metrics.domain from the effective IR and the
// generic Result before the digest; without it, or with no metric, the
// Result and its digest are what they were (D-047).
func TestModelMetrics(t *testing.T) {
	in := input(t, golden(t, "deterministic-queue.json"), 1010, 0, 1, simulation.TraceNone)
	plain := run(t, &Engine{DomainMetrics: dora}, in).Result
	if plain.Metrics.Domain != nil {
		t.Fatalf("a run without model metrics has domain metrics: %v", plain.Metrics.Domain)
	}
	var seen *model.ModelIR
	e := &Engine{DomainMetrics: dora, ModelMetrics: func(ir *model.ModelIR, r *simulation.Result) map[string]simulation.MetricSummary {
		seen = ir
		ir.Nodes[0].Name = "changed by the pack" // a pack's bug must not reach the caller's input
		return lineMetrics(ir, r)
	}}
	res := run(t, e, in).Result
	if seen == nil || len(seen.Nodes) != len(in.Model.Nodes) || in.Model.Nodes[0].Name == "changed by the pack" {
		t.Errorf("the hook saw %v; the input after the run %+v", seen, in.Model.Nodes[0])
	}
	if d := res.Metrics.Domain; len(d) != 2 || *d["nodes"].Mean != float64(len(in.Model.Nodes)) || *d["completed"].Mean != 100 {
		t.Errorf("domain metrics %+v", d)
	}
	validResult(t, res)
	if res.Reproducibility.ResultDigest == plain.Reproducibility.ResultDigest {
		t.Error("the domain metrics are not in the digest")
	}
	generic := *res
	generic.Metrics.Domain, generic.Reproducibility.ResultDigest = nil, ""
	want := *plain
	want.Reproducibility.ResultDigest = ""
	if !reflect.DeepEqual(generic, want) {
		t.Error("model metrics changed the generic Result")
	}
	again := run(t, &Engine{DomainMetrics: dora, ModelMetrics: lineMetrics}, in).Result
	if again.Reproducibility.ResultDigest != res.Reproducibility.ResultDigest {
		t.Error("the same run with the same model metrics has another digest")
	}
	// No metric: metrics.domain stays absent (the schema requires at least
	// one member when present) and the digest is the plain one.
	for name, f := range map[string]func(*model.ModelIR, *simulation.Result) map[string]simulation.MetricSummary{
		"nil": func(*model.ModelIR, *simulation.Result) map[string]simulation.MetricSummary { return nil },
		"empty": func(*model.ModelIR, *simulation.Result) map[string]simulation.MetricSummary {
			return map[string]simulation.MetricSummary{}
		},
	} {
		got := run(t, &Engine{DomainMetrics: dora, ModelMetrics: f}, in).Result
		data, _ := json.Marshal(got.Metrics)
		if got.Metrics.Domain != nil || got.Reproducibility.ResultDigest != plain.Reproducibility.ResultDigest || jsonHas(data, "domain") {
			t.Errorf("%s model metrics: %v %s", name, got.Metrics.Domain, got.Reproducibility.ResultDigest)
		}
	}
	// A panic in the pack is a pack bug: the run fails with ErrInternal.
	bad := &Engine{ModelMetrics: func(*model.ModelIR, *simulation.Result) map[string]simulation.MetricSummary { panic("pack bug") }}
	if res, err := bad.Run(context.Background(), in, nil); !errors.Is(err, engine.ErrInternal) || res != nil {
		t.Errorf("ModelMetrics panic: %v", err)
	}
}

func jsonHas(doc []byte, member string) bool {
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		return false
	}
	_, ok := m[member]
	return ok
}
