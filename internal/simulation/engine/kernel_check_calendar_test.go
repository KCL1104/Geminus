package engine

// The observation-only model of calendars and release schedules (ADR-026
// D-050 B2, D-051 (ii), D-052, D-053; T-049), part of bchecker
// (kernel_check_blocking_test.go). From the IR alone, never with
// internal/model/calendar (which the validator and the kernel share), it
// rebuilds each resource's units on shift S(t) as a step function up to
// the horizon, and checks against the observations:
//   - a grant only when in_use + units <= min(S, C - D) (fitsWith), and no
//     tick ends with a grantable request: a shift start wakes what it lets
//     in, without a release;
//   - every change of S up to the last tick is one Capacity observation at
//     that tick with the new S; the resources of one calendar change in one
//     event, in resource order; there is no other Capacity observation;
//   - a constant TTF fails at the least t with Θ(t) >= Θ(up) + TTF, Θ the
//     on-shift ticks (D-051 (ii); a target reached at a shift's end is
//     that end's tick);
//   - a release table's entities arrive at k·period + at, quantity a row,
//     numbered consecutively, until max_entities, with none missing; a rate
//     schedule's arrivals are strictly increasing, and a deterministic
//     one's are within a tick of the exact Λ⁻¹(n) (math/big), none missing;
//   - D-050 B2: a request in B2's state (held at the front, none of its
//     bundle in use, not fitting) since the end of tick s is due by
//     next_fit(max(s, X_end) + TTRmax), computed here by scanning S; no
//     tick ends past a deadline without ErrStalled, and an ErrStalled
//     names a request in the state with that since and that deadline.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

// stepFn is S(t) of one resource on a calendar from t=0 to past the
// horizon: S = units[i] on [at[i], at[i+1]), the last segment unbounded.
type stepFn struct {
	at    []int64
	units []int
	xEnd  int64 // the end of the calendar's last exception, 0 without one
}

func (s *stepFn) seg(t int64) int {
	i, found := slices.BinarySearch(s.at, t)
	if !found {
		i--
	}
	return i
}

func (s *stepFn) value(t int64) int { return s.units[s.seg(t)] }

// next is the first segment start after t, MaxInt64 without one.
func (s *stepFn) next(t int64) int64 {
	if i := s.seg(t); i+1 < len(s.at) {
		return s.at[i+1]
	}
	return math.MaxInt64
}

// theta is Θ(t): the ticks of [0, t) at which S > 0.
func (s *stepFn) theta(t int64) int64 {
	on := int64(0)
	for i, a := range s.at {
		if a >= t {
			break
		}
		end := t
		if i+1 < len(s.at) {
			end = min(end, s.at[i+1])
		}
		if s.units[i] > 0 {
			on += end - a
		}
	}
	return on
}

// due is the least t >= up with Θ(t) >= Θ(up) + ttf, or -1 past limit.
func (s *stepFn) due(up, ttf, limit int64) int64 {
	if ttf == 0 {
		return up
	}
	rem, t := ttf, up
	for i := s.seg(up); i < len(s.at); i++ {
		end := int64(math.MaxInt64)
		if i+1 < len(s.at) {
			end = s.at[i+1]
		}
		if s.units[i] > 0 {
			if rem <= end-t {
				if t+rem > limit {
					return -1
				}
				return t + rem
			}
			rem -= end - t
		}
		if end > limit {
			return -1
		}
		t = end
	}
	return -1
}

