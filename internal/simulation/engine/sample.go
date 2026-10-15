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
	if s.kind == model.DistEmpirical {
		// Equal weights, floor(U*N).
		i := int(u * float64(len(s.values)))
		if i >= len(s.values) { // U < 1 makes this unreachable; kept as a guard
			i = len(s.values) - 1
		}
		return s.values[i]
	}
	return s.ticksOf(s.value(u))
}

// continuous reports whether the sampler draws through value.
func (s *sampler) continuous() bool { return !s.fixed && s.kind != model.DistEmpirical }

// value is a continuous sampler's duration for uniform u, in the model unit,
// before quantization.
func (s *sampler) value(u float64) float64 {
	if s.kind == model.DistExponential {
		return -s.mean * ln(1-u) // -mean*ln(1-U)
	}
	// Triangular: the standard inverse CDF; min < max here, so nothing
	// divides by zero. Rounding can step just outside [min, max] (with
	// min = mode = 1e-300 and U = 2^-53, max - sqrt(...) is exactly 0, a zero
	// interarrival that D-033 forbids), so the value is clamped.
	width := s.max - s.min
	var v float64
	if u < (s.mode-s.min)/width {
		v = s.min + math.Sqrt(u*width*(s.mode-s.min))
	} else {
		v = s.max - math.Sqrt((1-u)*width*(s.max-s.mode))
	}
	return min(max(v, s.min), s.max)
}

// ticksOf quantizes a continuous sample. An exponential is never zero-time
// (D-033): a sample that underflows to 0 still takes one tick.
func (s *sampler) ticksOf(v float64) int64 {
	t := quantize(v, s.unitMs)
	if s.kind == model.DistExponential {
		return max(t, 1)
	}
	return t
}

// ln is the natural logarithm of the numerical profile, pinned so a sample
// does not depend on GOARCH: Go's math.Log is assembly on amd64 and s390x
// and pure Go elsewhere, where the compiler may fuse a*b+c into one FMA and
// change the last bit (found by the T-020 review). This is the pure-Go
// algorithm of Go's math.log with every product rounded by an explicit
// float64 conversion, which the Go spec says prevents fusion, so each
// operation is one IEEE-754 binary64 rounding on every platform.
// scripts/engine_reference.py ports it operation for operation, and
// TestLogProfile checks the two bit for bit.
//
// The algorithm and constants are from Go's src/math/log.go (Copyright 2009
// The Go Authors; BSD-3-Clause, see docs/THIRD_PARTY_NOTICES.md), which took
// them from FreeBSD's /usr/src/lib/msun/src/e_log.c with this notice:
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
func ln(x float64) float64 {
	switch {
	case math.IsNaN(x) || math.IsInf(x, 1):
		return x
	case x < 0:
		return math.NaN()
	case x == 0:
		return math.Inf(-1)
	}
	// Reduce: x = 2^k * (1+f) with sqrt(2)/2 <= 1+f < sqrt(2). Frexp is exact.
	f1, ki := math.Frexp(x)
	if f1 < sqrtHalf {
		f1 *= 2
		ki--
	}
	f := f1 - 1
	k := float64(ki)
	// Compute log(1+f) = f - hfsq + s*(hfsq+R), s = f/(2+f).
	s := f / (2 + f)
	s2 := float64(s * s)
	s4 := float64(s2 * s2)
	t1 := float64(s2 * (lnL1 + float64(s4*(lnL3+float64(s4*(lnL5+float64(s4*lnL7)))))))
	t2 := float64(s4 * (lnL2 + float64(s4*(lnL4+float64(s4*lnL6)))))
	r := t1 + t2
	hfsq := float64(float64(0.5*f) * f)
	return float64(k*ln2Hi) - ((hfsq - (float64(s*(hfsq+r)) + float64(k*ln2Lo))) - f)
}

// The constants of ln, by bit pattern (the hex values in Go's comments).
var (
	ln2Hi    = math.Float64frombits(0x3fe62e42fee00000)
	ln2Lo    = math.Float64frombits(0x3dea39ef35793c76)
	lnL1     = math.Float64frombits(0x3fe5555555555593)
	lnL2     = math.Float64frombits(0x3fd999999997fa04)
	lnL3     = math.Float64frombits(0x3fd2492494229359)
	lnL4     = math.Float64frombits(0x3fcc71c51d8e78af)
	lnL5     = math.Float64frombits(0x3fc7466496cb03de)
	lnL6     = math.Float64frombits(0x3fc39a09d078c69f)
	lnL7     = math.Float64frombits(0x3fc2f112df3e5244)
	sqrtHalf = math.Float64frombits(0x3fe6a09e667f3bcd) // sqrt(2)/2 rounded
)
