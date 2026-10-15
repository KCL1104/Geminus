package engine

import (
	"fmt"
	"math"

	"github.com/kcl1104/geminus/internal/model"
)

// beyondCap is the duration a sample gets when it exceeds the 365 day cap
// (only an exponential tail can): later than any horizon (D-005), so the
// event it schedules is never processed. It keeps tick arithmetic far from
// int64 overflow: now + beyondCap <= 2*365 days.
const beyondCap = model.MaxDurationTicks + 1

// quantize converts a sampled duration d (in the model's time unit, d >= 0)
// to ticks with exactly the model.Ticks numerical profile: one binary64
// multiplication, math.Round, a positive sub-tick sample becomes 1 tick.
// Unlike model.Ticks it saturates at beyondCap instead of failing, because a
// sample is not an input that can be rejected.
func quantize(d, unitMs float64) int64 {
	t := math.Round(d * unitMs)
	switch {
	case t > float64(model.MaxDurationTicks):
		return beyondCap
	case t == 0 && d > 0:
		return 1
	}
	return int64(t)
}

// sampler draws durations in ticks for one distribution
// (docs/SIMULATION_SPEC.md §4). It is immutable after compile and safe to
// share between replications; all state is in the stream passed to draw.
type sampler struct {
	kind   model.DistributionType
	fixed  bool    // constant, or a triangular with min = mode = max: no draw
	ticks  int64   // the duration when fixed
	unitMs float64 // milliseconds per model time unit
	mean   float64 // exponential, in the model unit
	min    float64 // triangular, in the model unit
	mode   float64
	max    float64
	values []int64 // empirical samples, already quantized
}

// newSampler compiles d. Empirical samples come from the resolved dataset, in
// that dataset's unit.
func newSampler(d model.Distribution, unit model.TimeUnit, empirical []int64) (*sampler, error) {
	ms, ok := unit.Millis()
	if !ok {
		return nil, fmt.Errorf("unknown time unit %q", unit)
	}
	s := &sampler{kind: d.Type, unitMs: float64(ms)}
	param := func(name string, p *float64) (float64, error) {
		if p == nil {
			return 0, fmt.Errorf("%s distribution without %s", d.Type, name)
		}
		return *p, nil
	}
	var err error
	switch d.Type {
	case model.DistConstant:
		var v float64
		if v, err = param("value", d.Value); err != nil {
			return nil, err
		}
		if s.ticks, err = model.Ticks(v, unit); err != nil {
			return nil, err
		}
		s.fixed = true
	case model.DistExponential:
		if s.mean, err = param("mean", d.Mean); err != nil {
			return nil, err
		}
		if !(s.mean > 0) || math.IsInf(s.mean, 0) {
			return nil, fmt.Errorf("exponential mean %g is not a positive finite number", s.mean)
		}
	case model.DistTriangular:
		if s.min, err = param("min", d.Min); err != nil {
			return nil, err
		}
		if s.mode, err = param("mode", d.Mode); err != nil {
			return nil, err
		}
		if s.max, err = param("max", d.Max); err != nil {
			return nil, err
		}
		if !(s.min <= s.mode && s.mode <= s.max) || s.min < 0 || math.IsInf(s.max, 0) {
			return nil, fmt.Errorf("triangular needs 0 <= min <= mode <= max, got %g, %g, %g", s.min, s.mode, s.max)
		}
		if _, err = model.Ticks(s.max, unit); err != nil {
			return nil, err
		}
		if s.min == s.max {
			s.fixed = true
			s.ticks, _ = model.Ticks(s.min, unit)
		}
	case model.DistEmpirical:
		if len(empirical) == 0 {
			return nil, fmt.Errorf("empirical distribution %s has no samples", d.SamplesRef)
		}
		s.values = empirical
	default:
		return nil, fmt.Errorf("unknown distribution type %q", d.Type)
	}
	return s, nil
}

// draw returns one duration in ticks. A fixed distribution consumes no draw.
func (s *sampler) draw(r *SplitMix64) int64 {
	if s.fixed {
		return s.ticks
	}
	u := r.Uniform() // strictly inside (0, 1)
	switch s.kind {
	case model.DistExponential:
		// -mean*ln(1-U). An exponential is never zero-time (D-033): a sample
		// that underflows to 0 still takes one tick.
		return max(quantize(-s.mean*math.Log(1-u), s.unitMs), 1)
	case model.DistTriangular:
		// Standard inverse CDF; min < max here, so nothing divides by zero.
		width := s.max - s.min
		if u < (s.mode-s.min)/width {
			return quantize(s.min+math.Sqrt(u*width*(s.mode-s.min)), s.unitMs)
		}
		return quantize(s.max-math.Sqrt((1-u)*width*(s.max-s.mode)), s.unitMs)
	default: // empirical: equal weights, floor(U*N)
		i := int(u * float64(len(s.values)))
		if i >= len(s.values) { // U < 1 makes this unreachable; kept as a guard
			i = len(s.values) - 1
		}
		return s.values[i]
	}
}
