package engine

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model"
)

// The hand-derived goldens of batch processes (ADR-026 D-047, D-048,
// D-049, D-050, D-054; T-050 interpretations D-096, PROPOSED;
// testdata/simulation/batch-goldens.json, HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN,
// each case with its derivation). The Result members of the same cases
// are checked in internal/simulation/des.

// batchExtras are what batch-goldens.json adds to calendar-goldens.json's
// expected facts.
type batchExtras struct {
	ID       string `json:"id"`
	Expected struct {
		Batches        [][4]jsontext.Value `json:"batches"`
		BatchStartSeqs [][3]jsontext.Value `json:"batch_start_seqs"`
		Routing        *struct {
			Decision  string   `json:"decision"`
			Members   []string `json:"members"`
			At        float64  `json:"at"`
			Threshold float64  `json:"threshold"`
		} `json:"routing_draws_in_member_order"`
	} `json:"expected"`
}

func batchGoldens(t testing.TB) ([]calendarCase, map[string]batchExtras) {
	t.Helper()
	data := contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/batch-goldens.json"))
	var doc struct {
		Status string         `json:"status"`
		Cases  []calendarCase `json:"cases"`
	}
	var extras struct {
		Cases []batchExtras `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &extras); err != nil {
		t.Fatal(err)
	}
	if doc.Status != "HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN" || len(doc.Cases) == 0 || len(doc.Cases) != len(extras.Cases) {
		t.Fatalf("batch goldens: status %q, %d cases", doc.Status, len(doc.Cases))
	}
	out := map[string]batchExtras{}
	for _, x := range extras.Cases {
		out[x.ID] = x
	}
	return doc.Cases, out
}

// batchSeeds are the models of batch-goldens.json, for the fuzzers.
func batchSeeds(tb testing.TB) []jsontext.Value {
	tb.Helper()
	cases, _ := batchGoldens(tb)
	var out []jsontext.Value
	for _, c := range cases {
		out = append(out, c.Model)
	}
	return out
}

// observedBatches rebuilds every batch from the observations: process#n@time
// with its members in FIFO order (BatchStart, then one ServiceStart per
// member in the same event, each carrying the batch number), and the
// sequence number of each BatchStart. It also checks that every member's
// ServiceEnd, Blocked and Unblocked carries its batch's number, that a
// batch's members all end in one event, and that non-members carry none.
func observedBatches(t *testing.T, p *Program, obs []Observation) (batches, seqs []string) {
	t.Helper()
	ms, _ := p.unit.Millis()
	at := func(tick int64) string { return fmt.Sprint(float64(tick) / float64(ms)) }
	ids := map[uint64]string{}
	member := map[uint64]uint64{} // entity -> its current batch number
	ended := map[string]uint64{}  // process#n -> the seq of its members' ServiceEnd
	for i := 0; i < len(obs); i++ {
		o := obs[i]
		switch o.Kind {
		case ObsArrival:
			ids[o.Entity] = FormatEntityID(0, p.nodes[o.Node].id, uint64(o.Value))
		case ObsBatchStart:
			if !p.IsBatch(o.Node) || o.Batch == 0 || o.Value < 1 {
				t.Errorf("BatchStart %+v", o)
			}
			var members []string
			for k := 1; k <= int(o.Value); k++ {
				if i+k >= len(obs) {
					t.Fatalf("BatchStart %+v without its members", o)
				}
				m := obs[i+k]
				if m.Kind != ObsServiceStart || m.Node != o.Node || m.Batch != o.Batch || m.Seq != o.Seq || m.Tick != o.Tick {
					t.Errorf("member %d of %+v: %+v", k, o, m)
				}
				members = append(members, ids[m.Entity])
				member[m.Entity] = o.Batch
			}
			batches = append(batches, fmt.Sprintf("%s#%d@%s:%s", p.nodes[o.Node].id, o.Batch, at(o.Tick), strings.Join(members, ",")))
			seqs = append(seqs, fmt.Sprintf("%s#%d:%d", p.nodes[o.Node].id, o.Batch, o.Seq))
			i += int(o.Value)
		case ObsServiceStart:
			if p.IsBatch(o.Node) || o.Batch != 0 {
				t.Errorf("a ServiceStart outside a batch group: %+v", o)
			}
		case ObsServiceEnd:
			want := uint64(0)
			if p.IsBatch(o.Node) {
				want = member[o.Entity]
				key := fmt.Sprintf("%d#%d", o.Node, want)
				if s, ok := ended[key]; ok && s != o.Seq {
					t.Errorf("the members of %s end in two events", key)
				}
				ended[key] = o.Seq
			}
			if o.Batch != want {
				t.Errorf("ServiceEnd %+v, its batch %d", o, want)
			}
		case ObsBlocked, ObsUnblocked:
			want := uint64(0)
			if p.nodes[o.Node].typ == model.NodeProcess && p.IsBatch(o.Node) {
				want = member[o.Entity]
			}
			if o.Batch != want {
				t.Errorf("%+v, its batch %d", o, want)
			}
		default:
			if o.Batch != 0 {
				t.Errorf("%+v carries a batch", o)
			}
		}
	}
	return batches, seqs
}

func (x batchExtras) want(t *testing.T) (batches, seqs []string) {
	t.Helper()
	for _, b := range x.Expected.Batches {
		var process string
		var n, at float64
		var members []string
		if json.Unmarshal(b[0], &process) != nil || json.Unmarshal(b[1], &n) != nil || json.Unmarshal(b[2], &at) != nil || json.Unmarshal(b[3], &members) != nil {
			t.Fatalf("%s: bad batch %v", x.ID, b)
		}
		batches = append(batches, fmt.Sprintf("%s#%v@%v:%s", process, n, at, strings.Join(members, ",")))
	}
	for _, s := range x.Expected.BatchStartSeqs {
		var process string
		var n, seq float64
		if json.Unmarshal(s[0], &process) != nil || json.Unmarshal(s[1], &n) != nil || json.Unmarshal(s[2], &seq) != nil {
			t.Fatalf("%s: bad seq %v", x.ID, s)
		}
		seqs = append(seqs, fmt.Sprintf("%s#%v:%v", process, n, seq))
	}
	return batches, seqs
}

// Every batch golden but the stalls (G-BT13, G-BT18), the deadlocks
// (G-BT20, G-BT22) and the routing draws (G-BT5), below: service starts,
// completions, blocks, downtimes, waiting failures, units on shift, every
// batch with its members and, where the case gives them, the sequence
// numbers of the BatchStarts and the event count, against the hand
// derivation. These fix D-054 (a batch is ready at max_size, or at
// min_size once its oldest has waited max_wait; one request, one draw,
// members start and end together; a member blocked at the end keeps the
// bundle until the last moves on), D-048 (BATCH_TIMEOUT at priority 15,
// after the tick's arrivals), D-096 (no timeout with min = max, stale ones
// ignored, an expired limit checked at once, as many unblocks as places a
// grant frees) and the interactions with breakdowns and calendars
// (finish-first). Review round 1 added G-BT19 (a BATCH_TIMEOUT after the
// horizon keeps D-050 B1 off: ADR-026 D-050 B1, D-096 (g) A; the run ends
// at the horizon without an error) and G-BT21 (a batch process without
// resources asks for no dispatch at a batch's end).
func TestBatchGoldens(t *testing.T) {
	cases, extras := batchGoldens(t)
	ran := 0
	for _, c := range cases {
		x := extras[c.ID]
		if c.Expected.Stalled != nil || c.Expected.Deadlock != nil || x.Expected.Routing != nil {
			continue
		}
		ran++
		t.Run(c.ID, func(t *testing.T) {
			p := c.compile(t)
			if !p.HasBatches() {
				t.Fatal("HasBatches is false")
			}
			col := &collect{}
			out, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col)
			if err != nil {
				t.Fatal(err)
			}
			if out.Created != out.Exited+out.Censored {
				t.Errorf("accounting %+v", out)
			}
			compareCalendarFacts(t, observedCalendarFacts(t, p, col.all), c.wantFacts(t), false)
			gotB, gotS := observedBatches(t, p, col.all)
			wantB, wantS := x.want(t)
			if !slices.Equal(gotB, wantB) {
				t.Errorf("batches %v\n hand-derived %v", gotB, wantB)
			}
			if wantS != nil && !slices.Equal(gotS, wantS) {
				t.Errorf("BatchStart sequence numbers %v, hand-derived %v", gotS, wantS)
			}
			if c.Expected.Events != nil && out.Events != *c.Expected.Events {
				t.Errorf("%d events, hand-derived %d", out.Events, *c.Expected.Events)
			}
		})
	}
	if ran != 17 {
		t.Errorf("%d batch goldens ran, want 17", ran)
	}
}

// D-050 B2 on a batch request (R4 bypassed): ErrStalled names the batch's
// oldest member (D-096 (g)) and, as for any request, since the end of the
// first tick it is in B2's state (D-074 (c)). G-BT13: ErrStalled at 960,
// the request in the state from the tick it was made (20). G-BT18 (review
// round 1 M1): the batch is ready at 20 while its machine is in use by
// another process until 100, so since is 100, not the ready tick.
func TestBatchStall(t *testing.T) {
	cases, _ := batchGoldens(t)
	ran := 0
	for _, c := range cases {
		if c.Expected.Stalled == nil {
			continue
		}
		ran++
		t.Run(c.ID, func(t *testing.T) {
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
		})
	}
	if ran != 2 {
		t.Errorf("%d batch stall goldens ran, want 2 (G-BT13, G-BT18)", ran)
	}
}

// D-050 B1 with batch processes, past the validator (T-050 review round 1).
// G-BT20 (M2): G-D2's circular wait beside an unrelated batch whose
// BATCH_TIMEOUT is within the horizon: B1 waits for the timeout (D-096 (g),
// A and B alike) and for the batch it starts, then reports at 36 the
// earliest entity still blocked. G-BT22 (L2): no entity blocked, a held
// batch request needing more units than its resource has: ErrDeadlock
// names its oldest member and, as Since, the tick the request was made
// (the batch became ready), not the member's entry (Queued).
func TestBatchDeadlocks(t *testing.T) {
	cases, extras := batchGoldens(t)
	ran := 0
	for _, c := range cases {
		if c.Expected.Deadlock == nil {
			continue
		}
		ran++
		t.Run(c.ID, func(t *testing.T) {
			p := c.compile(t)
			col := &collect{}
			_, _, err := runKernel(context.Background(), p, 0, NewBudget(1_000_000), col)
			var de *DeadlockError
			if !errors.As(err, &de) || !errors.Is(err, ErrDeadlock) {
				t.Fatalf("want ErrDeadlock, got %v", err)
			}
			w := c.Expected.Deadlock
			if de.Tick != w.Tick || de.Entity != w.Entity || de.Node != w.Node || de.Since != w.Since || de.Queued != w.Queued || de.Reason() != w.Reason {
				t.Errorf("deadlock %+v (%q), hand-derived %+v", *de, de.Reason(), *w)
			}
			compareCalendarFacts(t, observedCalendarFacts(t, p, col.all), c.wantFacts(t), false)
			gotB, _ := observedBatches(t, p, col.all)
			if wantB, _ := extras[c.ID].want(t); !slices.Equal(gotB, wantB) {
				t.Errorf("batches %v\n hand-derived %v", gotB, wantB)
			}
		})
	}
	if ran != 2 {
		t.Errorf("%d batch deadlock goldens ran, want 2 (G-BT20, G-BT22)", ran)
	}
}

// G-BT5 (ADR-026 D-054 G-T5): at a batch's end the members' routes are
// resolved in FIFO order, member i taking the i-th draw of the decision's
// routing stream. The draws are computed here from the stream itself.
func TestBatchRoutingDrawsInMemberOrder(t *testing.T) {
	cases, extras := batchGoldens(t)
	i := slices.IndexFunc(cases, func(c calendarCase) bool { return c.ID == "G-BT5" })
	c, w := cases[i], extras["G-BT5"].Expected.Routing
	p := c.compile(t)
	col := &collect{}
	if _, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col); err != nil {
		t.Fatal(err)
	}
	stream := Stream(ReplicationSeed(p.seed, 0), w.Decision, PurposeRouting)
	want := map[string]int64{}
	for _, m := range w.Members {
		outcome := int64(0) // abandoned: test__scrap
		if stream.Uniform() < w.Threshold {
			outcome = 1 // test__done
		}
		want[m] = outcome
	}
	ids := map[uint64]string{}
	got := map[string]int64{}
	var order []string
	for _, o := range col.all {
		switch o.Kind {
		case ObsArrival:
			ids[o.Entity] = FormatEntityID(0, p.nodes[o.Node].id, uint64(o.Value))
		case ObsComplete:
			if o.Tick != int64(w.At*60_000) {
				t.Errorf("%s left at tick %d", ids[o.Entity], o.Tick)
			}
			got[ids[o.Entity]] = o.Value
			order = append(order, ids[o.Entity])
		}
	}
	if !slices.Equal(order, w.Members) {
		t.Errorf("members left in the order %v, want FIFO %v", order, w.Members)
	}
	for m, v := range want {
		if got[m] != v {
			t.Errorf("%s: outcome %d, the %s draw in member order says %d", m, got[m], w.Decision, v)
		}
	}
	// The case must be able to tell the order: the members' outcomes are
	// not all alike, nor the same read backwards.
	var pattern []int64
	for _, m := range w.Members {
		pattern = append(pattern, want[m])
	}
	reversed := slices.Clone(pattern)
	slices.Reverse(reversed)
	if slices.Equal(pattern, reversed) {
		t.Fatalf("seed %d gives the outcomes %v: a palindrome cannot tell the order", p.seed, pattern)
	}
}

// D-096 (f): at a batch's end each member's route is resolved for that
// member, its visits charged to it, in FIFO order; a visit limit stops the
// end at the first member that passes it. Four members end together and
// go to done (2 visits) or through a rework node to done2 (3 visits) by
// the decision's draws in member order; with max_entity_visits 4 (a source
// visit and the batch process's before), the first member drawn for the
// rework fails entering done2. The draws are computed here from the
// stream itself. (Added after mutation run 2: resolving the routes in
// reverse order but committing them in FIFO order was invisible, since
// the destinations follow the draws; only the visits went to the wrong
// member.)
func TestBatchVisitsChargedToTheirMember(t *testing.T) {
	nodes := []model.Node{source("src", 1, 4), process("work", 5, "R"), {ID: "d", Type: model.NodeDecision},
		{ID: "done", Type: model.NodeSink}, {ID: "rw", Type: model.NodeRework}, {ID: "done2", Type: model.NodeSink}}
	nodes[1].Batch = &model.Batch{MinSize: 4, MaxSize: 4}
	ir := build(t, nodes, []model.Flow{flow("f1", "src", "work", 1), flow("f2", "work", "d", 1), flow("f3", "d", "done", 0.5),
		flow("f4", "d", "rw", 0.5), flow("f5", "rw", "done2", 1)}, "R")
	in := input(ir)
	in.Limits.MaxEntityVisits = 4
	p := compile(t, in)
	stream := Stream(ReplicationSeed(p.seed, 0), "d", PurposeRouting)
	first := -1
	for i := range 4 {
		if stream.Uniform() >= 0.5 && first < 0 { // flows by ID: f3 (done) below 0.5, f4 (rw) above
			first = i
		}
	}
	if first < 0 {
		t.Fatalf("seed %d sends every member to done: the case cannot tell", p.seed)
	}
	_, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), nil)
	want := FormatEntityID(0, "src", uint64(first+1)) + " entering done2 at tick 540000"
	if !errors.Is(err, ErrEntityVisits) || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want the visit limit for %s", err, want)
	}
}

// D-054: one service draw per batch, from the batch process's (process,
// service) stream, in grant order: batch k's service is the stream's k-th
// draw, the same for all its members. Full batches of 2 (min = max, no
// wait limit) every 20 minutes, an exponential service of mean 4. The
// draws are computed here from the stream itself. (Added after mutation
// run 2: a second draw per batch was caught only by des-go-0.5.0's
// observation lock, since every hand-derived golden has a constant
// service and the SimPy reference compares only statistically.)
func TestBatchServiceDrawsOncePerBatch(t *testing.T) {
	mean := 4.0
	nodes := []model.Node{source("src", 10, 40), process("work", 0, "R"), {ID: "done", Type: model.NodeSink}}
	nodes[1].ServiceTime.Distribution = model.Distribution{Type: model.DistExponential, Mean: &mean}
	nodes[1].Batch = &model.Batch{MinSize: 2, MaxSize: 2}
	p := compile(t, input(build(t, nodes, []model.Flow{flow("f1", "src", "work", 1), flow("f2", "work", "done", 1)}, "R")))
	n := slices.IndexFunc(p.nodes, func(x cnode) bool { return x.id == "work" })
	col := &collect{}
	if _, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col); err != nil {
		t.Fatal(err)
	}
	start, end := map[uint64]int64{}, map[uint64][]int64{}
	var order []uint64
	for _, o := range col.all {
		switch {
		case o.Kind == ObsBatchStart:
			start[o.Batch] = o.Tick
			order = append(order, o.Batch)
		case o.Kind == ObsServiceEnd && o.Node == n:
			end[o.Batch] = append(end[o.Batch], o.Tick)
		}
	}
	if len(order) != 20 {
		t.Fatalf("%d batches, want 20 (40 entities in batches of 2)", len(order))
	}
	stream := Stream(ReplicationSeed(p.seed, 0), "work", PurposeService)
	distinct := map[int64]bool{}
	for k, b := range order {
		want := p.nodes[n].service.draw(stream)
		distinct[want] = true
		if b != uint64(k+1) || len(end[b]) != 2 || end[b][0]-start[b] != want || end[b][1]-start[b] != want {
			t.Errorf("batch %d (number %d) started at %d, its members ended at %v; the stream's draw %d is %d ticks", k+1, b, start[b], end[b], k+1, want)
		}
	}
	if len(distinct) < 15 {
		t.Fatalf("seed %d gives %d distinct services of 20: the case cannot tell the draws apart", p.seed, len(distinct))
	}
}
