package engine

// Random models with breakdowns (D-051, T-048) against the observation-only
// model of kernel_check_blocking_test.go, which rebuilds from the
// Observations and the IR alone the units in use and down, the failures
// waiting for a release and, for constant clocks, when each failure is
// due, and checks: a failure goes down at once only with an idle unit and
// otherwise waits (finish-first); a released unit goes to the waiting
// failures first, in their order; a unit is down for exactly its TTR and
// fails again a TTF after its repair; in_use + down <= C; every dispatch
// is the literal fixpoint with C - D; no tick ends with a grantable
// request (so a repair never leaves a request it lets in waiting); valid
// models never end in ErrDeadlock or ErrStalled.

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

// randDistribution is randDist with lognormal and Weibull as well.
func randDistribution(rng *rand.Rand, allowZero bool) model.Distribution {
	switch rng.IntN(6) {
	case 0:
		m, cv := 0.25+rng.Float64()*6, 0.05+rng.Float64()*3
		return model.Distribution{Type: model.DistLognormal, Mean: &m, CV: &cv}
	case 1:
		shape, scale := 0.3+rng.Float64()*4, 0.25+rng.Float64()*5
		return model.Distribution{Type: model.DistWeibull, Shape: &shape, Scale: &scale}
	}
	return randDist(rng, allowZero)
}

// withBreakdowns gives breakdowns to some resources of ir. With r3, only
// to resources every requirement of which is 1 unit, at most one per
// bundle (D-050 R3), so that the model stays valid; without, anywhere.
// With constant, every clock is constant (the checker then checks its
// timing), with quarter-minute values so that ties with the model's
// whole-minute events happen and some do not.
func withBreakdowns(ir model.ModelIR, rng *rand.Rand, r3, constant bool) (model.ModelIR, int) {
	ir.Resources = slices.Clone(ir.Resources)
	chosen := map[string]bool{}
	for _, i := range rng.Perm(len(ir.Resources)) {
		if rng.IntN(3) == 0 {
			continue
		}
		id := ir.Resources[i].ID
		ok := true
		for _, n := range ir.Nodes {
			needs, other := false, false
			for _, q := range n.Requires {
				if q.ResourceID == id {
					needs = true
					ok = ok && q.Units == 1
				} else if chosen[q.ResourceID] {
					other = true
				}
			}
			ok = ok && !(needs && other)
		}
		if r3 && !ok {
			continue
		}
		chosen[id] = true
		var ttf, ttr model.Distribution
		if constant {
			f, r := 0.25*float64(4+rng.IntN(240)), 0.25*float64(rng.IntN(60))
			ttf, ttr = model.Distribution{Type: model.DistConstant, Value: &f}, model.Distribution{Type: model.DistConstant, Value: &r}
		} else {
			ttf, ttr = randDistribution(rng, false), randDistribution(rng, true)
			// Longer failure clocks, so that the stations still work.
			ttf = scaled(ttf, 10+rng.Float64()*30)
		}
		ir.Resources[i].Breakdowns = &model.Breakdowns{TimeToFailure: model.TimedDistribution{Distribution: ttf},
			TimeToRepair: model.TimedDistribution{Distribution: ttr}}
	}
	return ir, len(chosen)
}

// scaled multiplies a distribution's time parameters by k.
func scaled(d model.Distribution, k float64) model.Distribution {
	mul := func(p *float64) *float64 {
		if p == nil {
			return nil
		}
		v := *p * k
		return &v
	}
	d.Value, d.Mean, d.Min, d.Mode, d.Max, d.Scale = mul(d.Value), mul(d.Mean), mul(d.Min), mul(d.Mode), mul(d.Max), mul(d.Scale)
	return d
}

// randomBreakdownModel is a valid random model (with finite buffers when
// finite) with breakdowns on some of its resources, or false.
func randomBreakdownModel(base model.ModelIR, rng *rand.Rand, finite, constant bool) (model.ModelIR, bool) {
	ir := randomModelFrom(base, rng)
	for i := range ir.Nodes {
		if d := ir.Nodes[i].ServiceTime; d != nil && rng.IntN(4) == 0 {
			d.Distribution = randDistribution(rng, true)
		}
	}
	if finite {
		var ok bool
		if ir, ok = withFiniteBuffers(ir, rng); !ok {
			return ir, false
		}
	}
	ir, n := withBreakdowns(ir, rng, true, constant)
	return ir, n > 0
}

