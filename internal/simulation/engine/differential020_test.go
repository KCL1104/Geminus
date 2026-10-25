package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// The D-047 differential of T-048 (ADR-026 D-047 step 1): a model without
// breakdowns, lognormal or Weibull runs exactly as on des-go-0.2.0, frozen
// in frozen020_test.go, finite buffers included. Half of the random models
// are those of TestDifferentialAgainstFrozenKernel (no finite buffer), the
// other half get finite buffers on their explicit queues, minus the ones
// R1 refuses, so that they are valid; then the golden models and every
// model of blocking-goldens.json (G-D2 and G-D3 past the validator, as
// their goldens run them).

func runBoth020(p *Program, index uint32, events int64) (kernelRun, kernelRun) {
	nc, oc := &collect{}, &collect{}
	_, nout, nerr := runKernel(context.Background(), p, index, NewBudget(events), nc)
	_, oout, oerr := runKernel020(context.Background(), p, index, NewBudget(events), oc)
	return kernelRun{nc.all, nout, nerr}, kernelRun{oc.all, oout, oerr}
}

// withFiniteBuffers gives some explicit queues of ir a capacity of 1–3 and
// takes back each one R1 refuses (BLOCKING_CYCLE), as randomBlockingModel
// does, without a *testing.T so that worker goroutines can use it. ok is
// false when the model is not valid for another reason or has no finite
// buffer left.
func withFiniteBuffers(ir model.ModelIR, rng *rand.Rand) (model.ModelIR, bool) {
	ir.Nodes = slices.Clone(ir.Nodes)
	for i := range ir.Nodes {
		if ir.Nodes[i].Type == model.NodeQueue && rng.IntN(3) > 0 {
			ir.Nodes[i].Capacity = 1 + rng.IntN(3)
		}
	}
	for range 10 {
		data, err := model.Encode(&ir)
		if err != nil {
			return ir, false
		}
		r, err := validate.IR(data)
		if err != nil {
			return ir, false
		}
		cut := false
		for _, is := range r.Issues {
			var q int
			if is.Code == validate.BlockingCycle {
				if _, err := fmt.Sscanf(is.Path, "/nodes/%d/capacity", &q); err == nil {
					ir.Nodes[q].Capacity = 0
					cut = true
				}
			}
		}
		if !cut {
			return ir, r.Valid() && hasCapacity(ir)
		}
	}
	return ir, false
}

func TestDifferentialAgainstFrozen020(t *testing.T) {
	if os.Getenv(fullDifferentialEnv) == "1" && frozenDifferentialModels != differentialModels {
		t.Fatalf("%s=1 (make backend-test) but %d of %d models would run (raceEnabled %v)", fullDifferentialEnv, frozenDifferentialModels, differentialModels, raceEnabled)
	}
	base := minimal(t)
	type result struct {
		compiled, finite bool
		diff             string
		obs, blocks      int
		err              error
	}
	results := parallelSeeds(frozenDifferentialModels, func(seed int) result {
		rng := rand.New(rand.NewPCG(uint64(seed), 4801))
		ir := randomModelFrom(base, rng)
		finite := seed%2 == 1
		if finite {
			var ok bool
			if ir, ok = withFiniteBuffers(ir, rng); !ok {
				return result{}
			}
		}
		in := input(ir)
		in.Config = config(float64(30+rng.IntN(240)), model.Minute)
		in.Config.Replications = 5
		in.Limits = differentialLimits[seed%len(differentialLimits)]
		p, err := Compile(in)
		if err != nil {
			return result{}
		}
		a, b := runBoth020(p, uint32(seed%5), 200_000)
		blocks := 0
		for _, o := range a.obs {
			if o.Kind == ObsBlocked {
				blocks++
			}
		}
		return result{true, finite && p.HasFiniteBuffers(), sameRun(a, b), len(a.obs), blocks, a.err}
	})
	compiled, finite, observations, blocks, endings := 0, 0, 0, 0, map[string]int{}
	for seed, r := range results {
		if !r.compiled {
			continue
		}
		compiled++
		if r.diff != "" {
			t.Fatalf("seed %d: %s (des-go-0.1.0 reads des-go-0.2.0 here)", seed, r.diff)
		}
		if r.finite {
			finite++
		}
		observations += r.obs
		blocks += r.blocks
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
	t.Logf("%d of %d random models compiled (race detector: %v), %d with finite buffers; %d observations identical to des-go-0.2.0, %d of them Blocked; endings %v",
		compiled, frozenDifferentialModels, raceEnabled, finite, observations, blocks, endings)
	if compiled < frozenDifferentialModels/4 || finite < frozenDifferentialModels/20 || blocks < 1000 ||
		endings["visits"] < 100 || endings["active"] < 100 || endings["none"] < 1000 {
		t.Fatalf("differential too weak: %d compiled, %d finite, %d blocks, endings %v", compiled, finite, blocks, endings)
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
				a, b := runBoth020(p, i, 10_000_000)
				if d := sameRun(a, b); d != "" || a.err != nil {
					t.Fatalf("%s, %g days, r%d: %s %v", filepath.Base(path), days, i, d, a.err)
				}
			}
		}
	}
	// Every finite-buffer golden of T-047, the deadlocks included.
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
		a, b := runBoth020(p, 0, 1_000_000)
		if d := sameRun(a, b); d != "" {
			t.Fatalf("%s: %s", id, d)
		}
	}
}
