package metrics

import (
	"context"
	"encoding/json/v2"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

var limits = simulation.ExecutionLimits{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 10_000, MaxTraceEvents: 0}

func golden(t *testing.T, name string) model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/golden-models", name)), &ir); err != nil {
		t.Fatal(err)
	}
	return ir
}

func runInput(ir model.ModelIR, horizon, warmup float64, replications int) simulation.RunInput {
	return simulation.RunInput{Model: ir, Limits: limits, Config: simulation.RunConfig{
		Horizon: simulation.Duration{Value: horizon, Unit: model.Minute}, Warmup: simulation.Duration{Value: warmup, Unit: model.Minute},
		Replications: replications, Seed: 7, TraceLevel: simulation.TraceNone}}
}

// collect compiles in and runs every replication with its own collector.
func collect(t *testing.T, in simulation.RunInput) (*engine.Program, []Replication) {
	t.Helper()
	p, reps := collectUnfinished(t, in)
	for _, r := range reps {
		r.Collector.Finish()
	}
	return p, reps
}

// collectUnfinished is collect without Finish.
func collectUnfinished(t *testing.T, in simulation.RunInput) (*engine.Program, []Replication) {
	t.Helper()
	p, err := engine.Compile(in)
	if err != nil {
		var ie *engine.InputError
		errors.As(err, &ie)
		t.Fatalf("Compile: %v %v", err, ie)
	}
	b := engine.NewBudget(in.Limits.MaxEvents)
	var reps []Replication
	for i := range p.Replications() {
		c := NewCollector(p)
		out, err := engine.RunReplication(context.Background(), p, uint32(i), b, c)
		if err != nil {
			t.Fatalf("replication %d: %v", i, err)
		}
		reps = append(reps, Replication{Collector: c, Outcome: out})
	}
	return p, reps
}

func build(t *testing.T, in simulation.RunInput) (simulation.Metrics, []simulation.ReplicationSummary, simulation.MeasurementWindow) {
	t.Helper()
	p, reps := collect(t, in)
	m, s, w, err := Build(p, reps)
	if err != nil {
		t.Fatal(err)
	}
	return m, s, w
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-12*math.Max(1, math.Abs(b)) }