// calendarStep builds S(t) of resource r of capacity C from its calendar
// in ir, directly from the definition: in an exception C (open) or 0
// (closed); otherwise the units of the shift holding t mod period
// (shift_capacity of its label, C when unlabelled or unlisted), 0 when
// none does.
func calendarStep(ir *model.ModelIR, r model.Resource, horizon int64) *stepFn {
	var cal *model.Calendar
	for i := range ir.Calendars {
		if ir.Calendars[i].ID == r.CalendarID {
			cal = &ir.Calendars[i]
		}
	}
	if cal == nil {
		return nil
	}
	unit := model.TimeUnit(ir.TimeUnit)
	ticks := func(v float64) int64 {
		t, err := model.Ticks(v, unit)
		if err != nil {
			panic(err)
		}
		return t
	}
	period := ticks(cal.Period)
	type span struct {
		a, b  int64
		units int
	}
	var shifts, exc []span
	for _, s := range cal.Shifts {
		units := r.Capacity
		if n, ok := r.ShiftCapacity[s.Label]; ok && s.Label != "" {
			units = n
		}
		shifts = append(shifts, span{ticks(s.Start), ticks(s.End), units})
	}
	f := &stepFn{}
	for _, e := range cal.Exceptions {
		units := 0
		if e.Open {
			units = r.Capacity
		}
		exc = append(exc, span{ticks(e.Start), ticks(e.End), units})
		f.xEnd = max(f.xEnd, ticks(e.End))
	}
	at := func(t int64) int {
		for _, e := range exc {
			if e.a <= t && t < e.b {
				return e.units
			}
		}
		off := t % period
		for _, s := range shifts {
			if s.a <= off && off < s.b {
				return s.units
			}
		}
		return 0
	}
	points := []int64{0}
	for k := int64(0); k*period <= horizon+1; k++ {
		for _, s := range shifts {
			points = append(points, k*period+s.a, k*period+s.b)
		}
		points = append(points, (k+1)*period)
	}
	for _, e := range exc {
		points = append(points, e.a, e.b)
	}
	slices.Sort(points)
	for _, p := range slices.Compact(points) {
		if p > horizon+1 && len(f.at) > 0 {
			break
		}
		if v := at(p); len(f.units) == 0 || f.units[len(f.units)-1] != v {
			f.at, f.units = append(f.at, p), append(f.units, v)
		}
	}
	return f
}

// exactRate is a deterministic release schedule in exact arithmetic.
type exactRate struct {
	period, total           *big.Rat
	starts, rates, cumStart []*big.Rat
	cumEnd                  []*big.Rat
	ms                      int64
}

func newExactRate(s *model.ReleaseSchedule, ms int64) *exactRate {
	rat := func(v float64) *big.Rat { return new(big.Rat).SetFloat64(v) }
	e := &exactRate{period: rat(s.Period), total: new(big.Rat), ms: ms}
	for j, seg := range s.Segments {
		end := rat(s.Period)
		if j+1 < len(s.Segments) {
			end = rat(s.Segments[j+1].Start)
		}
		length := new(big.Rat).Sub(end, rat(seg.Start))
		e.starts, e.rates, e.cumStart = append(e.starts, rat(seg.Start)), append(e.rates, rat(seg.Rate)), append(e.cumStart, new(big.Rat).Set(e.total))
		e.total.Add(e.total, new(big.Rat).Mul(rat(seg.Rate), length))
		e.cumEnd = append(e.cumEnd, new(big.Rat).Set(e.total))
	}
	return e
}

// tick is the exact tick of Λ⁻¹(n): τ = k·period + start_j + (r -
// cumStart_j)/rate_j with k = ceil(n/P) - 1, r = n - kP in (0, P], j the
// first segment with a positive rate and cumEnd_j >= r; τ·ms rounded half
// up, a positive τ at least one tick.
func (e *exactRate) tick(n int64) int64 {
	a := big.NewRat(n, 1)
	q := new(big.Rat).Quo(a, e.total)
	k := new(big.Int).Quo(q.Num(), q.Denom()) // floor (q > 0)
	if new(big.Rat).SetInt(k).Cmp(q) == 0 {
		k.Sub(k, big.NewInt(1)) // ceil(q) - 1 = q - 1 for an integer q
	}
	kr := new(big.Rat).SetInt(k)
	r := new(big.Rat).Sub(a, new(big.Rat).Mul(kr, e.total))
	j := 0
	for j = range e.rates {
		if e.rates[j].Sign() > 0 && e.cumEnd[j].Cmp(r) >= 0 {
			break
		}
	}
	tau := new(big.Rat).Mul(kr, e.period)
	tau.Add(tau, e.starts[j])
	tau.Add(tau, new(big.Rat).Quo(new(big.Rat).Sub(r, e.cumStart[j]), e.rates[j]))
	x := new(big.Rat).Mul(tau, big.NewRat(e.ms, 1))
	x.Add(x, big.NewRat(1, 2))
	t := new(big.Int).Quo(x.Num(), x.Denom()).Int64()
	if t == 0 && tau.Sign() > 0 {
		t = 1
	}
	return t
}

