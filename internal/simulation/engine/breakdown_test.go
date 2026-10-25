package engine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The hand-derived goldens of resource breakdowns (ADR-026 D-048, D-050,
// D-051; T-048 interpretations D-074; testdata/simulation/
// breakdown-goldens.json, HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN, each
// case with its derivation). The Result members of the same cases are
// checked in internal/simulation/des.

type breakdownCase struct {
	ID        string               `json:"id"`
	Model     jsontext.Value       `json:"model"`
	Config    simulation.RunConfig `json:"config"`
	Validator *struct {
		CodesAt [][2]string `json:"codes_at"`
	} `json:"validator"`
	Expected struct {
		Starts      map[string][]jsontext.Value `json:"starts"`
		Completions []jsontext.Value            `json:"completions"`
		Blocks      []jsontext.Value            `json:"blocks"`
		Downs       [][4]any                    `json:"downs"`
		Pending     [][3]any                    `json:"pending"`
		StartSeqs   [][3]any                    `json:"service_start_seqs"`
		Events      *int64                      `json:"events"`
		AtTick8     []jsontext.Value            `json:"at_tick_8"`
		Deadlock    *struct {
			Tick   int64  `json:"tick"`
			Entity string `json:"entity"`
			Node   string `json:"node"`
			Since  int64  `json:"since"`
			Reason string `json:"reason"`
		} `json:"deadlock"`
		Stalled *struct {
			Tick     int64  `json:"tick"`
			Entity   string `json:"entity"`
			Node     string `json:"node"`
			Since    int64  `json:"since"`
			Deadline int64  `json:"deadline"`
			Reason   string `json:"reason"`
		} `json:"stalled"`
	} `json:"expected"`
}

func breakdownGoldens(t testing.TB) map[string]breakdownCase {
	t.Helper()
	var doc struct {
		Status string          `json:"status"`
		Cases  []breakdownCase `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/breakdown-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Status != "HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN" || len(doc.Cases) == 0 {
		t.Fatalf("breakdown goldens: status %q, %d cases", doc.Status, len(doc.Cases))
	}
	out := map[string]breakdownCase{}
	for _, c := range doc.Cases {
		out[c.ID] = c
	}
	return out
}

// breakdownSeeds are the models of breakdown-goldens.json, for the fuzzers.
func breakdownSeeds(tb testing.TB) []jsontext.Value {
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
	if err := json.Unmarshal(contracttest.ReadFile(tb, filepath.Join(contracttest.Root(tb), "testdata/simulation/breakdown-goldens.json")), &doc); err != nil {
		tb.Fatal(err)
	}
	var out []jsontext.Value
	for _, c := range append(doc.Cases, doc.Reference.Cases...) {
		out = append(out, c.Model)
	}
	return out
}

func (c breakdownCase) input(t *testing.T) simulation.RunInput {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(c.Model, &ir); err != nil {
		t.Fatal(err)
	}
	in := input(ir)
	in.Config = c.Config
	return in
}

// facts is the blocking golden's facts of the same case.
func (c breakdownCase) facts(t *testing.T) facts {
	return expectedFacts(t, blockingCase{ID: c.ID, Expected: struct {
		Starts      map[string][]jsontext.Value `json:"starts"`
		Completions []jsontext.Value            `json:"completions"`
		Blocks      []jsontext.Value            `json:"blocks"`
		Dispatch13  []jsontext.Value            `json:"dispatch_at_13"`
		Deadlock    *struct {
			Tick   int64  `json:"tick"`
			Entity string `json:"entity"`
			Node   string `json:"node"`
			Since  int64  `json:"since"`
			Reason string `json:"reason"`
		} `json:"deadlock"`
		Before *struct {
			Horizon float64 `json:"horizon"`
		} `json:"before_the_deadlock"`
	}{Starts: c.Expected.Starts, Completions: c.Expected.Completions, Blocks: c.Expected.Blocks}})
}

// compileCase compiles a case, past the validator when the case says the
// validator refuses it (after checking that it does, with exactly the
// codes the case lists).
func (c breakdownCase) compile(t *testing.T) *Program {
	t.Helper()
	in := c.input(t)
	if c.Validator == nil {
		return compile(t, in)
	}
	_, err := Compile(in)
	var ie *InputError
	if !errors.As(err, &ie) {
		t.Fatalf("%s: the validator accepted it: %v", c.ID, err)
	}
	var got, want [][2]string
	for _, is := range ie.Issues {
		got = append(got, [2]string{is.Code, is.Path})
	}
	for _, w := range c.Validator.CodesAt {
		want = append(want, [2]string{w[0], "/model" + w[1]})
	}
	if !sameSet(got, want) {
		t.Errorf("%s: validator %v, want %v", c.ID, got, want)
	}
	var p *Program
	withoutValidator(t, func() { p, err = Compile(in) })
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// downs and pending rebuilt from the observations, in model minutes:
// [resource, unit, from, to] in the order the downtimes started (to "-"
// while still down) and [resource, unit, minute] of every failure that
// waited for a released or repaired unit.
func breakdownFacts(p *Program, obs []Observation) (downs, pending []string) {
	minute := func(tick int64) string { return fmt.Sprint(float64(tick) / 60_000) }
	open := map[[2]int]int{}
	for _, o := range obs {
		key := [2]int{o.Resource, o.At}
		switch o.Kind {
		case ObsResourceDown:
			open[key] = len(downs)
			downs = append(downs, fmt.Sprintf("%s/%d:%s-", p.resources[o.Resource].id, o.At, minute(o.Tick)))
		case ObsResourceUp:
			i, ok := open[key]
			if !ok {
				downs = append(downs, fmt.Sprintf("up without a down: %s/%d at %s", p.resources[o.Resource].id, o.At, minute(o.Tick)))
				continue
			}
			delete(open, key)
			downs[i] += minute(o.Tick)
		case ObsBreakdownPending:
			pending = append(pending, fmt.Sprintf("%s/%d:%s", p.resources[o.Resource].id, o.At, minute(o.Tick)))
		}
	}
	for i := range downs {
		if downs[i][len(downs[i])-1] == '-' {
			downs[i] += "-"
		}
	}
	return downs, pending
}

func (c breakdownCase) wantBreakdowns() (downs, pending []string) {
	for _, d := range c.Expected.Downs {
		to := "-"
		if d[3] != nil {
			to = fmt.Sprint(d[3])
		}
		downs = append(downs, fmt.Sprintf("%v/%v:%v-%s", d[0], d[1], d[2], to))
	}
	for _, d := range c.Expected.Pending {
		pending = append(pending, fmt.Sprintf("%v/%v:%v", d[0], d[1], d[2]))
	}
	return downs, pending
}

// G-F1, G-F1W, G-F1W10, G-F2, G-F3, G-F3b, G-F7, G-F8, G-F9, G-F9R10,
// G-F10, G-T1, G-T2, G-T4, G-K1, G-S2: service starts, completions, blocks,
// every downtime and every waiting failure, against the hand derivation,
// and where the case gives them the sequence number of each service start
// and the replication's event count. G-F3 and G-F9 fix D-074 (a) option B
// (review round 1, M1): a repaired unit goes to the waiting failures
// first; G-F3b is ADR-026's planned G-F3 under it (review round 2, L1).
// G-F9R10's waiting failures fix REPAIR's own order (its next FAILURE
// before the waiting failure's REPAIR, SIMULATION_SPEC §3) and G-F10's
// sequence numbers and event count that a repair handing its unit over
// schedules no dispatch (review round 2, L2).
func TestBreakdownGoldens(t *testing.T) {
	cases := breakdownGoldens(t)
	for _, id := range []string{"G-F1", "G-F1W", "G-F1W10", "G-F2", "G-F3", "G-F3b", "G-F7", "G-F8", "G-F9", "G-F9R10", "G-F10", "G-T1", "G-T2", "G-T4", "G-K1", "G-S2"} {
		t.Run(id, func(t *testing.T) {
			c, ok := cases[id]
			if !ok {
				t.Fatalf("no case %s", id)
			}
			p := c.compile(t)
			if !p.HasBreakdowns() {
				t.Error("HasBreakdowns false")
			}
			col := &collect{}
			out, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col)
			if err != nil {
				t.Fatal(err)
			}
			if out.Created != out.Exited+out.Censored {
				t.Errorf("accounting %+v", out)
			}
			compareFacts(t, id, observedFacts(p, col.all), c.facts(t))
			downs, pending := breakdownFacts(p, col.all)
			wd, wp := c.wantBreakdowns()
			if !slices.Equal(downs, wd) {
				t.Errorf("downs %v, hand-derived %v", downs, wd)
			}
			if !slices.Equal(pending, wp) {
				t.Errorf("waiting failures %v, hand-derived %v", pending, wp)
			}
			if c.Expected.Events != nil && out.Events != *c.Expected.Events {
				t.Errorf("%d events, hand-derived %d", out.Events, *c.Expected.Events)
			}
			if c.Expected.StartSeqs != nil {
				got, want := startSeqs(p, col.all), []string{}
				for _, s := range c.Expected.StartSeqs {
					want = append(want, fmt.Sprintf("%v@%v#%v", s[0], s[1], s[2]))
				}
				if !slices.Equal(got, want) {
					t.Errorf("service starts with their sequence numbers %v, hand-derived %v", got, want)
				}
			}
		})
	}
}

// startSeqs lists each service start as entity@minute#seq, seq being the
// sequence number of the dispatch event that granted it.
func startSeqs(p *Program, obs []Observation) []string {
	ids := map[uint64]string{}
	out := []string{}
	for _, o := range obs {
		switch o.Kind {
		case ObsArrival:
			ids[o.Entity] = FormatEntityID(0, p.nodes[o.Node].id, uint64(o.Value))
		case ObsServiceStart:
			out = append(out, fmt.Sprintf("%s@%v#%d", ids[o.Entity], float64(o.Tick)/60_000, o.Seq))
		}
	}
	return out
}

// G-T4 (D-048 golden 4): at 8 the dispatch that unblocks e4 releases ma
// to its waiting failure, whose 0-tick repair runs after that dispatch
// (priority 15, same tick) and schedules a second dispatch in the tick,
// which grants e5 at 8.
func TestZeroTickRepairDispatchesAgain(t *testing.T) {
	c := breakdownGoldens(t)["G-T4"]
	p := c.compile(t)
	col := &collect{}
	if _, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col); err != nil {
		t.Fatal(err)
	}
	ids := map[uint64]string{}
	var got []string
	var seq uint64
	for _, o := range col.all {
		if o.Kind == ObsArrival {
			ids[o.Entity] = FormatEntityID(0, p.nodes[o.Node].id, uint64(o.Value))
		}
		if o.Tick != 8*60_000 {
			continue
		}
		if len(got) > 0 && o.Seq != seq && (o.Kind == ObsResourceUp || (o.Kind == ObsResource && got[len(got)-1] == "up ma 0")) {
			got = append(got, "next event")
		}
		seq = o.Seq
		switch o.Kind {
		case ObsResource:
			got = append(got, fmt.Sprintf("resource %s %d", p.resources[o.Resource].id, o.Value))
		case ObsResourceDown:
			got = append(got, fmt.Sprintf("down %s %d", p.resources[o.Resource].id, o.At))
		case ObsResourceUp:
			got = append(got, fmt.Sprintf("up %s %d", p.resources[o.Resource].id, o.At))
		case ObsArrival:
			got = append(got, fmt.Sprintf("arrival %s %s", ids[o.Entity], p.nodes[o.Node].id))
		case ObsQueueEnter:
			got = append(got, fmt.Sprintf("queue_enter %s %s", ids[o.Entity], p.nodes[o.Node].id))
		case ObsServiceStart:
			got = append(got, fmt.Sprintf("service_start %s %s", ids[o.Entity], p.nodes[o.Node].id))
		case ObsServiceEnd:
			got = append(got, fmt.Sprintf("service_end %s %s", ids[o.Entity], p.nodes[o.Node].id))
		case ObsComplete:
			got = append(got, fmt.Sprintf("complete %s %s", ids[o.Entity], p.nodes[o.Node].id))
		case ObsUnblocked:
			got = append(got, fmt.Sprintf("unblocked %s %s", ids[o.Entity], p.nodes[o.Node].id))
		default:
			got = append(got, fmt.Sprintf("kind %d", o.Kind))
		}
	}
	var want []string
	for _, v := range c.Expected.AtTick8 {
		var s string
		if json.Unmarshal(v, &s) == nil {
			want = append(want, s)
			continue
		}
		var x []any
		if err := json.Unmarshal(v, &x); err != nil || len(x) != 3 {
			t.Fatalf("bad entry %s", v)
		}
		want = append(want, fmt.Sprintf("%v %v %v", x[0], x[1], x[2]))
	}
	if !slices.Equal(got, want) {
		t.Errorf("tick 8:\n got  %v\n want %v", got, want)
	}
	// The second dispatch is a separate event after the repair, in the same tick.
	var dispatches []uint64
	for _, o := range col.all {
		if o.Tick == 8*60_000 && o.Kind == ObsServiceStart && !slices.Contains(dispatches, o.Seq) {
			dispatches = append(dispatches, o.Seq)
		}
	}
	if len(dispatches) != 2 {
		t.Errorf("tick 8 has %d dispatches granting something, want 2", len(dispatches))
	}
}

// G-K1 (T-047 review round 2, I1): a held candidate whose bundle fits the
// full capacity but not the capacity left by a downtime is
// capacity-bound: B1 must not call it a deadlock (in_use + units > C, not
// >=), and the repair, which raises the capacity without a release, must
// wake it.
func TestCapacityBoundIsNoDeadlock(t *testing.T) {
	c := breakdownGoldens(t)["G-K1"]
	p := c.compile(t)
	if !p.HasFiniteBuffers() {
		t.Fatal("G-K1 needs a finite buffer, or B1 never runs")
	}
	k, out, err := runKernel(context.Background(), p, 0, NewBudget(1_000_000), nil)
	if err != nil {
		t.Fatalf("G-K1 failed: %v", err)
	}
	if out.Exited != 1 || k.held != 0 {
		t.Errorf("outcome %+v, held %d", out, k.held)
	}
	// The check itself, at the state of tick 2: held, nothing in use, the unit down.
	in := c.input(t)
	in.Config.Horizon.Value = 5
	k, _, err = runKernel(context.Background(), compile(t, in), 0, NewBudget(1_000_000), nil)
	if err != nil || k.held != 1 || k.down[0] != 1 || k.inUse[0] != 0 || k.inService != 0 {
		t.Fatalf("state at 5: %v, held %d, down %v, in use %v", err, k.held, k.down, k.inUse)
	}
	if err := k.deadlock(); err != nil {
		t.Errorf("a capacity-bound held candidate is reported as a deadlock: %v", err)
	}
}

// G-D4: breakdown events beside a circular wait do not hide it from B1.
func TestDeadlockBesideBreakdowns(t *testing.T) {
	c := breakdownGoldens(t)["G-D4"]
	p := c.compile(t)
	col := &collect{}
	_, _, err := runKernel(context.Background(), p, 0, NewBudget(1_000_000), col)
	var de *DeadlockError
	if !errors.As(err, &de) {
		t.Fatalf("want ErrDeadlock, got %v", err)
	}
	w := c.Expected.Deadlock
	if de.Tick != w.Tick || de.Entity != w.Entity || de.Node != w.Node || de.Since != w.Since || de.Reason() != w.Reason {
		t.Errorf("deadlock %+v, hand-derived %+v", *de, *w)
	}
	compareFacts(t, c.ID, observedFacts(p, col.all), c.facts(t))
	downs, _ := breakdownFacts(p, col.all)
	if wd, _ := c.wantBreakdowns(); !slices.Equal(downs, wd) {
		t.Errorf("downs %v, hand-derived %v", downs, wd)
	}
}

// G-S1 (D-050 B2): with R3 bypassed, two failure clocks 10 apart keep a
// unit down for ever and the 2-unit request is never granted: ErrStalled
// at the end of the deadline tick, not before and not at the horizon.
func TestStallIsATypedError(t *testing.T) {
	c := breakdownGoldens(t)["G-S1"]
	p := c.compile(t)
	col := &collect{}
	_, _, err := runKernel(context.Background(), p, 0, NewBudget(1_000_000), col)
	var se *StalledError
	if !errors.Is(err, ErrStalled) || !errors.As(err, &se) {
		t.Fatalf("want ErrStalled, got %v", err)
	}
	w := c.Expected.Stalled
	if se.Tick != w.Tick || se.Entity != w.Entity || se.Node != w.Node || se.Since != w.Since || se.Deadline != w.Deadline || se.Reason() != w.Reason {
		t.Errorf("stall %+v (%q), hand-derived %+v", *se, se.Reason(), *w)
	}
	if last := col.all[len(col.all)-1]; last.Tick != w.Tick {
		t.Errorf("observations after the stall tick: %+v", last)
	}
	compareFacts(t, c.ID, observedFacts(p, col.all), c.facts(t))
	downs, pending := breakdownFacts(p, col.all)
	wd, wp := c.wantBreakdowns()
	if !slices.Equal(downs, wd) || !slices.Equal(pending, wp) {
		t.Errorf("downs %v pending %v, hand-derived %v %v", downs, pending, wd, wp)
	}
	// An unbounded repair time has no deadline: the same model with an
	// exponential TTR is never reported (D-050: "TTRmax 無上界時不檢查").
	in := c.input(t)
	mean := 10.0
	in.Model.Resources[0].Breakdowns.TimeToRepair.Distribution = model.Distribution{Type: model.DistExponential, Mean: &mean}
	withoutValidator(t, func() { p, err = Compile(in) })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runKernel(context.Background(), p, 0, NewBudget(1_000_000), nil); errors.Is(err, ErrStalled) {
		t.Errorf("an unbounded TTR was given a deadline: %v", err)
	}
}

// Two copies of G-S1 side by side (the second with every ID suffixed "2",
// so its source sorts after the first's and its request queues after it
// at 6): both are in B2's state from 20 and due at 30; ErrStalled names
// the earliest request in queue order, r0/sx/1 (D-074 (c)).
func TestStallNamesTheEarliestRequest(t *testing.T) {
	in := breakdownGoldens(t)["G-S1"].input(t)
	ir := in.Model
	suffix := func(id string) string {
		if id == "done" {
			return id
		}
		return id + "2"
	}
	for _, r := range slices.Clone(ir.Resources) {
		r.ID, r.Name = suffix(r.ID), suffix(r.Name)
		ir.Resources = append(ir.Resources, r)
	}
	for _, n := range slices.Clone(ir.Nodes) {
		if n.Type == model.NodeSink {
			continue
		}
		n.ID = suffix(n.ID)
		var reqs []model.ResourceRequirement
		for _, q := range n.Requires {
			reqs = append(reqs, model.ResourceRequirement{ResourceID: suffix(q.ResourceID), Units: q.Units})
		}
		n.Requires = reqs
		ir.Nodes = append(ir.Nodes, n)
	}
	for _, f := range slices.Clone(ir.Flows) {
		f.ID, f.From, f.To = f.ID+"2", suffix(f.From), suffix(f.To)
		ir.Flows = append(ir.Flows, f)
	}
	in.Model = ir
	var p *Program
	var err error
	withoutValidator(t, func() { p, err = Compile(in) })
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runKernel(context.Background(), p, 0, NewBudget(1_000_000), nil)
	var se *StalledError
	if !errors.As(err, &se) {
		t.Fatalf("want ErrStalled, got %v", err)
	}
	if se.Tick != 30*60_000 || se.Entity != "r0/sx/1" || se.Node != "x" || se.Since != 20*60_000 || se.Deadline != 30*60_000 {
		t.Errorf("stall %+v, want r0/sx/1 at x, since 1200000, due and found at 1800000", *se)
	}
}

// G-S2 (D-074 (c)): B2 checks at the end of a tick. At the deadline tick
// an arrival that needs no dispatch comes before the repair that lets the
// held request in; a check right after the arrival would report a stall.
func TestNoStallBeforeTheTickEnds(t *testing.T) {
	c := breakdownGoldens(t)["G-S2"]
	if _, err := RunReplication(context.Background(), c.compile(t), 0, NewBudget(1_000_000), nil); err != nil {
		t.Fatalf("G-S2: %v", err)
	}
}

// G-F4 and the streams (D-051): each unit u of a resource that breaks
// down alternates TTF and TTR drawn from its own streams (resource ID,
// ttf/<u>) and (resource ID, ttr/<u>); on a resource no process needs the
// downtimes are exactly those draws, and one more unit of capacity
// leaves units 0..C-1 as they were.
func TestBreakdownStreams(t *testing.T) {
	build := func(capacity int) *Program {
		ir := golden(t, "deterministic-queue.json")
		ttf, ttr := 30.0, 4.0
		ir.Resources = append(ir.Resources, model.Resource{ID: "spare", Type: model.ResourceMachine, Name: "spare", Capacity: capacity,
			QueueDiscipline: model.FIFO, Breakdowns: &model.Breakdowns{
				TimeToFailure: model.TimedDistribution{Distribution: model.Distribution{Type: model.DistExponential, Mean: &ttf}},
				TimeToRepair:  model.TimedDistribution{Distribution: model.Distribution{Type: model.DistExponential, Mean: &ttr}}}})
		in := input(ir)
		in.Config = config(3, model.Day)
		in.Config.Replications = 2
		return compile(t, in)
	}
	type span struct{ from, to int64 }
	downtimes := func(p *Program) map[int][]span {
		col := &collect{}
		if _, err := RunReplication(context.Background(), p, 1, NewBudget(1_000_000), col); err != nil {
			t.Fatal(err)
		}
		out := map[int][]span{}
		for _, o := range col.all {
			switch o.Kind {
			case ObsResourceDown:
				out[o.At] = append(out[o.At], span{o.Tick, -1})
			case ObsResourceUp:
				out[o.At][len(out[o.At])-1].to = o.Tick
			case ObsBreakdownPending:
				t.Fatalf("a failure waited on a resource no process needs: %+v", o)
			}
		}
		return out
	}
	p3 := build(3)
	got3, got4 := downtimes(p3), downtimes(build(4))
	r := slices.IndexFunc(p3.resources, func(c cresource) bool { return c.id == "spare" })
	rep := ReplicationSeed(p3.seed, 1)
	for u := range 4 {
		ttf, ttr := Stream(rep, "spare", "ttf/"+strconv.Itoa(u)), Stream(rep, "spare", "ttr/"+strconv.Itoa(u))
		var want []span
		up := int64(0)
		for {
			down := up + p3.resources[r].ttf.draw(ttf)
			if down > p3.horizon {
				break
			}
			up = down + p3.resources[r].ttr.draw(ttr)
			to := up
			if up > p3.horizon {
				to = -1
			}
			want = append(want, span{down, to})
			if to < 0 {
				break
			}
		}
		if len(want) < 10 {
			t.Fatalf("unit %d: only %d downtimes in 3 days", u, len(want))
		}
		if !slices.Equal(got4[u], want) {
			t.Errorf("capacity 4, unit %d: %v, its streams give %v", u, got4[u], want)
		}
		if u < 3 && !slices.Equal(got3[u], want) {
			t.Errorf("capacity 3, unit %d: %v, its streams give %v", u, got3[u], want)
		}
	}
	if len(got3) != 3 {
		t.Errorf("capacity 3 has failure clocks %v", len(got3))
	}
}

// ADR-026 D-047 step 2 and G-F5: breakdowns whose constant TTF is later
// than the horizon (never scheduled, no sequence number taken) give
// exactly the observations of the model without them, on the frozen
// des-go-0.2.0 kernel, and no Down, Up or Pending observation.
func TestNeutralBreakdownsChangeNothing(t *testing.T) {
	checked, resources := 0, 0
	for seed := range differentialModels / 10 {
		rng := rand.New(rand.NewPCG(uint64(seed), 4802))
		base := randomModel(t, rng)
		if seed%2 == 1 {
			var ok bool
			if base, ok = withFiniteBuffers(base, rng); !ok {
				continue
			}
		}
		in := input(base)
		in.Config = config(float64(30+rng.IntN(240)), model.Minute)
		in.Config.Replications = 3
		in.Limits = differentialLimits[seed%len(differentialLimits)]
		p, err := Compile(in)
		if err != nil {
			continue
		}
		nin := in
		nin.Model.Resources = slices.Clone(base.Resources)
		ttf, ttr := in.Config.Horizon.Value+1+float64(rng.IntN(1000)), float64(rng.IntN(5))
		n := 0
		for i := range nin.Model.Resources {
			if rng.IntN(3) > 0 {
				nin.Model.Resources[i].Breakdowns = &model.Breakdowns{TimeToFailure: model.TimedDistribution{Distribution: constant(ttf)},
					TimeToRepair: model.TimedDistribution{Distribution: constant(ttr)}}
				n++
			}
		}
		if n == 0 {
			continue
		}
		var np *Program
		// R3 may refuse a neutral breakdown on a 2-unit requirement; the
		// kernel must still run it as if it were absent.
		withoutValidator(t, func() { np, err = Compile(nin) })
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !np.HasBreakdowns() {
			t.Fatalf("seed %d: no breakdowns", seed)
		}
		checked++
		resources += n
		index := uint32(seed % 3)
		nc, oc := &collect{}, &collect{}
		_, nout, nerr := runKernel(context.Background(), np, index, NewBudget(200_000), nc)
		_, oout, oerr := runKernel020(context.Background(), p, index, NewBudget(200_000), oc)
		if d := sameRun(kernelRun{nc.all, nout, nerr}, kernelRun{oc.all, oout, oerr}); d != "" {
			t.Fatalf("seed %d: neutral breakdowns: %s", seed, d)
		}
	}
	t.Logf("%d neutral models (%d resources with breakdowns) identical to des-go-0.2.0", checked, resources)
	if checked < 1000 {
		t.Fatalf("only %d neutral models", checked)
	}
}

// R3 (D-050) holds for every valid model the kernel runs; the validator,
// not the kernel, refuses the others.
func TestCompileRefusesR3(t *testing.T) {
	c := breakdownGoldens(t)["G-S1"]
	_, err := Compile(c.input(t))
	issueExactly(t, err, "/model/nodes/1/requires/0/units", validate.ResourceRequirementInvalid)
}

// Compile checks the manifest and the datasets of resource distributions
// as it does a node's (T-048): an empirical time to repair needs its
// binding, its samples are quantized in the dataset's unit, and a 0-tick
// sample in an empirical time to failure is DISTRIBUTION_INVALID (D-033).
func TestCompileBreakdownDatasets(t *testing.T) {
	withEmpirical := func(member string) simulation.RunInput {
		ir := golden(t, "deterministic-queue.json")
		emp := model.Distribution{Type: model.DistEmpirical, SamplesRef: "dataset:review_duration"}
		b := &model.Breakdowns{TimeToFailure: model.TimedDistribution{Distribution: constant(600)}, TimeToRepair: model.TimedDistribution{Distribution: emp}}
		if member == "time_to_failure" {
			b = &model.Breakdowns{TimeToFailure: model.TimedDistribution{Distribution: emp}, TimeToRepair: model.TimedDistribution{Distribution: constant(5)}}
		}
		ir.Resources[0].Breakdowns = b
		return input(ir)
	}
	in := withEmpirical("time_to_repair")
	in.Manifest, in.Datasets = nil, nil
	_, err := Compile(in)
	issueExactly(t, err, "/model/resources/0/breakdowns/time_to_repair/distribution/samples_ref", validate.DatasetBindingInvalid)
	in = withEmpirical("time_to_repair")
	hours := dataset("dataset:review_duration", model.Hour, 2, 0.5)
	in.Datasets, in.Manifest = map[string]simulation.ResolvedDataset{"dataset:review_duration": hours}, []model.DatasetBinding{hours.Binding}
	p := compile(t, in)
	if r := p.resources[0]; r.ttr == nil || !slices.Equal(r.ttr.values, []int64{7_200_000, 1_800_000}) || r.ttrMax != 7_200_000 {
		t.Errorf("repair sampler %+v, TTRmax %d", r.ttr, r.ttrMax)
	}
	in = withEmpirical("time_to_failure")
	compile(t, in) // samples 120, 240, 360: fine
	zero := reviewDuration
	zero.Samples = []float64{0, 240, 360}
	in.Datasets = map[string]simulation.ResolvedDataset{"dataset:review_duration": zero}
	_, err = Compile(in)
	issueExactly(t, err, "/model/resources/0/breakdowns/time_to_failure/distribution", validate.DistributionInvalid)
}

// observationDigestAt is observationDigest with At as an eighth word: the
// failure clock of the breakdown observations (and the node of a queue
// enter or a block) is part of what is locked.
func observationDigestAt(all []Observation) string {
	h := sha256.New()
	var buf [64]byte
	for _, o := range all {
		for i, v := range []uint64{uint64(o.Kind), uint64(o.Tick), o.Seq, o.Entity, uint64(int64(o.Node)), uint64(int64(o.Resource)), uint64(o.Value), uint64(int64(o.At))} {
			binary.LittleEndian.PutUint64(buf[8*i:], v)
		}
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// The observation stream of every breakdown golden the validator accepts
// and of the four stochastic reference cases (replication 0) is pinned
// (testdata/simulation/kernel-observation-digests-0.3.0.json, a regression
// lock taken from des-go-0.3.0 after the hand-derived goldens passed, not
// an oracle). Any change to the order of failures, repairs, waiting
// failures and dispatches, or to their sequence numbers, shows up here.
func TestBreakdownObservationDigests(t *testing.T) {
	var lock struct {
		EngineVersion string `json:"engine_version"`
		Cases         []struct {
			ID           string `json:"id"`
			Observations int    `json:"observations"`
			Digest       string `json:"sha256"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/kernel-observation-digests-0.3.0.json")), &lock); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases     []breakdownCase `json:"cases"`
		Reference struct {
			Cases []breakdownCase `json:"cases"`
		} `json:"reference"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/breakdown-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range append(doc.Cases, doc.Reference.Cases...) {
		if c.Validator != nil {
			continue // G-D4, G-S1: refused by the validator; the deadlock and stall tests check their observations
		}
		col := &collect{}
		if _, err := RunReplication(context.Background(), compile(t, c.input(t)), 0, NewBudget(10_000_000), col); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		got[c.ID] = fmt.Sprintf("%d %s", len(col.all), observationDigestAt(col.all))
	}
	want := map[string]string{}
	for _, c := range lock.Cases {
		want[c.ID] = fmt.Sprintf("%d %s", c.Observations, c.Digest)
	}
	if lock.EngineVersion != "des-go-0.3.0" || !maps.Equal(got, want) {
		t.Errorf("observation digests\n got  %v\n want %v", got, want)
	}
}

// TTRmax (D-050 B2's deadline) per distribution: a constant's value, a
// triangular's max, an empirical's largest sample (above); none (-1) for
// the unbounded exponential, lognormal and Weibull. A bundle's is its
// resources' largest, 0 without a resource that breaks down.
func TestRepairBound(t *testing.T) {
	lo, mode, hi, mean, cv, shape, scale := 1.0, 2.0, 4.5, 3.0, 0.5, 1.5, 2.0
	for _, c := range []struct {
		d    model.Distribution
		want int64
	}{
		{constant(7), 7 * 60_000},
		{constant(0), 0},
		{model.Distribution{Type: model.DistTriangular, Min: &lo, Mode: &mode, Max: &hi}, 270_000},
		{model.Distribution{Type: model.DistExponential, Mean: &mean}, -1},
		{model.Distribution{Type: model.DistLognormal, Mean: &mean, CV: &cv}, -1},
		{model.Distribution{Type: model.DistWeibull, Shape: &shape, Scale: &scale}, -1},
	} {
		ir := golden(t, "deterministic-queue.json")
		ir.Resources[0].Breakdowns = &model.Breakdowns{TimeToFailure: model.TimedDistribution{Distribution: constant(600)}, TimeToRepair: model.TimedDistribution{Distribution: c.d}}
		p := compile(t, input(ir))
		if got := p.resources[0].ttrMax; got != c.want {
			t.Errorf("%s: TTRmax %d, want %d", c.d.Type, got, c.want)
		}
		if n := p.nodes[p.index["service"]]; n.ttrMax != c.want {
			t.Errorf("%s: the service bundle's TTRmax %d, want %d", c.d.Type, n.ttrMax, c.want)
		}
	}
	if p := compile(t, input(golden(t, "deterministic-queue.json"))); p.nodes[p.index["service"]].ttrMax != 0 {
		t.Errorf("a bundle without breakdowns has TTRmax %d", p.nodes[p.index["service"]].ttrMax)
	}
}
