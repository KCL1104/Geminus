package investigator_test

import (
	"encoding/json/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/results/compare"
)

func f(v float64) *float64 { return &v }

func available(path string, b, s float64) compare.Metric {
	abs, rel := s-b, (s-b)/b
	return compare.Metric{MetricPath: path, Baseline: f(b), Scenario: f(s), AbsoluteDelta: &abs, RelativeDelta: &rel, Status: compare.Available}
}

// The projection is the contract's RunComparison member for member, and
// what it shows of a comparison is fixed: the objective's metric, then at
// most six headline metrics in a fixed order (those the comparison has),
// with its comparability, reasons and warnings as they are. The same
// function builds the worker's observation and the API's candidates (H-061
// §10: "a test asserts that they equal the worker's observation").
func TestComparisonOf(t *testing.T) {
	contracttest.LoadSchemas(t).Parity(t, reflect.TypeFor[investigator.RunComparison](), "investigator.schema.json#/$defs/RunComparison")
	if len(investigator.Headline) > 6 {
		t.Fatalf("%d headline metrics: the contract shows at most 6", len(investigator.Headline))
	}
	c := compare.Comparison{BaselineRunID: sim, ScenarioRunID: "sim_01M4YDQK00B8S5WQXJ3TQ7ZK52", Comparable: true, IncomparableReasons: []string{},
		Warnings: []string{compare.WarningSingleReplication},
		Metrics: []compare.Metric{
			available("/metrics/throughput/mean", 120, 126),
			available("/metrics/lead_time_minutes/mean", 44, 41),
			available("/metrics/lead_time_minutes/p50", 42.5, 39.9),
			available("/metrics/lead_time_minutes/p95", 80, 70),
			available("/metrics/resources/ict/utilization_mean", 0.9, 0.6),
		}}
	got := investigator.ComparisonOf(c, "/metrics/lead_time_minutes/p50")
	if !got.Comparable || got.Objective == nil || got.Objective.MetricPath != "/metrics/lead_time_minutes/p50" ||
		!slices.Equal(got.Warnings, []string{compare.WarningSingleReplication}) || len(got.IncomparableReasons) != 0 {
		t.Errorf("projection %+v", got)
	}
	var paths []string
	for _, m := range got.Metrics {
		paths = append(paths, m.MetricPath)
	}
	if want := []string{"/metrics/lead_time_minutes/p50", "/metrics/lead_time_minutes/mean", "/metrics/lead_time_minutes/p95",
		"/metrics/throughput/mean"}; !slices.Equal(paths, want) {
		t.Errorf("metrics %v, want %v (headline order, those present only)", paths, want)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if issues, err := validate.Document(data, "investigator.schema.json#/$defs/RunComparison", "RESPONSE"); err != nil || len(issues) > 0 {
		t.Errorf("%s: %v %v", data, issues, err)
	}
	// An objective the comparison lacks is null, never a made-up metric; a
	// pair that is not comparable keeps its reasons.
	none := investigator.ComparisonOf(compare.Comparison{Comparable: false, IncomparableReasons: []string{compare.SeedMismatch}},
		"/metrics/throughput/mean")
	if none.Objective != nil || none.Comparable || !slices.Equal(none.IncomparableReasons, []string{compare.SeedMismatch}) ||
		none.Metrics == nil || none.Warnings == nil || len(none.Metrics) != 0 {
		t.Errorf("empty projection %+v", none)
	}
	if data, err := json.Marshal(none); err != nil {
		t.Fatal(err)
	} else if issues, err := validate.Document(data, "investigator.schema.json#/$defs/RunComparison", "RESPONSE"); err != nil || len(issues) > 0 {
		t.Errorf("%s: %v %v", data, issues, err)
	}
}