type calCheck struct {
	horizon int64
	steps   []*stepFn // per resource, nil without a calendar
	calOf   []string  // per resource, its calendar ID
	capS    []int
	changes [][]int64 // per resource, the ticks of its Capacity observations

	table   [][]int64    // per node: a release table's arrival ticks, in order
	rate    []bool       // per node: a rate schedule
	exact   []*exactRate // per node: a deterministic schedule
	last    []int64      // per node: its last arrival tick, -1 before
	arrived []int64      // per node
	max     []int        // per node: max_entities

	b2     bool // the program runs D-050 B2 (breakdowns or calendars)
	since  map[uint64]int64
	b2Tick int64
	stats  struct{ changes, wakes, timed, tableArrivals, rateArrivals, exactArrivals, inState int }
}

func newCalCheck(p *Program, ir *model.ModelIR) *calCheck {
	n := len(p.resources)
	c := &calCheck{horizon: p.horizon, steps: make([]*stepFn, n), calOf: make([]string, n), capS: make([]int, n), changes: make([][]int64, n),
		table: make([][]int64, len(p.nodes)), rate: make([]bool, len(p.nodes)), exact: make([]*exactRate, len(p.nodes)),
		last: make([]int64, len(p.nodes)), arrived: make([]int64, len(p.nodes)), max: make([]int, len(p.nodes)),
		b2: p.breakdowns || p.calendared, since: map[uint64]int64{}, b2Tick: -1}
	for i, r := range p.resources {
		c.capS[i] = r.capacity
		for _, x := range ir.Resources {
			if x.ID == r.id && x.CalendarID != "" {
				c.steps[i], c.calOf[i] = calendarStep(ir, x, p.horizon), x.CalendarID
				c.capS[i] = c.steps[i].value(0)
			}
		}
	}
	ms, _ := p.unit.Millis()
	for i, nd := range p.nodes {
		c.last[i] = -1
		for _, x := range ir.Nodes {
			if x.ID != nd.id || x.Type != model.NodeSource || x.Arrival.Schedule == nil {
				continue
			}
			s := x.Arrival.Schedule
			c.max[i] = x.Arrival.MaxEntities
			if s.Process != model.ReleaseTable {
				c.rate[i] = true
				if s.Process == model.ReleaseDeterministic {
					c.exact[i] = newExactRate(s, ms)
				}
				continue
			}
			period, _ := model.Ticks(s.Period, model.TimeUnit(ir.TimeUnit))
			list := []int64{}
		rows:
			for k := int64(0); ; k++ {
				for _, row := range s.Releases {
					at, _ := model.Ticks(row.At, model.TimeUnit(ir.TimeUnit))
					if k*period+at > p.horizon {
						break rows
					}
					for range row.Quantity {
						if c.max[i] > 0 && len(list) >= c.max[i] {
							break rows
						}
						list = append(list, k*period+at)
					}
				}
			}
			c.table[i] = list
		}
	}
	return c
}

// onShift is S of resource r now (C without a calendar).
func (c *bchecker) onShift(r int) int { return c.cal.capS[r] }

// dueAfter is the failure tick of a clock up (repaired or started) at up
// with the constant TTF ttf, -1 when not constant (or, on a calendar,
// after the horizon).
func (c *bchecker) dueAfter(r int, up, ttf int64) int64 {
	if ttf < 0 {
		return -1
	}
	if s := c.cal.steps[r]; s != nil {
		c.cal.stats.timed++
		return s.due(up, ttf, c.cal.horizon)
	}
	return up + ttf
}

// arrival checks an arrival at a source with a release schedule.
func (c *bchecker) arrival(o Observation) {
	k := c.cal
	n := o.Node
	k.arrived[n]++
	if int64(o.Value) != k.arrived[n] {
		c.fail("tick %d: arrival %d at %s, the %dth", o.Tick, o.Value, c.p.nodes[n].id, k.arrived[n])
	}
	switch {
	case k.table[n] != nil:
		if i := int(o.Value) - 1; i >= len(k.table[n]) || k.table[n][i] != o.Tick {
			c.fail("tick %d: release-table arrival %d of %s, expected at %v", o.Tick, o.Value, c.p.nodes[n].id, k.table[n][min(i, len(k.table[n])-1):])
		}
		k.stats.tableArrivals++
	case k.rate[n]:
		if o.Tick <= k.last[n] {
			c.fail("tick %d: rate-schedule arrival %d of %s not after the last, %d", o.Tick, o.Value, c.p.nodes[n].id, k.last[n])
		}
		if e := k.exact[n]; e != nil {
			want := max(e.tick(o.Value), k.last[n]+1)
			if d := o.Tick - want; d < -1 || d > 1 {
				c.fail("tick %d: deterministic arrival %d of %s, exactly at %d", o.Tick, o.Value, c.p.nodes[n].id, want)
			}
			k.stats.exactArrivals++
		}
		k.stats.rateArrivals++
	}
	k.last[n] = o.Tick
}