func value(t *testing.T, name string, p *float64, want float64) {
	t.Helper()
	if p == nil || !near(*p, want) {
		t.Errorf("%s = %v, want %v", name, deref(p), want)
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// Type 7 on known values (numpy.quantile's default gives the same).
func TestQuantile7(t *testing.T) {
	waits := []float64{}
	for i := range 20 {
		waits = append(waits, float64(5*i))
	}
	x := []float64{3, 1, 4, 1, 5, 9, 2, 6}
	for _, c := range []struct {
		x    []float64
		p    float64
		want float64
	}{
		{[]float64{1, 2, 3, 4}, 0.5, 2.5}, {[]float64{1, 2, 3, 4}, 0.95, 3.85}, {x, 0.95, 7.95}, {x, 0, 1}, {x, 1, 9},
		{waits, 0.5, 47.5}, {waits, 0.95, 90.25}, {[]float64{42}, 0.95, 42},
	} {
		if got := quantile7(c.x, c.p); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("quantile7(%v, %v) = %v, want %v", c.x, c.p, got, c.want)
		}
	}
	if !slices.Equal(x, []float64{3, 1, 4, 1, 5, 9, 2, 6}) {
		t.Error("quantile7 sorted its input")
	}
}

// studentT975 is checked against an independent computation: the t CDF by
// the regularized incomplete beta function (continued fraction), which must
// be 0.975 at every tabulated value, df = 1..999.
func TestStudentTTable(t *testing.T) {
	cdf := func(x float64, df float64) float64 {
		return 1 - 0.5*incompleteBeta(df/2, 0.5, df/(df+x*x))
	}
	prev := math.Inf(1)
	for df := 1; df < len(studentT975); df++ {
		v := studentT975[df]
		if got := cdf(v, float64(df)); math.Abs(got-0.975) > 1e-10 {
			t.Fatalf("df %d: CDF(%v) = %v", df, v, got)
		}
		if !(v < prev && v > 1.959963984540054) {
			t.Fatalf("df %d: %v not decreasing towards z = 1.95996", df, v)
		}
		prev = v
	}
	for df, want := range map[int]float64{1: 12.706204736432095, 4: 2.7764451051977987, 30: 2.0422724563012373, 999: 1.9623414611334487} {
		if studentT975[df] != want {
			t.Errorf("df %d: %v, scipy %v", df, studentT975[df], want)
		}
	}
}

// incompleteBeta is I_x(a, b) by the Lentz continued fraction.
func incompleteBeta(a, b, x float64) float64 {
	if x <= 0 || x >= 1 {
		return math.Max(0, math.Min(1, x))
	}
	la, _ := math.Lgamma(a + b)
	lb, _ := math.Lgamma(a)
	lc, _ := math.Lgamma(b)
	front := math.Exp(la - lb - lc + a*math.Log(x) + b*math.Log(1-x))
	if x > (a+1)/(a+b+2) {
		return 1 - incompleteBeta(b, a, 1-x)
	}
	const tiny = 1e-300
	f, c, d := 1.0, 1.0, 0.0
	for i := 0; i <= 10_000; i++ {
		m := float64(i / 2)
		var num float64
		switch {
		case i == 0:
			num = 1
		case i%2 == 0:
			num = m * (b - m) * x / ((a + 2*m - 1) * (a + 2*m))
		default:
			num = -(a + m) * (a + b + m) * x / ((a + 2*m) * (a + 2*m + 1))
		}
		d = 1 + num*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		d = 1 / d
		c = 1 + num/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		cd := c * d
		f *= cd
		if math.Abs(1-cd) < 1e-16 {
			return front * (f - 1) / a
		}
	}
	return math.NaN()
}

// The 95% interval of the mean of replication values, against scipy; null
// below two values; a zero-width interval for identical values.
func TestCI95(t *testing.T) {
	ci := ci95([]float64{1, 2, 3, 4, 5})
	if ci == nil || math.Abs(ci.Lower-1.0367568385224393) > 1e-12 || math.Abs(ci.Upper-4.9632431614775605) > 1e-12 ||
		ci.Level != 0.95 || ci.Method != "student_t_replication_means" {
		t.Errorf("ci %+v", ci)
	}
	if ci95([]float64{7}) != nil || ci95(nil) != nil {
		t.Error("a CI from fewer than two values")
	}
	if ci := ci95([]float64{2, 2, 2}); ci == nil || ci.Lower != 2 || ci.Upper != 2 {
		t.Errorf("identical values: %+v", ci)
	}
	if ci := ci95(make([]float64, 1000)); ci == nil {
		t.Error("no CI at the 1000 replication limit")
	}
}

// GOLDEN-001's generic metrics are exactly testdata/contracts/valid-result.json's
// (hand-derived; the fixture leaves additional_metrics empty and carries the
// domain pack's DORA entry, which the engine does not compute).
func TestGoldenResultMetrics(t *testing.T) {
	m, s, w := build(t, runInput(golden(t, "deterministic-queue.json"), 1010, 0, 1))
	var fixture struct {
		Metrics map[string]any `json:"metrics"`
		Reps    any            `json:"replication_summaries"`
		Window  any            `json:"measurement_window"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/valid-result.json")), &fixture); err != nil {
		t.Fatal(err)
	}
	asJSON := func(v any) any {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	got := asJSON(m).(map[string]any)
	for _, k := range []string{"throughput", "lead_time_minutes", "resources", "queues", "failure_count", "rework_count"} {
		if g, f := asJSON(got[k]), fixture.Metrics[k]; !jsonEqual(g, f) {
			t.Errorf("%s:\n got %v\nwant %v", k, g, f)
		}
	}
	if !jsonEqual(asJSON(s), fixture.Reps) || !jsonEqual(asJSON(w), fixture.Window) {
		t.Errorf("summaries %v / window %v differ from the fixture", asJSON(s), asJSON(w))
	}
	if len(m.DORA) != 0 {
		t.Errorf("the generic engine computed DORA: %v", m.DORA)
	}
	rm := m.AdditionalMetrics[ReplicationMeanLeadTime]
	if rm.Status != simulation.Available || rm.SampleCount != 1 || rm.Population != simulation.PopulationReplication || rm.ConfidenceIntervalMean != nil || rm.Reason != nil {
		t.Errorf("replication mean lead time %+v", rm)
	}
	value(t, "replication mean lead", rm.Mean, 5)
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a, json.Deterministic(true))
	y, _ := json.Marshal(b, json.Deterministic(true))
	return string(x) == string(y)
}

// GOLDEN-002 (hand-derived, expected-cases.json): waits 5i, i = 0..19, so
// the time integral of the queue length is 950 minutes over 205.
func TestCongestedMetrics(t *testing.T) {
	m, s, _ := build(t, runInput(golden(t, "congested-queue.json"), 205, 0, 1))
	q := m.Queues["service"]
	value(t, "wait mean", q.WaitMeanMinutes, 47.5)
	value(t, "wait p50", q.WaitP50Minutes, 47.5)
	value(t, "wait p95", q.WaitP95Minutes, 90.25)
	if q.SampleCount != 20 || !near(q.QueueLengthMean, 950.0/205) {
		t.Errorf("queue %+v", q)
	}
	value(t, "lead mean", m.LeadTimeMinutes.Mean, 57.5)
	value(t, "lead p95", m.LeadTimeMinutes.P95, 100.25)
	value(t, "utilization", m.Resources["server"].UtilizationMean, 200.0/205)
	if s[0].CompletedCount != 20 || s[0].CensoredEntities != 0 || *m.Throughput.Mean != 20 {
		t.Errorf("summary %+v", s[0])
	}
}

// The window [W, H] (SIMULATION_SPEC §5) on GOLDEN-002, where entity i
// arrives at 5(i+1), starts at 5+10i and completes at 15+10i:
//   - W = 45: the completion and the arrival exactly at W count; the
//     cohort is i = 8..19 (leads 50..105), waits for i = 8..19, the server
//     is busy throughout, and the queue integral over [45, 205] is 870.
//   - H = 204: entity 19 is censored, the cohort is i = 8..18, its wait still
//     counts (it started at 195), and the queue integral is cut at 204.
func TestMeasurementWindow(t *testing.T) {
	for _, c := range []struct {
		horizon                   float64
		throughput, leads, waits  int64
		leadMean, waitMean, queue float64
		censored                  int64
	}{
		{205, 17, 12, 12, 77.5, 67.5, 870.0 / 160, 0},
		{204, 16, 11, 12, 75, 67.5, 870.0 / 159, 1},
	} {
		m, s, w := build(t, runInput(golden(t, "congested-queue.json"), c.horizon, 45, 1))
		if w.WarmupTicks != 45*60_000 || w.HorizonTicks != int64(c.horizon*60_000) {
			t.Errorf("window %+v", w)
		}
		q := m.Queues["service"]
		if *m.Throughput.Mean != float64(c.throughput) || m.LeadTimeMinutes.SampleCount != c.leads || q.SampleCount != c.waits || s[0].CensoredEntities != c.censored {
			t.Errorf("H=%v: throughput %v, leads %d, waits %d, censored %d", c.horizon, *m.Throughput.Mean, m.LeadTimeMinutes.SampleCount, q.SampleCount, s[0].CensoredEntities)
		}
		value(t, "lead mean", m.LeadTimeMinutes.Mean, c.leadMean)
		value(t, "wait mean", q.WaitMeanMinutes, c.waitMean)
		value(t, "utilization", m.Resources["server"].UtilizationMean, 1)
		if !near(q.QueueLengthMean, c.queue) {
			t.Errorf("H=%v: queue length %v, want %v", c.horizon, q.QueueLengthMean, c.queue)
		}
		if m.Resources["server"].ObservationTicks != w.HorizonTicks-w.WarmupTicks {
			t.Error("observation ticks")
		}
	}
}

// A service interval that straddles W counts only its part inside [W, H]:
// GOLDEN-001 with W = 12 sees 3 of the first service's 5 minutes, then 99
// whole ones: 498 busy minutes in 998. The completion at 15 counts towards
// throughput, but its entity arrived before W, so it is not in the cohort.
func TestPartialIntervals(t *testing.T) {
	m, _, _ := build(t, runInput(golden(t, "deterministic-queue.json"), 1010, 12, 1))
	value(t, "utilization", m.Resources["server"].UtilizationMean, 498.0/998)
	if *m.Throughput.Mean != 100 || m.LeadTimeMinutes.SampleCount != 99 || m.Queues["service"].SampleCount != 99 {
		t.Errorf("throughput %v, cohort %d, waits %d", *m.Throughput.Mean, m.LeadTimeMinutes.SampleCount, m.Queues["service"].SampleCount)
	}
}

// Nothing completes by H = 10: throughput is an available 0, lead time and
// its replication mean are unavailable with a reason, never 0; the visit
// that started at 5 is a known zero wait.
func TestNothingCompleted(t *testing.T) {
	m, s, _ := build(t, runInput(golden(t, "congested-queue.json"), 10, 0, 1))
	if m.Throughput.Status != simulation.Available || *m.Throughput.Mean != 0 || m.Throughput.SampleCount != 1 {
		t.Errorf("throughput %+v", m.Throughput)
	}
	for name, ms := range map[string]simulation.MetricSummary{"lead": m.LeadTimeMinutes, "replication mean": m.AdditionalMetrics[ReplicationMeanLeadTime]} {
		if ms.Status != simulation.Unavailable || ms.Mean != nil || ms.P50 != nil || ms.P95 != nil || ms.ConfidenceIntervalMean != nil ||
			ms.Reason == nil || *ms.Reason != ReasonNoCompletedEntity {
			t.Errorf("%s %+v", name, ms)
		}
	}
	if s[0].MeanLeadTimeMinutes != nil || s[0].CensoredEntities != 2 {
		t.Errorf("summary %+v", s[0])
	}
	if q := m.Queues["service"]; q.Status != simulation.Available || q.SampleCount != 1 || *q.WaitMeanMinutes != 0 {
		t.Errorf("queue %+v", q)
	}
}

// stochastic is source (exponential, mean 60 min) -> work (1 min) -> done.
func stochastic(t *testing.T) model.ModelIR {
	t.Helper()
	ir := golden(t, "deterministic-queue.json")
	mean := 60.0
	ir.Nodes[0].Arrival = &model.Arrival{Distribution: model.Distribution{Type: model.DistExponential, Mean: &mean}}
	one := 1.0
	ir.Nodes[1].ServiceTime.Distribution = model.Distribution{Type: model.DistConstant, Value: &one}
	return ir
}

// Replication statistics: throughput and counts are replication values with
// a Student-t CI over the replications; pooled lead time has no CI; the
// replication mean of lead times carries it, and needs a cohort in every
// replication.
func TestReplicationStatistics(t *testing.T) {
	m, s, _ := build(t, runInput(stochastic(t), 600, 0, 8))
	var counts, means []float64
	pooled := int64(0)
	for i, r := range s {
		if r.ReplicationIndex != i {
			t.Fatalf("summary %d has index %d", i, r.ReplicationIndex)
		}
		counts = append(counts, float64(r.CompletedCount))
		if r.MeanLeadTimeMinutes == nil {
			t.Fatalf("replication %d has no cohort over 600 minutes", i)
		}
		means = append(means, *r.MeanLeadTimeMinutes)
		pooled += r.CompletedCount
	}
	if slices.Min(counts) == slices.Max(counts) {
		t.Fatalf("replications identical: %v", counts)
	}
	want := ci95(counts)
	if m.Throughput.SampleCount != 8 || *m.Throughput.Mean != mean(counts) || *m.Throughput.ConfidenceIntervalMean != *want ||
		*m.Throughput.P95 != quantile7(counts, 0.95) {
		t.Errorf("throughput %+v, counts %v", m.Throughput, counts)
	}
	if m.LeadTimeMinutes.ConfidenceIntervalMean != nil || m.LeadTimeMinutes.SampleCount != pooled || m.LeadTimeMinutes.Population != simulation.PopulationEntity {
		t.Errorf("pooled lead %+v", m.LeadTimeMinutes)
	}
	rm := m.AdditionalMetrics[ReplicationMeanLeadTime]
	if rm.Status != simulation.Available || rm.SampleCount != 8 || *rm.ConfidenceIntervalMean != *ci95(means) {
		t.Errorf("replication mean %+v", rm)
	}

	// Over 45 minutes some replications complete nothing: the mean of
	// replication means is undefined, though pooled entities exist.
	m, s, _ = build(t, runInput(stochastic(t), 45, 0, 8))
	with := slices.IndexFunc(s, func(r simulation.ReplicationSummary) bool { return r.MeanLeadTimeMinutes != nil })
	without := slices.IndexFunc(s, func(r simulation.ReplicationSummary) bool { return r.MeanLeadTimeMinutes == nil })
	if with < 0 || without < 0 {
		t.Fatalf("want mixed cohorts for this seed: %+v", s)
	}
	rm = m.AdditionalMetrics[ReplicationMeanLeadTime]
	if rm.Status != simulation.Unavailable || rm.Reason == nil || *rm.Reason != ReasonReplicationWithoutCohort || m.LeadTimeMinutes.Status != simulation.Available {
		t.Errorf("replication mean %+v, pooled %+v", rm, m.LeadTimeMinutes)
	}
}

// Abandoned sinks are not throughput; failure and rework observations count
// only inside the window.
func TestOutcomesAndCounts(t *testing.T) {
	ir := golden(t, "deterministic-queue.json")
	ir.Nodes = append(ir.Nodes,
		model.Node{ID: "d", Type: model.NodeDecision}, model.Node{ID: "fail", Type: model.NodeFailure},
		model.Node{ID: "redo", Type: model.NodeRework}, model.Node{ID: "gone", Type: model.NodeSink, Outcome: model.SinkAbandoned})
	ir.Flows = []model.Flow{
		{ID: "a", From: "arrival", To: "service", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}},
		{ID: "b", From: "service", To: "d", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}},
		{ID: "c", From: "d", To: "fail", Routing: model.Routing{Type: model.RoutingProbability, Probability: 0.5}},
		{ID: "e", From: "d", To: "done", Routing: model.Routing{Type: model.RoutingProbability, Probability: 0.5}},
		{ID: "f", From: "fail", To: "redo", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}},
		{ID: "g", From: "redo", To: "gone", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}},
	}
	full, s, _ := build(t, runInput(ir, 1010, 0, 1))
	failed := int64(*full.FailureCount.Mean)
	if failed == 0 || failed == 100 || *full.ReworkCount.Mean != float64(failed) || s[0].CompletedCount != 100-failed || full.LeadTimeMinutes.SampleCount != 100-failed {
		t.Fatalf("failures %d, completed %d, leads %d", failed, s[0].CompletedCount, full.LeadTimeMinutes.SampleCount)
	}
	// With W = 505 the first 50 entities' outcomes (at 15, 25, ..., 505) are
	// split: those at t >= 505 count, so the window sees fewer failures.
	late, _, _ := build(t, runInput(ir, 1010, 505, 1))
	if l := int64(*late.FailureCount.Mean); l >= failed || l == 0 || *late.ReworkCount.Mean != float64(l) {
		t.Errorf("failures in [505, 1010]: %d of %d", l, failed)
	}
}

// An explicit queue owns its process's queue metrics; the process ID is not
// a second key (SIMULATION_SPEC §3).
func TestExplicitQueueOwnsMetrics(t *testing.T) {
	ir := golden(t, "deterministic-queue.json")
	ir.Nodes = append(ir.Nodes, model.Node{ID: "inbox", Type: model.NodeQueue})
	ir.Flows[0].To = "inbox"
	ir.Flows = append(ir.Flows, model.Flow{ID: "inbox_service", From: "inbox", To: "service", Routing: model.Routing{Type: model.RoutingProbability, Probability: 1}})
	m, _, _ := build(t, runInput(ir, 1010, 0, 1))
	direct, _, _ := build(t, runInput(golden(t, "deterministic-queue.json"), 1010, 0, 1))
	if q, ok := m.Queues["inbox"]; !ok || len(m.Queues) != 1 || !jsonEqual(q, direct.Queues["service"]) {
		t.Errorf("queues %v; the same waits keyed by the process: %v", m.Queues, direct.Queues)
	}
}

// Build refuses replications it cannot combine.
func TestBuildRejects(t *testing.T) {
	p, reps := collect(t, runInput(stochastic(t), 100, 0, 2))
	other, otherReps := collect(t, runInput(stochastic(t), 100, 0, 1))
	_ = other
	for name, rs := range map[string][]Replication{
		"none":          nil,
		"out of order":  {reps[1], reps[0]},
		"other program": {otherReps[0], reps[1]},
		"no collector":  {{Outcome: reps[0].Outcome}, reps[1]},
	} {
		if _, _, _, err := Build(p, rs); !errors.Is(err, ErrInconsistent) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Finish drops the per-entity maps (T-022 review: a run kept every
// replication's maps until Build); Build refuses unfinished collectors, so
// a caller cannot forget it.
func TestFinishReleasesEntityState(t *testing.T) {
	in := runInput(golden(t, "congested-queue.json"), 100, 10, 2) // 11 still in the system at H
	p, reps := collectUnfinished(t, in)
	held := 0
	for _, r := range reps {
		held += len(r.Collector.arrived) + len(r.Collector.entered)
	}
	if held == 0 {
		t.Fatal("no entity in the system at the horizon: the test would be vacuous")
	}
	if _, _, _, err := Build(p, reps); !errors.Is(err, ErrInconsistent) {
		t.Errorf("Build accepted unfinished collectors: %v", err)
	}
	for _, r := range reps {
		r.Collector.Finish()
		if r.Collector.arrived != nil || r.Collector.entered != nil {
			t.Fatal("Finish kept the entity maps")
		}
	}
	got, _, _, err := Build(p, reps)
	want, _, _ := build(t, in)
	if err != nil || !jsonEqual(got, want) {
		t.Errorf("metrics after Finish: %v", err)
	}
}
