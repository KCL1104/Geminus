package engine

// The observation-only model of changeovers (ADR-026 D-055, D-051, D-049;
// T-050 interpretations D-097, PROPOSED), part of bchecker
// (kernel_check_blocking_test.go). From the IR alone it keeps, for each
// resource with setups, every unit's state (in use, down, the entity type
// it is set up for) and the unit each entity holds, and checks against the
// observations:
//   - a grant takes, of the units neither in use nor down, the one of least
//     index set up for the entity's type, else the one of least index;
//   - a changeover (an ObsSetup right after the grant's levels and before
//     its ServiceStart, on that unit) happens exactly when the unit is set
//     up for another type, with the transition's time, else the default's,
//     else 0 (constant ones exactly);
//   - a failure that goes down at once takes the idle unit of least index,
//     a waiting one the unit released (or repaired) that takes it over, a
//     repair frees its clock's unit, and a downtime keeps the setup state;
//   - a blocked entity keeps its unit until it moves on;
//   - for every process with a constant service time, the service ends
//     exactly changeover + service after the grant (one SERVICE_COMPLETE).
// It never reads the kernel's unit state.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

type sunit struct {
	busy, down bool
	state      int
}

type suCheck struct {
	product    []int // per node: a source's entity type index
	setupRes   []int // per node: the resource with setups a process needs, -1
	service    []int64
	units      [][]sunit // per resource with setups
	trans      []map[[2]int]int64
	def        []int64 // -1 not constant, -2 none
	clockUnit  [][]int
	entProduct map[uint64]int
	unitOf     map[uint64]int
	freed      []int            // per resource: the unit the release being read frees
	grantEnd   map[uint64]int64 // entity in service -> grant tick + changeover ticks
	stats      struct{ grants, setups, zero, matched, fresh, downsAtOnce, downsOnFreed, downsOnRepaired, ends int }
}

func newSuCheck(p *Program, ir *model.ModelIR) *suCheck {
	n, r := len(p.nodes), len(p.resources)
	s := &suCheck{product: make([]int, n), setupRes: make([]int, n), service: make([]int64, n), units: make([][]sunit, r),
		trans: make([]map[[2]int]int64, r), def: make([]int64, r), clockUnit: make([][]int, r), entProduct: map[uint64]int{},
		unitOf: map[uint64]int{}, freed: make([]int, r), grantEnd: map[uint64]int64{}}
	unit := model.TimeUnit(ir.TimeUnit)
	ticks := func(d model.Distribution) int64 {
		if d.Type != model.DistConstant {
			return -1
		}
		t, err := model.Ticks(*d.Value, unit)
		if err != nil {
			return -1
		}
		return t
	}
	types := map[string]int{}
	ids := []string{}
	for _, e := range ir.EntityTypes {
		ids = append(ids, e.ID)
	}
	slices.Sort(ids)
	for i, id := range ids {
		types[id] = i
	}
	for i, nd := range p.nodes {
		s.product[i], s.setupRes[i], s.service[i] = -1, -1, -1
		for _, x := range ir.Nodes {
			if x.ID != nd.id {
				continue
			}
			if x.Type == model.NodeSource {
				s.product[i] = types[x.EntityType]
			}
			if x.Type == model.NodeProcess && x.Batch == nil {
				s.service[i] = ticks(x.ServiceTime.Distribution)
			}
		}
	}
	any := false
	for i, res := range p.resources {
		s.freed[i] = -1
		for _, x := range ir.Resources {
			if x.ID != res.id || x.Setups == nil {
				continue
			}
			any = true
			initial := -1
			if x.Setups.Initial != "" {
				initial = types[x.Setups.Initial]
			}
			s.units[i] = make([]sunit, res.capacity)
			for u := range s.units[i] {
				s.units[i][u].state = initial
			}
			s.def[i] = -2
			if x.Setups.Default != nil {
				s.def[i] = ticks(*x.Setups.Default)
			}
			s.trans[i] = map[[2]int]int64{}
			for _, tr := range x.Setups.Transitions {
				s.trans[i][[2]int{types[tr.From], types[tr.To]}] = ticks(tr.Distribution)
			}
			if x.Breakdowns != nil {
				s.clockUnit[i] = slices.Repeat([]int{-1}, res.capacity)
			}
		}
	}
	for i, nd := range p.nodes {
		for _, q := range nd.requires {
			if s.units[q.resource] != nil {
				s.setupRes[i] = q.resource
			}
		}
	}
	if !any {
		// No changeover to check; the completion check still runs.
		s.units = nil
	}
	return s
}