// capacityChange checks a CAPACITY_CHANGE event's observations: Capacity
// of resources of one calendar, in resource order, each with the S its
// calendar has now and different from the last; every resource of that
// calendar whose S changes now is among them.
func (c *bchecker) capacityChange(g []Observation) {
	k := c.cal
	cal, prev := k.calOf[g[0].Resource], -1
	seen := map[int]bool{}
	for _, o := range g {
		r := o.Resource
		if o.Kind != ObsCapacity || r <= prev || k.calOf[r] != cal || k.steps[r] == nil {
			c.fail("tick %d: %+v in the CAPACITY_CHANGE of calendar %q", o.Tick, o, cal)
			return
		}
		prev = r
		seen[r] = true
		if want := k.steps[r].value(o.Tick); int64(want) != o.Value || int(o.Value) == k.capS[r] {
			c.fail("tick %d: %s on shift %d (was %d), the calendar says %d", o.Tick, c.p.resources[r].id, o.Value, k.capS[r], want)
		}
		if int(o.Value) > k.capS[r] {
			k.stats.wakes++
		}
		k.capS[r] = int(o.Value)
		k.changes[r] = append(k.changes[r], o.Tick)
		k.stats.changes++
	}
	for r := range k.steps {
		if k.calOf[r] == cal && !seen[r] && k.steps[r].value(g[0].Tick) != k.capS[r] {
			c.fail("tick %d: %s changes to %d on shift without a Capacity observation", g[0].Tick, c.p.resources[r].id, k.steps[r].value(g[0].Tick))
		}
	}
}

// b2Update records, once a tick, which waiting requests are in B2's state
// at the end of the last tick, and since when.
func (c *bchecker) b2Update() {
	k := c.cal
	if !k.b2 || k.b2Tick == c.lastTick {
		return
	}
	k.b2Tick = c.lastTick
	in := map[uint64]bool{}
	for _, w := range c.waiting {
		if c.stalled(w.entity) {
			in[w.entity] = true
			if _, ok := k.since[w.entity]; !ok {
				k.since[w.entity] = c.lastTick
				k.stats.inState++
			}
		}
	}
	for e := range k.since {
		if !in[e] {
			delete(k.since, e)
		}
	}
}

// b2Deadline is next_fit(max(since, X_end) + TTRmax) of process's bundle,
// by scanning S: the least tick >= that at which every resource of the
// bundle has the units it needs on shift; -1 for none before the horizon,
// -2 when a TTR is not constant (TTRmax unknown here).
func (c *bchecker) b2Deadline(process int, since int64) int64 {
	k := c.cal
	reqs := c.p.nodes[process].requires
	ttrMax, xEnd := int64(0), int64(0)
	for _, q := range reqs {
		if c.isDown[q.resource] != nil {
			if c.ttr[q.resource] < 0 {
				return -2
			}
			ttrMax = max(ttrMax, c.ttr[q.resource])
		}
		if s := k.steps[q.resource]; s != nil {
			xEnd = max(xEnd, s.xEnd)
		}
	}
	for t := max(since, xEnd) + ttrMax; t <= k.horizon; {
		ok, next := true, int64(math.MaxInt64)
		for _, q := range reqs {
			s := k.steps[q.resource]
			if s == nil {
				continue // S = C >= units
			}
			if s.value(t) < q.units {
				ok = false
			}
			next = min(next, s.next(t))
		}
		if ok {
			return t
		}
		t = next
	}
	return -1
}

// calendarBoundary runs at the end of every tick: S as the calendar says,
// and no request past its B2 deadline.
func (c *bchecker) calendarBoundary() {
	k := c.cal
	for r, s := range k.steps {
		if s != nil && s.value(c.lastTick) != k.capS[r] {
			c.fail("tick %d ended with %s at %d on shift, the calendar says %d", c.lastTick, c.p.resources[r].id, k.capS[r], s.value(c.lastTick))
		}
	}
	c.b2Update()
	for _, w := range c.waiting {
		since, ok := k.since[w.entity]
		if !ok {
			continue
		}
		if d := c.b2Deadline(w.process, since); d >= 0 && c.lastTick >= d {
			c.fail("tick %d ended with %s in B2's state since %d past its deadline %d without ErrStalled", c.lastTick, c.ids[w.entity], since, d)
		}
	}
}

