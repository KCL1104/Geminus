package engine

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

// D-047 step 2 for changeovers (T-050; ADR-026 D-055 G-S4): a model whose
// every source makes one entity type, with setups that have no initial
// (or, D-097 (a): an initial equal to that type), never changes over, so
// it runs exactly as on des-go-0.4.0 (frozen040_test.go), whose kernel
// does not read setups at all: every observation, sequence numbers
// included, and the outcome, finite buffers, breakdowns (now on definite
// units), calendars and release schedules included.

// spareType is the entity type withNeutralSetups adds for the transitions
// to name; no source makes it.
const spareType = "spare_type"

// withNeutralSetups gives some resources of ir changeovers that never take
// place: every source makes the model's first entity type; a resource
// every requirement of which is one unit, of a process needing no other
// resource with setups, gets setups with a random default, sometimes
// transitions to and from spareType, and sometimes initial set to the
// sources' type (D-055's P0 limits hold, so the model stays valid).
func withNeutralSetups(ir model.ModelIR, rng *rand.Rand) (model.ModelIR, int) {
	ir.Nodes = slices.Clone(ir.Nodes)
	ir.Resources = slices.Clone(ir.Resources)
	ir.EntityTypes = append(slices.Clone(ir.EntityTypes), model.EntityType{ID: spareType, Name: "Spare"})
	only := ir.EntityTypes[0].ID
	for i := range ir.Nodes {
		if ir.Nodes[i].Type == model.NodeSource {
			ir.Nodes[i].EntityType = only
		}
	}
	chosen := map[string]bool{}
	n := 0
	for _, r := range rng.Perm(len(ir.Resources)) {
		res := &ir.Resources[r]
		if rng.IntN(3) == 0 {
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
		d := randDistribution(rng, true)
		s := &model.Setups{Default: &d}
		if rng.IntN(2) == 0 {
			s.Transitions = []model.SetupTransition{{From: only, To: spareType, Distribution: randDistribution(rng, true)},
				{From: spareType, To: only, Distribution: randDistribution(rng, true)}}
		}
		if rng.IntN(4) == 0 {
			s.Initial = only
		}
		res.Setups = s
		chosen[res.ID] = true
		n++
	}
	return ir, n
}

func TestNeutralSetupsChangeNothing(t *testing.T) {
	const n = 6000
	base := minimal(t)
	type result struct {
		compiled, finite, breakdowns, calendars bool
		diff                                    string
		obs, downs, blocks                      int
	}
	results := parallelSeeds(n, func(seed int) result {
		ir, horizon, ok := randomModel040(base, seed)
		if !ok {
			return result{}
		}
		ir, k := withNeutralSetups(ir, rand.New(rand.NewPCG(uint64(seed), 5005)))
		if k == 0 {
			return result{}
		}
		in := input(ir)
		in.Config = config(horizon, model.Minute)
		in.Config.Replications = 5
		in.Limits = differentialLimits[seed%len(differentialLimits)]
		p, err := Compile(in)
		if err != nil {
			return result{}
		}
		if !p.HasSetups() {
			return result{compiled: true, diff: "HasSetups false"}
		}
		a, b := runBoth040(p, uint32(seed%5), 200_000)
		downs, blocks := 0, 0
		for _, o := range a.obs {
			switch o.Kind {
			case ObsSetup:
				return result{compiled: true, diff: "a Setup observation under neutral setups"}
			case ObsResourceDown:
				downs++
			case ObsBlocked:
				blocks++
			}
		}
		return result{true, p.HasFiniteBuffers(), p.HasBreakdowns(), p.HasCalendars(), sameRun(a, b), len(a.obs), downs, blocks}
	})
	var compiled, finite, breakdowns, calendars, obs, downs, blocks int
	for seed, r := range results {
		if r.diff != "" {
			t.Fatalf("seed %d: %s (des-go-0.1.0 reads des-go-0.4.0 here)", seed, r.diff)
		}
		if !r.compiled {
			continue
		}
		compiled++
		for _, x := range []struct {
			on bool
			n  *int
		}{{r.finite, &finite}, {r.breakdowns, &breakdowns}, {r.calendars, &calendars}} {
			if x.on {
				*x.n++
			}
		}
		obs += r.obs
		downs += r.downs
		blocks += r.blocks
	}
	t.Logf("%d of %d random models with neutral setups compiled, %d with finite buffers, %d with breakdowns, %d with calendars; %d observations (%d ResourceDown, %d Blocked) identical to des-go-0.4.0",
		compiled, n, finite, breakdowns, calendars, obs, downs, blocks)
	if compiled < n/5 || finite < n/20 || breakdowns < n/20 || calendars < n/20 || downs < 1000 || blocks < 1000 {
		t.Fatalf("too weak: %d compiled, %d finite, %d with breakdowns, %d with calendars, %d downs, %d blocks", compiled, finite, breakdowns, calendars, downs, blocks)
	}
}