// suArrival records an arriving entity's type.
func (c *bchecker) suArrival(o Observation) { c.su.entProduct[o.Entity] = c.su.product[o.Node] }

// grantService replays the rest of a non-batch grant after its levels: the
// unit and changeover (D-055), then the ServiceStart (read by the caller);
// it records when the service must end.
func (c *bchecker) grantService(u *cursor, e uint64, p int) {
	s := c.su
	setup := int64(0)
	if r := s.setupRes[p]; s.units != nil && r >= 0 {
		units := s.units[r]
		product := s.entProduct[e]
		pick := slices.IndexFunc(units, func(x sunit) bool { return !x.busy && !x.down && x.state == product })
		if pick >= 0 && pick != slices.IndexFunc(units, func(x sunit) bool { return !x.busy && !x.down }) {
			s.stats.matched++ // set up for it, not the first free unit
		}
		if pick < 0 {
			pick = slices.IndexFunc(units, func(x sunit) bool { return !x.busy && !x.down })
		}
		if pick < 0 {
			c.fail("tick %d: %d granted %s with no free unit", c.lastTick, e, c.p.resources[r].id)
			return
		}
		from := units[pick].state
		units[pick].busy, units[pick].state = true, product
		s.unitOf[e] = pick
		s.stats.grants++
		switch {
		case from < 0:
			s.stats.fresh++
		case from != product:
			want, ok := s.trans[r][[2]int{from, product}]
			if !ok {
				want = s.def[r]
				if want == -2 {
					want = 0
				}
			}
			o, ok := u.next(fmt.Sprintf("Setup of %d at %s on %s unit %d (%d ticks)", e, c.p.nodes[p].id, c.p.resources[r].id, pick, want), func(o Observation) bool {
				return o.Kind == ObsSetup && o.Entity == e && o.Node == p && o.Resource == r && o.At == pick && o.Value >= 0 && (want < 0 || o.Value == want)
			})
			if ok {
				setup = o.Value
				s.stats.setups++
				if o.Value == 0 {
					s.stats.zero++
				}
			}
		}
	}
	if s.service[p] >= 0 {
		s.grantEnd[e] = c.lastTick + setup
	}
}

// serviceEnded checks a non-batch service's end: changeover + service after
// its grant, for a constant service time.
func (c *bchecker) serviceEnded(end Observation) {
	s := c.su
	if g, ok := s.grantEnd[end.Entity]; ok {
		if end.Tick != g+s.service[end.Node] {
			c.fail("tick %d: %d's service at %s ends; granted with its changeover by %d, the service is %d", end.Tick, end.Entity, c.p.nodes[end.Node].id, g, s.service[end.Node])
		}
		delete(s.grantEnd, end.Entity)
		s.stats.ends++
	}
}

// suFree frees the unit entity e holds at process p (its bundle is about to
// be released): the release's waiting failure, if any, takes it.
func (c *bchecker) suFree(e uint64, p int) {
	s := c.su
	r := s.setupRes[p]
	if s.units == nil || r < 0 {
		return
	}
	u, ok := s.unitOf[e]
	if !ok || !s.units[r][u].busy {
		c.fail("tick %d: %d releases no unit of %s", c.lastTick, e, c.p.resources[r].id)
		return
	}
	delete(s.unitOf, e)
	s.units[r][u].busy = false
	s.freed[r] = u
}

// suDown takes a unit of resource r down for clock: which, or the idle
// unit of least index for -1 (D-097 (c)).
func (c *bchecker) suDown(r, clock, which int) {
	s := c.su
	if s.units == nil || s.units[r] == nil {
		return
	}
	switch {
	case which < 0:
		which = slices.IndexFunc(s.units[r], func(x sunit) bool { return !x.busy && !x.down })
		s.stats.downsAtOnce++
	case s.freed[r] == which:
		s.stats.downsOnFreed++
	default:
		s.stats.downsOnRepaired++
	}
	if which < 0 || s.units[r][which].busy || s.units[r][which].down {
		c.fail("tick %d: %s clock %d goes down on unit %d, which is not idle", c.lastTick, c.p.resources[r].id, clock, which)
		return
	}
	s.units[r][which].down = true
	s.clockUnit[r][clock] = which
}