// calendarFinish checks, at the end of a run, that no change of S and no
// scheduled arrival before the last tick was missed.
func (c *bchecker) calendarFinish() {
	k := c.cal
	for r, s := range k.steps {
		if s == nil {
			continue
		}
		var want []int64
		for i := 1; i < len(s.at) && s.at[i] < c.lastTick; i++ {
			want = append(want, s.at[i])
		}
		var got []int64
		for _, t := range k.changes[r] {
			if t < c.lastTick {
				got = append(got, t)
			}
		}
		if !slices.Equal(got, want) {
			c.fail("%s: units on shift changed at %v, the calendar at %v (before tick %d)", c.p.resources[r].id, got, want, c.lastTick)
		}
	}
	for n, list := range k.table {
		missing := 0
		for _, t := range list {
			if t < c.lastTick {
				missing++
			}
		}
		if int64(missing) > k.arrived[n] {
			c.fail("%s: %d table arrivals before tick %d, %d expected", c.p.nodes[n].id, k.arrived[n], c.lastTick, missing)
		}
	}
	for n, e := range k.exact {
		if e != nil && (k.max[n] == 0 || k.arrived[n] < int64(k.max[n])) {
			if next := e.tick(k.arrived[n] + 1); next+1 < c.lastTick {
				c.fail("%s: deterministic arrival %d due at %d, not observed by tick %d", c.p.nodes[n].id, k.arrived[n]+1, next, c.lastTick)
			}
		}
	}
}

// stallProblem checks an ErrStalled against the reconstruction (after the
// last group was finished): the request is in B2's state at the end of
// the tick, since the tick it says, its deadline is the one computed
// here (unless TTRmax is unknown) and passed, and it is the first such
// request in queue order.
func (c *bchecker) stallProblem(se *StalledError, e uint64) string {
	c.b2Update()
	if se.Tick != c.lastTick || !c.stalled(e) {
		return fmt.Sprintf("not in B2's state at the end of tick %d (last %d)", se.Tick, c.lastTick)
	}
	since, ok := c.cal.since[e]
	if !ok || since != se.Since {
		return fmt.Sprintf("since %d, reconstructed %d (%v)", se.Since, since, ok)
	}
	for _, w := range c.waiting {
		if w.entity == e {
			if d := c.b2Deadline(w.process, since); d != -2 && (d != se.Deadline || se.Tick < d) {
				return fmt.Sprintf("deadline %d, reconstructed %d", se.Deadline, d)
			}
			break
		}
		if s, ok := c.cal.since[w.entity]; ok {
			if d := c.b2Deadline(w.process, s); d >= 0 && se.Tick >= d {
				return fmt.Sprintf("an earlier request, %s, was due at %d", c.ids[w.entity], d)
			}
		}
	}
	return ""
}

