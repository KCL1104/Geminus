package observed_test

import (
	"math"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/results/observed"
	"github.com/kcl1104/geminus/internal/simulation"
)

func f(v float64) *float64 { return &v }

func eq(a, b *float64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// want is a comparison's numbers; nil is null.
type want struct {
	simulated, low, high, abs, rel *float64
	method, relReason              string
	within                         *bool
	reps                           int
}

func check(t *testing.T, name string, c observed.Comparison, o float64, w want) {
	t.Helper()
	if c.Status != simulation.Available || c.Reason != nil || c.Observed == nil || *c.Observed != o {
		t.Errorf("%s: status %s reason %s observed %v", name, c.Status, str(c.Reason), c.Observed)
	}
	if !eq(c.Simulated, w.simulated) || !eq(c.RangeLow, w.low) || !eq(c.RangeHigh, w.high) || !eq(c.AbsoluteError, w.abs) ||
		!eq(c.RelativeError, w.rel) || c.RangeMethod != w.method || str(c.RelativeReason) != w.relReason || c.Replications != w.reps ||
		(c.WithinRange == nil) != (w.within == nil) || (c.WithinRange != nil && *c.WithinRange != *w.within) {
		t.Errorf("%s: %+v", name, c)
	}
}

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

// D-060 option A, by hand (binary64, the order of operations of the
// package; checked with Python floats): the simulated mean over the
// replications, their range (min-max below 20 replications, Type 7 2.5% and
// 97.5% from 20), the absolute error mean - observed, the relative error
// over the observed (null at an observed 0: OBSERVED_ZERO) and whether the
// observed lies in the range (bounds included). No verdict.
func TestCompareOutput(t *testing.T) {
	c := observed.CompareOutput(10, []float64{13, 8, 9})
	if c.Kind != observed.Output {
		t.Errorf("kind %s", c.Kind)
	}
	check(t, "inside", c, 10, want{simulated: f(10), low: f(8), high: f(13), abs: f(0), rel: f(0), method: observed.RangeMinMax, within: yes(), reps: 3})
	check(t, "above", observed.CompareOutput(4, []float64{5, 7}), 4,
		want{simulated: f(6), low: f(5), high: f(7), abs: f(2), rel: f(0.5), method: observed.RangeMinMax, within: no(), reps: 2})
	check(t, "on a bound", observed.CompareOutput(7, []float64{5, 7}), 7,
		want{simulated: f(6), low: f(5), high: f(7), abs: f(-1), rel: f(-1.0 / 7), method: observed.RangeMinMax, within: yes(), reps: 2})
	check(t, "observed zero", observed.CompareOutput(0, []float64{1, 3}), 0,
		want{simulated: f(2), low: f(1), high: f(3), abs: f(2), method: observed.RangeMinMax, relReason: observed.ReasonObservedZero, within: no(), reps: 2})
	check(t, "one replication", observed.CompareOutput(3588, []float64{3588}), 3588,
		want{simulated: f(3588), low: f(3588), high: f(3588), abs: f(0), rel: f(0), method: observed.RangeMinMax, within: yes(), reps: 1})
	twenty := make([]float64, 20)
	for i := range twenty {
		twenty[19-i] = float64(i + 1) // unsorted on purpose
	}
	kept := slices.Clone(twenty)
	check(t, "twenty replications", observed.CompareOutput(10.5, twenty), 10.5,
		want{simulated: f(10.5), low: f(1.475), high: f(19.525), abs: f(0), rel: f(0), method: observed.RangeQuantiles, within: yes(), reps: 20})
	if !slices.Equal(twenty, kept) {
		t.Error("the replications were changed")
	}
	// Run-time binary64 (a Go constant expression would be exact): the mean
	// 17951/5 = 3590.2, its error 2.1999999999998181 and that over 3588.
	five := []float64{3584, 3601, 3577, 3590, 3599}
	m, o := 3590.2, 3588.0
	check(t, "five", observed.CompareOutput(3588, five), 3588,
		want{simulated: f(m), low: f(3577), high: f(3601), abs: f(m - o), rel: f((m - o) / o), method: observed.RangeMinMax, within: yes(), reps: 5})
	if d := m - o; d != 2.1999999999998181 {
		t.Errorf("binary64: %v", d)
	}
}

// The model's input against the observed value (input consistency, §7):
// no replications and no range; the errors as for an output.
func TestCompareInput(t *testing.T) {
	c := observed.CompareInput(0.95, 0.97)
	if c.Kind != observed.InputConsistency {
		t.Errorf("kind %s", c.Kind)
	}
	check(t, "fpy", c, 0.95, want{simulated: f(0.97), abs: f(0.020000000000000018), rel: f(0.02105263157894739)})
	check(t, "zero", observed.CompareInput(0, 2), 0, want{simulated: f(2), abs: f(2), relReason: observed.ReasonObservedZero})
}

// Never a number without its data (REQ-015): no replication, or a value
// that is not finite, is unavailable with its reason and no number at all.
func TestUnavailable(t *testing.T) {
	for name, tc := range map[string]struct {
		c      observed.Comparison
		reason string
	}{
		"no replication":           {observed.CompareOutput(1, nil), observed.ReasonSimulatedUnavailable},
		"a replication not finite": {observed.CompareOutput(1, []float64{1, math.NaN()}), observed.ReasonSimulatedUnavailable},
		"an infinite replication":  {observed.CompareOutput(1, []float64{math.Inf(1)}), observed.ReasonSimulatedUnavailable},
		"an observed NaN":          {observed.CompareOutput(math.NaN(), []float64{1}), observed.ReasonObservedNotFinite},
		"an input NaN":             {observed.CompareInput(1, math.NaN()), observed.ReasonSimulatedUnavailable},
		"an observed input Inf":    {observed.CompareInput(math.Inf(-1), 1), observed.ReasonObservedNotFinite},
		"given":                    {observed.Unavailable(observed.Output, observed.ReasonNoObservedData), observed.ReasonNoObservedData},
	} {
		c := tc.c
		if c.Status != simulation.Unavailable || str(c.Reason) != tc.reason || c.Observed != nil || c.Simulated != nil || c.RangeLow != nil ||
			c.RangeHigh != nil || c.AbsoluteError != nil || c.RelativeError != nil || c.RelativeReason != nil || c.WithinRange != nil || c.RangeMethod != "" {
			t.Errorf("%s: %+v", name, c)
		}
	}
}

// Review r1 (the coordinator's decision on I2): data generated by the
// compared model itself (synthetic ground truth) has its own label,
// neither in-sample nor hold-out; the labels are distinct, and the
// arithmetic never sets one (the caller labels, D-060).
func TestSampleLabels(t *testing.T) {
	labels := []observed.Sample{observed.InSample, observed.HoldOut, observed.ModelGenerated}
	if want := []observed.Sample{"in_sample", "hold_out", "model_generated"}; !slices.Equal(labels, want) {
		t.Errorf("labels %q", labels)
	}
	if c := observed.CompareOutput(1, []float64{1}); c.Sample != "" {
		t.Errorf("CompareOutput labels %q", c.Sample)
	}
	if c := observed.CompareInput(1, 1); c.Sample != "" {
		t.Errorf("CompareInput labels %q", c.Sample)
	}
}
