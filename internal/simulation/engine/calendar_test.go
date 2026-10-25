package engine

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The hand-derived goldens of resource calendars and source release
// schedules (ADR-026 D-047, D-048, D-050, D-051, D-052, D-053; T-049
// interpretations D-081, D-082, PROPOSED; testdata/simulation/
// calendar-goldens.json, HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN, each case
// with its derivation). The Result members of the same cases are checked
// in internal/simulation/des.

type calendarCase struct {
	ID        string               `json:"id"`
	Model     jsontext.Value       `json:"model"`
	Config    simulation.RunConfig `json:"config"`
	Validator *struct {
		CodesAt [][2]string `json:"codes_at"`
	} `json:"validator"`
	Expected struct {
		Arrivals     []jsontext.Value            `json:"arrivals"`
		Starts       map[string][]jsontext.Value `json:"starts"`
		Completions  []jsontext.Value            `json:"completions"`
		Blocks       []jsontext.Value            `json:"blocks"`
		Downs        [][4]any                    `json:"downs"`
		Pending      [][3]any                    `json:"pending"`
		Capacities   [][3]any                    `json:"capacities"`
		CapacitySeqs []uint64                    `json:"capacity_seqs"`
		StartSeqs    [][3]any                    `json:"service_start_seqs"`
		Events       *int64                      `json:"events"`
		AtTick6      []string                    `json:"at_tick_6"`
		Stalled      *struct {
			Tick     int64  `json:"tick"`
			Entity   string `json:"entity"`
			Node     string `json:"node"`
			Since    int64  `json:"since"`
			Deadline int64  `json:"deadline"`
			Reason   string `json:"reason"`
		} `json:"stalled"`
	} `json:"expected"`
}

func calendarGoldens(t testing.TB) []calendarCase {
	t.Helper()
	var doc struct {
		Status string         `json:"status"`
		Cases  []calendarCase `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/calendar-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Status != "HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN" || len(doc.Cases) == 0 {
		t.Fatalf("calendar goldens: status %q, %d cases", doc.Status, len(doc.Cases))
	}
	return doc.Cases
}

func calendarGolden(t *testing.T, id string) calendarCase {
	t.Helper()
	for _, c := range calendarGoldens(t) {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no case %s", id)
	return calendarCase{}
}

// calendarSeeds are the models of calendar-goldens.json, for the fuzzers.
func calendarSeeds(tb testing.TB) []jsontext.Value {
	tb.Helper()
	var doc struct {
		Cases []struct {
			Model jsontext.Value `json:"model"`
		} `json:"cases"`
		Reference struct {
			Cases []struct {
				Model jsontext.Value `json:"model"`
			} `json:"cases"`
		} `json:"reference"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(tb, filepath.Join(contracttest.Root(tb), "testdata/simulation/calendar-goldens.json")), &doc); err != nil {
		tb.Fatal(err)
	}
	var out []jsontext.Value
	for _, c := range append(doc.Cases, doc.Reference.Cases...) {
		out = append(out, c.Model)
	}
	return out
}

func (c calendarCase) ir(t *testing.T) model.ModelIR {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(c.Model, &ir); err != nil {
		t.Fatal(err)
	}
	return ir
}

// compile compiles the case, past the validator when the case says that
// the validator refuses it (after checking the codes it lists).
func (c calendarCase) compile(t *testing.T) *Program {
	t.Helper()
	return breakdownCase{ID: c.ID, Model: c.Model, Config: c.Config, Validator: c.Validator}.compile(t)
}

// calendarFacts are what the calendar goldens state about a run, rebuilt
// from its observations in the model's time unit (G-R5 is in seconds):
// arrivals, service starts per node, completions, blocks, downtimes,
// waiting failures, and every Capacity observation as resource@time=S with
// its sequence number.
type calendarFacts struct {
	arrivals, completions, blocks, downs, pending, capacities []string
	starts                                                    map[string][]string
	capacitySeqs                                              []uint64
}

