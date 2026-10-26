package metrics

import (
	"reflect"
	"testing"

	"github.com/kcl1104/geminus/internal/simulation"
)

// Summarize is the summary every Result statistic uses, for a domain
// pack's model metrics (D-058, T-051): the same numbers bit for bit, so a
// pack's metric and the engine's agree on mean, quantiles and interval.
func TestSummarize(t *testing.T) {
	for _, c := range []struct {
		values []float64
		pop    simulation.Population
	}{
		{[]float64{9, 10.8, 7.2}, simulation.PopulationReplication},
		{[]float64{0.25}, simulation.PopulationReplication},
		{[]float64{1, 2, 3, 4, 5}, simulation.PopulationEntity},
		{nil, simulation.PopulationReplication},
	} {
		got := Summarize(c.values, c.pop, "ratio", "NO_VALUE")
		if want := summary(c.values, c.pop, "ratio", "NO_VALUE"); !reflect.DeepEqual(got, want) {
			t.Errorf("%v: %+v, want %+v", c.values, got, want)
		}
	}
	got := Summarize([]float64{9, 10.8, 7.2}, simulation.PopulationReplication, "unit_per_hour", "x")
	if got.Status != simulation.Available || *got.Mean != 9 || *got.P50 != 9 || got.SampleCount != 3 || got.ConfidenceIntervalMean == nil || got.Reason != nil {
		t.Errorf("%+v", got)
	}
	if none := Summarize(nil, simulation.PopulationReplication, "ratio", "NO_VALUE"); none.Status != simulation.Unavailable || none.Mean != nil ||
		none.Reason == nil || *none.Reason != "NO_VALUE" {
		t.Errorf("no values: %+v", none)
	}
	// The values are the caller's: Summarize sorts a copy.
	v := []float64{3, 1, 2}
	Summarize(v, simulation.PopulationReplication, "ratio", "x")
	if v[0] != 3 || v[1] != 1 || v[2] != 2 {
		t.Errorf("the values were changed: %v", v)
	}
}
