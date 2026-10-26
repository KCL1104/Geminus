package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
)

// The D-047 differential of T-050 (ADR-026 D-047 step 1): a model without
// batch processes or changeovers runs exactly as on des-go-0.4.0, frozen
// in frozen040_test.go, finite buffers, breakdowns, lognormal, Weibull,
// calendars and release schedules included. The random models are those of
// TestDifferentialAgainstFrozen030 (a quarter plain, a quarter with finite
// buffers, a quarter with breakdowns, a quarter with both), a third of them
// on calendars and two fifths with release schedules on some sources; then
// the golden models and every model of blocking-goldens.json,
// breakdown-goldens.json and calendar-goldens.json, past the validator
// where their goldens run them so. Batch processes have no neutral setting
// (ADR-026 D-047 step 2: a batch request takes its FIFO place when the
// batch is ready, not as each member queues), so none is run here; the
// neutral changeovers are TestNeutralSetupsChangeNothing.

func runBoth040(p *Program, index uint32, events int64) (kernelRun, kernelRun) {
	nc, oc := &collect{}, &collect{}
	_, nout, nerr := runKernel(context.Background(), p, index, NewBudget(events), nc)
	_, oout, oerr := runKernel040(context.Background(), p, index, NewBudget(events), oc)
	return kernelRun{nc.all, nout, nerr}, kernelRun{oc.all, oout, oerr}
}

// randomModel040 is a random model of the differential: randomModel030's
// (seed%4: finite buffers on odd seeds, breakdowns on 2 and 3), with
// calendars on seed%3 == 0 and release schedules on seed%5 < 2, and its
// horizon in minutes. ok is false when it is not a valid model of its kind.
func randomModel040(base model.ModelIR, seed int) (model.ModelIR, float64, bool) {
	ir, rng, ok := randomModel030(base, seed)
	if !ok {
		return ir, 0, false
	}
	horizon := float64(30 + rng.IntN(600))
	if seed%3 == 0 {
		ir, _ = withCalendars(ir, rng, horizon)
	}
	if seed%5 < 2 {
		ir, _ = withSchedules(ir, rng)
	}
	return ir, horizon, true
}

func TestDifferentialAgainstFrozen040(t *testing.T) {
	if os.Getenv(fullDifferentialEnv) == "1" && frozenDifferentialModels != differentialModels {
		t.Fatalf("%s=1 (make backend-test) but %d of %d models would run (raceEnabled %v)", fullDifferentialEnv, frozenDifferentialModels, differentialModels, raceEnabled)
	}
	base := minimal(t)
	type result struct {
		compiled, finite, breakdowns, calendars, schedules bool
		diff                                               string
		obs, blocks, downs, capacities                     int
		err                                                error
	}
	results := parallelSeeds(frozenDifferentialModels, func(seed int) result {
		ir, horizon, ok := randomModel040(base, seed)
		if !ok {
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
		a, b := runBoth040(p, uint32(seed%5), 200_000)
		blocks, downs, capacities := 0, 0, 0
		for _, o := range a.obs {
			if o.Kind == ObsBatchStart || o.Kind == ObsSetup || o.Batch != 0 {
				return result{compiled: true, diff: "a model without batches or changeovers has a batch or Setup observation"}
			}
			switch o.Kind {
			case ObsBlocked:
				blocks++
			case ObsResourceDown:
				downs++
			case ObsCapacity:
				capacities++
			}
		}
		scheduled := slices.ContainsFunc(p.nodes, func(n cnode) bool { return n.release != nil })
		return result{true, p.HasFiniteBuffers(), p.HasBreakdowns(), p.HasCalendars(), scheduled, sameRun(a, b), len(a.obs), blocks, downs, capacities, a.err}
	})
	var compiled, finite, breakdowns, calendars, schedules, observations, blocks, downs, capacities int
	endings := map[string]int{}
	for seed, r := range results {
		if !r.compiled {
			continue
		}
		compiled++
		if r.diff != "" {
			t.Fatalf("seed %d: %s (des-go-0.1.0 reads des-go-0.4.0 here)", seed, r.diff)
		}
		for _, x := range []struct {
			on bool
			n  *int
		}{{r.finite, &finite}, {r.breakdowns, &breakdowns}, {r.calendars, &calendars}, {r.schedules, &schedules}} {
			if x.on {
				*x.n++
			}
		}
		observations += r.obs
		blocks += r.blocks
		downs += r.downs
		capacities += r.capacities
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
	t.Logf("%d of %d random models compiled (race detector: %v), %d with finite buffers, %d with breakdowns, %d with calendars, %d with release schedules; %d observations identical to des-go-0.4.0, %d of them Blocked, %d ResourceDown, %d Capacity; endings %v",
		compiled, frozenDifferentialModels, raceEnabled, finite, breakdowns, calendars, schedules, observations, blocks, downs, capacities, endings)
	if compiled < frozenDifferentialModels/5 || finite < frozenDifferentialModels/20 || breakdowns < frozenDifferentialModels/20 ||
		calendars < frozenDifferentialModels/20 || schedules < frozenDifferentialModels/20 ||
		blocks < 1000 || downs < 1000 || capacities < 1000 || endings["visits"] < 100 || endings["active"] < 100 || endings["none"] < 1000 {
		t.Fatalf("differential too weak: %d compiled, %d finite, %d with breakdowns, %d with calendars, %d with schedules, %d blocks, %d downs, %d capacities, endings %v",
			compiled, finite, breakdowns, calendars, schedules, blocks, downs, capacities, endings)
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
				a, b := runBoth040(p, i, 10_000_000)
				if d := sameRun(a, b); d != "" || a.err != nil {
					t.Fatalf("%s, %g days, r%d: %s %v", filepath.Base(path), days, i, d, a.err)
				}
			}
		}
	}
	// Every golden of T-047, T-048 and T-049, the deadlocks and stalls
	// included.
	same := func(id string, p *Program) {
		t.Helper()
		a, b := runBoth040(p, 0, 1_000_000)
		if d := sameRun(a, b); d != "" {
			t.Fatalf("%s: %s", id, d)
		}
	}
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
		same(id, p)
	}
	for id, c := range breakdownGoldens(t) {
		same(id, c.compile(t))
	}
	for _, c := range calendarGoldens(t) {
		same(c.ID, c.compile(t))
	}
}
