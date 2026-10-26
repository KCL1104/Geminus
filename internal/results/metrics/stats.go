// Package results turns engine observations into the run Result
// (docs/SIMULATION_SPEC.md §5, §9; T-022): measurement-window filtering,
// populations, Type 7 quantiles, replication-mean confidence intervals,
// sampled traces and the result digest.
//
// Every number is computed in a fixed order (replications by index, entities
// and visits in event order) from integer ticks, so the same observations
// give the same bits; the Student-t critical values are constants. As in the
// engine (D-038), every product that feeds a sum is rounded by an explicit
// float64 conversion so no GOARCH fuses it into an FMA (make backend-numeric).
package metrics

import (
	"math"
	"slices"

	"github.com/kcl1104/geminus/internal/simulation"
)

// quantile7 is the Type 7 sample quantile of x at p in [0, 1]: sort,
// h = (n-1)p, interpolate between floor(h) and ceil(h). x must not be empty;
// it is not modified.
func quantile7(x []float64, p float64) float64 {
	s := slices.Clone(x)
	slices.Sort(s)
	h := float64(float64(len(s)-1) * p)
	lo, hi := math.Floor(h), math.Ceil(h)
	return s[int(lo)] + float64((h-lo)*(s[int(hi)]-s[int(lo)]))
}

// mean sums in the given order; callers pass a deterministic order.
func mean(x []float64) float64 {
	sum := 0.0
	for _, v := range x {
		sum += v
	}
	return sum / float64(len(x))
}

// ci95 is the Student-t 95% interval of the mean of independent replication
// values; nil for fewer than two values (SIMULATION_SPEC §5).
func ci95(x []float64) *simulation.ConfidenceInterval {
	n := len(x)
	if n < 2 || n > len(studentT975) {
		return nil
	}
	m := mean(x)
	ss := 0.0
	for _, v := range x {
		ss += float64((v - m) * (v - m))
	}
	half := studentT975[n-1] * math.Sqrt(ss/float64(n-1)) / math.Sqrt(float64(n))
	return &simulation.ConfidenceInterval{Level: 0.95, Lower: m - half, Upper: m + half, Method: "student_t_replication_means"}
}

func ptr[T any](v T) *T { return &v }

// Summarize is summary for a domain pack's model metrics (D-058, T-051):
// the same mean, Type 7 quantiles and replication-mean interval as every
// statistic of the Result, in the same numerical profile. values are in a
// deterministic order (replications by index) and are not modified.
func Summarize(values []float64, pop simulation.Population, unit, reasonIfEmpty string) simulation.MetricSummary {
	return summary(values, pop, unit, reasonIfEmpty)
}

// summary describes values of one population: available with mean, p50,
// p95 and (for the replication population) a 95% CI, or unavailable with a
// reason when there are no values. A missing metric is never 0.
func summary(values []float64, pop simulation.Population, unit, reasonIfEmpty string) simulation.MetricSummary {
	if len(values) == 0 {
		return simulation.MetricSummary{Status: simulation.Unavailable, Population: pop, Unit: unit, Reason: ptr(reasonIfEmpty)}
	}
	s := simulation.MetricSummary{Status: simulation.Available, Population: pop, Unit: unit, SampleCount: int64(len(values)),
		Mean: ptr(mean(values)), P50: ptr(quantile7(values, 0.5)), P95: ptr(quantile7(values, 0.95))}
	if pop == simulation.PopulationReplication {
		s.ConfidenceIntervalMean = ci95(values)
	}
	return s
}
