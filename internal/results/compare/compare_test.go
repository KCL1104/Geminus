package compare_test

// Unit tests of the baseline/scenario comparison (T-029; SIMULATION_SPEC
// §7, D-010; D-088, D-090 PROPOSED): TEST-022 (deltas, baseline zero),
// TEST-023 (DORA unavailable stays unavailable, generic failures are not
// DORA), and the metric paths an analysis may cite (TEST-024's basis).

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/results/compare"
	"github.com/kcl1104/geminus/internal/simulation"
)

const (
	versionID = "mv_01M4YDQK00FRYWV7GS7X1QCXZR"
	baseID    = "sim_01M4YDQK00FGZZWH0PP15XPNFA"
	scenID    = "sim_01M4YDQK00FGZZWH0PP15XPNFB"
	zeroHash  = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	engineV   = "des-go-0.4.0"
	// 1010 minutes, the window of the deterministic-queue golden.
	horizonTicks = 60_600_000
	twoServers   = `[{"op":"test","path":"/resources/0/id","value":"server"},{"op":"replace","path":"/resources/0/capacity","value":2}]`
)

func ptr(v float64) *float64 { return &v }
func str(s string) *string   { return &s }

func summary(v float64, pop simulation.Population, unit string) simulation.MetricSummary {
	return simulation.MetricSummary{Status: simulation.Available, Mean: ptr(v), P50: ptr(v), P95: ptr(v), SampleCount: 1, Population: pop, Unit: unit}
}

func unavailable(pop simulation.Population, unit, reason string) simulation.MetricSummary {
	return simulation.MetricSummary{Status: simulation.Unavailable, Population: pop, Unit: unit, Reason: str(reason)}
}

// result is a completed result of one replication over [0, 1010 min].
func result(id string) *simulation.Result {
	return &simulation.Result{
		RunID: id, Status: "completed", EngineVersion: engineV,
		Metrics: simulation.Metrics{
			Throughput:      summary(100, simulation.PopulationReplication, "count"),
			LeadTimeMinutes: summary(5, simulation.PopulationEntity, "minute"),
			Resources: map[string]simulation.ResourceStats{
				"server": {UtilizationMean: ptr(0.5), ObservationTicks: horizonTicks, Capacity: 1},
			},
			Queues: map[string]simulation.QueueStats{
				"service": {Status: simulation.Available, WaitMeanMinutes: ptr(0), WaitP50Minutes: ptr(0), WaitP95Minutes: ptr(0), QueueLengthMean: 0, SampleCount: 100},
			},
			FailureCount:      summary(0, simulation.PopulationReplication, "count"),
			ReworkCount:       summary(0, simulation.PopulationReplication, "count"),
			DORA:              map[string]simulation.MetricSummary{"change_lead_time": unavailable(simulation.PopulationEntity, "minute", "MISSING_DORA_EVENT_MAPPING")},
			AdditionalMetrics: map[string]simulation.MetricSummary{},
		},
		ReplicationSummaries: []simulation.ReplicationSummary{{ReplicationIndex: 0, CompletedCount: 100, MeanLeadTimeMinutes: ptr(5),
			ResourceUtilization: map[string]float64{"server": 0.5}}},
		MeasurementWindow: simulation.MeasurementWindow{TickUnit: "millisecond", WarmupTicks: 0, HorizonTicks: horizonTicks, TimeBoundary: "warmup <= t <= horizon"},
		Reproducibility: simulation.Reproducibility{Seed: 7, Replications: 1, ModelHash: zeroHash, EffectiveModelHash: zeroHash, ConfigHash: zeroHash,
			DatasetManifestHash: zeroHash, ResultDigest: zeroHash},
		Trace: simulation.Trace{},
	}
}

func run(id, patch string, r *simulation.Result) compare.Run {
	return compare.Run{ID: id, ModelVersionID: versionID, EngineVersion: engineV, DatasetManifestHash: zeroHash, ScenarioPatch: []byte(patch),
		ModelHash: zeroHash, EffectiveModelHash: zeroHash, ConfigHash: zeroHash,
		Config: simulation.RunConfig{Horizon: simulation.Duration{Value: 1010, Unit: model.Minute}, Warmup: simulation.Duration{Value: 0, Unit: model.Minute},
			Replications: 1, Seed: 7, TraceLevel: simulation.TraceNone}, Result: r}
}

// pair is a baseline run and a two-server scenario of it, with equal metrics.
func pair() (compare.Run, compare.Run) {
	return run(baseID, "[]", result(baseID)), run(scenID, twoServers, result(scenID))
}

// replications gives r n replications, consistently in its config and result.
func replications(r *compare.Run, n int) {
	r.Config.Replications = n
	r.Result.Reproducibility.Replications = n
	r.Result.ReplicationSummaries = nil
	for i := range n {
		r.Result.ReplicationSummaries = append(r.Result.ReplicationSummaries, simulation.ReplicationSummary{ReplicationIndex: i, CompletedCount: 100,
			ResourceUtilization: map[string]float64{}})
	}
}

func mustCompare(t *testing.T, b, s compare.Run) compare.Comparison {
	t.Helper()
	c, err := compare.Compare(b, s)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	return c
}

func metricOf(t *testing.T, c compare.Comparison, path string) compare.Metric {
	t.Helper()
	i := slices.IndexFunc(c.Metrics, func(m compare.Metric) bool { return m.MetricPath == path })
	if i < 0 {
		var paths []string
		for _, m := range c.Metrics {
			paths = append(paths, m.MetricPath)
		}
		t.Fatalf("no metric %s in %v", path, paths)
	}
	m, ok := c.Metric(path)
	if !ok || !reflect.DeepEqual(m, c.Metrics[i]) {
		t.Errorf("Metric(%s) = %+v, %v; listed %+v", path, m, ok, c.Metrics[i])
	}
	return c.Metrics[i]
}

func show(p *float64) string {
	if p == nil {
		return "null"
	}
	return fmt.Sprint(*p)
}

func same(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b && math.Signbit(*a) == math.Signbit(*b)
}

// want checks one comparison metric; nil is JSON null.
func want(t *testing.T, m compare.Metric, b, s, abs, rel *float64, status compare.Status, reason string) {
	t.Helper()
	got := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	if !same(m.Baseline, b) || !same(m.Scenario, s) || !same(m.AbsoluteDelta, abs) || !same(m.RelativeDelta, rel) || m.Status != status || got(m.Reason) != reason {
		t.Errorf("%s: baseline %s scenario %s abs %s rel %s %s %q; want %s %s %s %s %s %q", m.MetricPath,
			show(m.Baseline), show(m.Scenario), show(m.AbsoluteDelta), show(m.RelativeDelta), m.Status, got(m.Reason),
			show(b), show(s), show(abs), show(rel), status, reason)
	}
}