// suUp frees clock's unit at its repair and returns it, -1 off setups.
func (c *bchecker) suUp(r, clock int) int {
	s := c.su
	if s.units == nil || s.units[r] == nil {
		return -1
	}
	u := s.clockUnit[r][clock]
	if u < 0 || !s.units[r][u].down {
		c.fail("tick %d: %s clock %d repaired with no unit down", c.lastTick, c.p.resources[r].id, clock)
		return -1
	}
	s.units[r][u].down, s.clockUnit[r][clock] = false, -1
	return u
}

// withSetups gives some resources of ir changeovers and its sources one of
// two or three entity types: a resource every requirement of which is one
// unit, of a non-batch process needing no other resource with setups
// (D-055's P0 limits); its initial (or none), default (or none) and
// transitions are random, mostly constant in quarter minutes, some 0.
func withSetups(ir model.ModelIR, rng *rand.Rand) (model.ModelIR, int) {
	ir.Nodes = slices.Clone(ir.Nodes)
	ir.Resources = slices.Clone(ir.Resources)
	types := []string{ir.EntityTypes[0].ID, "type_b", "type_c"}[:2+rng.IntN(2)]
	ir.EntityTypes = slices.Clone(ir.EntityTypes)
	for _, t := range types[1:] {
		ir.EntityTypes = append(ir.EntityTypes, model.EntityType{ID: t, Name: t})
	}
	for i := range ir.Nodes {
		if ir.Nodes[i].Type == model.NodeSource {
			ir.Nodes[i].EntityType = types[rng.IntN(len(types))]
		}
	}
	// A second source like the first, of another type, so that the types
	// mix at the stations whatever the model's sources.
	if first := slices.IndexFunc(ir.Nodes, func(n model.Node) bool { return n.Type == model.NodeSource }); first >= 0 {
		twin := ir.Nodes[first]
		twin.ID = twin.ID + "_twin"
		twin.EntityType = types[(slices.Index(types, twin.EntityType)+1+rng.IntN(len(types)-1))%len(types)]
		ir.Nodes = append(ir.Nodes, twin)
		ir.Flows = slices.Clone(ir.Flows)
		for _, f := range ir.Flows {
			if f.From == ir.Nodes[first].ID {
				f.ID, f.From = f.ID+"_twin", twin.ID
				ir.Flows = append(ir.Flows, f)
			}
		}
	}
	dist := func() model.Distribution {
		if rng.IntN(4) == 0 {
			return randDistribution(rng, true)
		}
		v := 0.25 * float64(rng.IntN(24))
		return model.Distribution{Type: model.DistConstant, Value: &v}
	}
	chosen := map[string]bool{}
	n := 0
	for _, r := range rng.Perm(len(ir.Resources)) {
		res := &ir.Resources[r]
		if rng.IntN(4) == 0 {
			continue
		}
		ok := true
		for _, nd := range ir.Nodes {
			needs, other := false, false
			for _, q := range nd.Requires {
				if q.ResourceID == res.ID {
					needs = true
					ok = ok && q.Units == 1 && nd.Batch == nil
				} else if chosen[q.ResourceID] {
					other = true
				}
			}
			ok = ok && !(needs && other)
		}
		if !ok {
			continue
		}
		s := &model.Setups{}
		if rng.IntN(3) == 0 {
			s.Initial = types[rng.IntN(len(types))]
		}
		if rng.IntN(3) > 0 {
			d := dist()
			s.Default = &d
		}
		for _, from := range types {
			for _, to := range types {
				if from != to && rng.IntN(2) == 0 {
					s.Transitions = append(s.Transitions, model.SetupTransition{From: from, To: to, Distribution: dist()})
				}
			}
		}
		res.Setups = s
		chosen[res.ID] = true
		n++
	}
	return ir, n
}

// randomSetupModel is a random model with changeovers (on all but a few
// seeds), finite buffers (seed%3 != 1, when valid), breakdowns with
// constant clocks (seed%4 == 1, half of them) or any clocks, calendars
// (seed%5 == 2) and batch processes on other resources (seed%7 == 3), and
// its horizon in minutes.
func randomSetupModel(base model.ModelIR, seed int, stream uint64) (model.ModelIR, float64, bool) {
	rng := rand.New(rand.NewPCG(uint64(seed), stream))
	ir := randomModelFrom(base, rng)
	horizon := float64(120 + rng.IntN(600))
	if seed%3 != 1 {
		if finite, ok := withFiniteBuffers(ir, rng); ok {
			ir = finite
		}
	}
	if seed%4 == 1 || seed%4 == 2 {
		ir, _ = withBreakdowns(ir, rng, true, seed%4 == 1)
	}
	if seed%5 == 2 {
		ir, _ = withCalendars(ir, rng, horizon)
	}
	if seed%7 == 3 {
		ir, _ = withBatches(ir, rng)
	}
	ir, n := withSetups(ir, rng)
	return ir, horizon, n > 0
}

