package aicontext

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
)

// templateRun runs the electronics line template (with ict_tester's
// capacity set when capacity > 0) as the demo does: one day, 5
// replications, seed 1, the pack's metrics.
func templateRun(t *testing.T, id string, capacity int) compare.Run {
	t.Helper()
	ir, err := electronicsline.Template(electronicsline.DefaultTemplate, mdlA, "Electronics assembly and test line", domainpack.English)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := model.Encode(ir)
	if err != nil {
		t.Fatal(err)
	}
	modelHash, err := hashing.Model(pinned)
	if err != nil {
		t.Fatal(err)
	}
	patch := []byte("[]")
	if capacity > 0 {
		for i := range ir.Resources {
			if ir.Resources[i].ID == "ict_tester" {
				ir.Resources[i].Capacity = capacity
			}
		}
		patch = []byte(`[{"op":"replace","path":"/resources/2/capacity","value":3}]`)
	}
	effective, err := model.Encode(ir)
	if err != nil {
		t.Fatal(err)
	}
	effectiveHash, err := hashing.Model(effective)
	if err != nil {
		t.Fatal(err)
	}
	cfg := simulation.RunConfig{Horizon: simulation.Duration{Value: 1440, Unit: model.Minute}, Warmup: simulation.Duration{Value: 0, Unit: model.Minute},
		Replications: 5, Seed: 1, TraceLevel: simulation.TraceNone}
	pack := electronicsline.Pack{}
	res, err := (&des.Engine{DomainMetrics: pack.ComputeMetrics, ModelMetrics: pack.ComputeModelMetrics}).Run(context.Background(),
		simulation.RunInput{Model: *ir, ModelHash: modelHash, EffectiveModelHash: effectiveHash, Config: cfg,
			Limits: simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 10_000}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res.RunID = id
	rep := res.Reproducibility
	return compare.Run{ID: id, ModelVersionID: mvA, EngineVersion: res.EngineVersion, DatasetManifestHash: rep.DatasetManifestHash,
		ModelHash: rep.ModelHash, EffectiveModelHash: rep.EffectiveModelHash, ConfigHash: rep.ConfigHash, ScenarioPatch: patch, Config: cfg, Result: res}
}

// T-030 (found live, 2026-10-28): an analysis of the demo's runs of the
// electronics line template, with its baseline and the server's
// comparison, fits the default budget. The comparison keeps only its
// deltas (each run's values are in its results), and what is not
// available is left out and counted.
func TestElectronicsLineAnalysisContext(t *testing.T) {
	base, scen := templateRun(t, simA2, 0), templateRun(t, simA1, 3)
	c, err := compare.Compare(base, scen)
	if err != nil || !c.Comparable {
		t.Fatalf("comparison: %v %v", err, c.IncomparableReasons)
	}
	cj, err := c.JSON()
	if err != nil {
		t.Fatal(err)
	}
	ir, err := electronicsline.Template(electronicsline.DefaultTemplate, mdlA, "Electronics assembly and test line", domainpack.English)
	if err != nil {
		t.Fatal(err)
	}
	in := AnalysisInput{
		Scope:         Scope{OrganizationID: orgA, ModelID: mdlA},
		Version:       Version{OrganizationID: orgA, ModelID: mdlA, VersionID: mvA, IR: ir},
		Domain:        electronicsline.Pack{}.PromptContext(),
		Scenario:      RunResult{OrganizationID: orgA, ModelID: mdlA, RunID: simA1, Result: scen.Result},
		ScenarioPatch: model.Patch{{Op: model.PatchReplace, Path: "/resources/2/capacity", Value: []byte("3")}},
		Config:        scen.Config,
		Baseline:      &RunResult{OrganizationID: orgA, ModelID: mdlA, RunID: simA2, Result: base.Result},
		Comparison:    cj,
		Question:      "Why did lead time change?",
		OutputLocale:  "en",
		Budget:        provider.DefaultBudget(),
	}
	a, err := Analysis(in)
	if err != nil {
		t.Fatalf("the demo's analysis does not fit the default budget: %v", err)
	}
	// The names prose may quote are the model's, as MODEL_SUMMARY shows them.
	if !slices.Contains(a.Names, ir.Nodes[0].Name) || !slices.Contains(a.Names, ir.Resources[0].Name) {
		t.Errorf("names %v", a.Names)
	}
	size := len(a.Context.Prompt.System) + len(a.Context.Prompt.User())
	t.Logf("%d of %d bytes", size, in.Budget.MaxPromptBytes)

	b := blocks(t, a.Context.Prompt)
	var cmp struct {
		Metrics []map[string]any `json:"metrics"`
	}
	decode(t, b["COMPARISON"].Content, &cmp)
	if len(cmp.Metrics) == 0 || len(cmp.Metrics) >= len(c.Metrics) {
		t.Errorf("comparison rows: %d of %d", len(cmp.Metrics), len(c.Metrics))
	}
	for _, row := range cmp.Metrics {
		_, hasBase := row["baseline"]
		_, hasScen := row["scenario"]
		if row["status"] == "unavailable" || hasBase || hasScen || row["absolute_delta"] == nil && row["status"] == "available" {
			t.Fatalf("comparison row %v", row)
		}
	}
	var res struct {
		Metrics struct {
			Domain map[string]struct {
				Status string `json:"status"`
			} `json:"domain"`
		} `json:"metrics"`
	}
	decode(t, b["SCENARIO_RESULTS"].Content, &res)
	if len(res.Metrics.Domain) == 0 {
		t.Fatal("no domain metrics")
	}
	for k, s := range res.Metrics.Domain {
		if s.Status != "available" {
			t.Errorf("%s is %s", k, s.Status)
		}
	}
	// Every metric the AI may cite is still in its run's results (the trim
	// leaves out only what is not available).
	shown := map[string]any{}
	for run, name := range map[string]string{"scenario": "SCENARIO_RESULTS", "baseline": "BASELINE_RESULTS"} {
		var v any
		decode(t, b[name].Content, &v)
		shown[run] = v
	}
	for _, ref := range a.Allowed {
		v := shown[ref.Run]
		for _, tok := range strings.Split(ref.MetricPath, "/")[1:] {
			m, _ := v.(map[string]any)
			v = m[strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)]
		}
		if s, _ := v.(map[string]any); s == nil || s["status"] != nil && s["status"] != "available" {
			t.Errorf("%s %s is cited but not shown", ref.Run, ref.MetricPath)
		}
	}
	var om struct {
		Omitted []Omission `json:"omitted"`
	}
	decode(t, b["CONTEXT_OMISSIONS"].Content, &om)
	counted := map[string]int{}
	for _, o := range om.Omitted {
		counted[o.What] = o.Count
	}
	if counted["comparison.metrics"] != len(c.Metrics)-len(cmp.Metrics) || counted["scenario_results.unavailable_named_metrics"] == 0 ||
		counted["baseline_results.unavailable_named_metrics"] == 0 {
		t.Errorf("omissions %+v", om.Omitted)
	}
	// The AI may say how much was left out (review M1).
	if !slices.Contains(a.Supplied, float64(counted["comparison.metrics"])) {
		t.Errorf("the comparison's left-out count %d is not a supplied number", counted["comparison.metrics"])
	}
}
