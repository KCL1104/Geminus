package engine

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

// D-047 step 2 for T-049: neutral settings change nothing. A model whose
// calendars keep S = C at every tick of the horizon, or whose sources
// release on a deterministic schedule at the ticks of a constant
// interarrival, runs exactly as on des-go-0.3.0 (frozen030_test.go):
// every observation, sequence numbers included, and the outcome.

// withNeutralCalendars puts some resources of ir on calendars that are on
// shift with the whole capacity at every tick up to horizon (minutes): one
// shift over the period; two labelled shifts at the whole capacity (or
// unlisted); open exceptions (S = C there too); a closed exception after
// the horizon (the pattern is then not always on, so the failure clock
// takes the on-shift search, D-051 (ii), and finds calendar time).
func withNeutralCalendars(ir model.ModelIR, rng *rand.Rand, horizon float64) (model.ModelIR, int) {
	ir.Resources = append([]model.Resource(nil), ir.Resources...)
	ir.Calendars = nil
	n := 0
	for i := range ir.Resources {
		if rng.IntN(3) == 0 {
			continue
		}
		r := &ir.Resources[i]
		// Mostly periods of a few common lengths (D-050 R2 refuses a set
		// whose LCM is above 365 days), sometimes any quarter minute.
		period := []float64{5, 7.5, 10, 15, 30, 60, 90, 120, 240, 480, 1440}[rng.IntN(11)]
		if rng.IntN(4) == 0 {
			period = 0.25 * float64(4+rng.IntN(2000))
		}
		c := model.Calendar{ID: fmt.Sprintf("cal%d", i), Period: period}
		switch kind := rng.IntN(4); kind {
		case 1:
			x := 0.25 * float64(1+rng.IntN(int(period*4)-1))
			c.Shifts = []model.Shift{{Start: 0, End: x, Label: "a"}, {Start: x, End: period, Label: "b"}}
			if rng.IntN(2) == 0 {
				r.ShiftCapacity = map[string]int{"a": r.Capacity, "b": r.Capacity}
			}
		default:
			c.Shifts = []model.Shift{{Start: 0, End: period}}
			if kind == 2 {
				at := 0.0
				for range 1 + rng.IntN(3) {
					start := at + 0.25*float64(rng.IntN(int(horizon*2)+1))
					end := start + 0.25*float64(1+rng.IntN(200))
					c.Exceptions = append(c.Exceptions, model.CalendarException{Start: start, End: end, Open: true})
					at = end
				}
			}
			if kind == 3 {
				start := horizon + 0.25*float64(1+rng.IntN(400))
				c.Exceptions = []model.CalendarException{{Start: start, End: start + 0.25*float64(1+rng.IntN(400))}}
			}
		}
		ir.Calendars = append(ir.Calendars, c)
		r.CalendarID = c.ID
		n++
	}
	return ir, n
}

func TestNeutralCalendarsChangeNothing(t *testing.T) {
	const n = 6000
	base := minimal(t)
	type result struct {
		compiled, breakdowns bool
		diff                 string
		obs, downs, changes  int
	}
	results := parallelSeeds(n, func(seed int) result {
		ir, rng, ok := randomModel030(base, seed)
		if !ok {
			return result{}
		}
		horizon := float64(30 + rng.IntN(240))
		ir, k := withNeutralCalendars(ir, rng, horizon)
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
		if !p.HasCalendars() {
			return result{diff: "HasCalendars false"}
		}
		a, b := runBoth030(p, uint32(seed%5), 200_000)
		downs, changes := 0, 0
		for _, o := range a.obs {
			switch o.Kind {
			case ObsResourceDown:
				downs++
			case ObsCapacity:
				changes++
			}
		}
		return result{true, p.HasBreakdowns(), sameRun(a, b), len(a.obs), downs, changes}
	})
	compiled, breakdowns, obs, downs := 0, 0, 0, 0
	for seed, r := range results {
		if r.diff != "" {
			t.Fatalf("seed %d: %s (des-go-0.1.0 reads des-go-0.3.0 here)", seed, r.diff)
		}
		if !r.compiled {
			continue
		}
		if r.changes != 0 {
			t.Fatalf("seed %d: %d Capacity observations under a neutral calendar", seed, r.changes)
		}
		compiled++
		obs += r.obs
		downs += r.downs
		if r.breakdowns {
			breakdowns++
		}
	}
	t.Logf("%d of %d random models with neutral calendars compiled, %d with breakdowns; %d observations (%d ResourceDown) identical to des-go-0.3.0", compiled, n, breakdowns, obs, downs)
	if compiled < n/5 || breakdowns < n/20 || downs < 1000 {
		t.Fatalf("too weak: %d compiled, %d with breakdowns, %d downs", compiled, breakdowns, downs)
	}
}

