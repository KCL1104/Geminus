package softwaredelivery_test

import (
	"context"
	"encoding/json/v2"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/domainpack/softwaredelivery"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
)

// TEST-023 through the engine: the default template, run by des.Engine with
// the pack's ComputeMetrics as its DomainMetrics, has generic failures and
// rework (failed CI runs) yet every DORA metric is unavailable with the
// missing-mapping reason; the Result is schema-valid and the DORA entries
// are part of its digest (T-022 review).
func TestDORAThroughTheEngine(t *testing.T) {
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/golden-models/default-delivery.json")), &ir); err != nil {
		t.Fatal(err)
	}
	data, err := model.Encode(&ir)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := hashing.Model(data)
	if err != nil {
		t.Fatal(err)
	}
	in := simulation.RunInput{Model: ir, ModelHash: effective, EffectiveModelHash: effective,
		Config: simulation.RunConfig{Horizon: simulation.Duration{Value: 14, Unit: model.Day}, Warmup: simulation.Duration{Value: 1, Unit: model.Day},
			Replications: 3, Seed: 7, TraceLevel: simulation.TraceNone},
		Limits: simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 10_000, MaxTraceEvents: 0}}
	pack := softwaredelivery.Pack{}
	res, err := (&des.Engine{DomainMetrics: pack.ComputeMetrics}).Run(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Metrics.FailureCount.Mean == nil || *res.Metrics.FailureCount.Mean == 0 || *res.Metrics.ReworkCount.Mean == 0 {
		t.Fatalf("the template produced no failure or rework: %+v", res.Metrics.FailureCount)
	}
	if len(res.Metrics.DORA) != 5 {
		t.Fatalf("DORA %v", res.Metrics.DORA)
	}
	for name, m := range res.Metrics.DORA {
		if m.Status != simulation.Unavailable || m.Mean != nil || m.Reason == nil || *m.Reason != softwaredelivery.ReasonMissingDORAMapping {
			t.Errorf("%s: %+v", name, m)
		}
	}
	c := *res
	c.RunID = "sim_01M4YDQK00FGZZWH0PP15XPNFA"
	doc, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := validate.Document(doc, "simulation.schema.json#/$defs/Result", validate.IRSchemaInvalid); err != nil || len(issues) > 0 {
		t.Fatalf("Result not schema-valid: %v %v", err, issues)
	}
	plain, err := (&des.Engine{}).Run(context.Background(), in, nil)
	if err != nil || plain.Reproducibility.ResultDigest == res.Reproducibility.ResultDigest {
		t.Errorf("the DORA entries are not in the digest: %v", err)
	}
}