func observedCalendarFacts(t *testing.T, p *Program, obs []Observation) calendarFacts {
	t.Helper()
	ms, ok := p.unit.Millis()
	if !ok {
		t.Fatalf("unit %q", p.unit)
	}
	at := func(tick int64) string { return fmt.Sprint(float64(tick) / float64(ms)) }
	f := calendarFacts{starts: map[string][]string{}}
	ids := map[uint64]string{}
	openBlock := map[uint64]int{}
	blockFrom := map[uint64]int64{}
	openDown := map[[2]int]int{}
	for _, o := range obs {
		switch o.Kind {
		case ObsArrival:
			ids[o.Entity] = FormatEntityID(0, p.nodes[o.Node].id, uint64(o.Value))
			f.arrivals = append(f.arrivals, ids[o.Entity]+"@"+at(o.Tick))
		case ObsServiceStart:
			n := p.nodes[o.Node].id
			f.starts[n] = append(f.starts[n], ids[o.Entity]+"@"+at(o.Tick))
		case ObsComplete:
			f.completions = append(f.completions, ids[o.Entity]+"@"+at(o.Tick))
		case ObsBlocked:
			openBlock[o.Entity], blockFrom[o.Entity] = len(f.blocks), o.Tick
			f.blocks = append(f.blocks, ids[o.Entity]+"@"+p.nodes[o.Node].id+":"+at(o.Tick)+"-?")
		case ObsUnblocked:
			i, ok := openBlock[o.Entity]
			if !ok {
				f.blocks = append(f.blocks, "unblocked without a block: "+ids[o.Entity])
				continue
			}
			delete(openBlock, o.Entity)
			f.blocks[i] = ids[o.Entity] + "@" + p.nodes[o.Node].id + ":" + at(blockFrom[o.Entity]) + "-" + at(o.Tick)
		case ObsResourceDown:
			openDown[[2]int{o.Resource, o.At}] = len(f.downs)
			f.downs = append(f.downs, fmt.Sprintf("%s/%d:%s-?", p.resources[o.Resource].id, o.At, at(o.Tick)))
		case ObsResourceUp:
			key := [2]int{o.Resource, o.At}
			i, ok := openDown[key]
			if !ok {
				f.downs = append(f.downs, fmt.Sprintf("up without a down: %s/%d", p.resources[o.Resource].id, o.At))
				continue
			}
			delete(openDown, key)
			f.downs[i] = f.downs[i][:len(f.downs[i])-1] + at(o.Tick)
		case ObsBreakdownPending:
			f.pending = append(f.pending, fmt.Sprintf("%s/%d:%s", p.resources[o.Resource].id, o.At, at(o.Tick)))
		case ObsCapacity:
			f.capacities = append(f.capacities, fmt.Sprintf("%s@%s=%d", p.resources[o.Resource].id, at(o.Tick), o.Value))
			f.capacitySeqs = append(f.capacitySeqs, o.Seq)
		}
	}
	for i, b := range f.blocks {
		if b[len(b)-1] == '?' {
			f.blocks[i] = b[:len(b)-1] + "-"
		}
	}
	for i, d := range f.downs {
		if d[len(d)-1] == '?' {
			f.downs[i] = d[:len(d)-1] + "-"
		}
	}
	return f
}

func (c calendarCase) wantFacts(t *testing.T) calendarFacts {
	t.Helper()
	pair := func(v jsontext.Value) string {
		var x []any
		if err := json.Unmarshal(v, &x); err != nil || len(x) != 2 {
			t.Fatalf("%s: bad entry %s", c.ID, v)
		}
		return fmt.Sprintf("%v@%v", x[0], x[1])
	}
	to := func(v any) string {
		if v == nil {
			return "-"
		}
		return fmt.Sprint(v)
	}
	f := calendarFacts{starts: map[string][]string{}, capacitySeqs: c.Expected.CapacitySeqs}
	for _, v := range c.Expected.Arrivals {
		f.arrivals = append(f.arrivals, pair(v))
	}
	for n, list := range c.Expected.Starts {
		for _, v := range list {
			f.starts[n] = append(f.starts[n], pair(v))
		}
	}
	for _, v := range c.Expected.Completions {
		f.completions = append(f.completions, pair(v))
	}
	for _, v := range c.Expected.Blocks {
		var x []any
		if err := json.Unmarshal(v, &x); err != nil || len(x) != 4 {
			t.Fatalf("%s: bad block %s", c.ID, v)
		}
		f.blocks = append(f.blocks, fmt.Sprintf("%v@%v:%v-%s", x[0], x[1], x[2], to(x[3])))
	}
	for _, d := range c.Expected.Downs {
		f.downs = append(f.downs, fmt.Sprintf("%v/%v:%v-%s", d[0], d[1], d[2], to(d[3])))
	}
	for _, d := range c.Expected.Pending {
		f.pending = append(f.pending, fmt.Sprintf("%v/%v:%v", d[0], d[1], d[2]))
	}
	for _, d := range c.Expected.Capacities {
		f.capacities = append(f.capacities, fmt.Sprintf("%v@%v=%v", d[0], d[1], d[2]))
	}
	return f
}

