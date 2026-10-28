package analyst_test

// The server's check of an analyst answer (T-030; AI_SPEC §9, D-026,
// D-090): TEST-024 (evidence cites supplied, available metrics of the run
// it names; deltas are the comparison's; prose numbers are supplied
// values) and the adversarial invented-metric answers that must never be
// stored as completed (AI_SPEC §10's gate).

import (
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	"github.com/kcl1104/geminus/internal/ai/analyst"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/simulation"
)

const (
	zeroHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	baseID   = "sim_01M4YDQK00FGZZWH0PP15XPNFA"
	scenID   = "sim_01M4YDQK00FGZZWH0PP15XPNFB"
	ticks    = 60_600_000 // 1010 minutes
)

func ptr(v float64) *float64 { return &v }

func summary(v float64, pop simulation.Population, unit string) simulation.MetricSummary {
	return simulation.MetricSummary{Status: simulation.Available, Mean: ptr(v), P50: ptr(v), P95: ptr(v), SampleCount: 1, Population: pop, Unit: unit}
}

// result is a completed one-replication result: throughput, lead time, the
// ICT tester's utilization and its queue; first-pass yield as a domain
// metric; a DORA metric unavailable.
func result(id string, throughput, lead, util float64) *simulation.Result {
	reason := "MISSING_DORA_EVENT_MAPPING"
	return &simulation.Result{
		RunID: id, Status: "completed", EngineVersion: "des-go-0.5.0",
		Metrics: simulation.Metrics{
			Throughput:      summary(throughput, simulation.PopulationReplication, "count"),
			LeadTimeMinutes: summary(lead, simulation.PopulationEntity, "minute"),
			Resources:       map[string]simulation.ResourceStats{"ict": {UtilizationMean: ptr(util), ObservationTicks: ticks, Capacity: 1}},
			Queues: map[string]simulation.QueueStats{"q_ict": {Status: simulation.Available, WaitMeanMinutes: ptr(lead / 2), WaitP50Minutes: ptr(lead / 2),
				WaitP95Minutes: ptr(lead / 2), QueueLengthMean: 1.5, SampleCount: 100}},
			FailureCount:      summary(0, simulation.PopulationReplication, "count"),
			ReworkCount:       summary(0, simulation.PopulationReplication, "count"),
			DORA:              map[string]simulation.MetricSummary{"change_lead_time": {Status: simulation.Unavailable, Population: simulation.PopulationEntity, Unit: "minute", Reason: &reason}},
			AdditionalMetrics: map[string]simulation.MetricSummary{},
			Domain:            map[string]simulation.MetricSummary{"first_pass_yield": summary(0.97, simulation.PopulationReplication, "ratio")},
		},
		ReplicationSummaries: []simulation.ReplicationSummary{{ReplicationIndex: 0, CompletedCount: int64(throughput), MeanLeadTimeMinutes: ptr(lead),
			ResourceUtilization: map[string]float64{"ict": util}}},
		MeasurementWindow: simulation.MeasurementWindow{TickUnit: "millisecond", HorizonTicks: ticks, TimeBoundary: "warmup <= t <= horizon"},
		Reproducibility: simulation.Reproducibility{Seed: 7, Replications: 1, ModelHash: zeroHash, EffectiveModelHash: zeroHash, ConfigHash: zeroHash,
			DatasetManifestHash: zeroHash, ResultDigest: zeroHash},
	}
}

func run(id, patch string, r *simulation.Result) compare.Run {
	return compare.Run{ID: id, ModelVersionID: "mv_01M4YDQK00FRYWV7GS7X1QCXZR", EngineVersion: "des-go-0.5.0", DatasetManifestHash: zeroHash,
		ScenarioPatch: []byte(patch), ModelHash: zeroHash, EffectiveModelHash: zeroHash, ConfigHash: zeroHash,
		Config: simulation.RunConfig{Horizon: simulation.Duration{Value: 1010, Unit: model.Minute}, Warmup: simulation.Duration{Value: 0, Unit: model.Minute},
			Replications: 1, Seed: 7, TraceLevel: simulation.TraceNone}, Result: r}
}

