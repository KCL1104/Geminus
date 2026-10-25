package des

import (
	"encoding/json/v2"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The Result members of resource breakdowns (D-051, D-058; SIMULATION_SPEC
// §5) against testdata/simulation/breakdown-goldens.json, hand-derived; the
// observation-level facts of the same cases are in internal/simulation/engine.

func breakdownCases(t *testing.T) []goldenCase {
	t.Helper()
	var doc struct {
		Cases []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/breakdown-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cases
}

// Every breakdown case with an expected result: resource_states (the
// downtime and available groups, and blocked_ticks with a finite buffer)
// exactly, blocking and buffers exactly (absent when the case says null),
// and the existing members the case states.
func TestBreakdownGoldenResults(t *testing.T) {
	checked := 0
	for _, c := range breakdownCases(t) {
		if c.Expected.Result == nil {
			continue
		}
		checked++
		t.Run(c.ID, func(t *testing.T) {
			w := *c.Expected.Result
			res := run(t, &Engine{}, caseInput(t, c)).Result
			validResult(t, res)
			s := res.ReplicationSummaries[0]
			if s.CompletedCount != w.CompletedCount || s.CensoredEntities != w.Censored {
				t.Errorf("completed %d, censored %d; hand-derived %d, %d", s.CompletedCount, s.CensoredEntities, w.CompletedCount, w.Censored)
			}
			if !reflect.DeepEqual(s.ResourceStates, w.ResourceStates) {
				got, _ := json.Marshal(s.ResourceStates)
				want, _ := json.Marshal(w.ResourceStates)
				t.Errorf("resource states %s\n hand-derived %s", got, want)
			}
			if !reflect.DeepEqual(s.Blocking, w.Blocking) || !reflect.DeepEqual(s.Buffers, w.Buffers) {
				t.Errorf("blocking %+v buffers %+v\n hand-derived %+v %+v", s.Blocking, s.Buffers, w.Blocking, w.Buffers)
			}
			for r, u := range w.ResourceUtilization {
				if !near(s.ResourceUtilization[r], u) {
					t.Errorf("utilization of %s %v, hand-derived %v", r, s.ResourceUtilization[r], u)
				}
			}
			if len(s.ResourceUtilization) != len(w.ResourceUtilization) {
				t.Errorf("utilization %v, hand-derived %v", s.ResourceUtilization, w.ResourceUtilization)
			}
			lt := res.Metrics.LeadTimeMinutes
			switch {
			case len(w.LeadTimes) == 0:
				// Nothing completes in the window (G-F9): unavailable, never 0.
				if lt.SampleCount != 0 || lt.Status != simulation.Unavailable || lt.Mean != nil || s.MeanLeadTimeMinutes != nil {
					t.Errorf("lead time %+v, hand-derived: none", lt)
				}
			case lt.SampleCount != int64(len(w.LeadTimes)) || !near(*lt.Mean, average(w.LeadTimes)) || !near(*lt.P95, quantile7(w.LeadTimes, 0.95)):
				t.Errorf("lead time %+v, hand-derived %v", lt, w.LeadTimes)
			}
			for q, waits := range w.QueueWaits {
				st := res.Metrics.Queues[q]
				if st.SampleCount != int64(len(waits)) || !near(*st.WaitMeanMinutes, average(waits)) || !near(*st.WaitP95Minutes, quantile7(waits, 0.95)) {
					t.Errorf("queue %s %+v, hand-derived waits %v", q, st, waits)
				}
			}
			// The existing members are untouched by breakdowns: utilization
			// is still in use over capacity and the window (D-051).
			for id, st := range s.ResourceStates {
				if want := float64(st.InUseTicks) / (float64(res.Metrics.Resources[id].Capacity) * float64(res.MeasurementWindow.HorizonTicks-res.MeasurementWindow.WarmupTicks)); !near(s.ResourceUtilization[id], want) {
					t.Errorf("%s: utilization %v, in_use_ticks says %v", id, s.ResourceUtilization[id], want)
				}
			}
			cp := *res
			cp.Reproducibility.ResultDigest = ""
			if d, err := hashing.Result(cp); err != nil || d != res.Reproducibility.ResultDigest {
				t.Errorf("digest %s, recomputed %s %v", res.Reproducibility.ResultDigest, d, err)
			}
		})
	}
	if checked < 10 {
		t.Fatalf("%d cases with an expected result", checked)
	}
}

// D-058's identities for a resource without a calendar (S = C): available
// = C*T - down, idle = available - in use (U + D <= C at every tick), and
// processing = in use - blocked, on stochastic runs: the reference cases'
// 30 replications.
func TestBreakdownStateIdentities(t *testing.T) {
	var doc struct {
		Reference struct {
			Cases []goldenCase `json:"cases"`
		} `json:"reference"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/breakdown-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Reference.Cases) < 4 {
		t.Fatalf("%d reference cases", len(doc.Reference.Cases))
	}
	downs := 0
	for _, c := range doc.Reference.Cases {
		res := run(t, &Engine{}, caseInput(t, c)).Result
		validResult(t, res)
		span := res.MeasurementWindow.HorizonTicks - res.MeasurementWindow.WarmupTicks
		for _, s := range res.ReplicationSummaries {
			for id, st := range s.ResourceStates {
				capTicks := int64(res.Metrics.Resources[id].Capacity) * span
				blocked := int64(0)
				if st.BlockedTicks != nil {
					blocked = *st.BlockedTicks
				}
				if st.ScheduledProcessingTicks != st.InUseTicks-blocked {
					t.Errorf("%s r%d %s: processing %d, in use %d, blocked %d", c.ID, s.ReplicationIndex, id, st.ScheduledProcessingTicks, st.InUseTicks, blocked)
				}
				if st.DownTicks == nil {
					continue
				}
				downs += int(*st.BreakdownsStarted)
				if *st.AvailableTicks != capTicks-*st.DownTicks || *st.IdleTicks != *st.AvailableTicks-st.InUseTicks ||
					*st.RepairsCompleted > *st.BreakdownsStarted+int64(res.Metrics.Resources[id].Capacity) || *st.CompletedRepairTicks > *st.DownTicks {
					t.Errorf("%s r%d %s: %+v (C*T %d)", c.ID, s.ReplicationIndex, id, st, capTicks)
				}
			}
		}
	}
	if downs < 1000 {
		t.Fatalf("only %d breakdowns in the reference cases", downs)
	}
}

// D-047: a model without breakdowns has no breakdown member; with
// neutral breakdowns (a constant TTF later than the horizon) every
// existing member is the same as without them, and only the resources
// with breakdowns gain a resource_states entry (all zero downtime).
func TestNeutralBreakdownsKeepTheResult(t *testing.T) {
	for _, name := range []string{"deterministic-queue.json", "congested-queue.json", "default-delivery.json", "report-example.json"} {
		in := input(t, golden(t, name), 600, 60, 2, simulation.TraceSampled)
		review := simulation.ResolvedDataset{
			Binding: model.DatasetBinding{LogicalRef: "dataset:review_duration", DatasetVersionID: "dsv_01M4YDQK00NMKD3J34KSJZVY40",
				ContentHash: "sha256:" + strings.Repeat("0", 64), Unit: model.Minute, SampleCount: 3},
			Samples: []float64{120, 240, 360},
		}
		in.Datasets = map[string]simulation.ResolvedDataset{review.Binding.LogicalRef: review}
		in.Manifest = []model.DatasetBinding{review.Binding}
		base := run(t, &Engine{}, in)
		data, _ := json.Marshal(base.Result)
		for _, member := range []string{"down_ticks", "available_ticks", "breakdowns_started", "resource_states"} {
			if slices.Contains(jsonKeys(t, data), member) {
				t.Errorf("%s: the Result has %s", name, member)
			}
		}
		nin := in
		nin.Model.Resources = slices.Clone(in.Model.Resources)
		value, repair := 601.0, 3.0
		nin.Model.Resources[0].Breakdowns = &model.Breakdowns{
			TimeToFailure: model.TimedDistribution{Distribution: model.Distribution{Type: model.DistConstant, Value: &value}},
			TimeToRepair:  model.TimedDistribution{Distribution: model.Distribution{Type: model.DistConstant, Value: &repair}}}
		ndata, err := model.Encode(&nin.Model)
		if err != nil {
			t.Fatal(err)
		}
		if nin.EffectiveModelHash, err = hashing.Model(ndata); err != nil {
			t.Fatal(err)
		}
		neutral := run(t, &Engine{}, nin)
		validResult(t, neutral.Result)
		if !reflect.DeepEqual(neutral.Result.Metrics, base.Result.Metrics) || !reflect.DeepEqual(neutral.Trace.Events, base.Trace.Events) {
			t.Errorf("%s: neutral breakdowns changed the metrics or the trace", name)
		}
		for i, s := range neutral.Result.ReplicationSummaries {
			b := base.Result.ReplicationSummaries[i]
			st, ok := s.ResourceStates[nin.Model.Resources[0].ID]
			if len(s.ResourceStates) != 1 || !ok || *st.DownTicks != 0 || *st.BreakdownsStarted != 0 || *st.RepairsCompleted != 0 || *st.CompletedRepairTicks != 0 {
				t.Errorf("%s r%d: resource states %+v", name, i, s.ResourceStates)
			}
			s.ResourceStates = nil
			if !reflect.DeepEqual(s, b) {
				t.Errorf("%s r%d: %+v\n without breakdowns %+v", name, i, s, b)
			}
		}
	}
}

// TEST-019 with breakdowns: the reference cases (30 replications,
// breakdowns, lognormal and Weibull, a finite buffer) give the same
// Result and trace bit for bit whatever the worker count; another seed
// gives another digest.
func TestBreakdownDeterminism(t *testing.T) {
	var doc struct {
		Reference struct {
			Cases []goldenCase `json:"cases"`
		} `json:"reference"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/breakdown-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Reference.Cases {
		in := caseInput(t, c)
		in.Config.TraceLevel = simulation.TraceSampled
		var digests []string
		var traces [][]byte
		for _, workers := range []int{1, 3, 8, 0} {
			out := run(t, &Engine{Workers: workers}, in)
			digests = append(digests, out.Result.Reproducibility.ResultDigest)
			data, _ := json.Marshal(out.Trace)
			traces = append(traces, data)
		}
		for i := range digests {
			if digests[i] != digests[0] || string(traces[i]) != string(traces[0]) {
				t.Errorf("%s: worker counts give different results: %v", c.ID, digests)
			}
		}
		other := in
		other.Config.Seed = rand.New(rand.NewPCG(uint64(len(c.ID)), 48)).Int64N(1 << 40)
		if d := run(t, &Engine{}, other).Result.Reproducibility.ResultDigest; d == digests[0] {
			t.Errorf("%s: another seed gave the same digest", c.ID)
		}
	}
}

// des-go-0.3.0's own regression lock (engine output, not an oracle; ADR-026
// D-047 "新版本另立自己的鎖"; testdata/simulation/engine-golden-digests-
// 0.3.0.json): GOLDEN-001/002, the finite-buffer goldens, the breakdown
// goldens with an expected result and the four stochastic breakdown
// reference cases. The values are the hand-derived ones' (the tests above);
// the lock only makes a later numerical change visible.
func TestGoldenDigests030(t *testing.T) {
	root := contracttest.Root(t)
	var lock struct {
		EngineVersion string            `json:"engine_version"`
		Digests       map[string]string `json:"digests"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(root, "testdata/simulation/engine-golden-digests-0.3.0.json")), &lock); err != nil {
		t.Fatal(err)
	}
	if lock.EngineVersion != Version {
		t.Fatalf("lock for %s, engine %s", lock.EngineVersion, Version)
	}
	var cases struct {
		Cases []struct {
			ID     string               `json:"id"`
			Model  string               `json:"model"`
			Config simulation.RunConfig `json:"config"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(root, "testdata/simulation/expected-cases.json")), &cases); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range cases.Cases {
		in := input(t, golden(t, filepath.Base(c.Model)), 0, 0, 1, simulation.TraceNone)
		in.Config = c.Config
		got[c.ID] = run(t, &Engine{DomainMetrics: dora}, in).Result.Reproducibility.ResultDigest
	}
	var doc struct {
		Reference struct {
			Cases []goldenCase `json:"cases"`
		} `json:"reference"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(root, "testdata/simulation/breakdown-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range append(append(blockingCases(t), breakdownCases(t)...), doc.Reference.Cases...) {
		if c.Expected.Result != nil || strings.HasPrefix(c.ID, "B-S") {
			got[c.ID] = run(t, &Engine{}, caseInput(t, c)).Result.Reproducibility.ResultDigest
		}
	}
	if !reflect.DeepEqual(got, lock.Digests) {
		data, _ := json.Marshal(got, json.Deterministic(true))
		t.Errorf("digests %s\n locked %v", data, lock.Digests)
	}
}

// Trace: breakdowns are not entity events; the trace of a model with
// them has only the entity event types, and the same events as the
// model's run without the trace (TEST-019: trace_level changes no metric).
func TestBreakdownsAreNotTraced(t *testing.T) {
	var c goldenCase
	for _, x := range breakdownCases(t) {
		if x.ID == "G-F1" {
			c = x
		}
	}
	in := caseInput(t, c)
	in.Config.TraceLevel = simulation.TraceSampled
	out := run(t, &Engine{}, in)
	// G-F1's r0/src/9, 10 and 14 are selected (the fixed entity hash).
	if out.Trace == nil || !slices.ContainsFunc(out.Trace.Events, func(e simulation.TraceEvent) bool { return e.EntityID == "r0/src/14" }) {
		t.Fatalf("trace %+v", out.Trace)
	}
	schemaValid(t, out.Trace, "simulation.schema.json#/$defs/TraceArtifact")
	for _, e := range out.Trace.Events {
		if e.EntityID == "" || strings.Contains(string(e.Type), "down") || strings.Contains(string(e.Type), "repair") {
			t.Errorf("trace event %+v", e)
		}
	}
	in.Config.TraceLevel = simulation.TraceNone
	if !reflect.DeepEqual(run(t, &Engine{}, in).Result.Metrics, out.Result.Metrics) {
		t.Error("the trace changed the metrics")
	}
}