// TEST-022: absolute = scenario − baseline, relative = absolute / baseline,
// as the server computes them once (SIMULATION_SPEC §7); the comparison is
// comparable and lists every statistic.
func TestDeltas(t *testing.T) {
	b, s := pair()
	s.Result.Metrics.Throughput = summary(110, simulation.PopulationReplication, "count")
	s.Result.Metrics.Throughput.P95 = ptr(120)
	s.Result.Metrics.LeadTimeMinutes = summary(4, simulation.PopulationEntity, "minute")
	s.Result.Metrics.LeadTimeMinutes.P50 = ptr(6.5)
	s.Result.Metrics.Resources["server"] = simulation.ResourceStats{UtilizationMean: ptr(0.25), ObservationTicks: horizonTicks, Capacity: 2}
	c := mustCompare(t, b, s)
	if !c.Comparable || len(c.IncomparableReasons) != 0 || c.BaselineRunID != baseID || c.ScenarioRunID != scenID {
		t.Fatalf("comparison %+v", c)
	}
	want(t, metricOf(t, c, "/metrics/throughput/mean"), ptr(100), ptr(110), ptr(10), ptr(0.1), compare.Available, "")
	want(t, metricOf(t, c, "/metrics/throughput/p95"), ptr(100), ptr(120), ptr(20), ptr(0.2), compare.Available, "")
	want(t, metricOf(t, c, "/metrics/lead_time_minutes/mean"), ptr(5), ptr(4), ptr(-1), ptr(-0.2), compare.Available, "")
	want(t, metricOf(t, c, "/metrics/lead_time_minutes/p50"), ptr(5), ptr(6.5), ptr(1.5), ptr(0.3), compare.Available, "")
	want(t, metricOf(t, c, "/metrics/resources/server/utilization_mean"), ptr(0.5), ptr(0.25), ptr(-0.25), ptr(-0.5), compare.Available, "")
	// The same value on both sides: a zero delta, available.
	want(t, metricOf(t, c, "/metrics/throughput/p50"), ptr(100), ptr(110), ptr(10), ptr(0.1), compare.Available, "")
	b2, s2 := pair()
	want(t, metricOf(t, mustCompare(t, b2, s2), "/metrics/lead_time_minutes/p95"), ptr(5), ptr(5), ptr(0), ptr(0), compare.Available, "")
	// The relative delta follows the formula, also below a negative baseline
	// (none of the generic metrics can be negative; a domain metric might).
	b.Result.Metrics.Domain = map[string]simulation.MetricSummary{"margin": summary(-2, simulation.PopulationReplication, "count")}
	s.Result.Metrics.Domain = map[string]simulation.MetricSummary{"margin": summary(-1, simulation.PopulationReplication, "count")}
	want(t, metricOf(t, mustCompare(t, b, s), "/metrics/domain/margin/mean"), ptr(-2), ptr(-1), ptr(1), ptr(-0.5), compare.Available, "")
}

// TEST-022: a baseline of 0 has no relative delta (null, BASELINE_ZERO),
// whatever the scenario; the absolute delta stays. Never a fake percentage.
func TestBaselineZero(t *testing.T) {
	b, s := pair()
	s.Result.Metrics.FailureCount = summary(3, simulation.PopulationReplication, "count")
	s.Result.Metrics.Queues["service"] = simulation.QueueStats{Status: simulation.Available, WaitMeanMinutes: ptr(2.5), WaitP50Minutes: ptr(0),
		WaitP95Minutes: ptr(10), QueueLengthMean: 0.25, SampleCount: 100}
	c := mustCompare(t, b, s)
	want(t, metricOf(t, c, "/metrics/failure_count/mean"), ptr(0), ptr(3), ptr(3), nil, compare.Available, compare.ReasonBaselineZero)
	want(t, metricOf(t, c, "/metrics/rework_count/mean"), ptr(0), ptr(0), ptr(0), nil, compare.Available, compare.ReasonBaselineZero)
	want(t, metricOf(t, c, "/metrics/queues/service/wait_mean_minutes"), ptr(0), ptr(2.5), ptr(2.5), nil, compare.Available, compare.ReasonBaselineZero)
	want(t, metricOf(t, c, "/metrics/queues/service/wait_p50_minutes"), ptr(0), ptr(0), ptr(0), nil, compare.Available, compare.ReasonBaselineZero)
	want(t, metricOf(t, c, "/metrics/queues/service/queue_length_mean"), ptr(0), ptr(0.25), ptr(0.25), nil, compare.Available, compare.ReasonBaselineZero)
	// A baseline that decoded as -0 is zero too.
	b.Result.Metrics.Domain = map[string]simulation.MetricSummary{"x": summary(math.Copysign(0, -1), simulation.PopulationReplication, "count")}
	s.Result.Metrics.Domain = map[string]simulation.MetricSummary{"x": summary(1, simulation.PopulationReplication, "count")}
	want(t, metricOf(t, mustCompare(t, b, s), "/metrics/domain/x/mean"), ptr(0), ptr(1), ptr(1), nil, compare.Available, compare.ReasonBaselineZero)
}

// An unavailable statistic is null, never 0, and leaves both deltas null;
// the other run's value is kept for a side-by-side view. A queue without a
// completed visit has no waits but still its time-integral length.
func TestUnavailable(t *testing.T) {
	noLead := unavailable(simulation.PopulationEntity, "minute", "NO_COMPLETED_ENTITY_IN_WINDOW")
	noVisit := simulation.QueueStats{Status: simulation.Unavailable, QueueLengthMean: 0.5, Reason: str("NO_COMPLETED_QUEUE_VISIT_IN_WINDOW")}
	b, s := pair()
	b.Result.Metrics.LeadTimeMinutes = noLead
	s.Result.Metrics.Queues["service"] = noVisit
	s.Result.Metrics.Resources["server"] = simulation.ResourceStats{ObservationTicks: horizonTicks, Capacity: 1, Reason: str("SOME_REASON")}
	c := mustCompare(t, b, s)
	for _, stat := range []string{"mean", "p50", "p95"} {
		want(t, metricOf(t, c, "/metrics/lead_time_minutes/"+stat), nil, ptr(5), nil, nil, compare.Unavailable, compare.ReasonBaselineUnavailable)
	}
	for _, stat := range []string{"wait_mean_minutes", "wait_p50_minutes", "wait_p95_minutes"} {
		want(t, metricOf(t, c, "/metrics/queues/service/"+stat), ptr(0), nil, nil, nil, compare.Unavailable, compare.ReasonScenarioUnavailable)
	}
	want(t, metricOf(t, c, "/metrics/queues/service/queue_length_mean"), ptr(0), ptr(0.5), ptr(0.5), nil, compare.Available, compare.ReasonBaselineZero)
	want(t, metricOf(t, c, "/metrics/resources/server/utilization_mean"), ptr(0.5), nil, nil, nil, compare.Unavailable, compare.ReasonScenarioUnavailable)
	s.Result.Metrics.LeadTimeMinutes = noLead
	want(t, metricOf(t, mustCompare(t, b, s), "/metrics/lead_time_minutes/mean"), nil, nil, nil, nil, compare.Unavailable, compare.ReasonBothUnavailable)
	if !mustCompare(t, b, s).Comparable {
		t.Error("an unavailable metric made the runs incomparable")
	}
}