// fixture: a second ICT tester takes the lead time from 40 to 32 minutes
// (-8, -0.2) and throughput from 100 to 125 (+25, +0.25).
func fixture(t *testing.T) analyst.Input {
	t.Helper()
	b, s := result(baseID, 100, 40, 0.9), result(scenID, 125, 32, 0.6)
	c, err := compare.Compare(run(baseID, "[]", b), run(scenID, `[{"op":"test","path":"/resources/0/id","value":"ict"},{"op":"replace","path":"/resources/0/capacity","value":2}]`, s))
	if err != nil {
		t.Fatal(err)
	}
	var allowed []aicontext.MetricRef
	for _, role := range []string{"baseline", "scenario"} {
		for _, p := range []string{"/metrics/domain/first_pass_yield", "/metrics/failure_count", "/metrics/lead_time_minutes", "/metrics/queues/q_ict",
			"/metrics/resources/ict", "/metrics/rework_count", "/metrics/throughput"} {
			allowed = append(allowed, aicontext.MetricRef{Run: role, MetricPath: p})
		}
	}
	// The numbers of the results and the comparison (aicontext collects them the same way).
	supplied := []float64{0, 1, 1.5, 2, 7, 1010, 100, 125, 40, 32, 20, 16, 0.9, 0.6, 0.97, 25, 0.25, -8, -0.2, -0.3, -0.33333333333333337, -4}
	return analyst.Input{Output: grounded(), Allowed: allowed, Supplied: supplied, Scenario: s, Baseline: b, Comparison: &c}
}

// grounded is an answer every check accepts: deltas rounded the way a
// person writes them, evidence by role, prose numbers that were supplied.
func grounded() provider.AnalysisOutput {
	reason := "A second tester halves the wait before ICT."
	return provider.AnalysisOutput{
		Baseline: &provider.AnalysisSummary{Summary: "With 1 ICT tester the line ships 100 boards; the tester is busy 90% of the time."},
		Scenario: provider.AnalysisSummary{Summary: "With 2 testers the line ships 125 boards and lead time falls to 32 minutes."},
		Delta: []provider.AnalysisDelta{
			{MetricPath: "/metrics/lead_time_minutes/p50", AbsoluteDelta: ptr(-8), RelativeDelta: ptr(-0.2), Reason: &reason},
			{MetricPath: "/metrics/throughput/mean", AbsoluteDelta: ptr(25), RelativeDelta: ptr(0.25), Reason: nil},
			{MetricPath: "/metrics/resources/ict/utilization_mean", AbsoluteDelta: ptr(-0.3), RelativeDelta: ptr(-0.333), Reason: nil},
		},
		Bottleneck: []provider.Bottleneck{
			{Statement: "ICT was the bottleneck at 90% utilization.", Kind: "observation", Evidence: []provider.Evidence{
				{Run: "baseline", MetricPath: "/metrics/resources/ict"}, {Run: "baseline", MetricPath: "/metrics/queues/q_ict/wait_mean_minutes"}}},
			{Statement: "With 2 testers the constraint likely moves downstream.", Kind: "inference", Evidence: []provider.Evidence{
				{Run: "scenario", MetricPath: "/metrics/resources/ict/utilization_mean"}}},
		},
		Assumptions:              []string{"The test times stay as modeled."},
		Uncertainty:              []string{"Only 1 replication was run, so the spread is unknown."},
		SuggestedNextExperiments: []provider.Experiment{{Name: "More replications", Hypothesis: "Running more replications narrows the interval.", RequiresConfirmation: true}},
	}
}

func codes(issues []analyst.Issue) []string {
	var out []string
	for _, i := range issues {
		out = append(out, i.Code+" "+i.Path)
	}
	return out
}

func TestGroundedAnswerIsStoredWithTheComparisonsValues(t *testing.T) {
	in := fixture(t)
	got, issues := analyst.Check(in)
	if len(issues) > 0 {
		t.Fatalf("issues %v", codes(issues))
	}
	// The stored deltas are the server's, not the rounded ones the answer wrote.
	m, _ := in.Comparison.Metric("/metrics/resources/ict/utilization_mean")
	if d := got.Delta[2]; *d.AbsoluteDelta != *m.AbsoluteDelta || *d.RelativeDelta != *m.RelativeDelta || *d.RelativeDelta == -0.333 {
		t.Errorf("delta %v %v, comparison %v %v", *d.AbsoluteDelta, *d.RelativeDelta, *m.AbsoluteDelta, *m.RelativeDelta)
	}
	if got.Scenario.Summary != in.Output.Scenario.Summary || len(got.Bottleneck) != 2 || got.Bottleneck[1].Kind != "inference" {
		t.Errorf("the prose changed: %+v", got)
	}
	// Nothing of the input is shared with what is stored.
	got.Delta[0].Reason = nil
	if in.Output.Delta[0].Reason == nil {
		t.Error("the stored analysis shares the answer's memory")
	}
}

