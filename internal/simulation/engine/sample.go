package engine

import (
	"fmt"
	"math"
	"slices"

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
	mu     float64 // lognormal: ln(mean) - sigma^2/2 (D-056)
	sigma  float64 // lognormal: sqrt(ln(1 + cv^2))
	shape  float64 // weibull
	scale  float64 // weibull, in the model unit
}

// The parameter ranges of D-056 and D-075, as the validator has them
// (validate.MaxLognormalCV, MinWeibullShape, MaxWeibullShape); the engine
// checks again instead of trusting its caller.
const (
	maxLognormalCV  = 10.0
	minWeibullShape = 0.1
	maxWeibullShape = 20.0
)

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
	case model.DistLognormal:
		mean, err := param("mean", d.Mean)
		if err != nil {
			return nil, err
		}
		cv, err := param("cv", d.CV)
		if err != nil {
			return nil, err
		}
		if !(mean > 0) || !(cv > 0 && cv <= maxLognormalCV) {
			return nil, fmt.Errorf("lognormal needs mean > 0 and 0 < cv <= %g, got %g, %g", maxLognormalCV, mean, cv)
		}
		if _, err := model.Ticks(mean, unit); err != nil {
			return nil, err
		}
		// D-056: sigma^2 = ln(1 + cv^2), mu = ln(mean) - sigma^2/2, with
		// the pinned ln; every product is rounded before the sum (s2/2,
		// which the compiler turns into s2*0.5, would otherwise be fused
		// into the subtraction on arm64, riscv64 and loong64: found by
		// scripts/check_no_fma.sh).
		s2 := ln(1 + float64(cv*cv))
		s.sigma, s.mu = math.Sqrt(s2), ln(mean)-float64(s2/2)
	case model.DistWeibull:
		if s.shape, err = param("shape", d.Shape); err != nil {
			return nil, err
		}
		if s.scale, err = param("scale", d.Scale); err != nil {
			return nil, err
		}
		if !(s.scale > 0) || !(s.shape >= minWeibullShape && s.shape <= maxWeibullShape) {
			return nil, fmt.Errorf("weibull needs scale > 0 and %g <= shape <= %g, got %g, %g", minWeibullShape, maxWeibullShape, s.scale, s.shape)
		}
		if _, err := model.Ticks(s.scale, unit); err != nil {
			return nil, err
		}
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
	switch s.kind {
	case model.DistExponential:
		return -s.mean * ln(1-u) // -mean*ln(1-U)
	case model.DistLognormal:
		// D-056: exp(mu + sigma*Phi^-1(U)), one draw per sample; the product
		// is rounded before the sum, so no FMA.
		return exp(s.mu + float64(s.sigma*ndtri(u)))
	case model.DistWeibull:
		// D-056: scale*(-ln(1-U))^(1/shape), the power as exp(ln(y)/shape).
		return s.scale * exp(ln(-ln(1-u))/s.shape)
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

// ticksOf quantizes a continuous sample. An exponential, lognormal or
// Weibull sample is never zero-time (D-033, D-056): one that underflows to
// 0 still takes one tick.
func (s *sampler) ticksOf(v float64) int64 {
	t := quantize(v, s.unitMs)
	switch s.kind {
	case model.DistExponential, model.DistLognormal, model.DistWeibull:
		return max(t, 1)
	}
	return t
}

// bound is the largest number of ticks a sample can take, or -1 when
// there is none (D-050 B2's TTRmax): a constant's value, a triangular's
// max, an empirical's largest sample; the exponential, lognormal and
// Weibull have no upper bound.
func (s *sampler) bound() int64 {
	switch {
	case s.fixed:
		return s.ticks
	case s.kind == model.DistTriangular:
		return quantize(s.max, s.unitMs)
	case s.kind == model.DistEmpirical:
		return slices.Max(s.values)
	}
	return -1
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

// exp is the exponential of the numerical profile (D-056, T-048), pinned
// like ln: Go's math.Exp is assembly on amd64, arm64, loong64, riscv64 and
// s390x and pure Go elsewhere, where a*b+c may fuse. This is the pure-Go
// algorithm of Go's math.exp with every product rounded by an explicit
// float64 conversion, so each operation is one binary64 rounding on every
// platform; math.Ldexp is exact, or one correct rounding for a subnormal
// result, and pure Go on every GOARCH. scripts/engine_reference.py ports
// it operation for operation (TestExpProfile).
//
// The algorithm and constants are from Go's src/math/exp.go (Copyright
// 2009 The Go Authors; BSD-3-Clause, see docs/THIRD_PARTY_NOTICES.md),
// which took them from FreeBSD's /usr/src/lib/msun/src/e_exp.c with this
// notice:
//
// ====================================================
// Copyright (C) 2004 by Sun Microsystems, Inc. All rights reserved.
//
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
func exp(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return x
	case x > expOverflow: // +Inf too
		return math.Inf(1)
	case x < expUnderflow: // -Inf too
		return 0
	case -expNearZero < x && x < expNearZero:
		return 1 + x
	}
	// Reduce: x = k*ln2 + r, |r| <= ln2/2, r = hi - lo for extra precision.
	var k int
	if x < 0 {
		k = int(float64(expLog2e*x) - 0.5)
	} else {
		k = int(float64(expLog2e*x) + 0.5)
	}
	hi := x - float64(float64(k)*expLn2Hi)
	lo := float64(float64(k) * expLn2Lo)
	r := hi - lo
	t := float64(r * r)
	c := r - float64(t*(expP1+float64(t*(expP2+float64(t*(expP3+float64(t*(expP4+float64(t*expP5)))))))))
	y := 1 - ((lo - float64(r*c)/(2-c)) - hi)
	return math.Ldexp(y, k)
}

// The constants of exp, as Go's source writes them (their bit patterns are
// in testdata/simulation/engine-vectors.json).
const (
	expLn2Hi     = 6.93147180369123816490e-01
	expLn2Lo     = 1.90821492927058770002e-10
	expLog2e     = 1.44269504088896338700e+00
	expOverflow  = 7.09782712893383973096e+02
	expUnderflow = -7.45133219101941108420e+02
	expNearZero  = 1.0 / (1 << 28) // 2^-28
	expP1        = 1.66666666666666657415e-01
	expP2        = -2.77777777770155933842e-03
	expP3        = 6.61375632143793436117e-05
	expP4        = -1.65339022054652515390e-06
	expP5        = 4.13813679705723846039e-08
)

// ndtri is the inverse of the standard normal CDF by Wichura's algorithm
// AS241 (PPND16, Applied Statistics 37 (1988) 477–484, accurate to about
// 1e-16), D-056's Phi^-1: two rational approximations, in q = p - 0.5 for
// |q| <= 0.425 and in r = sqrt(-ln(min(p, 1-p))) beyond, split at r = 5.
// Horner's rule from the highest coefficient with each product rounded,
// the pinned ln and the correctly rounded sqrt, so it is the same on
// every platform; scripts/engine_reference.py ports it (TestNdtriProfile).
// p is in (0, 1).
func ndtri(p float64) float64 {
	q := p - 0.5
	if math.Abs(q) <= 0.425 {
		r := 0.180625 - float64(q*q)
		return float64(q*horner(&as241A, r)) / horner(&as241B, r)
	}
	r := p
	if q > 0 {
		r = 1 - p
	}
	r = math.Sqrt(-ln(r))
	var v float64
	if r <= 5 {
		r -= 1.6
		v = horner(&as241C, r) / horner(&as241D, r)
	} else {
		r -= 5
		v = horner(&as241E, r) / horner(&as241F, r)
	}
	if q < 0 {
		v = -v
	}
	return v
}

// horner evaluates c[0] + c[1] r + ... + c[7] r^7 from the highest term,
// every product rounded before its sum.
func horner(c *[8]float64, r float64) float64 {
	v := c[7]
	for i := 6; i >= 0; i-- {
		v = float64(v*r) + c[i]
	}
	return v
}

// AS241's coefficients, lowest first (b, d and f start with 1).
var (
	as241A = [8]float64{3.3871328727963666080e0, 1.3314166789178437745e+2, 1.9715909503065514427e+3, 1.3731693765509461125e+4,
		4.5921953931549871457e+4, 6.7265770927008700853e+4, 3.3430575583588128105e+4, 2.5090809287301226727e+3}
	as241B = [8]float64{1, 4.2313330701600911252e+1, 6.8718700749205790830e+2, 5.3941960214247511077e+3,
		2.1213794301586595867e+4, 3.9307895800092710610e+4, 2.8729085735721942674e+4, 5.2264952788528545610e+3}
	as241C = [8]float64{1.42343711074968357734e0, 4.63033784615654529590e0, 5.76949722146069140550e0, 3.64784832476320460504e0,
		1.27045825245236838258e0, 2.41780725177450611770e-1, 2.27238449892691845833e-2, 7.74545014278341407640e-4}
	as241D = [8]float64{1, 2.05319162663775882187e0, 1.67638483018380384940e0, 6.89767334985100004550e-1,
		1.48103976427480074590e-1, 1.51986665636164571966e-2, 5.47593808499534494600e-4, 1.05075007164441684324e-9}
	as241E = [8]float64{6.65790464350110377720e0, 5.46378491116411436990e0, 1.78482653991729133580e0, 2.96560571828504891230e-1,
		2.65321895265761230930e-2, 1.24266094738807843860e-3, 2.71155556874348757815e-5, 2.01033439929228813265e-7}
	as241F = [8]float64{1, 5.99832206555887937690e-1, 1.36929880922735805310e-1, 1.48753612908506148525e-2,
		7.86869131145613259100e-4, 1.84631831751005468180e-5, 1.42151175831644588870e-7, 2.04426310338993978564e-15}
)