// withCalendars puts some resources of ir on one to three random
// calendars: periods of common lengths or any quarter minute, one to
// three shifts, labels some of which have fewer units on shift, and open
// or closed exceptions within the horizon (minutes). Some models are
// invalid (R2, R4); the caller lets the validator decide.
func withCalendars(ir model.ModelIR, rng *rand.Rand, horizon float64) (model.ModelIR, int) {
	ir.Resources = slices.Clone(ir.Resources)
	q := func(n int) float64 { return 0.25 * float64(n) }
	cals := make([]model.Calendar, 1+rng.IntN(3))
	for k := range cals {
		period := []float64{5, 7.5, 10, 15, 20, 30, 45, 60, 90, 120, 240}[rng.IntN(11)]
		if rng.IntN(5) == 0 {
			period = q(8 + rng.IntN(1000))
		}
		c := model.Calendar{ID: fmt.Sprintf("cal%d", k), Period: period}
		at := 0.0
		for s := range 1 + rng.IntN(3) {
			room := int((period - at) * 4)
			if room < 2 {
				break
			}
			start := at + q(rng.IntN(room/2))
			end := start + q(1+rng.IntN(int((period-start)*4)))
			sh := model.Shift{Start: start, End: min(end, period)}
			if rng.IntN(3) > 0 {
				sh.Label = fmt.Sprintf("s%d", s)
			}
			c.Shifts = append(c.Shifts, sh)
			at = sh.End
		}
		at = 0
		for range rng.IntN(3) {
			start := at + q(rng.IntN(int(horizon*2)+1))
			end := start + q(1+rng.IntN(int(period*8)))
			c.Exceptions = append(c.Exceptions, model.CalendarException{Start: start, End: end, Open: rng.IntN(3) == 0})
			at = end
		}
		cals[k] = c
	}
	used := map[string]bool{}
	n := 0
	for i := range ir.Resources {
		if rng.IntN(3) == 0 {
			continue
		}
		r := &ir.Resources[i]
		c := cals[rng.IntN(len(cals))]
		r.CalendarID = c.ID
		used[c.ID] = true
		for _, s := range c.Shifts {
			if s.Label != "" && rng.IntN(2) == 0 {
				if r.ShiftCapacity == nil {
					r.ShiftCapacity = map[string]int{}
				}
				r.ShiftCapacity[s.Label] = rng.IntN(r.Capacity + 1)
			}
		}
		n++
	}
	ir.Calendars = nil
	for _, c := range cals {
		if used[c.ID] {
			ir.Calendars = append(ir.Calendars, c)
		}
	}
	return ir, n
}

// withSchedules gives some sources of ir a release schedule: poisson or
// deterministic with one to three segments (one with a positive rate), or
// a table of one to three rows.
func withSchedules(ir model.ModelIR, rng *rand.Rand) (model.ModelIR, int) {
	ir.Nodes = slices.Clone(ir.Nodes)
	n := 0
	for i := range ir.Nodes {
		if ir.Nodes[i].Type != model.NodeSource || rng.IntN(3) == 0 {
			continue
		}
		a := *ir.Nodes[i].Arrival
		a.Distribution = model.Distribution{}
		period := []float64{10, 15, 30, 60, 90, 120}[rng.IntN(6)]
		s := &model.ReleaseSchedule{Period: period}
		switch rng.IntN(3) {
		case 0, 1:
			s.Process = model.ReleasePoisson
			if rng.IntN(2) == 0 {
				s.Process = model.ReleaseDeterministic
			}
			at := 0.0
			for j := range 1 + rng.IntN(3) {
				rate := []float64{0, 0.1, 0.25, 0.3, 0.5, 1, 2}[rng.IntN(7)]
				if j == 0 {
					rate = []float64{0.1, 0.2, 0.3, 0.5, 0.75, 1}[rng.IntN(6)]
				}
				s.Segments = append(s.Segments, model.RateSegment{Start: at, Rate: rate})
				at += 0.25 * float64(1+rng.IntN(int((period-at)*2)))
				if at >= period {
					break
				}
			}
		default:
			s.Process = model.ReleaseTable
			at := 0.25 * float64(rng.IntN(int(period)))
			for range 1 + rng.IntN(3) {
				if at >= period {
					break
				}
				s.Releases = append(s.Releases, model.Release{At: at, Quantity: 1 + rng.IntN(5)})
				at += 0.25 * float64(1+rng.IntN(int(period)))
			}
		}
		a.Schedule = s
		ir.Nodes[i].Arrival = &a
		n++
	}
	return ir, n
}

// randomCalendarModel is a random model with calendars (seed%2 == 0 or
// seed%3 == 0) and release schedules (seed%2 == 1 or seed%3 == 0), finite
// buffers (seed%5 < 2) and breakdowns with constant clocks (seed%4 >= 2;
// R3 holds), and the horizon in minutes.
func randomCalendarModel(base model.ModelIR, seed int, stream uint64) (model.ModelIR, float64, bool) {
	rng := rand.New(rand.NewPCG(uint64(seed), stream))
	ir := randomModelFrom(base, rng)
	horizon := float64(120 + rng.IntN(600))
	if seed%5 < 2 {
		var ok bool
		if ir, ok = withFiniteBuffers(ir, rng); !ok {
			return ir, 0, false
		}
	}
	if seed%4 >= 2 {
		ir, _ = withBreakdowns(ir, rng, true, true)
	}
	k := 0
	if seed%2 == 0 || seed%3 == 0 {
		var n int
		ir, n = withCalendars(ir, rng, horizon)
		k += n
	}
	if seed%2 == 1 || seed%3 == 0 {
		var n int
		ir, n = withSchedules(ir, rng)
		k += n
	}
	return ir, horizon, k > 0
}

