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

// Every batch golden but the stall (G-BT13, below) and the routing draws
// (G-BT5, below): service starts, completions, blocks, downtimes, waiting
// failures, units on shift, every batch with its members and, where the
// case gives them, the sequence numbers of the BatchStarts and the event
// count, against the hand derivation. These fix D-054 (a batch is ready
// at max_size, or at min_size once its oldest has waited max_wait; one
// request, one draw, members start and end together; a member blocked at
// the end keeps the bundle until the last moves on), D-048 (BATCH_TIMEOUT
// at priority 15, after the tick's arrivals), D-096 (no timeout with min =
// max, stale ones ignored, an expired limit checked at once, as many
// unblocks as places a grant frees) and the interactions with breakdowns
// and calendars (finish-first).
func TestBatchGoldens(t *testing.T) {
	cases, extras := batchGoldens(t)
	ran := 0
	for _, c := range cases {
		x := extras[c.ID]
		if c.Expected.Stalled != nil || x.Expected.Routing != nil {
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
	if ran != 12 {
		t.Errorf("%d batch goldens ran, want 12", ran)
	}
}

// G-BT13 (D-050 B2 on a batch request, R4 bypassed): ErrStalled at 960,
// naming the batch's oldest member and the end of the tick its request
// entered the state (D-096 (g)).
func TestBatchStall(t *testing.T) {
	cases, _ := batchGoldens(t)
	i := slices.IndexFunc(cases, func(c calendarCase) bool { return c.ID == "G-BT13" })
	c := cases[i]
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
