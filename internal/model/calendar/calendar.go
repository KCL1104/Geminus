// Package calendar is the integer-tick arithmetic of shift calendars
// (ADR-026 D-052, D-050 R2, R4 and B2, D-051): a resource's units on
// shift S(t), when S changes, the next tick at which S reaches or falls
// below a number of units, the on-shift time Θ(t) by which a failure clock
// advances, and the first tick at which several resources are all on
// shift together (next_fit). Times are 1 ms ticks from t=0 (model.Ticks):
// no wall clock, date or time zone (D-052 A+).
//
// The validator (R2, R4) and the engine (CAPACITY_CHANGE events, the
// scheduled failure clock and B2's deadline) share it, so the two can
// never read one calendar differently. Its inputs are checked by the
// validator first (D-052's shape rules); New refuses what they would.
//
// Like internal/model it is a pure leaf: the standard library and
// internal/model only, no map ever decides an order.
package calendar

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/kcl1104/geminus/internal/model"
)

// Shift is one periodic on-shift interval [Start, End) within a period,
// in ticks, with the resource's units on shift during it.
type Shift struct {
	Start, End int64
	Units      int
}

// Exception overrides the period over [Start, End), in absolute ticks
// from t=0: closed (S = 0) or open (S = the capacity) (D-052).
type Exception struct {
	Start, End int64
	Open       bool
}

// Pattern is one resource's S(t): a step function periodic in Period,
// overridden by the exceptions. It is immutable and safe to share.
type Pattern struct {
	period   int64
	capacity int
	// The periodic step function on [0, period): segment k holds values[k]
	// from starts[k] to the next start (or the period's end); starts[0] is
	// 0 and consecutive values differ.
	starts []int64
	values []int
	// breaks are the offsets in [0, period) where the periodic S changes,
	// read cyclically (offset 0 is one only when the last segment differs
	// from the first); empty when the periodic S is constant.
	breaks []int64
	// onAt[k] is the on-shift ticks (S > 0) of [0, starts[k]); onPer of a
	// whole period.
	onAt  []int64
	onPer int64
	exc   []Exception
	// alwaysOn: S > 0 at every tick (no closed exception, the periodic S
	// never 0), so the on-shift time is the calendar time.
	alwaysOn bool
}

// ErrInvalid: a calendar that the validator's shape rules refuse.
var ErrInvalid = errors.New("calendar: invalid calendar")

// New builds a pattern of a resource of capacity units: on shift with the
// given units during each shift, off otherwise, the exceptions on top.
// Shifts and exceptions must be in order, disjoint and within the period
// and 365 days.
func New(period int64, capacity int, shifts []Shift, exceptions []Exception) (*Pattern, error) {
	if period <= 0 || period > model.MaxDurationTicks || capacity < 1 {
		return nil, fmt.Errorf("%w: period %d, capacity %d", ErrInvalid, period, capacity)
	}
	p := &Pattern{period: period, capacity: capacity, exc: slices.Clone(exceptions)}
	add := func(start int64, units int) {
		if n := len(p.values); n > 0 && p.values[n-1] == units {
			return
		}
		p.starts, p.values = append(p.starts, start), append(p.values, units)
	}
	at := int64(0)
	for _, s := range shifts {
		if s.Start < at || s.End <= s.Start || s.End > period || s.Units < 0 || s.Units > capacity {
			return nil, fmt.Errorf("%w: shift [%d, %d) of %d units after %d in a period of %d", ErrInvalid, s.Start, s.End, s.Units, at, period)
		}
		if s.Start > at {
			add(at, 0)
		}
		add(s.Start, s.Units)
		at = s.End
	}
	if at < period {
		add(at, 0)
	}
	last := int64(0)
	for _, e := range exceptions {
		if e.Start < last || e.End <= e.Start || e.End > model.MaxDurationTicks {
			return nil, fmt.Errorf("%w: exception [%d, %d) after %d", ErrInvalid, e.Start, e.End, last)
		}
		last = e.End
	}
	for k := range p.starts {
		if k > 0 || p.values[0] != p.values[len(p.values)-1] {
			p.breaks = append(p.breaks, p.starts[k])
		}
	}
	if len(p.values) == 1 {
		p.breaks = nil
	}
	p.onAt = make([]int64, len(p.starts))
	for k := range p.starts {
		if k > 0 {
			p.onAt[k] = p.onAt[k-1]
			if p.values[k-1] > 0 {
				p.onAt[k] += p.starts[k] - p.starts[k-1]
			}
		}
	}
	p.onPer = p.onAt[len(p.onAt)-1]
	if p.values[len(p.values)-1] > 0 {
		p.onPer += period - p.starts[len(p.starts)-1]
	}
	p.alwaysOn = !slices.Contains(p.values, 0) && !slices.ContainsFunc(p.exc, func(e Exception) bool { return !e.Open })
	return p, nil
}