// TEST-023 (example pack): DORA metrics without an approved mapping are
// unavailable in both runs and stay unavailable in the comparison, with
// null deltas; the generic failure count is compared as itself, never under
// a DORA path, so a CI failure is not a DORA failure.
func TestDORAStaysUnavailable(t *testing.T) {
	b, s := pair()
	for _, r := range []*compare.Run{&b, &s} {
		r.Result.Metrics.DORA = map[string]simulation.MetricSummary{}
		for _, k := range []string{"deployment_frequency", "change_lead_time", "change_failure_percentage", "failed_deployment_recovery_time", "deployment_rework_rate"} {
			r.Result.Metrics.DORA[k] = unavailable(simulation.PopulationEntity, "minute", "MISSING_DORA_EVENT_MAPPING")
		}
	}
	s.Result.Metrics.FailureCount = summary(4, simulation.PopulationReplication, "count")
	c := mustCompare(t, b, s)
	dora := 0
	for _, m := range c.Metrics {
		if strings.HasPrefix(m.MetricPath, "/metrics/dora/") {
			dora++
			want(t, m, nil, nil, nil, nil, compare.Unavailable, compare.ReasonBothUnavailable)
		}
	}
	if dora != 15 {
		t.Errorf("%d DORA statistics, want 5 metrics × 3", dora)
	}
	want(t, metricOf(t, c, "/metrics/failure_count/mean"), ptr(0), ptr(4), ptr(4), nil, compare.Available, compare.ReasonBaselineZero)
	// A DORA metric one run has and the other lacks is missing, not 0.
	delete(b.Result.Metrics.DORA, "change_lead_time")
	s.Result.Metrics.DORA["change_lead_time"] = summary(30, simulation.PopulationEntity, "minute")
	want(t, metricOf(t, mustCompare(t, b, s), "/metrics/dora/change_lead_time/mean"), nil, ptr(30), nil, nil, compare.Unavailable, compare.ReasonMissingInBaseline)
}

// A metric only one run has (a key in one run's map) is unavailable as
// missing on the other side; missing outranks unavailable.
func TestMissing(t *testing.T) {
	b, s := pair()
	s.Result.Metrics.Resources["tester"] = simulation.ResourceStats{UtilizationMean: ptr(0.75), ObservationTicks: horizonTicks, Capacity: 1}
	b.Result.Metrics.Queues["review"] = simulation.QueueStats{Status: simulation.Unavailable, QueueLengthMean: 0, Reason: str("NO_COMPLETED_QUEUE_VISIT_IN_WINDOW")}
	s.Result.Metrics.AdditionalMetrics = map[string]simulation.MetricSummary{"wip": summary(3, simulation.PopulationReplication, "count")}
	c := mustCompare(t, b, s)
	want(t, metricOf(t, c, "/metrics/resources/tester/utilization_mean"), nil, ptr(0.75), nil, nil, compare.Unavailable, compare.ReasonMissingInBaseline)
	want(t, metricOf(t, c, "/metrics/queues/review/wait_mean_minutes"), nil, nil, nil, nil, compare.Unavailable, compare.ReasonMissingInScenario)
	want(t, metricOf(t, c, "/metrics/queues/review/queue_length_mean"), ptr(0), nil, nil, nil, compare.Unavailable, compare.ReasonMissingInScenario)
	want(t, metricOf(t, c, "/metrics/additional_metrics/wip/p95"), nil, ptr(3), nil, nil, compare.Unavailable, compare.ReasonMissingInBaseline)
	// A domain map in one run only (metrics.domain is optional).
	b.Result.Metrics.Domain = map[string]simulation.MetricSummary{"fpy": summary(0.9, simulation.PopulationReplication, "ratio")}
	want(t, metricOf(t, mustCompare(t, b, s), "/metrics/domain/fpy/mean"), ptr(0.9), nil, nil, nil, compare.Unavailable, compare.ReasonMissingInScenario)
}

// SIMULATION_SPEC §7 (D-088 (a)): the same model version, engine, dataset
// manifest, horizon, warmup, replications and seed, and a baseline whose
// patch is []; otherwise comparable=false with every reason, every metric
// incomparable, both values kept side by side and no delta.
func TestIncomparable(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(b, s *compare.Run)
		reason string
	}{
		"model version": {func(b, s *compare.Run) { s.ModelVersionID = "mv_01M4YDQK00FRYWV7GS7X1QCXZS" }, compare.ModelVersionMismatch},
		"engine":        {func(b, s *compare.Run) { s.EngineVersion, s.Result.EngineVersion = "des-go-0.3.0", "des-go-0.3.0" }, compare.EngineVersionMismatch},
		"dataset manifest": {func(b, s *compare.Run) {
			s.DatasetManifestHash, s.Result.Reproducibility.DatasetManifestHash = "sha256:"+strings.Repeat("1", 64), "sha256:"+strings.Repeat("1", 64)
		}, compare.DatasetManifestMismatch},
		"horizon": {func(b, s *compare.Run) {
			s.Config.Horizon = simulation.Duration{Value: 1011, Unit: model.Minute}
			s.Result.MeasurementWindow.HorizonTicks = 60_660_000
		}, compare.HorizonMismatch},
		"warmup": {func(b, s *compare.Run) {
			s.Config.Warmup = simulation.Duration{Value: 1, Unit: model.Minute}
			s.Result.MeasurementWindow.WarmupTicks = 60_000
		}, compare.WarmupMismatch},
		"replications":   {func(b, s *compare.Run) { replications(s, 2) }, compare.ReplicationsMismatch},
		"seed":           {func(b, s *compare.Run) { s.Config.Seed, s.Result.Reproducibility.Seed = 8, 8 }, compare.SeedMismatch},
		"baseline patch": {func(b, s *compare.Run) { b.ScenarioPatch = []byte(twoServers) }, compare.BaselinePatchNotEmpty},
		// Only [] is a baseline (REQ-010, D-078 (b)): a patch that changes
		// nothing is not.
		"no-op baseline patch": {func(b, s *compare.Run) { b.ScenarioPatch = []byte(`[{"op":"test","path":"/name","value":"x"}]`) }, compare.BaselinePatchNotEmpty},
	} {
		t.Run(name, func(t *testing.T) {
			b, s := pair()
			s.Result.Metrics.Throughput = summary(110, simulation.PopulationReplication, "count")
			b.Result.Metrics.LeadTimeMinutes = unavailable(simulation.PopulationEntity, "minute", "NO_COMPLETED_ENTITY_IN_WINDOW")
			tc.mutate(&b, &s)
			c := mustCompare(t, b, s)
			if c.Comparable || !slices.Equal(c.IncomparableReasons, []string{tc.reason}) {
				t.Fatalf("comparable %v, reasons %v; want false, [%s]", c.Comparable, c.IncomparableReasons, tc.reason)
			}
			if len(c.Metrics) == 0 {
				t.Fatal("no metrics: the descriptive values are kept")
			}
			for _, m := range c.Metrics {
				if m.Status != compare.Incomparable || m.Reason == nil || *m.Reason != compare.ReasonRunsNotComparable || m.AbsoluteDelta != nil || m.RelativeDelta != nil {
					t.Errorf("%s: %+v", m.MetricPath, m)
				}
			}
			want(t, metricOf(t, c, "/metrics/throughput/mean"), ptr(100), ptr(110), nil, nil, compare.Incomparable, compare.ReasonRunsNotComparable)
			want(t, metricOf(t, c, "/metrics/lead_time_minutes/mean"), nil, ptr(5), nil, nil, compare.Incomparable, compare.ReasonRunsNotComparable)
		})
	}
	// Every reason at once, in the fixed order.
	b, s := pair()
	s.ModelVersionID = "mv_01M4YDQK00FRYWV7GS7X1QCXZS"
	s.EngineVersion, s.Result.EngineVersion = "des-go-0.3.0", "des-go-0.3.0"
	s.DatasetManifestHash, s.Result.Reproducibility.DatasetManifestHash = "sha256:"+strings.Repeat("1", 64), "sha256:"+strings.Repeat("1", 64)
	s.Config.Horizon, s.Result.MeasurementWindow.HorizonTicks = simulation.Duration{Value: 2, Unit: model.Day}, 2*86_400_000
	s.Config.Warmup, s.Result.MeasurementWindow.WarmupTicks = simulation.Duration{Value: 1, Unit: model.Hour}, 3_600_000
	replications(&s, 3)
	s.Config.Seed, s.Result.Reproducibility.Seed = 9, 9
	b.ScenarioPatch = []byte(twoServers)
	all := []string{compare.ModelVersionMismatch, compare.EngineVersionMismatch, compare.DatasetManifestMismatch, compare.HorizonMismatch,
		compare.WarmupMismatch, compare.ReplicationsMismatch, compare.SeedMismatch, compare.BaselinePatchNotEmpty}
	if c := mustCompare(t, b, s); c.Comparable || !slices.Equal(c.IncomparableReasons, all) {
		t.Errorf("reasons %v, want %v", c.IncomparableReasons, all)
	}
}