func compareCalendarFacts(t *testing.T, got, want calendarFacts, arrivals bool) {
	t.Helper()
	list := func(what string, g, w []string) {
		if !slices.Equal(g, w) && !(len(g) == 0 && len(w) == 0) {
			t.Errorf("%s %v, hand-derived %v", what, g, w)
		}
	}
	if arrivals {
		list("arrivals", got.arrivals, want.arrivals)
	}
	for n, w := range want.starts {
		list("starts at "+n, got.starts[n], w)
	}
	for n := range got.starts {
		if _, ok := want.starts[n]; !ok {
			t.Errorf("unexpected starts at %s: %v", n, got.starts[n])
		}
	}
	list("completions", got.completions, want.completions)
	list("blocks", got.blocks, want.blocks)
	list("downs", got.downs, want.downs)
	list("waiting failures", got.pending, want.pending)
	list("units on shift", got.capacities, want.capacities)
	if want.capacitySeqs != nil && !slices.Equal(got.capacitySeqs, want.capacitySeqs) {
		t.Errorf("Capacity sequence numbers %v, hand-derived %v", got.capacitySeqs, want.capacitySeqs)
	}
}

// startSeqsIn is startSeqs in the model's time unit.
func startSeqsIn(p *Program, obs []Observation) []string {
	ms, _ := p.unit.Millis()
	ids := map[uint64]string{}
	out := []string{}
	for _, o := range obs {
		switch o.Kind {
		case ObsArrival:
			ids[o.Entity] = FormatEntityID(0, p.nodes[o.Node].id, uint64(o.Value))
		case ObsServiceStart:
			out = append(out, fmt.Sprintf("%s@%v#%d", ids[o.Entity], float64(o.Tick)/float64(ms), o.Seq))
		}
	}
	return out
}

// Every calendar golden but the stall (G-D1, below): arrivals, service
// starts, completions, blocks, downtimes, waiting failures and every
// change of the units on shift (resource, time, S), against the hand
// derivation, and where the case gives them the sequence numbers of the
// Capacity observations and of the service starts and the replication's
// event count. These fix D-052 (a shift end lets the service in progress
// finish, a start wakes the held front without a release, one
// CAPACITY_CHANGE per calendar observing only the resources that change),
// D-051 (ii) (the failure clock runs on shift only, G-C6, G-F6, G-D5,
// G-T5), D-074 (a) B with a calendar (G-CF1, D-081 (c)), D-050 B1 with a
// shift (G-K2) and B2's next_fit and X_end (G-D3C, G-D4C, G-D5), and
// D-053 (G-R1, G-R5, G-R7).
func TestCalendarGoldens(t *testing.T) {
	ran := 0
	for _, c := range calendarGoldens(t) {
		if c.Expected.Stalled != nil {
			continue
		}
		ran++
		t.Run(c.ID, func(t *testing.T) {
			p := c.compile(t)
			ir := c.ir(t)
			calendared := slices.ContainsFunc(ir.Resources, func(r model.Resource) bool { return r.CalendarID != "" })
			if p.HasCalendars() != calendared {
				t.Errorf("HasCalendars %v, model has a calendar: %v", p.HasCalendars(), calendared)
			}
			col := &collect{}
			out, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col)
			if err != nil {
				t.Fatal(err)
			}
			if out.Created != out.Exited+out.Censored {
				t.Errorf("accounting %+v", out)
			}
			compareCalendarFacts(t, observedCalendarFacts(t, p, col.all), c.wantFacts(t), c.Expected.Arrivals != nil)
			if c.Expected.Events != nil && out.Events != *c.Expected.Events {
				t.Errorf("%d events, hand-derived %d", out.Events, *c.Expected.Events)
			}
			if c.Expected.StartSeqs != nil {
				want := []string{}
				for _, s := range c.Expected.StartSeqs {
					want = append(want, fmt.Sprintf("%v@%v#%v", s[0], s[1], s[2]))
				}
				if got := startSeqsIn(p, col.all); !slices.Equal(got, want) {
					t.Errorf("service starts with their sequence numbers %v, hand-derived %v", got, want)
				}
			}
		})
	}
	if ran != 19 {
		t.Errorf("%d calendar goldens ran, want 19", ran)
	}
}