// Always is the pattern of a resource without a calendar: S = capacity.
func Always(capacity int) *Pattern {
	p, err := New(1, capacity, []Shift{{0, 1, capacity}}, nil)
	if err != nil {
		panic(err) // capacity < 1: a resource the schema refuses
	}
	return p
}

// FromModel quantizes c with model.Ticks (the engine's numerical profile,
// SIMULATION_SPEC §2) into the pattern of a resource of the given capacity
// and shift_capacity (labels not listed, and shifts without a label, have
// the whole capacity, D-052).
func FromModel(c model.Calendar, unit model.TimeUnit, capacity int, shiftCapacity map[string]int) (*Pattern, error) {
	period, err := model.Ticks(c.Period, unit)
	if err != nil {
		return nil, fmt.Errorf("%w: period: %v", ErrInvalid, err)
	}
	var shifts []Shift
	for _, s := range c.Shifts {
		start, err1 := model.Ticks(s.Start, unit)
		end, err2 := model.Ticks(s.End, unit)
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("%w: shift: %v", ErrInvalid, err)
		}
		units := capacity
		if n, ok := shiftCapacity[s.Label]; ok && s.Label != "" {
			units = n
		}
		shifts = append(shifts, Shift{start, end, units})
	}
	var exceptions []Exception
	for _, e := range c.Exceptions {
		start, err1 := model.Ticks(e.Start, unit)
		end, err2 := model.Ticks(e.End, unit)
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("%w: exception: %v", ErrInvalid, err)
		}
		exceptions = append(exceptions, Exception{start, end, e.Open})
	}
	return New(period, capacity, shifts, exceptions)
}

// Period is the pattern's period in ticks.
func (p *Pattern) Period() int64 { return p.period }

// Capacity is the resource's capacity C.
func (p *Pattern) Capacity() int { return p.capacity }

// AlwaysOn reports whether S > 0 at every tick.
func (p *Pattern) AlwaysOn() bool { return p.alwaysOn }

// Constant reports whether S never changes (and is then the capacity of
// every tick: S(0)).
func (p *Pattern) Constant() bool {
	if len(p.exc) == 0 {
		return len(p.breaks) == 0
	}
	_, changes := p.NextChange(-1)
	return !changes
}

// ExceptionsEnd is the end of the last exception, 0 without one (X_end's
// part from this calendar, D-050).
func (p *Pattern) ExceptionsEnd() int64 {
	if len(p.exc) == 0 {
		return 0
	}
	return p.exc[len(p.exc)-1].End
}

// segment is the periodic segment containing offset x in [0, period).
func (p *Pattern) segment(x int64) int {
	return sort.Search(len(p.starts), func(k int) bool { return p.starts[k] > x }) - 1
}

// periodic is the periodic S at tick t >= 0.
func (p *Pattern) periodic(t int64) int { return p.values[p.segment(t%p.period)] }

// exception is the exception containing t, or nil.
func (p *Pattern) exception(t int64) *Exception {
	k := sort.Search(len(p.exc), func(k int) bool { return p.exc[k].End > t })
	if k < len(p.exc) && p.exc[k].Start <= t {
		return &p.exc[k]
	}
	return nil
}