// The kernel agrees with the observation-only model on random valid
// models with calendars and release schedules (with finite buffers and
// breakdowns on some): no valid model stalls or deadlocks (D-050 R2–R4),
// and the same replication runs identically in parallel.
func TestRandomCalendarModelsAgainstSpec(t *testing.T) {
	const n = 3000
	base := minimal(t)
	compiled, calendared, scheduled, finite, broken := 0, 0, 0, 0, 0
	var total calCheck
	for seed := range n {
		ir, horizon, ok := randomCalendarModel(base, seed, 4901)
		if !ok {
			continue
		}
		in := input(ir)
		in.Config = config(horizon, model.Minute)
		in.Config.Replications = 3
		p, err := Compile(in)
		if err != nil {
			continue // R2, R4 or another rule
		}
		compiled++
		if p.HasCalendars() {
			calendared++
		}
		if slices.ContainsFunc(p.nodes, func(n cnode) bool { return n.release != nil }) {
			scheduled++
		}
		if p.HasFiniteBuffers() {
			finite++
		}
		if p.HasBreakdowns() && p.HasCalendars() {
			broken++
		}
		c := newBChecker(p, &ir, uint32(seed%3))
		_, err = RunReplication(context.Background(), p, uint32(seed%3), NewBudget(400_000), c)
		if errors.Is(err, ErrDeadlock) || errors.Is(err, ErrStalled) {
			t.Fatalf("seed %d: a valid model failed: %v", seed, err)
		}
		if err != nil && !errors.Is(err, ErrEventBudget) && !errors.Is(err, ErrEntityVisits) && !errors.Is(err, ErrActiveEntities) {
			t.Fatalf("seed %d: %v", seed, err)
		}
		c.finish()
		if len(c.errs) > 0 {
			t.Fatalf("seed %d: %d problems, first: %v", seed, len(c.errs), c.errs)
		}
		s := c.cal.stats
		total.stats.changes += s.changes
		total.stats.wakes += s.wakes
		total.stats.timed += s.timed
		total.stats.tableArrivals += s.tableArrivals
		total.stats.rateArrivals += s.rateArrivals
		total.stats.exactArrivals += s.exactArrivals
		total.stats.inState += s.inState
		if seed%7 == 0 {
			res := make([][]Observation, 3)
			done := make(chan int, 3)
			for i := range res {
				go func() {
					col := &collect{}
					RunReplication(context.Background(), p, uint32(seed%3), NewBudget(400_000), col)
					res[i] = col.all
					done <- i
				}()
			}
			for range res {
				<-done
			}
			for i := 1; i < len(res); i++ {
				if !slices.Equal(res[0], res[i]) {
					t.Fatalf("seed %d: concurrent run %d differs", seed, i)
				}
			}
		}
	}
	t.Logf("%d of %d random models compiled: %d with calendars (%d with breakdowns too), %d with release schedules, %d with finite buffers; %+v",
		compiled, n, calendared, broken, scheduled, finite, total.stats)
	if compiled < n/5 || calendared < n/10 || scheduled < n/10 || finite < n/20 || broken < n/50 || total.stats.changes < 10_000 || total.stats.wakes < 1000 ||
		total.stats.timed < 1000 || total.stats.tableArrivals < 10_000 || total.stats.exactArrivals < 10_000 || total.stats.inState < 1000 {
		t.Fatalf("generator too weak: %d compiled, %d calendared, %d scheduled, %d finite, %d with breakdowns, %+v", compiled, calendared, scheduled, finite, broken, total.stats)
	}
}

