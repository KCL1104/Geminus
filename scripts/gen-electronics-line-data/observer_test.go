package main

import (
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// Review r1 L2 (D-118 (a)): at every step's start the units the observer
// books must be the engine's level of units in use and down, or the
// generator fails; a consistent run never shows it, so hand-written
// observations do. A board arrives and starts at ICT (two testers) at
// Monday 07:00 of the data week; the engine's level before the start is
// one tester in use (the start's), two (one the observer never saw
// taken), or two and then one (a release the observer cannot match to a
// holder of the tester).
func TestObserverFailsWhenItsBookingIsNotTheEngines(t *testing.T) {
	ir, err := groundModel()
	if err != nil {
		t.Fatal(err)
	}
	shifts, err := newShiftTable(ir)
	if err != nil {
		t.Fatal(err)
	}
	in, err := DefaultConfig().runInput(ir, 15)
	if err != nil {
		t.Fatal(err)
	}
	prog, err := engine.Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	node := func(id string) int {
		for i := range prog.NodeCount() {
			if prog.NodeID(i) == id {
				return i
			}
		}
		t.Fatalf("no node %s", id)
		return -1
	}
	source, ict := node("release_a"), node("ict")
	reqs := prog.Requires(ict)
	if len(reqs) != 1 || prog.ResourceID(reqs[0].Resource) != "ict_tester" || prog.ResourceCapacity(reqs[0].Resource) != 2 {
		t.Fatalf("ICT requires %+v", reqs)
	}
	tester := reqs[0].Resource
	tick := weekTicks + 7*60*60*1000
	for _, c := range []struct {
		name   string
		levels []int64 // the engine's units of the tester in use, in order, before the start
		fails  bool
	}{
		{"the engine's level", []int64{1}, false},
		{"a unit the observer never saw taken", []int64{2}, true},
		{"a release of a unit no one holds", []int64{2, 1}, true},
	} {
		o, err := newObserver(prog, ir, shifts)
		if err != nil {
			t.Fatal(err)
		}
		o.Observe(engine.Observation{Kind: engine.ObsArrival, Tick: tick, Entity: 1, Node: source, Value: 1})
		for _, v := range c.levels {
			o.Observe(engine.Observation{Kind: engine.ObsResource, Tick: tick, Entity: 1, Node: ict, Resource: tester, Value: v})
		}
		o.Observe(engine.Observation{Kind: engine.ObsServiceStart, Tick: tick, Entity: 1, Node: ict})
		switch {
		case !c.fails && o.err != nil:
			t.Errorf("%s: %v", c.name, o.err)
		case c.fails && (o.err == nil || !strings.Contains(o.err.Error(), "booked")):
			t.Errorf("%s: the observer does not fail on its booking: %v", c.name, o.err)
		case !c.fails && (len(o.records) != 1 || o.records[0].equipment != "ict_tester_1" || o.records[0].station != "ict"):
			t.Errorf("%s: records %+v", c.name, o.records)
		}
	}
}