// D-053 G-R4: a deterministic schedule of one segment at rate 1/c (c a
// power of 2, so 1/c, the period c·m and every τ_k = k·c are exact)
// releases at the ticks of the constant interarrival c, with the same
// sequence numbers: the run is des-go-0.3.0's with the constant.
func TestNeutralSchedulesChangeNothing(t *testing.T) {
	const n = 4000
	base := minimal(t)
	type result struct {
		compiled bool
		diff     string
		obs      int
	}
	results := parallelSeeds(n, func(seed int) result {
		ir, rng, ok := randomModel030(base, seed)
		if !ok {
			return result{}
		}
		scheduled := ir
		ir.Nodes, scheduled.Nodes = append([]model.Node(nil), ir.Nodes...), append([]model.Node(nil), ir.Nodes...)
		k := 0
		for i := range ir.Nodes {
			if ir.Nodes[i].Type != model.NodeSource || rng.IntN(4) == 0 {
				continue
			}
			c := []float64{0.25, 0.5, 1, 2, 4, 8}[rng.IntN(6)]
			constant := *ir.Nodes[i].Arrival
			constant.Distribution = model.Distribution{Type: model.DistConstant, Value: &c}
			ir.Nodes[i].Arrival = &constant
			sched := constant
			sched.Distribution = model.Distribution{}
			sched.Schedule = &model.ReleaseSchedule{Process: model.ReleaseDeterministic, Period: c * float64(1+rng.IntN(20)),
				Segments: []model.RateSegment{{Start: 0, Rate: 1 / c}}}
			scheduled.Nodes[i].Arrival = &sched
			k++
		}
		if k == 0 {
			return result{}
		}
		cfg := config(float64(30+rng.IntN(240)), model.Minute)
		cfg.Replications = 5
		limits := differentialLimits[seed%len(differentialLimits)]
		ain, bin := input(ir), input(scheduled)
		ain.Config, bin.Config, ain.Limits, bin.Limits = cfg, cfg, limits, limits
		pa, err := Compile(ain)
		if err != nil {
			return result{}
		}
		pb, err := Compile(bin)
		if err != nil {
			return result{diff: fmt.Sprintf("the scheduled model does not compile: %v", err)}
		}
		ac, bc := &collect{}, &collect{}
		index := uint32(seed % 5)
		_, aout, aerr := runKernel030(context.Background(), pa, index, NewBudget(200_000), ac)
		_, bout, berr := runKernel(context.Background(), pb, index, NewBudget(200_000), bc)
		return result{true, sameRun(kernelRun{bc.all, bout, berr}, kernelRun{ac.all, aout, aerr}), len(bc.all)}
	})
	compiled, obs := 0, 0
	for seed, r := range results {
		if r.diff != "" {
			t.Fatalf("seed %d: %s (des-go-0.1.0 reads des-go-0.3.0 with the constant here)", seed, r.diff)
		}
		if r.compiled {
			compiled++
			obs += r.obs
		}
	}
	t.Logf("%d of %d random models with neutral schedules compiled; %d observations identical to des-go-0.3.0 with the constant interarrival", compiled, n, obs)
	if compiled < n/5 {
		t.Fatalf("too weak: %d compiled", compiled)
	}
}