// nextExceptionStart is the first exception start > t, or -1.
func (p *Pattern) nextExceptionStart(t int64) int64 {
	k := sort.Search(len(p.exc), func(k int) bool { return p.exc[k].Start > t })
	if k < len(p.exc) {
		return p.exc[k].Start
	}
	return -1
}

// At is S(t) for t >= 0: the units on shift during tick t.
func (p *Pattern) At(t int64) int {
	if e := p.exception(t); e != nil {
		if e.Open {
			return p.capacity
		}
		return 0
	}
	return p.periodic(t)
}

// Periodic is S(t) for t >= 0 without the exceptions (D-050 R2 and R4
// read only the periodic part).
func (p *Pattern) Periodic(t int64) int { return p.periodic(t) }

// nextBreak is the first periodic change point > t, or -1 when the
// periodic S is constant.
func (p *Pattern) nextBreak(t int64) int64 {
	if len(p.breaks) == 0 {
		return -1
	}
	s := max(t+1, 0) // the first break >= s
	base, x := s/p.period*p.period, s%p.period
	k := sort.Search(len(p.breaks), func(k int) bool { return p.breaks[k] >= x })
	if k == len(p.breaks) {
		return base + p.period + p.breaks[0]
	}
	return base + p.breaks[k]
}

// NextChange is the first tick b > t at which S changes (S(b) != S(b-1)),
// with false when S never changes after t. The search passes at most the
// exceptions' boundaries and one change point of the period, never a
// whole year of ticks (D-051's "到 horizon 即停" holds for any limit).
func (p *Pattern) NextChange(t int64) (int64, bool) {
	for {
		b := p.nextBreak(t)
		if a := p.nextExceptionBoundary(t); a >= 0 && (b < 0 || a < b) {
			b = a
		}
		if b < 0 {
			return 0, false
		}
		if b > 0 && p.At(b) != p.At(b-1) {
			return b, true
		}
		t = b
		// Inside an exception S is constant: go to its end.
		if e := p.exception(b); e != nil && e.End-1 > t {
			t = e.End - 1
		}
	}
}

// nextExceptionBoundary is the first exception start or end > t, or -1.
func (p *Pattern) nextExceptionBoundary(t int64) int64 {
	k := sort.Search(len(p.exc), func(k int) bool { return p.exc[k].End > t })
	if k == len(p.exc) {
		return -1
	}
	if p.exc[k].Start > t {
		return p.exc[k].Start
	}
	return p.exc[k].End
}

// periodicNext is the first tick >= t at which the periodic S satisfies
// ok, or -1 when no offset of the period does.
func (p *Pattern) periodicNext(t int64, ok func(int) bool) int64 {
	base, x := t/p.period*p.period, t%p.period
	k := p.segment(x)
	for n := 0; n <= len(p.values); n++ {
		j := (k + n) % len(p.values)
		if ok(p.values[j]) {
			at := base + p.starts[j]
			if n == 0 {
				at = t
			} else if j <= k {
				at += p.period
			}
			return at
		}
	}
	return -1
}

// next is the first tick >= t at which S (with the exceptions when
// exceptions is set) satisfies ok, with false when there is none.
func (p *Pattern) next(t int64, ok func(int) bool, exceptions bool) (int64, bool) {
	if t < 0 {
		t = 0
	}
	if !exceptions || len(p.exc) == 0 {
		c := p.periodicNext(t, ok)
		return c, c >= 0
	}
	for {
		if e := p.exception(t); e != nil {
			v := 0
			if e.Open {
				v = p.capacity
			}
			if ok(v) {
				return t, true
			}
			t = e.End
			continue
		}
		c := p.periodicNext(t, ok)
		a := p.nextExceptionStart(t)
		if c >= 0 && (a < 0 || c < a) {
			return c, true
		}
		if a < 0 {
			return 0, false
		}
		t = a
	}
}

