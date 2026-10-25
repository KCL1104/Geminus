package compare

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/simulation"
)

// The comparison's own range (finite, |v| <= MaxValue, the Comparison
// contract's bound) is a second line behind the metrics contract. Compare,
// Statistics and Resolve all validate the metrics against metricsRef
// first, and simulation.schema.json bounds these values today, so since
// review round 2 (R2-L1) nothing outside this package reaches the check:
// the round-1 mutants summary-not-finite-ok and wait-not-finite-ok, and
// the reviewer's R13, survive every black-box test. The check stays so
// that a loosened Result schema cannot put a value outside the
// Comparison's range into a comparison or an evidence value; this test
// keeps it from rotting.
func TestLookupKeepsTheComparisonRange(t *testing.T) {
	g := groups[slices.IndexFunc(groups, func(g group) bool { return g.name == "resources" })]
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 2e15, -2e15, math.Nextafter(MaxValue, math.Inf(1))} {
		s := simulation.MetricSummary{Status: simulation.Available, Mean: &v, P50: &v, P95: &v, SampleCount: 1}
		for _, stat := range []string{"mean", "p50", "p95"} {
			if got, err := summaryStat(s, stat, "/metrics/x"); !errors.Is(err, ErrInconsistent) {
				t.Errorf("summary %s %v: %v %v, want ErrInconsistent", stat, v, got, err)
			}
		}
		q := simulation.QueueStats{Status: simulation.Available, WaitMeanMinutes: &v, WaitP50Minutes: &v, WaitP95Minutes: &v, QueueLengthMean: v,
			SampleCount: 1}
		for _, stat := range []string{"wait_mean_minutes", "wait_p50_minutes", "wait_p95_minutes", "queue_length_mean"} {
			if got, err := queueStat(q, stat, "/metrics/queues/q"); !errors.Is(err, ErrInconsistent) {
				t.Errorf("queue %s %v: %v %v, want ErrInconsistent", stat, v, got, err)
			}
		}
		m := simulation.Metrics{Resources: map[string]simulation.ResourceStats{"r": {UtilizationMean: &v, Capacity: 1}}}
		if got, _, err := lookup(m, g, "r", "utilization_mean"); !errors.Is(err, ErrInconsistent) {
			t.Errorf("utilization %v: %v %v, want ErrInconsistent", v, got, err)
		}
	}
	// The bound itself is inside the range (D-088 (c), TestRangeBounds).
	for _, v := range []float64{MaxValue, -MaxValue, 0} {
		s := simulation.MetricSummary{Status: simulation.Available, Mean: &v, P50: &v, P95: &v, SampleCount: 1}
		if got, err := summaryStat(s, "p95", "/metrics/x"); err != nil || got == nil || *got != v {
			t.Errorf("summary %v: %v %v", v, got, err)
		}
		q := simulation.QueueStats{Status: simulation.Available, WaitMeanMinutes: &v, WaitP50Minutes: &v, WaitP95Minutes: &v, QueueLengthMean: v,
			SampleCount: 1}
		for _, stat := range []string{"wait_p95_minutes", "queue_length_mean"} {
			if got, err := queueStat(q, stat, "/metrics/queues/q"); err != nil || got == nil || *got != v {
				t.Errorf("queue %s %v: %v %v", stat, v, got, err)
			}
		}
		m := simulation.Metrics{Resources: map[string]simulation.ResourceStats{"r": {UtilizationMean: &v, Capacity: 1}}}
		if got, _, err := lookup(m, g, "r", "utilization_mean"); err != nil || got == nil || *got != v {
			t.Errorf("utilization %v: %v %v", v, got, err)
		}
	}
}