// Words of doubt belong where the analysis says what is uncertain or
// assumed; there they are not refused.
func TestDoubtIsNotACertainty(t *testing.T) {
	in := fixture(t)
	out := grounded()
	out.Uncertainty = append(out.Uncertainty, "The gain is not guaranteed beyond the simulated week.")
	out.Assumptions = append(out.Assumptions, "保證測試時間與模型相同。")
	in.Output = out
	if _, issues := analyst.Check(in); len(issues) > 0 {
		t.Fatalf("issues %v", codes(issues))
	}
}

func TestWithoutABaseline(t *testing.T) {
	in := fixture(t)
	in.Baseline, in.Comparison = nil, nil
	in.Allowed = slices.DeleteFunc(in.Allowed, func(r aicontext.MetricRef) bool { return r.Run == "baseline" })
	out := grounded()
	out.Baseline, out.Delta = nil, []provider.AnalysisDelta{}
	out.Bottleneck = out.Bottleneck[1:]
	in.Output = out
	if _, issues := analyst.Check(in); len(issues) > 0 {
		t.Fatalf("a scenario-only answer: %v", codes(issues))
	}
	// A baseline summary, a delta or baseline evidence invents a comparison.
	for name, change := range map[string]func(*provider.AnalysisOutput){
		"summary": func(o *provider.AnalysisOutput) {
			o.Baseline = &provider.AnalysisSummary{Summary: "The baseline did well."}
		},
		"delta":    func(o *provider.AnalysisOutput) { o.Delta = grounded().Delta[:1] },
		"evidence": func(o *provider.AnalysisOutput) { o.Bottleneck = grounded().Bottleneck[:1] },
	} {
		o := out
		o.Bottleneck = slices.Clone(out.Bottleneck)
		change(&o)
		in.Output = o
		if _, issues := analyst.Check(in); len(issues) == 0 || issues[0].Code != analyst.NoBaseline {
			t.Errorf("%s without a baseline: %v", name, codes(issues))
		}
	}
}