func TestRandomBreakdownModelsAgainstSpec(t *testing.T) {
	const n = 3000
	base := minimal(t)
	compiled, finite, timed := 0, 0, 0
	var total bchecker
	for seed := range n {
		rng := rand.New(rand.NewPCG(uint64(seed), 4803))
		ir, ok := randomBreakdownModel(base, rng, seed%2 == 1, seed%3 != 0)
		if !ok {
			continue
		}
		in := input(ir)
		in.Config = config(float64(120+rng.IntN(600)), model.Minute)
		in.Config.Replications = 3
		p, err := Compile(in)
		if err != nil {
			continue // invalid for another reason (a long constant clock, say)
		}
		if !p.HasBreakdowns() {
			t.Fatalf("seed %d: no breakdowns", seed)
		}
		compiled++
		if p.HasFiniteBuffers() {
			finite++
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
		total.stats.downs += c.stats.downs
		total.stats.pendings += c.stats.pendings
		total.stats.repairs += c.stats.repairs
		total.stats.grants += c.stats.grants
		total.stats.blocks += c.stats.blocks
		timed += c.stats.timed
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
	t.Logf("%d of %d random models compiled, %d with finite buffers; %d downtimes (%d after a waiting failure), %d repairs, %d failures checked against a constant TTF, %d grants, %d blocks",
		compiled, n, finite, total.stats.downs, total.stats.pendings, total.stats.repairs, timed, total.stats.grants, total.stats.blocks)
	if compiled < n/5 || finite < n/20 || total.stats.downs < 10_000 || total.stats.pendings < 1000 || timed < 5000 || total.stats.blocks < 1000 {
		t.Fatalf("generator too weak: %d compiled, %d finite, %+v, %d timed", compiled, finite, total.stats, timed)
	}
}

// With R3 bypassed (test-only: several units of a resource that breaks
// down, or two such resources in one bundle), a bundle may never be
// filled: every ErrStalled is reported for a request really in D-050
// B2's state (held at the front, none of its bundle in use, not fitting
// what is up) at the end of its tick; every other run passes the
// observation-only model.
func TestRandomStallsAreInTheState(t *testing.T) {
	const n = 3000
	base := minimal(t)
	stalls, runs := 0, 0
	for seed := range n {
		rng := rand.New(rand.NewPCG(uint64(seed), 4804))
		ir := randomModelFrom(base, rng)
		in := input(ir)
		if _, err := Compile(in); err != nil {
			continue
		}
		ir, k := withBreakdowns(ir, rng, false, true)
		if k == 0 {
			continue
		}
		in.Model = ir
		in.Config = config(float64(240+rng.IntN(1200)), model.Minute)
		in.Config.Replications = 2
		var p *Program
		var err error
		withoutValidator(t, func() { p, err = Compile(in) })
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
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
			if se.Tick != c.lastTick || se.Tick < se.Deadline || !c.stalled(findEntity(t, c, se.Entity)) {
				t.Fatalf("seed %d: ErrStalled %+v not in B2's state at its tick (%v)", seed, *se, c.errs)
			}
		case err == nil || errors.Is(err, ErrEventBudget) || errors.Is(err, ErrEntityVisits) || errors.Is(err, ErrActiveEntities):
			c.finish()
		default:
			t.Fatalf("seed %d: %v", seed, err)
		}
		if len(c.errs) > 0 {
			t.Fatalf("seed %d: %d problems, first: %v", seed, len(c.errs), c.errs)
		}
	}
	t.Logf("%d runs with R3 bypassed, %d ended in ErrStalled", runs, stalls)
	if stalls < 20 || runs-stalls < 100 {
		t.Fatalf("too weak: %d runs, %d stalls", runs, stalls)
	}
}

// FuzzBreakdowns (T-048): a random model (the generator's seed) with
// breakdowns, constant clocks or not, finite buffers or not, validated
// in full (R3 holds: no run may stall or deadlock) or, with bypass,
// compiled past the validator (stalls are allowed but must be in B2's
// state); every run must agree with the observation-only model.
func FuzzBreakdowns(f *testing.F) {
	for _, s := range []struct {
		seed                     uint64
		finite, constant, bypass bool
	}{{1, false, true, false}, {7, true, true, false}, {42, true, false, false}, {99, false, true, true}, {1234, false, false, true}} {
		f.Add(s.seed, s.finite, s.constant, s.bypass)
	}
	base := minimal(f)
	f.Fuzz(func(t *testing.T, seed uint64, finite, constant, bypass bool) {
		rng := rand.New(rand.NewPCG(seed, 4805))
		var ir model.ModelIR
		var ok bool
		if bypass {
			ir = randomModelFrom(base, rng)
			if _, err := Compile(input(ir)); err != nil {
				return
			}
			var k int
			ir, k = withBreakdowns(ir, rng, false, constant)
			ok = k > 0
		} else {
			ir, ok = randomBreakdownModel(base, rng, finite, constant)
		}
		if !ok {
			return
		}
		in := input(ir)
		in.Config = config(float64(60+rng.IntN(900)), model.Minute)
		in.Config.Replications = 2
		var p *Program
		var err error
		if bypass {
			withoutValidator(t, func() { p, err = Compile(in) })
		} else if p, err = Compile(in); err != nil {
			return
		}
		if err != nil {
			t.Fatal(err)
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
			if se.Tick != c.lastTick || !c.stalled(findEntity(t, c, se.Entity)) {
				t.Fatalf("ErrStalled not in B2's state: %v %v", err, c.errs)
			}
		case errors.Is(err, ErrDeadlock):
			t.Fatalf("a model without a blocking cycle deadlocked: %v", err)
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
