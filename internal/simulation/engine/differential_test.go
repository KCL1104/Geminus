package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The D-047 differential (ADR-026 D-047, steps 1 and 2; T-047): a model
// without a finite buffer runs exactly as on des-go-0.1.0, frozen in
// frozen010_test.go; a model whose finite buffers can never fill (capacity
// at least max_active_entities, the neutral setting) runs exactly as the
// same model without them, and records no new observation.

// differentialModels is how many random models each differential runs. It
// is fixed by T-047 (ADR-026 D-047 step 1: "件數在實作任務中固定"; the
// T-021 re-review ran about 50,000).
const differentialModels = 50_000

// frozenDifferentialModels is how many of them TestDifferentialAgainstFrozenKernel
// runs: all 50,000 in a normal build (make backend-test), the first 5,000
// under the race detector (make backend-race; T-047 review round 1, I5).
// The race detector looks for unsynchronized memory access: a replication
// is one goroutine and the kernel shares nothing between replications, so
// the only concurrency here is the test's own worker pool (parallelSeeds),
// which 5,000 models exercise as well as 50,000; with the full count the
// package took about 607 s under -race, almost all of it here, at the edge
// of backend-race's timeout. Every check is the same in both builds: the
// floors of the endings (at least 100 on max_entity_visits, 100 on
// max_active_entities, 1,000 without a limit) are absolute, and a quarter
// of the models run must compile.
var frozenDifferentialModels = func() int {
	if raceEnabled {
		return differentialModels / 10
	}
	return differentialModels
}()

// raceEnabled: this test binary was built with the race detector (go test
// -race), as its build settings say. Not a //go:build race file: such a
// file compiles on no platform of the architecture checker's matrix, which
// refuses it (every-file-built, T-042). Without build information the
// full count runs.
var raceEnabled = func() bool {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, s := range bi.Settings {
		if s.Key == "-race" {
			return s.Value == "true"
		}
	}
	return false
}()

// fullDifferentialEnv is set to 1 by make backend-test, which builds
// without the race detector (make backend-race does not set it): a signal
// independent of raceEnabled that the full count must run, so a wrong
// race detection cannot silently weaken the differential of
// make backend-test and CI's backend job (T-047 review round 2, I2).
const fullDifferentialEnv = "GEMINUS_FULL_DIFFERENTIAL"

type kernelRun struct {
	obs []Observation
	out ReplicationOutcome
	err error
}

func runBoth(p *Program, index uint32, events int64) (kernelRun, kernelRun) {
	nc, oc := &collect{}, &collect{}
	_, nout, nerr := runKernel(context.Background(), p, index, NewBudget(events), nc)
	_, oout, oerr := runKernel010(context.Background(), p, index, NewBudget(events), oc)
	return kernelRun{nc.all, nout, nerr}, kernelRun{oc.all, oout, oerr}
}

// parallelSeeds runs f for the seeds 0..n-1 on GOMAXPROCS goroutines and
// returns the results in seed order. f must not use t: only the test's own
// goroutine may fail it.
func parallelSeeds[T any](n int, f func(seed int) T) []T {
	out := make([]T, n)
	var next atomic.Int64
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for {
				seed := int(next.Add(1) - 1)
				if seed >= n {
					return
				}
				out[seed] = f(seed)
			}
		})
	}
	wg.Wait()
	return out
}

// sameRun reports how two runs differ: observations (every field, At
// included), outcome, and the error by its message (the typed limit
// errors carry entity, node and tick).
func sameRun(a, b kernelRun) string {
	if n := min(len(a.obs), len(b.obs)); !slices.Equal(a.obs[:n], b.obs[:n]) {
		i := 0
		for i < n && a.obs[i] == b.obs[i] {
			i++
		}
		return fmt.Sprintf("observation %d: %+v, des-go-0.1.0 %+v", i, a.obs[i], b.obs[i])
	}
	if len(a.obs) != len(b.obs) {
		return fmt.Sprintf("%d observations, des-go-0.1.0 %d", len(a.obs), len(b.obs))
	}
	if a.out != b.out {
		return fmt.Sprintf("outcome %+v, des-go-0.1.0 %+v", a.out, b.out)
	}
	if (a.err == nil) != (b.err == nil) || (a.err != nil && a.err.Error() != b.err.Error()) {
		return fmt.Sprintf("error %v, des-go-0.1.0 %v", a.err, b.err)
	}
	return ""
}

// differentialLimits varies the limits so that runs also end in the middle
// of a route (max_entity_visits) and at an arrival (max_active_entities):
// the observations before the error must be the same too (D-049, review L3).
var differentialLimits = []simulation.ExecutionLimits{
	limits,
	{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 100_000, MaxEntityVisits: 7, MaxTraceEvents: 0},
	{WallTimeoutSeconds: 300, MaxEvents: 10_000_000, MaxActiveEntities: 12, MaxEntityVisits: 10_000, MaxTraceEvents: 0},
}

func TestDifferentialAgainstFrozenKernel(t *testing.T) {
	if os.Getenv(fullDifferentialEnv) == "1" && frozenDifferentialModels != differentialModels {
		t.Fatalf("%s=1 (make backend-test) but %d of %d models would run (raceEnabled %v)", fullDifferentialEnv, frozenDifferentialModels, differentialModels, raceEnabled)
	}
	base := minimal(t)
	type result struct {
		compiled bool
		diff     string
		obs      int
		err      error
	}
	results := parallelSeeds(frozenDifferentialModels, func(seed int) result {
		rng := rand.New(rand.NewPCG(uint64(seed), 4701))
		in := input(randomModelFrom(base, rng))
		in.Config = config(float64(30+rng.IntN(240)), model.Minute)
		in.Config.Replications = 5
		in.Limits = differentialLimits[seed%len(differentialLimits)]
		p, err := Compile(in)
		if err != nil {
			return result{}
		}
		a, b := runBoth(p, uint32(seed%5), 200_000)
		return result{true, sameRun(a, b), len(a.obs), a.err}
	})
	compiled, observations, failures := 0, 0, map[string]int{}
	for seed, r := range results {
		if !r.compiled {
			continue
		}
		compiled++
		if r.diff != "" {
			t.Fatalf("seed %d: %s", seed, r.diff)
		}
		observations += r.obs
		switch {
		case r.err == nil:
			failures["none"]++
		case errors.Is(r.err, ErrEntityVisits):
			failures["visits"]++
		case errors.Is(r.err, ErrActiveEntities):
			failures["active"]++
		case errors.Is(r.err, ErrEventBudget):
			failures["budget"]++
		default:
			t.Fatalf("seed %d: unexpected error %v", seed, r.err)
		}
	}
	t.Logf("%d of %d random models compiled (race detector: %v); %d observations identical; endings %v", compiled, frozenDifferentialModels, raceEnabled, observations, failures)
	if compiled < frozenDifferentialModels/4 || failures["visits"] < 100 || failures["active"] < 100 || failures["none"] < 1000 {
		t.Fatalf("differential too weak: %d compiled, endings %v", compiled, failures)
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
				a, b := runBoth(p, i, 10_000_000)
				if d := sameRun(a, b); d != "" || a.err != nil {
					t.Fatalf("%s, %g days, r%d: %s %v", filepath.Base(path), days, i, d, a.err)
				}
			}
		}
	}
}