// What does not make runs incomparable: durations in other units that are
// the same ticks, another trace level (it changes no metric, §9), another
// name or scenario.
func TestComparableAcrossSpellings(t *testing.T) {
	b, s := pair()
	b.Config.Horizon, s.Config.Horizon = simulation.Duration{Value: 1, Unit: model.Day}, simulation.Duration{Value: 1440, Unit: model.Minute}
	b.Config.Warmup, s.Config.Warmup = simulation.Duration{Value: 1, Unit: model.Hour}, simulation.Duration{Value: 3600, Unit: model.Second}
	for _, r := range []*compare.Run{&b, &s} {
		r.Result.MeasurementWindow.HorizonTicks, r.Result.MeasurementWindow.WarmupTicks = 86_400_000, 3_600_000
	}
	s.Config.TraceLevel = simulation.TraceSampled
	// An empty array spelled with white space is still [] (review round 1
	// L5 checks the kind, not the bytes).
	b.ScenarioPatch = []byte(" \n[ ]\t")
	c := mustCompare(t, b, s)
	if !c.Comparable || len(c.IncomparableReasons) != 0 {
		t.Errorf("comparable %v %v", c.Comparable, c.IncomparableReasons)
	}
}

// Warnings (D-088 (d)), in their fixed order: the scenario is a baseline
// too, a run has one replication (no CI), a replication ended with active
// entities (censoring), the list was cut at MaxMetrics.
func TestWarnings(t *testing.T) {
	b, s := pair()
	if c := mustCompare(t, b, s); !slices.Equal(c.Warnings, []string{compare.WarningSingleReplication}) {
		t.Errorf("one replication: %v", c.Warnings)
	}
	replications(&b, 5)
	replications(&s, 5)
	if c := mustCompare(t, b, s); len(c.Warnings) != 0 || c.Warnings == nil {
		t.Errorf("five replications: %#v", c.Warnings)
	}
	s.ScenarioPatch = []byte("[]")
	b.Result.ReplicationSummaries[3].CensoredEntities = 2
	if c := mustCompare(t, b, s); !slices.Equal(c.Warnings, []string{compare.WarningScenarioPatchEmpty, compare.WarningCensoredEntities}) {
		t.Errorf("warnings %v", c.Warnings)
	}
	b.Result.ReplicationSummaries[3].CensoredEntities = 0
	s.Result.ReplicationSummaries[0].CensoredEntities = 1
	if c := mustCompare(t, b, s); !slices.Equal(c.Warnings, []string{compare.WarningScenarioPatchEmpty, compare.WarningCensoredEntities}) {
		t.Errorf("censored scenario: %v", c.Warnings)
	}
	// Review round 1 L3. Either run with one replication warns, the
	// scenario's too (D-088 (d): "有一個 run"), even when the runs are not
	// comparable.
	b, s = pair()
	replications(&b, 3)
	if c := mustCompare(t, b, s); !slices.Equal(c.Warnings, []string{compare.WarningSingleReplication}) || c.Comparable {
		t.Errorf("one replication in the scenario only: %v %v", c.Warnings, c.Comparable)
	}
	// All four at once, in the contract's order.
	b, s = pair()
	s.ScenarioPatch = []byte("[]")
	b.Result.ReplicationSummaries[0].CensoredEntities = 1
	for i := range compare.MaxMetrics / 4 {
		q := simulation.QueueStats{Status: simulation.Available, WaitMeanMinutes: ptr(1), WaitP50Minutes: ptr(1), WaitP95Minutes: ptr(1), SampleCount: 1}
		b.Result.Metrics.Queues[fmt.Sprintf("q%05d", i)], s.Result.Metrics.Queues[fmt.Sprintf("q%05d", i)] = q, q
	}
	all := []string{compare.WarningScenarioPatchEmpty, compare.WarningSingleReplication, compare.WarningCensoredEntities, compare.WarningMetricsTruncated}
	if c := mustCompare(t, b, s); !slices.Equal(c.Warnings, all) {
		t.Errorf("all four: %v, want %v", c.Warnings, all)
	}
}

// The contract's ±1e15 bound is inclusive (review round 1 L2): a value of
// exactly ±1e15 is a Result's and is compared, a delta of exactly 1e15 (or
// a relative delta of exactly 1e15) is kept; the next binary64 above is not
// a Result's value (fail closed) and is not a delta (null, with its reason).
func TestRangeBounds(t *testing.T) {
	above := math.Nextafter(compare.MaxValue, math.Inf(1))
	spanLow, spanHigh := math.Nextafter(-5e14, math.Inf(-1)), math.Nextafter(5e14, math.Inf(1))
	relAbove := math.Nextafter(5e14, math.Inf(1)) + 0.5
	// The arithmetic the cases rely on, so a change of the cases cannot pass
	// silently.
	if spanHigh-spanLow != above || 1e15-0 != compare.MaxValue || (5e14+0.5-0.5)/0.5 != compare.MaxValue || (relAbove-0.5)/0.5 != above {
		t.Fatalf("the fixed points moved: %v %v %v", spanHigh-spanLow, (5e14+0.5-0.5)/0.5, (relAbove-0.5)/0.5)
	}
	cases := []struct {
		key      string
		b, s     float64
		abs, rel *float64
		reason   string
	}{
		{"max", compare.MaxValue, compare.MaxValue, ptr(0), ptr(0), ""},
		{"min", -compare.MaxValue, -compare.MaxValue, ptr(0), ptr(0), ""},
		{"zero-to-max", 0, compare.MaxValue, ptr(compare.MaxValue), nil, compare.ReasonBaselineZero},
		{"span", -5e14, 5e14, ptr(compare.MaxValue), ptr(-2), ""},
		{"span-above", spanLow, spanHigh, nil, nil, compare.ReasonDeltaOutOfRange},
		{"relative-max", 0.5, 5e14 + 0.5, ptr(5e14), ptr(compare.MaxValue), ""},
		{"relative-above", 0.5, relAbove, ptr(relAbove - 0.5), nil, compare.ReasonRelativeDeltaOutOfRange},
	}
	b, s := pair()
	b.Result.Metrics.Domain, s.Result.Metrics.Domain = map[string]simulation.MetricSummary{}, map[string]simulation.MetricSummary{}
	for _, c := range cases {
		b.Result.Metrics.Domain[c.key] = summary(c.b, simulation.PopulationReplication, "count")
		s.Result.Metrics.Domain[c.key] = summary(c.s, simulation.PopulationReplication, "count")
	}
	cmp := mustCompare(t, b, s)
	for _, c := range cases {
		want(t, metricOf(t, cmp, "/metrics/domain/"+c.key+"/mean"), ptr(c.b), ptr(c.s), c.abs, c.rel, compare.Available, c.reason)
	}
	data, err := cmp.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := validate.Document(data, "http.schema.json#/$defs/Comparison", "COMPARISON"); err != nil || len(issues) > 0 {
		t.Errorf("the bounds are not the contract's: %v %v", issues, err)
	}
	for _, v := range []float64{above, -above} {
		b, s := pair()
		b.Result.Metrics.Domain = map[string]simulation.MetricSummary{"x": summary(v, simulation.PopulationReplication, "count")}
		if _, err := compare.Compare(b, s); !errors.Is(err, compare.ErrInconsistent) {
			t.Errorf("a value %v: %v, want ErrInconsistent", v, err)
		}
	}
}