// The kernel agrees with the observation-only model on random valid
// models with changeovers (some with finite buffers, breakdowns,
// calendars and batch processes): unit choice, changeovers and their
// times, unit identity under breakdowns, completion after changeover and
// service; no valid model deadlocks or stalls (D-050).
func TestRandomSetupModelsAgainstSpec(t *testing.T) {
	const n = 3000
	base := minimal(t)
	compiled, finite, broken, calendared, batched := 0, 0, 0, 0, 0
	var total suCheck
	for seed := range n {
		ir, horizon, ok := randomSetupModel(base, seed, 5011)
		if !ok {
			continue
		}
		in := input(ir)
		in.Config = config(horizon, model.Minute)
		in.Config.Replications = 3
		p, err := Compile(in)
		if err != nil {
			continue // R1, R2, R4 or another rule
		}
		compiled++
		for _, x := range []struct {
			on bool
			n  *int
		}{{p.HasFiniteBuffers(), &finite}, {p.HasBreakdowns(), &broken}, {p.HasCalendars(), &calendared}, {p.HasBatches(), &batched}} {
			if x.on {
				*x.n++
			}
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
		s := c.su.stats
		total.stats.grants += s.grants
		total.stats.setups += s.setups
		total.stats.zero += s.zero
		total.stats.matched += s.matched
		total.stats.fresh += s.fresh
		total.stats.downsAtOnce += s.downsAtOnce
		total.stats.downsOnFreed += s.downsOnFreed
		total.stats.downsOnRepaired += s.downsOnRepaired
		total.stats.ends += s.ends
	}
	ts := total.stats
	t.Logf("%d of %d random models compiled: %d with finite buffers, %d with breakdowns, %d with calendars, %d with batches; %d grants on units that change over, %d changeovers (%d of 0 ticks), %d to a unit set up for the type that was not the first free one, %d first pieces; downs on such resources: %d at once, %d on the released unit, %d on the repaired unit; %d service ends timed",
		compiled, n, finite, broken, calendared, batched, ts.grants, ts.setups, ts.zero, ts.matched, ts.fresh, ts.downsAtOnce, ts.downsOnFreed, ts.downsOnRepaired, ts.ends)
	if compiled < n/5 || finite < n/20 || broken < n/20 || calendared < n/50 || batched < n/50 || ts.setups < 20_000 || ts.zero < 200 ||
		ts.matched < 1000 || ts.downsAtOnce < 1000 || ts.downsOnFreed < 200 || ts.downsOnRepaired < 50 || ts.ends < 50_000 {
		t.Fatalf("generator too weak: %d compiled, %d finite, %d with breakdowns, %d with calendars, %d with batches, %+v", compiled, finite, broken, calendared, batched, ts)
	}
}

// FuzzSetups (T-050): a random model with changeovers (the generator's
// seed), validated in full; every run must agree with the observation-only
// model and no valid model may deadlock or stall.
func FuzzSetups(f *testing.F) {
	for _, seed := range []uint64{0, 2, 5, 9, 13, 4242} {
		f.Add(seed)
	}
	base := minimal(f)
	f.Fuzz(func(t *testing.T, seed uint64) {
		ir, horizon, ok := randomSetupModel(base, int(seed%(1<<31)), 5013)
		if !ok {
			return
		}
		in := input(ir)
		in.Config = config(horizon, model.Minute)
		in.Config.Replications = 2
		p, err := Compile(in)
		if err != nil {
			return
		}
		c := newBChecker(p, &ir, uint32(seed%2))
		_, err = RunReplication(context.Background(), p, uint32(seed%2), NewBudget(100_000), c)
		if errors.Is(err, ErrDeadlock) || errors.Is(err, ErrStalled) {
			t.Fatalf("a valid model failed: %v", err)
		}
		if err != nil && !errors.Is(err, ErrEventBudget) && !errors.Is(err, ErrEntityVisits) && !errors.Is(err, ErrActiveEntities) {
			t.Fatal(err)
		}
		c.finish()
		if len(c.errs) > 0 {
			t.Fatalf("%d problems, first: %v", len(c.errs), c.errs)
		}
	})
}