// NextAtLeast is the first tick >= t with S >= units; exceptions says
// whether the exceptions count (B2's next_fit) or only the period does
// (R2, R4).
func (p *Pattern) NextAtLeast(t int64, units int, exceptions bool) (int64, bool) {
	return p.next(t, func(v int) bool { return v >= units }, exceptions)
}

// NextBelow is the first tick >= t with S < units.
func (p *Pattern) NextBelow(t int64, units int, exceptions bool) (int64, bool) {
	return p.next(t, func(v int) bool { return v < units }, exceptions)
}

// periodicOn is the on-shift ticks (periodic S > 0) of [0, t).
func (p *Pattern) periodicOn(t int64) int64 {
	x := t % p.period
	k := p.segment(x)
	on := t/p.period*p.onPer + p.onAt[k]
	if p.values[k] > 0 {
		on += x - p.starts[k]
	}
	return on
}

// OnTicks is Θ(t): the ticks of [0, t) at which S > 0, the on-shift time
// by which a failure clock advances (D-051 (ii)).
func (p *Pattern) OnTicks(t int64) int64 {
	if t <= 0 {
		return 0
	}
	on := p.periodicOn(t)
	for _, e := range p.exc {
		if e.Start >= t {
			break
		}
		x := min(e.End, t)
		on -= p.periodicOn(x) - p.periodicOn(e.Start)
		if e.Open {
			on += x - e.Start
		}
	}
	return on
}

// FailureAt is the tick at which a failure clock repaired (or started) at
// up fails after ttf ticks of on-shift time: the least t with Θ(t) >=
// Θ(up) + ttf (D-051 (ii)); a target reached at the end of a shift is that
// end's tick. False when it is after limit (the horizon): the search stops
// there. A ttf of 0 ticks (a sample that quantizes to 0) fails at up, on
// shift or not, as it does at calendar time.
func (p *Pattern) FailureAt(up, ttf, limit int64) (int64, bool) {
	if ttf < 0 {
		return 0, false
	}
	lo := up + ttf // Θ grows by at most one a tick
	if lo > limit {
		return 0, false
	}
	if p.alwaysOn || ttf == 0 {
		return lo, true
	}
	target := p.OnTicks(up) + ttf
	if p.OnTicks(limit) < target {
		return 0, false
	}
	hi := limit
	for lo < hi {
		mid := lo + (hi-lo)/2
		if p.OnTicks(mid) >= target {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, true
}

// Leapfrog is the least tick in [from, limit] that every member function
// admits: each returns the first tick >= its argument that it admits
// (false when none). Every round moves to the latest of their answers,
// so it stops at the first common tick (none is skipped: none is in
// [from, t) at any time).
func Leapfrog(from, limit int64, members ...func(int64) (int64, bool)) (int64, bool) {
	t := from
	for t <= limit {
		moved := false
		for _, next := range members {
			n, ok := next(t)
			if !ok || n > limit {
				return 0, false
			}
			if n > t {
				t, moved = n, true
			}
		}
		if !moved {
			return t, true
		}
	}
	return 0, false
}

// Need is one resource of a bundle: its pattern and the units the bundle
// needs of it.
type Need struct {
	Pattern *Pattern
	Units   int
}

// NextFit is the first tick in [from, limit] at which every resource of
// the bundle has S >= its units (D-050's fit and next_fit; the shifts,
// the units on shift and, with exceptions, the exceptions; not the
// downtime or the units in use).
func NextFit(bundle []Need, from, limit int64, exceptions bool) (int64, bool) {
	members := make([]func(int64) (int64, bool), len(bundle))
	for i, n := range bundle {
		members[i] = func(t int64) (int64, bool) { return n.Pattern.NextAtLeast(t, n.Units, exceptions) }
	}
	return Leapfrog(from, limit, members...)
}

// LCM is the least common multiple of a and b, with false when it is
// above limit.
func LCM(a, b, limit int64) (int64, bool) {
	x, y := a, b
	for y != 0 {
		x, y = y, x%y
	}
	q := a / x
	if q > limit/b {
		return 0, false
	}
	if l := q * b; l <= limit {
		return l, true
	}
	return 0, false
}
