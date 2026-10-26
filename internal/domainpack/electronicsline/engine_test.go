package electronicsline_test

import (
	"context"
	"encoding/json/v2"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/electronicsline"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
)

// lockFile holds the regression lock of the template's run: the result
// digest des-go produced for the config below, taken after the checks of
// TestTemplateThroughTheEngine passed. It is a lock on the engine's own
// output, not an independent oracle (SIMULATION_SPEC §9).
const lockFile = "testdata/template-run-lock.json"

type runLock struct {
	Note          string `json:"note"`
	EngineVersion string `json:"engine_version"`
	HorizonDays   int    `json:"horizon_days"`
	WarmupDays    int    `json:"warmup_days"`
	Replications  int    `json:"replications"`
	Seed          int64  `json:"seed"`
	ResultDigest  string `json:"result_digest"`
}

func runInput(t *testing.T, ir *model.ModelIR, horizon, warmup, reps int, seed int64) simulation.RunInput {
	t.Helper()
	data, err := model.Encode(ir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hashing.Model(data)
	if err != nil {
		t.Fatal(err)
	}
	return simulation.RunInput{Model: *ir, ModelHash: h, EffectiveModelHash: h,
		Config: simulation.RunConfig{Horizon: simulation.Duration{Value: float64(horizon), Unit: model.Day},
			Warmup: simulation.Duration{Value: float64(warmup), Unit: model.Day}, Replications: reps, Seed: seed, TraceLevel: simulation.TraceNone},
		Limits: simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 10_000}}
}

func engine() *des.Engine {
	return &des.Engine{DomainMetrics: pack.ComputeMetrics, ModelMetrics: pack.ComputeModelMetrics}
}

