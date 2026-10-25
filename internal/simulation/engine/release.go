package engine

import (
	"fmt"
	"math"

	"github.com/kcl1104/geminus/internal/model"
)

// releaser is a source's release schedule (ADR-026 D-053, T-049): a
// periodic piecewise-constant rate (poisson or deterministic) or a release
// table, instead of an interarrival distribution.
//
// A rate schedule makes the k-th arrival at the least t with Λ(t) >= a_k,
// Λ the cumulative rate and a_k = a_(k-1) + X_k, X_k = 1 (deterministic,
// no draw) or -ln(1 - U) (poisson: one draw of the source's arrival
// stream, so the k-th arrival of every scenario takes the same draw). The
// numerical profile is D-053's (review L4): P = Λ(period) summed in
// segment order with every product rounded; k = ceil(a/P) - 1 and
// r = a - kP, corrected once into (0, P]; the first segment j with a
// positive rate and cumEnd_j >= r; τ = k·period + start_j +
// (r - cumStart_j)/rate_j clamped to the segment; τ quantized like a
// sample (one product, round half up, a positive τ at least one tick, past
// 365 days 365 days + 1 tick: never processed) and at least one tick
// after the previous arrival. a is kept unquantized. ln is the pinned ln.
// scripts/engine_reference.py ports it operation for operation.
//
// A table releases quantity entities at k·period + at for every row, in
// ticks: the period and the times are quantized with model.Ticks first
// (D-082 (b)), so the table repeats exactly in ticks.
type releaser struct {
	table   bool
	poisson bool
	unitMs  float64
	// Rate schedules, in the model unit.
	period                                float64
	starts, ends, rates, cumStart, cumEnd []float64
	total                                 float64 // P = Λ(period)
	// Tables, in ticks.
	periodTicks int64
	at          []int64
	quantity    []int
}

func newReleaser(s model.ReleaseSchedule, unit model.TimeUnit) (*releaser, error) {
	ms, ok := unit.Millis()
	if !ok {
		return nil, fmt.Errorf("unknown time unit %q", unit)
	}
	r := &releaser{unitMs: float64(ms), period: s.Period}
	switch s.Process {
	case model.ReleaseTable:
		r.table = true
		p, err := model.Ticks(s.Period, unit)
		if err != nil {
			return nil, fmt.Errorf("release period: %w", err)
		}
		r.periodTicks = p
		last := int64(-1)
		for _, rel := range s.Releases {
			at, err := model.Ticks(rel.At, unit)
			if err != nil || at <= last || at >= p || rel.Quantity < 1 {
				return nil, fmt.Errorf("release table: a release at %g of %d is out of order or outside the period", rel.At, rel.Quantity)
			}
			r.at, r.quantity, last = append(r.at, at), append(r.quantity, rel.Quantity), at
		}
		if len(r.at) == 0 {
			return nil, fmt.Errorf("release table without releases")
		}
		return r, nil
	case model.ReleasePoisson:
		r.poisson = true
	case model.ReleaseDeterministic:
	default:
		return nil, fmt.Errorf("unknown release process %q", s.Process)
	}
	if !(s.Period > 0) || math.IsInf(s.Period, 0) || len(s.Segments) == 0 || s.Segments[0].Start != 0 {
		return nil, fmt.Errorf("release schedule: period %g, %d segments", s.Period, len(s.Segments))
	}
	cum := 0.0
	for j, seg := range s.Segments {
		end := s.Period
		if j+1 < len(s.Segments) {
			end = s.Segments[j+1].Start
		}
		if !(seg.Start < end) || !(seg.Rate >= 0) || math.IsInf(seg.Rate, 0) || seg.Rate > r.unitMs {
			return nil, fmt.Errorf("release schedule: segment %d [%g, %g) at rate %g", j, seg.Start, end, seg.Rate)
		}
		r.starts, r.ends, r.rates, r.cumStart = append(r.starts, seg.Start), append(r.ends, end), append(r.rates, seg.Rate), append(r.cumStart, cum)
		cum += float64(seg.Rate * (end - seg.Start)) // the product rounded first (no FMA)
		r.cumEnd = append(r.cumEnd, cum)
	}
	if !(cum > 0) {
		return nil, fmt.Errorf("release schedule: every rate is 0")
	}
	r.total = cum
	return r, nil
}

// draw is X_k: 1 for a deterministic schedule, -ln(1 - U) for a poisson
// one (one draw).
func (s *releaser) draw(rng *SplitMix64) float64 {
	if !s.poisson {
		return 1
	}
	return -ln(1 - rng.Uniform())
}

// tau is Λ⁻¹(a) in the model unit, D-053's profile.
func (s *releaser) tau(a float64) float64 {
	k := math.Ceil(a/s.total) - 1
	r := a - float64(k*s.total)
	// In binary64 r can fall outside (0, P] (P = 0.3, a = 502.5): one
	// correction.
	if r <= 0 {
		k, r = k-1, r+s.total
	} else if r > s.total {
		k, r = k+1, r-s.total
	}
	j := -1
	for i := range s.rates {
		if s.rates[i] > 0 && s.cumEnd[i] >= r {
			j = i
			break
		}
	}
	if j < 0 {
		// Unreachable: after the correction r <= P, the cumEnd of the last
		// segment with a positive rate. Kept as a guard.
		for i := len(s.rates) - 1; j < 0; i-- {
			if s.rates[i] > 0 {
				j = i
			}
		}
	}
	base := float64(k * s.period)
	lo, hi := base+s.starts[j], base+s.ends[j]
	return min(max(lo+(r-s.cumStart[j])/s.rates[j], lo), hi)
}

// tick is the tick of cumulative rate a: τ quantized as a sample
// (quantize: past 365 days it is beyondCap, never processed).
func (s *releaser) tick(a float64) int64 { return quantize(s.tau(a), s.unitMs) }