// G-T5 (D-048): at tick 6 the CAPACITY_CHANGE (scheduled at t=0 before the
// failure clock, D-081 (d)) comes before the FAILURE, whose target is
// reached exactly at the shift's end (D-051): the unit, off shift and
// idle, goes down at once.
func TestShiftEndBeforeFailure(t *testing.T) {
	c := calendarGolden(t, "G-T5")
	p := c.compile(t)
	col := &collect{}
	if _, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range col.all {
		if o.Tick != 6*60_000 {
			continue
		}
		switch o.Kind {
		case ObsCapacity:
			got = append(got, fmt.Sprintf("capacity %s %d", p.resources[o.Resource].id, o.Value))
		case ObsResourceDown:
			got = append(got, fmt.Sprintf("down %s %d", p.resources[o.Resource].id, o.At))
		default:
			got = append(got, fmt.Sprintf("kind %d", o.Kind))
		}
	}
	if !slices.Equal(got, c.Expected.AtTick6) {
		t.Errorf("tick 6: %v, hand-derived %v", got, c.Expected.AtTick6)
	}
}

// G-K2 (T-047 review round 2, I1, with a shift): a held candidate that
// fits the capacity C but not the units on shift is capacity-bound. With
// a blocked entity and nothing in service, B1 must not call it a deadlock
// (the bound is in_use + units > C, not >= and not min(S, C - D)), and the
// shift start, which raises the capacity without a release, must wake it.
func TestShiftBoundIsNoDeadlock(t *testing.T) {
	c := calendarGolden(t, "G-K2")
	p := c.compile(t)
	if !p.HasFiniteBuffers() || !p.HasCalendars() {
		t.Fatal("G-K2 needs a finite buffer (B1 runs) and a calendar")
	}
	k, out, err := runKernel(context.Background(), p, 0, NewBudget(1_000_000), nil)
	if err != nil {
		t.Fatalf("G-K2 failed: %v", err)
	}
	if out.Exited != 2 || k.held != 0 || k.nBlocked != 0 {
		t.Errorf("outcome %+v, held %d, blocked %d", out, k.held, k.nBlocked)
	}
	// The check itself, at the state of tick 4: e1 held off shift, e2
	// blocked at src, nothing in service.
	var ir model.ModelIR
	if err := model.Decode(c.Model, &ir); err != nil {
		t.Fatal(err)
	}
	in := input(ir)
	in.Config = c.Config
	in.Config.Horizon.Value = 4
	k, _, err = runKernel(context.Background(), compile(t, in), 0, NewBudget(1_000_000), nil)
	if err != nil || k.held != 1 || k.nBlocked != 1 || k.inUse[0] != 0 || k.capS[0] != 0 || k.inService != 0 {
		t.Fatalf("state at 4: %v, held %d, blocked %d, in use %v, S %v", err, k.held, k.nBlocked, k.inUse, k.capS)
	}
	if err := k.deadlock(); err != nil {
		t.Errorf("a shift-bound held candidate is reported as a deadlock: %v", err)
	}
}

// G-D1 (D-050 B2 with a calendar): with R4 bypassed, the machine fails as
// its partner's shift starts and the request is never granted: ErrStalled
// at the end of tick next_fit(since + TTRmax) = 960, not before (at 490 the
// operator is off shift) and not at the horizon.
func TestStallWithCalendar(t *testing.T) {
	c := calendarGolden(t, "G-D1")
	p := c.compile(t)
	col := &collect{}
	_, _, err := runKernel(context.Background(), p, 0, NewBudget(1_000_000), col)
	var se *StalledError
	if !errors.As(err, &se) || !errors.Is(err, ErrStalled) {
		t.Fatalf("want ErrStalled, got %v", err)
	}
	w := c.Expected.Stalled
	if se.Tick != w.Tick || se.Entity != w.Entity || se.Node != w.Node || se.Since != w.Since || se.Deadline != w.Deadline || se.Reason() != w.Reason {
		t.Errorf("stall %+v, hand-derived %+v", *se, *w)
	}
	compareCalendarFacts(t, observedCalendarFacts(t, p, col.all), c.wantFacts(t), false)
	if last := col.all[len(col.all)-1]; last.Tick != w.Tick {
		t.Errorf("the run went on to tick %d", last.Tick)
	}
}