// The template runs through the engine with the pack's metrics: the
// Result is schema-valid, every metric the pack declares for the line and
// each of its stations is there, available, or unavailable with the reason
// ELECTRONICS_LINE §5 gives (REQ-015: never a number the engine cannot
// back); the numbers agree with the generic Result they come from; the
// same seed gives the same digest, which locks the run.
func TestTemplateThroughTheEngine(t *testing.T) {
	ir := tmpl(t)
	var lock runLock
	if err := json.Unmarshal(contractFile(t, lockFile), &lock); err != nil {
		t.Fatal(err)
	}
	in := runInput(t, ir, lock.HorizonDays, lock.WarmupDays, lock.Replications, lock.Seed)
	res, err := engine().Run(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	withRun := *res
	withRun.RunID = "sim_01M4YDQK00FGZZWH0PP15XPNFA"
	doc, err := json.Marshal(withRun)
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := validate.Document(doc, "simulation.schema.json#/$defs/Result", validate.IRSchemaInvalid); err != nil || len(issues) > 0 {
		t.Fatalf("the Result is not schema-valid: %v %v", err, issues)
	}
	d := res.Metrics.Domain
	stations := []string{"smt", "aoi", "ict", "fct", "burn_in", "pack_ship"}
	tests := map[string]bool{"aoi": true, "ict": true, "fct": true, "burn_in": true}
	retests := map[string]bool{"ict": true, "fct": true}
	unavailable := map[string]string{"line_wip": "WIP_NOT_DECLARED", "scrap_rate": "SINK_COUNT_NOT_DECLARED", "starved_ratio.burn_in": "STARVATION_UNDEFINED_BATCH"}
	listed := map[string]bool{}
	for _, def := range electronicsline.Metrics() {
		if def.Scope == electronicsline.LineScope {
			listed[def.Name] = true
			continue
		}
		for _, s := range stations {
			switch {
			case (def.Name == "fpy_input" || def.Name == "simulated_first_pass_ratio") && !tests[s],
				def.Name == "retest_ratio" && !retests[s],
				(def.Name == "scheduled_utilization" || def.Name == "overtime_minutes") && s == "burn_in":
				continue
			}
			listed[def.Name+"."+s] = true
			key := def.Name + "." + s
			switch {
			case def.Name == "simulated_first_pass_ratio" || def.Name == "retest_ratio":
				unavailable[key] = "FLOW_COUNT_NOT_DECLARED"
			case (def.Name == "setup_ratio" || def.Name == "setups_started") && s != "smt":
				unavailable[key] = "NO_SETUP_MODEL"
			case (def.Name == "down_ratio" || def.Name == "availability") && s == "pack_ship":
				unavailable[key] = "NO_BREAKDOWN_MODEL"
			case (def.Name == "batches_started" || def.Name == "mean_batch_size" || def.Name == "batch_fill_ratio") && s != "burn_in":
				unavailable[key] = "NO_BATCH_MODEL"
			}
		}
	}
	// pack_ship is never blocked (no finite buffer after it).
	unavailable["mean_blocking_minutes.pack_ship"] = "NO_COMPLETED_BLOCK_IN_WINDOW"
	if len(d) != len(listed) {
		t.Errorf("%d domain metrics, want %d", len(d), len(listed))
	}
	for key := range listed {
		m, ok := d[key]
		switch {
		case !ok:
			t.Errorf("%s is missing", key)
		case strings.HasPrefix(key, "mean_blocking_minutes.") && key != "mean_blocking_minutes.aoi" && m.Status == simulation.Unavailable:
			// Whether a station's block ended inside the window depends on
			// the run; AOI, before the ICT buffer of 10, always has some.
			if m.Reason == nil || *m.Reason != "NO_COMPLETED_BLOCK_IN_WINDOW" {
				t.Errorf("%s: %+v", key, m)
			}
		case unavailable[key] != "":
			if m.Status != simulation.Unavailable || m.Reason == nil || *m.Reason != unavailable[key] {
				t.Errorf("%s: %+v, want unavailable %s", key, m, unavailable[key])
			}
		case m.Status != simulation.Available || m.Mean == nil || m.SampleCount != int64(lock.Replications):
			t.Errorf("%s: %+v, want available", key, m)
		}
	}
	mean := func(key string) float64 {
		t.Helper()
		m := d[key]
		if m.Mean == nil {
			t.Fatalf("%s has no mean", key)
		}
		return *m.Mean
	}
	near := func(what string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}
	window := float64(res.MeasurementWindow.HorizonTicks - res.MeasurementWindow.WarmupTicks)
	near("uph", mean("uph"), *res.Metrics.Throughput.Mean*3_600_000/window)
	// The line calendar's on-shift time is the SMT line's scheduled time:
	// one unit, on shift whenever the calendar is.
	var onShift float64
	for _, r := range res.ReplicationSummaries {
		onShift = float64(*r.ResourceStates["smt_line"].ScheduledTicks)
	}
	near("uph_scheduled", mean("uph_scheduled"), *res.Metrics.Throughput.Mean*3_600_000/onShift)
	for s, y := range map[string]float64{"aoi": 0.97, "ict": 0.95, "fct": 0.93, "burn_in": 0.995} {
		near("fpy_input."+s, mean("fpy_input."+s), y)
	}
	near("rolled_throughput_yield_input", mean("rolled_throughput_yield_input"), 0.97*0.95*0.93*0.995)
	for _, s := range stations {
		sum := 0.0
		for _, part := range []string{"processing_ratio", "setup_ratio", "blocking_ratio", "down_ratio", "idle_ratio", "off_shift_ratio"} {
			if m := d[part+"."+s]; m.Mean != nil {
				sum += *m.Mean
			}
		}
		near("the six states of "+s, sum, 1)
	}
	if b := mean("mean_batch_size.burn_in"); b < 20 || b > 40 {
		t.Errorf("mean batch size %v outside [20, 40]", b)
	}
	near("batch_fill_ratio.burn_in", mean("batch_fill_ratio.burn_in"), mean("mean_batch_size.burn_in")/40)
	if lb := mean("line_balance"); lb <= 0 || lb > 1 {
		t.Errorf("line balance %v", lb)
	}
	for _, key := range []string{"setups_started.smt", "down_ratio.smt", "down_ratio.fct", "down_ratio.burn_in", "blocking_ratio.aoi", "overtime_minutes.smt"} {
		if mean(key) <= 0 {
			t.Errorf("%s = %v; the template should show it", key, mean(key))
		}
	}
	// The FCT testers do the most work on shift: the candidate bottleneck
	// of ELECTRONICS_LINE §4.2's estimate.
	for _, s := range stations {
		if s != "fct" && mean("work_ratio."+s) >= mean("work_ratio.fct") {
			t.Errorf("work ratio of %s %v is not below FCT's %v", s, mean("work_ratio."+s), mean("work_ratio.fct"))
		}
	}

	// Determinism: the same seed, the same digest; another seed, another.
	again, err := engine().Run(context.Background(), in, nil)
	if err != nil || again.Reproducibility.ResultDigest != res.Reproducibility.ResultDigest {
		t.Errorf("a second run: %v %s vs %s", err, again.Reproducibility.ResultDigest, res.Reproducibility.ResultDigest)
	}
	other := runInput(t, ir, lock.HorizonDays, lock.WarmupDays, lock.Replications, lock.Seed+1)
	if o, err := engine().Run(context.Background(), other, nil); err != nil || o.Reproducibility.ResultDigest == res.Reproducibility.ResultDigest {
		t.Errorf("another seed gives the same digest: %v", err)
	}
	// The domain metrics are in the digest; without the pack they are absent.
	plain, err := (&des.Engine{}).Run(context.Background(), in, nil)
	if err != nil || plain.Metrics.Domain != nil || plain.Reproducibility.ResultDigest == res.Reproducibility.ResultDigest {
		t.Errorf("without the pack's metrics: %v %v", err, plain.Metrics.Domain)
	}
	if res.EngineVersion != lock.EngineVersion || res.Reproducibility.ResultDigest != lock.ResultDigest {
		t.Errorf("the run's digest is %s (%s); %s locks %s (%s). A change to the engine, the template or the pack's metrics must be reviewed before the lock is updated",
			res.Reproducibility.ResultDigest, res.EngineVersion, lockFile, lock.ResultDigest, lock.EngineVersion)
	}
}

// The zh-Hant template runs to the same numbers: names are not part of
// the simulation.
func TestTemplateLocalesRunAlike(t *testing.T) {
	zh, err := electronicsline.Template(electronicsline.DefaultTemplate, newModel, "我的產線", domainpack.TraditionalChinese)
	if err != nil {
		t.Fatal(err)
	}
	a, err := engine().Run(context.Background(), runInput(t, tmpl(t), 7, 0, 2, 11), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := engine().Run(context.Background(), runInput(t, zh, 7, 0, 2, 11), nil)
	if err != nil {
		t.Fatal(err)
	}
	x, _ := json.Marshal(a.Metrics, json.Deterministic(true))
	y, _ := json.Marshal(b.Metrics, json.Deterministic(true))
	if string(x) != string(y) || strings.Count(string(x), `"status":"available"`) < 50 {
		t.Error("the zh-Hant template runs to other metrics")
	}
}

func contractFile(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