// The order of the metrics never depends on map iteration: run-level
// metrics first, then the keyed groups, keys in byte order, statistics in
// their fixed order (D-088 (b)). The same input always gives the same bytes.
func TestOrder(t *testing.T) {
	b, s := pair()
	b.Result.Metrics.Resources["alpha"] = simulation.ResourceStats{UtilizationMean: ptr(0.1), ObservationTicks: horizonTicks, Capacity: 1}
	s.Result.Metrics.Resources["Zulu"] = simulation.ResourceStats{UtilizationMean: ptr(0.2), ObservationTicks: horizonTicks, Capacity: 1}
	s.Result.Metrics.AdditionalMetrics = map[string]simulation.MetricSummary{"wip": summary(1, simulation.PopulationReplication, "count")}
	s.Result.Metrics.Domain = map[string]simulation.MetricSummary{"uph": summary(1, simulation.PopulationReplication, "count")}
	b.Result.Metrics.Domain = map[string]simulation.MetricSummary{"fpy": summary(1, simulation.PopulationReplication, "ratio")}
	c := mustCompare(t, b, s)
	var got []string
	for _, m := range c.Metrics {
		got = append(got, m.MetricPath)
	}
	wantPaths := []string{
		"/metrics/throughput/mean", "/metrics/throughput/p50", "/metrics/throughput/p95",
		"/metrics/lead_time_minutes/mean", "/metrics/lead_time_minutes/p50", "/metrics/lead_time_minutes/p95",
		"/metrics/failure_count/mean", "/metrics/failure_count/p50", "/metrics/failure_count/p95",
		"/metrics/rework_count/mean", "/metrics/rework_count/p50", "/metrics/rework_count/p95",
		"/metrics/additional_metrics/wip/mean", "/metrics/additional_metrics/wip/p50", "/metrics/additional_metrics/wip/p95",
		"/metrics/dora/change_lead_time/mean", "/metrics/dora/change_lead_time/p50", "/metrics/dora/change_lead_time/p95",
		"/metrics/domain/fpy/mean", "/metrics/domain/fpy/p50", "/metrics/domain/fpy/p95",
		"/metrics/domain/uph/mean", "/metrics/domain/uph/p50", "/metrics/domain/uph/p95",
		"/metrics/resources/Zulu/utilization_mean", "/metrics/resources/alpha/utilization_mean", "/metrics/resources/server/utilization_mean",
		"/metrics/queues/service/wait_mean_minutes", "/metrics/queues/service/wait_p50_minutes", "/metrics/queues/service/wait_p95_minutes",
		"/metrics/queues/service/queue_length_mean",
	}
	if !slices.Equal(got, wantPaths) {
		t.Errorf("order\n got %v\nwant %v", got, wantPaths)
	}
	// Many keys, inserted in a different order each time: the same bytes.
	var first []byte
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 40 {
		b, s := pair()
		keys := make([]string, 60)
		for k := range keys {
			keys[k] = fmt.Sprintf("r%02d", k)
		}
		rng.Shuffle(len(keys), func(x, y int) { keys[x], keys[y] = keys[y], keys[x] })
		for _, key := range keys {
			var n int
			fmt.Sscanf(key, "r%d", &n) // the value depends on the key, not on the shuffle
			b.Result.Metrics.Resources[key] = simulation.ResourceStats{UtilizationMean: ptr(float64(n%7) / 8), ObservationTicks: horizonTicks, Capacity: 1}
			s.Result.Metrics.Queues[key] = simulation.QueueStats{Status: simulation.Available, WaitMeanMinutes: ptr(1), WaitP50Minutes: ptr(1), WaitP95Minutes: ptr(1), SampleCount: 1}
		}
		data, err := mustCompare(t, b, s).JSON()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = data
		} else if string(data) != string(first) {
			t.Fatalf("iteration %d: the comparison changed with the map order", i)
		}
		// One run's citable statistics are in the same order (mutation
		// round 1: keys-in-map-order survived, Compare re-sorts the union).
		stats, err := compare.Statistics(b.Result)
		if err != nil {
			t.Fatal(err)
		}
		var resources []string
		for _, st := range stats {
			if strings.HasPrefix(st.Path, "/metrics/resources/") {
				resources = append(resources, st.Path)
			}
		}
		if len(resources) != 61 || !slices.IsSorted(resources) {
			t.Fatalf("iteration %d: Statistics lists the resources in map order: %v", i, resources[:min(5, len(resources))])
		}
	}
}