// Each answer invents something; none is stored, and the issue says where.
func TestUngroundedAnswersAreRefused(t *testing.T) {
	cases := []struct {
		name   string
		change func(*provider.AnalysisOutput)
		want   string
	}{
		{"a delta of a metric the comparison does not have", func(o *provider.AnalysisOutput) {
			o.Delta[0].MetricPath = "/metrics/domain/oee/mean"
		}, analyst.DeltaUnknown + " /delta/0/metric_path"},
		{"a summary-level delta path", func(o *provider.AnalysisOutput) {
			o.Delta[0].MetricPath = "/metrics/lead_time_minutes"
		}, analyst.DeltaUnknown + " /delta/0/metric_path"},
		{"the wrong direction", func(o *provider.AnalysisOutput) { o.Delta[0].AbsoluteDelta = ptr(8) }, analyst.DeltaMismatch + " /delta/0/absolute_delta"},
		{"a wrong rounding", func(o *provider.AnalysisOutput) { o.Delta[1].RelativeDelta = ptr(0.26) }, analyst.DeltaMismatch + " /delta/1/relative_delta"},
		{"a relative delta rounded to nothing (review r1)", func(o *provider.AnalysisOutput) { o.Delta[0].RelativeDelta = ptr(0) }, analyst.DeltaMismatch + " /delta/0/relative_delta"},
		{"a relative delta to one decimal", func(o *provider.AnalysisOutput) { o.Delta[2].RelativeDelta = ptr(-0.3) }, analyst.DeltaMismatch + " /delta/2/relative_delta"},
		{"a value where the comparison has none", func(o *provider.AnalysisOutput) {
			o.Delta = append(o.Delta, provider.AnalysisDelta{MetricPath: "/metrics/dora/change_lead_time/p50", AbsoluteDelta: ptr(0), RelativeDelta: nil})
		}, analyst.DeltaMismatch + " /delta/3/absolute_delta"},
		{"no value where the comparison has one", func(o *provider.AnalysisOutput) { o.Delta[1].AbsoluteDelta = nil }, analyst.DeltaMismatch + " /delta/1/absolute_delta"},
		{"a metric twice", func(o *provider.AnalysisOutput) { o.Delta = append(o.Delta, o.Delta[0]) }, analyst.DeltaRepeated + " /delta/3/metric_path"},
		{"evidence of an unlisted metric", func(o *provider.AnalysisOutput) {
			o.Bottleneck[0].Evidence[0].MetricPath = "/metrics/resources/aoi"
		}, analyst.EvidenceUnknown + " /bottleneck/0/evidence/0"},
		{"evidence of an unavailable metric", func(o *provider.AnalysisOutput) {
			o.Bottleneck[0].Evidence[0].MetricPath = "/metrics/dora/change_lead_time/p50"
		}, analyst.EvidenceUnknown + " /bottleneck/0/evidence/0"},
		{"evidence that is no statistic under a listed metric", func(o *provider.AnalysisOutput) {
			o.Bottleneck[0].Evidence[0].MetricPath = "/metrics/resources/ict/capacity"
		}, analyst.EvidenceUnknown + " /bottleneck/0/evidence/0"},
		{"a prefix that is not a member", func(o *provider.AnalysisOutput) {
			o.Bottleneck[0].Evidence[0].MetricPath = "/metrics/throughputs"
		}, analyst.EvidenceUnknown + " /bottleneck/0/evidence/0"},
		{"evidence of a run that is not there", func(o *provider.AnalysisOutput) { o.Bottleneck[0].Evidence[0].Run = "observed" }, analyst.EvidenceUnknown + " /bottleneck/0/evidence/0"},
		{"an invented number in a summary", func(o *provider.AnalysisOutput) {
			o.Scenario.Summary = "Lead time falls to 31 minutes."
		}, analyst.NumberUnverified + " /scenario/summary"},
		{"an invented percentage in a statement", func(o *provider.AnalysisOutput) {
			o.Bottleneck[1].Statement = "Throughput will rise another 12% next quarter."
		}, analyst.NumberUnverified + " /bottleneck/1/statement"},
		{"a number word in an experiment", func(o *provider.AnalysisOutput) {
			o.SuggestedNextExperiments[0].Hypothesis = "Forty-seven replications would settle it."
		}, analyst.NumberUnverified + " /suggested_next_experiments/0/hypothesis"},
		{"an invented number in a delta's reason", func(o *provider.AnalysisOutput) {
			r := "The wait fell by 13 minutes."
			o.Delta[0].Reason = &r
		}, analyst.NumberUnverified + " /delta/0/reason"},
		{"an invented number in an assumption", func(o *provider.AnalysisOutput) { o.Assumptions[0] = "Yield is 99.5%." }, analyst.NumberUnverified + " /assumptions/0"},
		{"an invented number in the uncertainty", func(o *provider.AnalysisOutput) { o.Uncertainty[0] = "The error is about 3 minutes." }, analyst.NumberUnverified + " /uncertainty/0"},
		{"an experiment that claims to need no person", func(o *provider.AnalysisOutput) {
			o.SuggestedNextExperiments[0].RequiresConfirmation = false
		}, analyst.OutputInvalid + " /suggested_next_experiments/0/requires_confirmation"},
		{"a kind that is neither", func(o *provider.AnalysisOutput) { o.Bottleneck[0].Kind = "fact" }, analyst.OutputInvalid + " /bottleneck/0/kind"},
		{"a conclusion without evidence", func(o *provider.AnalysisOutput) { o.Bottleneck[0].Evidence = nil }, analyst.OutputInvalid + " /bottleneck/0/evidence"},
		{"a promised outcome", func(o *provider.AnalysisOutput) {
			o.Scenario.Summary = "A second tester is guaranteed to ship more boards."
		}, analyst.Certainty + " /scenario/summary"},
		{"a promised outcome in Chinese", func(o *provider.AnalysisOutput) {
			o.Bottleneck[1].Statement = "增加測試機必然縮短前置時間。"
		}, analyst.Certainty + " /bottleneck/1/statement"},
		{"a promised outcome in an experiment", func(o *provider.AnalysisOutput) {
			o.SuggestedNextExperiments[0].Hypothesis = "More replications will definitely narrow it."
		}, analyst.Certainty + " /suggested_next_experiments/0/hypothesis"},
		{"a run ID in the prose", func(o *provider.AnalysisOutput) {
			o.Assumptions[0] = "Compared with sim_01M4YDQK00TB00000000000003."
		}, analyst.ServerID + " /assumptions/0"},
		{"one replication and nothing uncertain", func(o *provider.AnalysisOutput) { o.Uncertainty = nil }, analyst.UncertaintyMissing + " /uncertainty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture(t)
			out := grounded()
			c.change(&out)
			in.Output = out
			got, issues := analyst.Check(in)
			if !slices.Contains(codes(issues), c.want) {
				t.Fatalf("issues %v, want %q", codes(issues), c.want)
			}
			if got.Scenario.Summary != "" || got.Delta != nil {
				t.Errorf("an ungrounded answer came back: %+v", got)
			}
		})
	}
}
