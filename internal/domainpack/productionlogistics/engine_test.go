package productionlogistics_test

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/domainpack/productionlogistics"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/des"
)

// lockFile holds the regression lock of the templates' runs: the result
// digest des-go produced for the config below, taken after the checks of
// TestTemplatesThroughTheEngine passed. It is a lock on the engine's own
// output, not an independent oracle (SIMULATION_SPEC §9).
const lockFile = "testdata/template-run-lock.json"

type runLock struct {
	Note          string            `json:"note"`
	EngineVersion string            `json:"engine_version"`
	HorizonDays   int               `json:"horizon_days"`
	WarmupDays    int               `json:"warmup_days"`
	Replications  int               `json:"replications"`
	Seed          int64             `json:"seed"`
	ResultDigests map[string]string `json:"result_digests"`
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

// The pack's engine, as the worker builds it: the pack has no model metrics.
func engine() *des.Engine { return &des.Engine{DomainMetrics: pack.ComputeMetrics} }

// shiftRatios are, for each resource on a calendar, the share of its
// on-shift unit time spent processing (work) and in use (processing,
// setups and holding blocked items), over every replication.
func shiftRatios(res *simulation.Result) (work, inUse map[string]float64) {
	work, inUse = map[string]float64{}, map[string]float64{}
	scheduled := map[string]float64{}
	for _, rep := range res.ReplicationSummaries {
		for id, s := range rep.ResourceStates {
			if s.ScheduledTicks == nil {
				continue
			}
			scheduled[id] += float64(*s.ScheduledTicks)
			work[id] += float64(s.ScheduledProcessingTicks)
			inUse[id] += float64(*s.ScheduledInUseTicks)
		}
	}
	for id, s := range scheduled {
		work[id] /= s
		inUse[id] /= s
	}
	return work, inUse
}

// checkBottleneck: the template's bottleneck (PRODUCTION_LOGISTICS §3)
// does the most work on shift of all its resources and is in use at least
// 80% of its shift time, so a what-if on it is visible.
func checkBottleneck(t *testing.T, name string, res *simulation.Result) {
	t.Helper()
	b := templates[name].bottleneck
	work, inUse := shiftRatios(res)
	if inUse[b] < 0.8 {
		t.Errorf("%s: %s is in use %.3f of its shift time", name, b, inUse[b])
	}
	for id, w := range work {
		if id != b && w >= work[b] {
			t.Errorf("%s: %s works %.3f of its shift time, not below the bottleneck %s's %.3f", name, id, w, b, work[b])
		}
	}
}

// H-045: each template runs through the engine as the worker runs it. The
// Result is schema-valid with the engine's generic metrics only (the pack
// has none of its own); items complete; the bottleneck is the busiest
// resource; the same seed gives the same digest, another seed another,
// and the digest is locked.
func TestTemplatesThroughTheEngine(t *testing.T) {
	var lock runLock
	data, err := os.ReadFile(filepath.FromSlash(lockFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	if len(lock.ResultDigests) != len(productionlogistics.Templates()) {
		t.Errorf("%s locks %d templates", lockFile, len(lock.ResultDigests))
	}
	for _, name := range productionlogistics.Templates() {
		ir := tmpl(t, name)
		in := runInput(t, ir, lock.HorizonDays, lock.WarmupDays, lock.Replications, lock.Seed)
		res, err := engine().Run(context.Background(), in, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		withRun := *res
		withRun.RunID = "sim_01M4YDQK00FGZZWH0PP15XPNFA"
		doc, err := json.Marshal(withRun)
		if err != nil {
			t.Fatal(err)
		}
		if issues, err := validate.Document(doc, "simulation.schema.json#/$defs/Result", validate.IRSchemaInvalid); err != nil || len(issues) > 0 {
			t.Fatalf("%s: the Result is not schema-valid: %v %v", name, err, issues)
		}
		m := res.Metrics
		if m.Domain != nil || m.Throughput.Status != simulation.Available || m.Throughput.Mean == nil || *m.Throughput.Mean <= 0 ||
			m.LeadTimeMinutes.Mean == nil || *m.LeadTimeMinutes.Mean <= 0 {
			t.Errorf("%s: metrics %+v", name, m)
		}
		if f := m.FailureCount.Mean; f == nil || *f <= 0 {
			t.Errorf("%s: no defect, reject or pick error", name)
		}
		for _, r := range ir.Resources {
			if u := m.Resources[r.ID].UtilizationMean; u == nil || *u <= 0 || *u > 1 {
				t.Errorf("%s: %s utilization %v", name, r.ID, u)
			}
		}
		checkBottleneck(t, name, res)

		again, err := engine().Run(context.Background(), in, nil)
		if err != nil || again.Reproducibility.ResultDigest != res.Reproducibility.ResultDigest {
			t.Errorf("%s: a second run: %v %s vs %s", name, err, again.Reproducibility.ResultDigest, res.Reproducibility.ResultDigest)
		}
		other := runInput(t, ir, lock.HorizonDays, lock.WarmupDays, lock.Replications, lock.Seed+1)
		if o, err := engine().Run(context.Background(), other, nil); err != nil || o.Reproducibility.ResultDigest == res.Reproducibility.ResultDigest {
			t.Errorf("%s: another seed gives the same digest: %v", name, err)
		}
		if res.EngineVersion != lock.EngineVersion || res.Reproducibility.ResultDigest != lock.ResultDigests[name] {
			t.Errorf("%s: the run's digest is %s (%s); %s locks %s (%s). A change to the engine or the template must be reviewed before the lock is updated",
				name, res.Reproducibility.ResultDigest, res.EngineVersion, lockFile, lock.ResultDigests[name], lock.EngineVersion)
		}
	}
}

// The UI's default run (one week from Monday 00:00, no warmup, 5
// replications, seed 1, a sampled trace; H-047, apps/web
// features/simulations/run-config.ts) fits the execution limits every run
// freezes (D-007's defaults) and shows the same bottleneck: a new model's
// first run already tells the story.
func TestDefaultRunShowsTheBottleneck(t *testing.T) {
	for _, name := range productionlogistics.Templates() {
		in := runInput(t, tmpl(t, name), 7, 0, 5, 1)
		in.Config.TraceLevel = simulation.TraceSampled
		in.Limits = simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 10_000, MaxTraceEvents: 100_000}
		start := time.Now()
		res, err := engine().Run(context.Background(), in, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s: a week, 5 replications, sampled trace in %v", name, time.Since(start).Round(time.Millisecond))
		if res.Metrics.Throughput.Mean == nil || *res.Metrics.Throughput.Mean <= 0 {
			t.Errorf("%s: nothing completed in the first week", name)
		}
		checkBottleneck(t, name, res)
	}
}

// The zh-Hant templates run to the same numbers: names are not part of the
// simulation.
func TestTemplateLocalesRunAlike(t *testing.T) {
	for _, name := range productionlogistics.Templates() {
		zh, err := productionlogistics.Template(name, newModel, "我的工廠", domainpack.TraditionalChinese)
		if err != nil {
			t.Fatal(err)
		}
		a, err := engine().Run(context.Background(), runInput(t, tmpl(t, name), 3, 0, 2, 11), nil)
		if err != nil {
			t.Fatal(err)
		}
		b, err := engine().Run(context.Background(), runInput(t, zh, 3, 0, 2, 11), nil)
		if err != nil {
			t.Fatal(err)
		}
		x, _ := json.Marshal(a.Metrics, json.Deterministic(true))
		y, _ := json.Marshal(b.Metrics, json.Deterministic(true))
		if string(x) != string(y) || strings.Count(string(x), `"status":"available"`) < 5 {
			t.Errorf("%s: the zh-Hant template runs to other metrics", name)
		}
	}
}
