package engine

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
)

// The D-047 differential of T-049 (ADR-026 D-047 step 1): a model without
// calendars or release schedules runs exactly as on des-go-0.3.0, frozen in
// frozen030_test.go, finite buffers, breakdowns, lognormal and Weibull
// included. A quarter of the random models are those of
// TestDifferentialAgainstFrozenKernel, a quarter get finite buffers, a
// quarter breakdowns (R3 holding, constant or random clocks) and a quarter
// both, some with lognormal and Weibull services; then the golden models
// and every model of blocking-goldens.json and breakdown-goldens.json,
// past the validator where their goldens run them so.

func runBoth030(p *Program, index uint32, events int64) (kernelRun, kernelRun) {
	nc, oc := &collect{}, &collect{}
	_, nout, nerr := runKernel(context.Background(), p, index, NewBudget(events), nc)
	_, oout, oerr := runKernel030(context.Background(), p, index, NewBudget(events), oc)
	return kernelRun{nc.all, nout, nerr}, kernelRun{oc.all, oout, oerr}
}

// randomModel030 is a random model of the differential: seed%4 says
// whether it has finite buffers (odd) and breakdowns (2 and 3). ok is
// false when it is not a valid model of its kind.
func randomModel030(base model.ModelIR, seed int) (model.ModelIR, *rand.Rand, bool) {
	rng := rand.New(rand.NewPCG(uint64(seed), 4901))
	ir := randomModelFrom(base, rng)
	for i := range ir.Nodes {
		if d := ir.Nodes[i].ServiceTime; d != nil && rng.IntN(5) == 0 {
			d.Distribution = randDistribution(rng, true)
		}
	}
	if seed%2 == 1 {
		var ok bool
		if ir, ok = withFiniteBuffers(ir, rng); !ok {
			return ir, rng, false
		}
	}
	if seed%4 >= 2 {
		var n int
		if ir, n = withBreakdowns(ir, rng, true, rng.IntN(2) == 0); n == 0 {
			return ir, rng, false
		}
	}
	return ir, rng, true
}

func TestDifferentialAgainstFrozen030(t *testing.T) {
	if os.Getenv(fullDifferentialEnv) == "1" && frozenDifferentialModels != differentialModels {
		t.Fatalf("%s=1 (make backend-test) but %d of %d models would run (raceEnabled %v)", fullDifferentialEnv, frozenDifferentialModels, differentialModels, raceEnabled)
	}
	base := minimal(t)
	type result struct {
		compiled, finite, breakdowns bool
		diff                         string
		obs, blocks, downs           int
		err                          error
	}
	results := parallelSeeds(frozenDifferentialModels, func(seed int) result {
		ir, rng, ok := randomModel030(base, seed)
		if !ok {
			return result{}
		}
		in := input(ir)
		in.Config = config(float64(30+rng.IntN(240)), model.Minute)
		in.Config.Replications = 5
		in.Limits = differentialLimits[seed%len(differentialLimits)]
		p, err := Compile(in)
		if err != nil {
			return result{}
		}
		a, b := runBoth030(p, uint32(seed%5), 200_000)
		blocks, downs := 0, 0
		for _, o := range a.obs {
			switch o.Kind {
			case ObsBlocked:
				blocks++
			case ObsResourceDown:
				downs++
			}
		}
		return result{true, p.HasFiniteBuffers(), p.HasBreakdowns(), sameRun(a, b), len(a.obs), blocks, downs, a.err}
	})
	compiled, finite, breakdowns, observations, blocks, downs, endings := 0, 0, 0, 0, 0, 0, map[string]int{}
	for seed, r := range results {
		if !r.compiled {
			continue
		}
		compiled++
		if r.diff != "" {
			t.Fatalf("seed %d: %s (des-go-0.1.0 reads des-go-0.3.0 here)", seed, r.diff)
		}
		if r.finite {
			finite++
		}
		if r.breakdowns {
			breakdowns++
		}
		observations += r.obs
		blocks += r.blocks
		downs += r.downs
		switch {
		case r.err == nil:
			endings["none"]++
		case errors.Is(r.err, ErrEntityVisits):
			endings["visits"]++
		case errors.Is(r.err, ErrActiveEntities):
			endings["active"]++
		case errors.Is(r.err, ErrEventBudget):
			endings["budget"]++
		default:
			t.Fatalf("seed %d: unexpected error %v", seed, r.err)
		}
	}
	t.Logf("%d of %d random models compiled (race detector: %v), %d with finite buffers, %d with breakdowns; %d observations identical to des-go-0.3.0, %d of them Blocked, %d ResourceDown; endings %v",
		compiled, frozenDifferentialModels, raceEnabled, finite, breakdowns, observations, blocks, downs, endings)
	if compiled < frozenDifferentialModels/5 || finite < frozenDifferentialModels/20 || breakdowns < frozenDifferentialModels/20 ||
		blocks < 1000 || downs < 1000 || endings["visits"] < 100 || endings["active"] < 100 || endings["none"] < 1000 {
		t.Fatalf("differential too weak: %d compiled, %d finite, %d with breakdowns, %d blocks, %d downs, endings %v",
			compiled, finite, breakdowns, blocks, downs, endings)
	}
	// The golden models, over long horizons and every replication of three.
	paths, _ := filepath.Glob(filepath.Join(contracttest.Root(t), "testdata/golden-models/*.json"))
	for _, path := range paths {
		for _, days := range []float64{1, 14, 60} {
			in := input(golden(t, filepath.Base(path)))
			in.Config = config(days, model.Day)
			in.Config.Replications = 3
			p := compile(t, in)
			for i := range uint32(3) {
				a, b := runBoth030(p, i, 10_000_000)
				if d := sameRun(a, b); d != "" || a.err != nil {
					t.Fatalf("%s, %g days, r%d: %s %v", filepath.Base(path), days, i, d, a.err)
				}
			}
		}
	}
	// Every finite-buffer golden of T-047 and breakdown golden of T-048,
	// the deadlocks and stalls included.
	for id, c := range blockingGoldens(t) {
		in := c.input(t)
		var p *Program
		var err error
		if c.Validator != nil {
			withoutValidator(t, func() { p, err = Compile(in) })
		} else {
			p, err = Compile(in)
		}
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		a, b := runBoth030(p, 0, 1_000_000)
		if d := sameRun(a, b); d != "" {
			t.Fatalf("%s: %s", id, d)
		}
	}
	for id, c := range breakdownGoldens(t) {
		in := c.input(t)
		var p *Program
		var err error
		if c.Validator != nil {
			withoutValidator(t, func() { p, err = Compile(in) })
		} else {
			p, err = Compile(in)
		}
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		a, b := runBoth030(p, 0, 1_000_000)
		if d := sameRun(a, b); d != "" {
			t.Fatalf("%s: %s", id, d)
		}
	}
}