// With the validator bypassed (test-only: R2 and R4 refuse such models),
// bundles may never be filled: every ErrStalled names a request in D-050
// B2's state with the since and next_fit deadline reconstructed here, and
// no run goes past a deadline without one.
func TestRandomCalendarStallsAreInTheState(t *testing.T) {
	const n = 3000
	base := minimal(t)
	stalls, runs := 0, 0
	for seed := range n {
		rng := rand.New(rand.NewPCG(uint64(seed), 4902))
		ir := randomModelFrom(base, rng)
		if _, err := Compile(input(ir)); err != nil {
			continue
		}
		horizon := float64(240 + rng.IntN(1200))
		if seed%2 == 0 {
			ir, _ = withBreakdowns(ir, rng, seed%4 == 0, true)
		}
		ir, k := withCalendars(ir, rng, horizon)
		if k == 0 {
			continue
		}
		in := input(ir)
		in.Config = config(horizon, model.Minute)
		in.Config.Replications = 2
		var p *Program
		var err error
		withoutValidator(t, func() { p, err = Compile(in) })
		if err != nil {
			continue // a shape the calendar package refuses (the validator's CALENDAR_INVALID)
		}
		runs++
		c := newBChecker(p, &ir, uint32(seed%2))
		_, err = RunReplication(context.Background(), p, uint32(seed%2), NewBudget(400_000), c)
		var se *StalledError
		switch {
		case errors.As(err, &se):
			stalls++
			if len(c.group) > 0 {
				c.finishGroup()
			}
			if problem := c.stallProblem(se, findEntity(t, c, se.Entity)); problem != "" {
				t.Fatalf("seed %d: ErrStalled %+v: %s (%v)", seed, *se, problem, c.errs)
			}
		case errors.Is(err, ErrDeadlock):
			// R1 holds (no finite buffer here): a circular wait of units
			// only blocked entities hold cannot happen.
			t.Fatalf("seed %d: %v", seed, err)
		case err == nil || errors.Is(err, ErrEventBudget) || errors.Is(err, ErrEntityVisits) || errors.Is(err, ErrActiveEntities):
			c.finish()
		default:
			t.Fatalf("seed %d: %v", seed, err)
		}
		if len(c.errs) > 0 {
			t.Fatalf("seed %d: %d problems, first: %v", seed, len(c.errs), c.errs)
		}
	}
	t.Logf("%d runs with the validator bypassed, %d ended in ErrStalled", runs, stalls)
	if stalls < 50 || runs-stalls < 100 {
		t.Fatalf("too weak: %d runs, %d stalls", runs, stalls)
	}
}

// FuzzCalendars (T-049): a random model (the generator's seed) with
// calendars and release schedules, validated in full (no run may stall or
// deadlock) or, with bypass, compiled past the validator (stalls must be
// in B2's state); every run must agree with the observation-only model.
func FuzzCalendars(f *testing.F) {
	for _, s := range []struct {
		seed   uint64
		bypass bool
	}{{0, false}, {3, false}, {6, false}, {10, true}, {12, true}, {4242, false}} {
		f.Add(s.seed, s.bypass)
	}
	base := minimal(f)
	f.Fuzz(func(t *testing.T, seed uint64, bypass bool) {
		ir, horizon, ok := randomCalendarModel(base, int(seed%(1<<31)), 4903)
		if !ok {
			return
		}
		in := input(ir)
		in.Config = config(horizon, model.Minute)
		in.Config.Replications = 2
		var p *Program
		var err error
		if bypass {
			withoutValidator(t, func() { p, err = Compile(in) })
		} else {
			p, err = Compile(in)
		}
		if err != nil {
			return
		}
		c := newBChecker(p, &ir, uint32(seed%2))
		_, err = RunReplication(context.Background(), p, uint32(seed%2), NewBudget(100_000), c)
		var se *StalledError
		switch {
		case errors.As(err, &se):
			if !bypass {
				t.Fatalf("a valid model stalled: %v", err)
			}
			if len(c.group) > 0 {
				c.finishGroup()
			}
			if problem := c.stallProblem(se, findEntity(t, c, se.Entity)); problem != "" {
				t.Fatalf("ErrStalled %+v: %s (%v)", *se, problem, c.errs)
			}
		case errors.Is(err, ErrDeadlock):
			if !bypass {
				t.Fatalf("a valid model deadlocked: %v", err)
			}
			if len(c.group) > 0 {
				c.finishGroup()
			}
			if !c.deadlocked() {
				t.Fatalf("ErrDeadlock without a circular wait: %v", err)
			}
		case err == nil || errors.Is(err, ErrEventBudget) || errors.Is(err, ErrEntityVisits) || errors.Is(err, ErrActiveEntities):
			c.finish()
		default:
			t.Fatal(err)
		}
		if len(c.errs) > 0 {
			t.Fatalf("%d problems, first: %v", len(c.errs), c.errs)
		}
	})
}