// D-050 B2's deadline (D-081 (e)): next_fit(max(since, X_end) + TTRmax),
// computed when a request enters the state; without a calendar it is
// since + TTRmax (T-048), with no bound on the repair time none.
func TestStallDeadlines(t *testing.T) {
	for _, c := range []struct {
		id              string
		since, deadline int64 // minutes
	}{
		{"G-D3C", 900, 1800},  // TTRmax 0, X_end 0: next_fit(900), the next day shift
		{"G-D4C", 10, 144540}, // X_end 143160 (the last exception of a): next_fit(143160)
		{"G-D5", 1450, 4320},  // next_fit(max(1450, X_end 1920) + TTRmax 1500) = next_fit(3420)
		{"G-D1", 10, 960},     // no calendar on the machine: next_fit(490), the operator's shift
		{"G-CF1", 2, 4},       // TTRmax 2: next_fit(4) = 4 (on shift)
		{"G-CF1", 6, 10},      // next_fit(8) = 10, the next shift
		{"G-F6", 25, 30},      // next_fit(25 + 1) = 30
		{"G-C1", 8, 10},       // no breakdowns: next_fit(8) = 10
		{"G-C1", 10, 10},      // on shift: the tick itself
		{"G-C6", 12, 17},      // after the closed exception: next_fit(max(12, X_end 15) + TTRmax 2) = 17
	} {
		p := calendarGolden(t, c.id).compile(t)
		n := slices.IndexFunc(p.nodes, func(n cnode) bool { return n.id == "work" })
		k := &kernel{r: newReplication(p, 0)}
		k.r.now = c.since * 60_000
		want := c.deadline * 60_000
		if d := k.deadlineOf(n); d != want {
			t.Errorf("%s since %d: deadline %d, hand-derived %d", c.id, c.since, d, want)
		}
	}
	// A repair time with no bound: no deadline (D-050).
	c := calendarGolden(t, "G-D1")
	var ir model.ModelIR
	if err := model.Decode(c.Model, &ir); err != nil {
		t.Fatal(err)
	}
	mean := 480.0
	ir.Resources[0].Breakdowns.TimeToRepair.Distribution = model.Distribution{Type: model.DistExponential, Mean: &mean}
	in := input(ir)
	in.Config = c.Config
	var p *Program
	var err error
	withoutValidator(t, func() { p, err = Compile(in) })
	if err != nil {
		t.Fatal(err)
	}
	k := &kernel{r: newReplication(p, 0)}
	k.r.now = 10 * 60_000
	if d := k.deadlineOf(slices.IndexFunc(p.nodes, func(n cnode) bool { return n.id == "work" })); d != -1 {
		t.Errorf("an unbounded repair time has the deadline %d", d)
	}
}

// The observation stream of every calendar golden the validator accepts
// and of the four stochastic reference cases (replication 0) is pinned
// (testdata/simulation/kernel-observation-digests-0.4.0.json, a regression
// lock taken from des-go-0.4.0 after the hand-derived goldens passed, not
// an oracle). Any change to the order of capacity changes, failures,
// repairs, arrivals and dispatches, or to their sequence numbers, shows up
// here. The older locks hold unchanged (TestBlockingObservationDigests,
// TestBreakdownObservationDigests).
func TestCalendarObservationDigests(t *testing.T) {
	var lock struct {
		EngineVersion string `json:"engine_version"`
		Cases         []struct {
			ID           string `json:"id"`
			Observations int    `json:"observations"`
			Digest       string `json:"sha256"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/kernel-observation-digests-0.4.0.json")), &lock); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases     []calendarCase `json:"cases"`
		Reference struct {
			Cases []calendarCase `json:"cases"`
		} `json:"reference"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/calendar-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var order []string
	for _, c := range append(doc.Cases, doc.Reference.Cases...) {
		if c.Validator != nil {
			continue // G-D1: refused by the validator; TestStallWithCalendar checks its observations
		}
		col := &collect{}
		if _, err := RunReplication(context.Background(), compile(t, c.input(t)), 0, NewBudget(10_000_000), col); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		got[c.ID] = fmt.Sprintf("%d %s", len(col.all), observationDigestAt(col.all))
		order = append(order, c.ID)
	}
	want := map[string]string{}
	for _, c := range lock.Cases {
		want[c.ID] = fmt.Sprintf("%d %s", c.Observations, c.Digest)
	}
	if lock.EngineVersion != "des-go-0.4.0" || len(got) != 23 || !maps.Equal(got, want) {
		var rows []string
		for _, id := range order {
			rows = append(rows, fmt.Sprintf("%s=%s", id, got[id]))
		}
		t.Errorf("observation digests\n got  %v\n want %v", rows, want)
	}
}

func (c calendarCase) input(t *testing.T) simulation.RunInput {
	t.Helper()
	in := input(c.ir(t))
	in.Config = c.Config
	return in
}