// Metric paths are RFC 6901 JSON Pointers from the Result's root: a key
// with "/" or "~" is escaped, so the path names exactly one statistic.
func TestPathEscaping(t *testing.T) {
	if got := compare.Path("metrics", "resources", "a/b~c", "utilization_mean"); got != "/metrics/resources/a~1b~0c/utilization_mean" {
		t.Errorf("Path: %s", got)
	}
	b, s := pair()
	for _, r := range []*compare.Run{&b, &s} {
		r.Result.Metrics.Resources["a/b~c"] = simulation.ResourceStats{UtilizationMean: ptr(0.5), ObservationTicks: horizonTicks, Capacity: 1}
	}
	c := mustCompare(t, b, s)
	want(t, metricOf(t, c, "/metrics/resources/a~1b~0c/utilization_mean"), ptr(0.5), ptr(0.5), ptr(0), ptr(0), compare.Available, "")
	if v, err := compare.Resolve(b.Result, "/metrics/resources/a~1b~0c/utilization_mean"); err != nil || v == nil || *v != 0.5 {
		t.Errorf("Resolve: %v %v", show(v), err)
	}
	// "~" not followed by 0 or 1 is no RFC 6901 pointer: a key spelled
	// "x~2" has the one path /…/x~02/…, never /…/x~2/….
	b.Result.Metrics.Resources["x~2"] = simulation.ResourceStats{UtilizationMean: ptr(0.25), ObservationTicks: horizonTicks, Capacity: 1}
	if v, err := compare.Resolve(b.Result, "/metrics/resources/x~02/utilization_mean"); err != nil || v == nil || *v != 0.25 {
		t.Errorf("canonical: %v %v", show(v), err)
	}
	for _, p := range []string{"/metrics/resources/x~2/utilization_mean", "/metrics/resources/x~/utilization_mean", "/metrics/resources/a/b~c/utilization_mean"} {
		if _, err := compare.Resolve(b.Result, p); !errors.Is(err, compare.ErrUnknownPath) {
			t.Errorf("%s: %v, want ErrUnknownPath", p, err)
		}
	}
	// RFC 6901 decodes "~1" before "~0" (review round 1 L1): a key "~1" is
	// the path …/~01/… and a key "/" is …/~1/…, two different statistics.
	r := result(baseID)
	r.Metrics.Resources["~1"] = simulation.ResourceStats{UtilizationMean: ptr(0.125), ObservationTicks: horizonTicks, Capacity: 1}
	r.Metrics.Resources["/"] = simulation.ResourceStats{UtilizationMean: ptr(0.375), ObservationTicks: horizonTicks, Capacity: 1}
	for key, want := range map[string]float64{"~1": 0.125, "/": 0.375} {
		p := compare.Path("metrics", "resources", key, "utilization_mean")
		if v, err := compare.Resolve(r, p); err != nil || v == nil || *v != want {
			t.Errorf("key %q (path %s): %v %v, want %v", key, p, show(v), err, want)
		}
	}
	if compare.Path("metrics", "resources", "~1", "x") != "/metrics/resources/~01/x" || compare.Path("metrics", "resources", "/", "x") != "/metrics/resources/~1/x" {
		t.Errorf("Path of ~1 and /: %s %s", compare.Path("metrics", "resources", "~1", "x"), compare.Path("metrics", "resources", "/", "x"))
	}
	stats, err := compare.Statistics(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stats {
		if v, err := compare.Resolve(r, st.Path); err != nil || !same(v, st.Value) {
			t.Errorf("%s: listed %s, resolves to %s (%v)", st.Path, show(st.Value), show(v), err)
		}
	}
}

// The paths an analysis may cite (AI_SPEC §9, T-030): every statistic of
// Statistics resolves to its value (nil when unavailable, never 0); any
// other path, a malformed one, a member that is not a statistic (sample
// count, CI, capacity, reason) or a key the result lacks is refused.
func TestResolve(t *testing.T) {
	r := result(baseID)
	r.Metrics.LeadTimeMinutes = unavailable(simulation.PopulationEntity, "minute", "NO_COMPLETED_ENTITY_IN_WINDOW")
	r.Metrics.Domain = map[string]simulation.MetricSummary{"fpy": summary(0.9, simulation.PopulationReplication, "ratio")}
	stats, err := compare.Statistics(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 3+3+3+3+3+3+1+4 {
		t.Errorf("%d statistics: %v", len(stats), stats)
	}
	for _, st := range stats {
		v, err := compare.Resolve(r, st.Path)
		if err != nil || !same(v, st.Value) {
			t.Errorf("%s: Resolve %s %v, Statistics %s", st.Path, show(v), err, show(st.Value))
		}
	}
	if v, err := compare.Resolve(r, "/metrics/lead_time_minutes/p95"); err != nil || v != nil {
		t.Errorf("an unavailable statistic: %s %v", show(v), err)
	}
	if v, err := compare.Resolve(r, "/metrics/queues/service/wait_p95_minutes"); err != nil || v == nil || *v != 0 {
		t.Errorf("an available 0: %s %v", show(v), err)
	}
	for _, path := range []string{
		"", "/", "metrics/throughput/mean", "/metrics", "/metrics/throughput", "/metrics/throughput/", "/metrics/throughput/mean/",
		"/metrics/throughput/sample_count", "/metrics/throughput/confidence_interval_mean", "/metrics/throughput/unit",
		"/metrics/throughput/reason", "/metrics/throughput/mean/x", "/metrics/resources/server", "/metrics/resources/server/capacity",
		"/metrics/resources/server/observation_ticks", "/metrics/resources/nope/utilization_mean", "/metrics/queues/service/sample_count",
		"/metrics/queues/service/reason", "/metrics/queues/service/status", "/metrics/dora/change_lead_time", "/metrics/dora/nope/mean",
		"/metrics/additional_metrics/replication_mean_lead_time_minutes/mean", "/metrics/domain/fpy/population", "/metrics/domain/nope/mean",
		"/run_id", "/reproducibility/seed", "/replication_summaries/0/completed_count", "/metrics/nope/mean",
		"/metrics/resources/server~2/utilization_mean", "/metrics/resources/server~/utilization_mean", "/metrics/dora/change_lead_time/mean ",
		"/Metrics/throughput/mean",
	} {
		if v, err := compare.Resolve(r, path); !errors.Is(err, compare.ErrUnknownPath) {
			t.Errorf("%q: %s %v, want ErrUnknownPath", path, show(v), err)
		}
	}
	// No domain map: its paths are unknown, not unavailable.
	if _, err := compare.Resolve(result(baseID), "/metrics/domain/fpy/mean"); !errors.Is(err, compare.ErrUnknownPath) {
		t.Errorf("a result without metrics.domain: %v", err)
	}
	if _, err := compare.Resolve(nil, "/metrics/throughput/mean"); err == nil {
		t.Error("a nil result resolved")
	}
}

// A delta outside the contract's number range (or not finite) is null with
// a reason, never a rounded or clipped value (D-088 (c)).
func TestOutOfRange(t *testing.T) {
	b, s := pair()
	b.Result.Metrics.Domain = map[string]simulation.MetricSummary{
		"tiny": summary(1e-10, simulation.PopulationReplication, "ratio"),
		"wide": summary(-9e14, simulation.PopulationReplication, "count"),
	}
	s.Result.Metrics.Domain = map[string]simulation.MetricSummary{
		"tiny": summary(1e6, simulation.PopulationReplication, "ratio"),
		"wide": summary(9e14, simulation.PopulationReplication, "count"),
	}
	c := mustCompare(t, b, s)
	want(t, metricOf(t, c, "/metrics/domain/tiny/mean"), ptr(1e-10), ptr(1e6), ptr(1e6-1e-10), nil, compare.Available, compare.ReasonRelativeDeltaOutOfRange)
	want(t, metricOf(t, c, "/metrics/domain/wide/mean"), ptr(-9e14), ptr(9e14), nil, nil, compare.Available, compare.ReasonDeltaOutOfRange)
	data, err := c.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := validate.Document(data, "http.schema.json#/$defs/Comparison", "COMPARISON"); err != nil || len(issues) > 0 {
		t.Errorf("out-of-range comparison is not the contract's: %v %v", issues, err)
	}
}

// No -0 reaches the output: a zero delta is 0.
func TestNoNegativeZero(t *testing.T) {
	b, s := pair()
	b.Result.Metrics.Domain = map[string]simulation.MetricSummary{"neg": summary(-3, simulation.PopulationReplication, "count")}
	s.Result.Metrics.Domain = map[string]simulation.MetricSummary{"neg": summary(-3, simulation.PopulationReplication, "count")}
	c := mustCompare(t, b, s)
	want(t, metricOf(t, c, "/metrics/domain/neg/mean"), ptr(-3), ptr(-3), ptr(0), ptr(0), compare.Available, "")
	data, err := c.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "-0,") || strings.Contains(string(data), "-0}") {
		t.Errorf("-0 in %s", data)
	}
}

// A run whose result does not match its own frozen input, or that breaks
// the Result contract, is never compared (fail closed: a server fault).
func TestInconsistent(t *testing.T) {
	for name, mutate := range map[string]func(r *compare.Run){
		"no result":                 func(r *compare.Run) { r.Result = nil },
		"another run's result":      func(r *compare.Run) { r.Result.RunID = "sim_01M4YDQK00FGZZWH0PP15XPNFC" },
		"not completed":             func(r *compare.Run) { r.Result.Status = "running" },
		"another engine":            func(r *compare.Run) { r.Result.EngineVersion = "des-go-0.1.0" },
		"another seed":              func(r *compare.Run) { r.Result.Reproducibility.Seed = 8 },
		"another replication count": func(r *compare.Run) { r.Result.Reproducibility.Replications = 2 },
		"missing replication":       func(r *compare.Run) { r.Result.ReplicationSummaries = nil },
		"another manifest": func(r *compare.Run) {
			r.Result.Reproducibility.DatasetManifestHash = "sha256:" + strings.Repeat("2", 64)
		},
		"another horizon":          func(r *compare.Run) { r.Result.MeasurementWindow.HorizonTicks++ },
		"another warmup":           func(r *compare.Run) { r.Result.MeasurementWindow.WarmupTicks = 1 },
		"a config without ticks":   func(r *compare.Run) { r.Config.Horizon.Unit = "fortnight" },
		"a patch that is not JSON": func(r *compare.Run) { r.ScenarioPatch = []byte("[") },
		"a patch that is no array": func(r *compare.Run) { r.ScenarioPatch = []byte(`{}`) },
		"available without a mean": func(r *compare.Run) { r.Result.Metrics.Throughput.Mean = nil },
		"unavailable with a mean":  func(r *compare.Run) { r.Result.Metrics.Throughput.Status = simulation.Unavailable },
		"unknown status":           func(r *compare.Run) { r.Result.Metrics.ReworkCount.Status = "maybe" },
		"available queue, no wait": func(r *compare.Run) {
			q := r.Result.Metrics.Queues["service"]
			q.WaitP95Minutes = nil
			r.Result.Metrics.Queues["service"] = q
		},
		"unavailable queue, a wait": func(r *compare.Run) {
			q := r.Result.Metrics.Queues["service"]
			q.Status = simulation.Unavailable
			r.Result.Metrics.Queues["service"] = q
		},
		"a value beyond the range": func(r *compare.Run) { r.Result.Metrics.Throughput.P95 = ptr(2e15) },
		"a non-finite value": func(r *compare.Run) {
			r.Result.Metrics.Resources["server"] = simulation.ResourceStats{UtilizationMean: ptr(math.Inf(1)), Capacity: 1}
		},
		"a non-finite queue length": func(r *compare.Run) {
			q := r.Result.Metrics.Queues["service"]
			q.QueueLengthMean = math.NaN()
			r.Result.Metrics.Queues["service"] = q
		},
		// Review round 1 L5: a JSON null is no patch, so no baseline either.
		"a null patch": func(r *compare.Run) { r.ScenarioPatch = []byte("null") },
		// Review round 1 I1: the Result's other hashes and its window's
		// units are the run's too.
		"another model hash": func(r *compare.Run) { r.Result.Reproducibility.ModelHash = "sha256:" + strings.Repeat("3", 64) },
		"another effective model hash": func(r *compare.Run) {
			r.Result.Reproducibility.EffectiveModelHash = "sha256:" + strings.Repeat("3", 64)
		},
		"another config hash":   func(r *compare.Run) { r.Result.Reproducibility.ConfigHash = "sha256:" + strings.Repeat("3", 64) },
		"another tick unit":     func(r *compare.Run) { r.Result.MeasurementWindow.TickUnit = "second" },
		"another time boundary": func(r *compare.Run) { r.Result.MeasurementWindow.TimeBoundary = "warmup < t <= horizon" },
		// Review round 1 I2: the metrics break the Result contract
		// (simulation.schema.json) in ways only the schema says.
		"a utilization above 1": func(r *compare.Run) {
			r.Result.Metrics.Resources["server"] = simulation.ResourceStats{UtilizationMean: ptr(7), ObservationTicks: horizonTicks, Capacity: 1}
		},
		"a null utilization without a reason": func(r *compare.Run) {
			r.Result.Metrics.Resources["server"] = simulation.ResourceStats{ObservationTicks: horizonTicks, Capacity: 1}
		},
		"an unavailable summary with a CI": func(r *compare.Run) {
			r.Result.Metrics.LeadTimeMinutes = unavailable(simulation.PopulationReplication, "minute", "NO_COMPLETED_ENTITY_IN_WINDOW")
			r.Result.Metrics.LeadTimeMinutes.ConfidenceIntervalMean = &simulation.ConfidenceInterval{Level: 0.95, Lower: 1, Upper: 2, Method: "student_t_replication_means"}
		},
		"an entity population with a CI": func(r *compare.Run) {
			r.Result.Metrics.LeadTimeMinutes.ConfidenceIntervalMean = &simulation.ConfidenceInterval{Level: 0.95, Lower: 1, Upper: 2, Method: "student_t_replication_means"}
		},
		"an unavailable summary without a reason": func(r *compare.Run) {
			r.Result.Metrics.DORA["change_lead_time"] = simulation.MetricSummary{Status: simulation.Unavailable, Population: simulation.PopulationEntity, Unit: "minute"}
		},
		"an available summary of no sample": func(r *compare.Run) { r.Result.Metrics.Throughput.SampleCount = 0 },
		"an available queue of no visit": func(r *compare.Run) {
			q := r.Result.Metrics.Queues["service"]
			q.SampleCount = 0
			r.Result.Metrics.Queues["service"] = q
		},
		"a negative wait": func(r *compare.Run) {
			q := r.Result.Metrics.Queues["service"]
			q.WaitMeanMinutes = ptr(-1)
			r.Result.Metrics.Queues["service"] = q
		},
		"an unknown population": func(r *compare.Run) { r.Result.Metrics.FailureCount.Population = "visits" },
	} {
		for _, side := range []string{"baseline", "scenario"} {
			t.Run(name+"/"+side, func(t *testing.T) {
				b, s := pair()
				if side == "baseline" {
					mutate(&b)
				} else {
					mutate(&s)
				}
				if _, err := compare.Compare(b, s); !errors.Is(err, compare.ErrInconsistent) {
					t.Errorf("%v, want ErrInconsistent", err)
				}
			})
		}
	}
	// Statistics refuses a result that breaks the contract the same way.
	r := result(baseID)
	r.Metrics.Throughput.Mean = nil
	if _, err := compare.Statistics(r); !errors.Is(err, compare.ErrInconsistent) {
		t.Errorf("Statistics: %v", err)
	}
	// Statistics and Resolve (what T-030 cites) read no run row, so Compare's
	// schema check does not stand between them and a value out of the
	// contract's range: they refuse it themselves (review round 1, R13 of
	// the reviewer survived once Compare validated the metrics).
	for name, c := range map[string]struct {
		mutate func(r *simulation.Result)
		path   string
	}{
		"a mean beyond the range":  {func(r *simulation.Result) { r.Metrics.Throughput.Mean = ptr(2e15) }, "/metrics/throughput/mean"},
		"a p95 that is not finite": {func(r *simulation.Result) { r.Metrics.LeadTimeMinutes.P95 = ptr(math.NaN()) }, "/metrics/lead_time_minutes/p95"},
		"a wait beyond the range": {func(r *simulation.Result) {
			q := r.Metrics.Queues["service"]
			q.WaitP50Minutes = ptr(math.Inf(1))
			r.Metrics.Queues["service"] = q
		}, "/metrics/queues/service/wait_p50_minutes"},
	} {
		r := result(baseID)
		c.mutate(r)
		if _, err := compare.Statistics(r); !errors.Is(err, compare.ErrInconsistent) {
			t.Errorf("%s: Statistics %v", name, err)
		}
		if _, err := compare.Resolve(r, c.path); !errors.Is(err, compare.ErrInconsistent) {
			t.Errorf("%s: Resolve %v", name, err)
		}
	}
}

// The baseline and the scenario are two runs (as the analysis context
// requires, internal/ai/aicontext).
func TestSameRun(t *testing.T) {
	b, _ := pair()
	if _, err := compare.Compare(b, b); !errors.Is(err, compare.ErrSameRun) {
		t.Errorf("%v, want ErrSameRun", err)
	}
}

// More statistics than the contract's maxItems: the first MaxMetrics in the
// comparison's order, and the METRICS_TRUNCATED warning (never a silent cut,
// as D-045 does for /me).
func TestTruncation(t *testing.T) {
	b, s := pair()
	for i := range compare.MaxMetrics / 4 {
		key := fmt.Sprintf("q%05d", i)
		q := simulation.QueueStats{Status: simulation.Available, WaitMeanMinutes: ptr(1), WaitP50Minutes: ptr(1), WaitP95Minutes: ptr(1), SampleCount: 1}
		b.Result.Metrics.Queues[key], s.Result.Metrics.Queues[key] = q, q
	}
	c := mustCompare(t, b, s)
	if len(c.Metrics) != compare.MaxMetrics || !slices.Contains(c.Warnings, compare.WarningMetricsTruncated) {
		t.Fatalf("%d metrics, warnings %v", len(c.Metrics), c.Warnings)
	}
	if c.Metrics[0].MetricPath != "/metrics/throughput/mean" || !strings.HasPrefix(c.Metrics[len(c.Metrics)-1].MetricPath, "/metrics/queues/q") {
		t.Errorf("kept %s … %s", c.Metrics[0].MetricPath, c.Metrics[len(c.Metrics)-1].MetricPath)
	}
	// Exactly MaxMetrics is not truncated.
	b2, s2 := pair()
	n := len(mustCompare(t, b2, s2).Metrics)
	for i := 0; n < compare.MaxMetrics; i++ {
		key := fmt.Sprintf("r%05d", i)
		u := simulation.ResourceStats{UtilizationMean: ptr(0.5), ObservationTicks: horizonTicks, Capacity: 1}
		b2.Result.Metrics.Resources[key], s2.Result.Metrics.Resources[key] = u, u
		n++
	}
	if c := mustCompare(t, b2, s2); len(c.Metrics) != compare.MaxMetrics || slices.Contains(c.Warnings, compare.WarningMetricsTruncated) {
		t.Errorf("exactly MaxMetrics: %d, %v", len(c.Metrics), c.Warnings)
	}
}

// The Go types are the contract's (schemas/ is the source of truth), and
// every comparison the package makes is a valid Comparison document.
func TestContract(t *testing.T) {
	s := contracttest.LoadSchemas(t)
	s.Parity(t, reflect.TypeFor[compare.Comparison](), "http.schema.json#/$defs/Comparison")
	s.Parity(t, reflect.TypeFor[compare.Metric](), "http.schema.json#/$defs/ComparisonMetric")
	for _, f := range []string{"valid-comparison.json", "valid-comparison-incomparable.json"} {
		contracttest.RoundTrip[compare.Comparison](t, f, contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", f)))
	}
	// The Go validator agrees with make spec-contracts on every Comparison
	// fixture: the positives pass, each negative fails.
	n := 0
	for _, c := range contracttest.Cases(t) {
		if c.Schema != "schemas/http.schema.json#/$defs/Comparison" {
			continue
		}
		n++
		issues, err := validate.Document(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), c.Instance)), "http.schema.json#/$defs/Comparison", "COMPARISON")
		if err != nil || (len(issues) == 0) != c.Valid {
			t.Errorf("%s: valid %v, issues %v %v", c.ID, c.Valid, issues, err)
		}
	}
	if n < 15 {
		t.Errorf("%d Comparison fixtures", n)
	}
	b, s2 := pair()
	s2.Result.Metrics.LeadTimeMinutes = unavailable(simulation.PopulationEntity, "minute", "NO_COMPLETED_ENTITY_IN_WINDOW")
	s2.Result.Metrics.Resources["extra"] = simulation.ResourceStats{UtilizationMean: ptr(0.1), ObservationTicks: horizonTicks, Capacity: 1}
	incomparable := func() (compare.Run, compare.Run) {
		b, s := pair()
		s.Config.Seed, s.Result.Reproducibility.Seed = 8, 8
		return b, s
	}
	ib, is := incomparable()
	for name, c := range map[string]compare.Comparison{"mixed": mustCompare(t, b, s2), "incomparable": mustCompare(t, ib, is)} {
		data, err := c.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if issues, err := validate.Document(data, "http.schema.json#/$defs/Comparison", "COMPARISON"); err != nil || len(issues) > 0 {
			t.Errorf("%s: %s does not match the contract: %v %v", name, data, issues, err)
		}
		canonical, err := hashing.Canonical(data)
		if err != nil || string(canonical) != string(data) {
			t.Errorf("%s: JSON is not RFC 8785 (%v)", name, err)
		}
	}
}

// A hand-derived golden vector: testdata/contracts/valid-result.json as the
// baseline and valid-result-scenario.json (two servers) as the scenario
// give exactly valid-comparison.json (values derived by hand in
// testdata/contracts/README.md, not by this code).
func TestGolden(t *testing.T) {
	read := func(name string) *simulation.Result {
		var r simulation.Result
		if err := model.Decode(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", name)), &r); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return &r
	}
	b := read("valid-result.json")
	s := read("valid-result-scenario.json")
	br, sr := run(b.RunID, "[]", b), run(s.RunID, twoServers, s)
	br.EngineVersion, sr.EngineVersion = b.EngineVersion, s.EngineVersion
	c := mustCompare(t, br, sr)
	got, err := c.JSON()
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := hashing.Canonical(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", "valid-comparison.json")))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(wantJSON) {
		t.Errorf("golden\n got %s\nwant %s", got, wantJSON)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
}
