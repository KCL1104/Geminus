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
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
)

// The hand-derived goldens of finite buffers and blocking (ADR-026 D-048,
// D-049, D-050; testdata/simulation/blocking-goldens.json,
// HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN). The Result members of the same
// cases are checked in internal/simulation/des.

type blockingCase struct {
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
	} `json:"expected"`
}

func blockingGoldens(t *testing.T) map[string]blockingCase {
	t.Helper()
	var doc struct {
		Status string         `json:"status"`
		Cases  []blockingCase `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/blocking-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Status != "HAND_DERIVED_EXPECTATION_NOT_ENGINE_RUN" || len(doc.Cases) == 0 {
		t.Fatalf("blocking goldens: status %q, %d cases", doc.Status, len(doc.Cases))
	}
	out := map[string]blockingCase{}
	for _, c := range doc.Cases {
		out[c.ID] = c
	}
	return out
}

// blockingSeeds are the models of blocking-goldens.json, for the fuzzers.
func blockingSeeds(tb testing.TB) []jsontext.Value {
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
	if err := json.Unmarshal(contracttest.ReadFile(tb, filepath.Join(contracttest.Root(tb), "testdata/simulation/blocking-goldens.json")), &doc); err != nil {
		tb.Fatal(err)
	}
	var out []jsontext.Value
	for _, c := range doc.Cases {
		out = append(out, c.Model)
	}
	for _, c := range doc.Reference.Cases {
		out = append(out, c.Model)
	}
	return out
}

func (c blockingCase) input(t *testing.T) simulation.RunInput {
	t.Helper()
	var ir model.ModelIR
	if err := model.Decode(c.Model, &ir); err != nil {
		t.Fatal(err)
	}
	in := input(ir)
	in.Config = c.Config
	return in
}

// facts are what the goldens state about a run, rebuilt from its
// observations: service starts per node, completions and blocks, with
// entity IDs and model minutes.
type facts struct {
	starts      map[string][]string // node -> "entity@minute"
	completions []string
	blocks      []string // "entity@node:from-to", "-" when still blocked
}

func observedFacts(p *Program, obs []Observation) facts {
	ids := map[uint64]string{}
	f := facts{starts: map[string][]string{}}
	open := map[uint64]int{} // entity -> index in f.blocks of its open block
	from := map[uint64]int64{}
	minute := func(tick int64) string { return fmt.Sprint(float64(tick) / 60_000) }
	for _, o := range obs {
		switch o.Kind {
		case ObsArrival:
			ids[o.Entity] = FormatEntityID(0, p.nodes[o.Node].id, uint64(o.Value))
		case ObsServiceStart:
			n := p.nodes[o.Node].id
			f.starts[n] = append(f.starts[n], ids[o.Entity]+"@"+minute(o.Tick))
		case ObsComplete:
			f.completions = append(f.completions, ids[o.Entity]+"@"+minute(o.Tick))
		case ObsBlocked:
			open[o.Entity] = len(f.blocks)
			from[o.Entity] = o.Tick
			f.blocks = append(f.blocks, ids[o.Entity]+"@"+p.nodes[o.Node].id+":"+minute(o.Tick)+"--")
		case ObsUnblocked:
			i, ok := open[o.Entity]
			if !ok {
				f.blocks = append(f.blocks, "unblocked without a block: "+ids[o.Entity])
				continue
			}
			delete(open, o.Entity)
			f.blocks[i] = ids[o.Entity] + "@" + p.nodes[o.Node].id + ":" + minute(from[o.Entity]) + "-" + minute(o.Tick)
		}
	}
	return f
}

func expectedFacts(t *testing.T, c blockingCase) facts {
	t.Helper()
	pair := func(v jsontext.Value) string {
		var x []any
		if err := json.Unmarshal(v, &x); err != nil || len(x) < 2 {
			t.Fatalf("%s: bad entry %s", c.ID, v)
		}
		return fmt.Sprintf("%v@%v", x[0], x[1])
	}
	f := facts{starts: map[string][]string{}}
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
		to := "-"
		if x[3] != nil {
			to = fmt.Sprint(x[3])
		}
		f.blocks = append(f.blocks, fmt.Sprintf("%v@%v:%v-%s", x[0], x[1], x[2], to))
	}
	return f
}

func compareFacts(t *testing.T, id string, got, want facts) {
	t.Helper()
	for n, w := range want.starts {
		if g := got.starts[n]; !slices.Equal(g, w) {
			t.Errorf("%s: starts at %s %v, hand-derived %v", id, n, g, w)
		}
	}
	for n := range got.starts {
		if _, ok := want.starts[n]; !ok {
			t.Errorf("%s: unexpected starts at %s: %v", id, n, got.starts[n])
		}
	}
	if !slices.Equal(got.completions, want.completions) {
		t.Errorf("%s: completions %v, hand-derived %v", id, got.completions, want.completions)
	}
	if !slices.Equal(got.blocks, want.blocks) && !(len(got.blocks) == 0 && len(want.blocks) == 0) {
		t.Errorf("%s: blocks %v, hand-derived %v", id, got.blocks, want.blocks)
	}
}

// G-B1–G-B4, G-B7–G-B14: service starts, completions and every block
// interval, by entity, against the hand derivation (G-B4W, G-B10–G-B13:
// T-047 review round 1; G-B14: round 2).
func TestBlockingGoldens(t *testing.T) {
	cases := blockingGoldens(t)
	for _, id := range []string{"G-B1", "G-B1W", "G-B2", "G-B3", "G-B4", "G-B4W", "G-B7", "G-B8", "G-B9", "G-B10", "G-B11", "G-B12", "G-B13", "G-B14"} {
		t.Run(id, func(t *testing.T) {
			c, ok := cases[id]
			if !ok {
				t.Fatalf("no case %s", id)
			}
			in := c.input(t)
			p := compile(t, in)
			if hasFinite := id != "G-B2"; p.HasFiniteBuffers() != hasFinite {
				t.Errorf("HasFiniteBuffers %v", p.HasFiniteBuffers())
			}
			col := &collect{}
			out, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col)
			if err != nil {
				t.Fatal(err)
			}
			if out.Created != out.Exited+out.Censored {
				t.Errorf("accounting %+v", out)
			}
			compareFacts(t, id, observedFacts(p, col.all), expectedFacts(t, c))
		})
	}
}

// G-B8: the chain of unblocks at 13 is one dispatch, in the order of
// D-049: a grant, then the unblock it allows (the released bundle, the
// unblocked entity, its new request), then the next smallest-seq
// candidate.
func TestUnblockChainInOneDispatch(t *testing.T) {
	c := blockingGoldens(t)["G-B8"]
	p := compile(t, c.input(t))
	col := &collect{}
	if _, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), col); err != nil {
		t.Fatal(err)
	}
	ids := map[uint64]string{}
	var group []Observation
	for _, o := range col.all {
		if o.Kind == ObsArrival {
			ids[o.Entity] = FormatEntityID(0, p.nodes[o.Node].id, uint64(o.Value))
		}
		if o.Tick == 13*60_000 && o.Kind == ObsServiceStart && group == nil {
			for _, g := range col.all {
				if g.Tick == o.Tick && g.Seq == o.Seq {
					group = append(group, g)
				}
			}
		}
	}
	var got []string
	for _, o := range group {
		switch o.Kind {
		case ObsResource:
			got = append(got, fmt.Sprintf("resource %s %d", p.resources[o.Resource].id, o.Value))
		case ObsServiceStart:
			got = append(got, fmt.Sprintf("service_start %s %s", ids[o.Entity], p.nodes[o.Node].id))
		case ObsUnblocked:
			got = append(got, fmt.Sprintf("unblocked %s %s", ids[o.Entity], p.nodes[o.Node].id))
		case ObsQueueEnter:
			got = append(got, fmt.Sprintf("queue_enter %s %s", ids[o.Entity], p.nodes[o.At].id))
		default:
			got = append(got, fmt.Sprintf("kind %d", o.Kind))
		}
	}
	var want []string
	for _, v := range c.Expected.Dispatch13 {
		var x []any
		if err := json.Unmarshal(v, &x); err != nil || len(x) != 3 {
			t.Fatalf("bad entry %s", v)
		}
		want = append(want, fmt.Sprintf("%v %v %v", x[0], x[1], x[2]))
	}
	if !slices.Equal(got, want) {
		t.Errorf("the 13 dispatch:\n got  %v\n want %v", got, want)
	}
}

// G-B5: a decision resolved at the completion before a full buffer draws
// its routing stream once per completion; an entity that was blocked
// enters the buffer it drew, when it is unblocked, without a second draw.
func TestBlockedRouteIsDrawnOnce(t *testing.T) {
	c := blockingGoldens(t)["G-B5"]
	in := c.input(t)
	p := compile(t, in)
	col := &collect{}
	k, _, err := runKernel(context.Background(), p, 0, NewBudget(1_000_000), col)
	if err != nil {
		t.Fatal(err)
	}
	d, a := p.index["d"], p.index["a"]
	stream := Stream(ReplicationSeed(p.seed, 0), "d", PurposeRouting)
	want := map[uint64]int{} // entity -> the queue its draw picked
	completions, blocked := 0, 0
	for _, o := range col.all {
		if o.Kind == ObsServiceEnd && o.Node == a {
			completions++
			u := stream.Uniform()
			target := p.nodes[d].routes[len(p.nodes[d].routes)-1].to
			for _, rt := range p.nodes[d].routes {
				if u < rt.cumulative {
					target = rt.to
					break
				}
			}
			want[o.Entity] = target
		}
		if o.Kind == ObsBlocked {
			blocked++
			if o.Node != a || o.At != want[o.Entity] {
				t.Errorf("entity %d blocked at %d for %d, its draw picked %d", o.Entity, o.Node, o.At, want[o.Entity])
			}
		}
		if o.Kind == ObsQueueEnter && (o.At == p.index["q1"] || o.At == p.index["q2"]) && o.At != want[o.Entity] {
			t.Errorf("entity %d entered %s, its draw picked %s", o.Entity, p.nodes[o.At].id, p.nodes[want[o.Entity]].id)
		}
	}
	if blocked < 3 || completions < 10 {
		t.Fatalf("too weak: %d completions, %d blocks", completions, blocked)
	}
	// The kernel's routing stream is exactly as far as one draw per completion.
	if got, ok := k.r.streams[streamKey{d, PurposeRouting}]; !ok || *got != *stream {
		t.Errorf("the routing stream drew more or less than once per completion at a (%d)", completions)
	}
}

// G-B6: capacity at least max_active_entities is G-B2 exactly (D-047).
func TestNeutralBufferGolden(t *testing.T) {
	c := blockingGoldens(t)["G-B2"]
	base := c.input(t)
	p := compile(t, base)
	withCap := base
	withCap.Model.Nodes = slices.Clone(base.Model.Nodes)
	for i := range withCap.Model.Nodes {
		if withCap.Model.Nodes[i].ID == "qb" {
			withCap.Model.Nodes[i].Capacity = base.Limits.MaxActiveEntities
		}
	}
	np := compile(t, withCap)
	a, b := &collect{}, &collect{}
	if _, err := RunReplication(context.Background(), p, 0, NewBudget(1_000_000), a); err != nil {
		t.Fatal(err)
	}
	if _, err := RunReplication(context.Background(), np, 0, NewBudget(1_000_000), b); err != nil {
		t.Fatal(err)
	}
	if !np.HasFiniteBuffers() || !slices.Equal(a.all, b.all) {
		t.Errorf("a neutral buffer changed the observations")
	}
}

// G-D2 (D-050 B1): a circular wait is ErrDeadlock at the tick it forms,
// reporting the earliest entity still blocked; the run is not continued
// to the horizon. The same model is refused by the validator (R1).
func TestDeadlockIsATypedError(t *testing.T) {
	c := blockingGoldens(t)["G-D2"]
	in := c.input(t)
	if _, err := Compile(in); err == nil {
		t.Fatal("the validator accepted G-D2")
	} else {
		var ie *InputError
		if !errors.As(err, &ie) {
			t.Fatal(err)
		}
		var got [][2]string
		for _, is := range ie.Issues {
			got = append(got, [2]string{is.Code, is.Path})
		}
		var want [][2]string
		for _, w := range c.Validator.CodesAt {
			want = append(want, [2]string{w[0], "/model" + w[1]})
		}
		if !sameSet(got, want) || !slices.ContainsFunc(ie.Issues, func(i model.ValidationIssue) bool { return i.Code == validate.BlockingCycle }) {
			t.Errorf("validator: %v, want %v", got, want)
		}
	}
	var p *Program
	var err error
	withoutValidator(t, func() { p, err = Compile(in) })
	if err != nil {
		t.Fatal(err)
	}
	col := &collect{}
	_, _, err = runKernel(context.Background(), p, 0, NewBudget(1_000_000), col)
	var de *DeadlockError
	if !errors.Is(err, ErrDeadlock) || !errors.As(err, &de) {
		t.Fatalf("want ErrDeadlock, got %v", err)
	}
	w := c.Expected.Deadlock
	if de.Tick != w.Tick || de.Entity != w.Entity || de.Node != w.Node || de.Since != w.Since || de.Reason() != w.Reason {
		t.Errorf("deadlock %+v (%q), hand-derived %+v", *de, de.Reason(), *w)
	}
	if last := col.all[len(col.all)-1]; last.Kind != ObsBlocked || last.Tick != w.Tick || p.nodes[last.Node].id != "s1" {
		t.Errorf("last observation %+v", last)
	}
	compareFacts(t, c.ID, observedFacts(p, col.all), expectedFacts(t, c))

	// Before the circular wait forms the run completes, censored.
	in.Config.Horizon.Value = c.Expected.Before.Horizon
	withoutValidator(t, func() { p, err = Compile(in) })
	if err != nil {
		t.Fatal(err)
	}
	if _, out, err := runKernel(context.Background(), p, 0, NewBudget(1_000_000), nil); err != nil || out.Censored == 0 {
		t.Errorf("before the deadlock: %+v, %v", out, err)
	}
}

// G-D3 (D-050 B1, D-064 (d); T-047 review round 1, L2): the report names
// the entity blocked first. Both blocks start in the same tick, and the
// later one waits for the buffer that comes first by node order, so
// neither the smallest block start nor the first buffer would name it.
func TestDeadlockNamesTheEntityBlockedFirst(t *testing.T) {
	c := blockingGoldens(t)["G-D3"]
	in := c.input(t)
	_, err := Compile(in)
	var ie *InputError
	if !errors.As(err, &ie) {
		t.Fatalf("the validator accepted G-D3: %v", err)
	}
	var got, want [][2]string
	for _, is := range ie.Issues {
		got = append(got, [2]string{is.Code, is.Path})
	}
	for _, w := range c.Validator.CodesAt {
		want = append(want, [2]string{w[0], "/model" + w[1]})
	}
	if !sameSet(got, want) {
		t.Errorf("validator: %v, want %v", got, want)
	}
	var p *Program
	withoutValidator(t, func() { p, err = Compile(in) })
	if err != nil {
		t.Fatal(err)
	}
	col := &collect{}
	_, _, err = runKernel(context.Background(), p, 0, NewBudget(1_000_000), col)
	var de *DeadlockError
	if !errors.As(err, &de) {
		t.Fatalf("want ErrDeadlock, got %v", err)
	}
	w := c.Expected.Deadlock
	if de.Tick != w.Tick || de.Entity != w.Entity || de.Node != w.Node || de.Since != w.Since || de.Queued || de.Reason() != w.Reason {
		t.Errorf("deadlock %+v (%q), hand-derived %+v", *de, de.Reason(), *w)
	}
	compareFacts(t, c.ID, observedFacts(p, col.all), expectedFacts(t, c))
	// The two blocked entities wait in different buffers: the one blocked
	// first in Y's (later by node order), the other in X's.
	if x, y := p.index["X"], p.index["Y"]; x >= y {
		t.Fatalf("X (%d) must come before Y (%d) for this case to mean anything", x, y)
	}
}

func sameSet(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// The observation stream of every finite-buffer golden and reference case
// (replication 0) is pinned (testdata/simulation/kernel-observation-
// digests-0.2.0.json, a regression lock taken from des-go-0.2.0, not an
// oracle: the semantics are the hand-derived goldens' and the
// observation-only checker's). Any change to the order of blocking,
// unblocking and dispatch, or to their sequence numbers, shows up here.
func TestBlockingObservationDigests(t *testing.T) {
	var lock struct {
		EngineVersion string `json:"engine_version"`
		Cases         []struct {
			ID           string `json:"id"`
			Observations int    `json:"observations"`
			Digest       string `json:"sha256"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/kernel-observation-digests-0.2.0.json")), &lock); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases     []blockingCase `json:"cases"`
		Reference struct {
			Cases []blockingCase `json:"cases"`
		} `json:"reference"`
	}
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/simulation/blocking-goldens.json")), &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range append(doc.Cases, doc.Reference.Cases...) {
		if c.Expected.Deadlock != nil {
			continue // G-D2, G-D3: refused by the validator; their observations are checked by the deadlock tests
		}
		col := &collect{}
		if _, err := RunReplication(context.Background(), compile(t, c.input(t)), 0, NewBudget(10_000_000), col); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		got[c.ID] = fmt.Sprintf("%d %s", len(col.all), observationDigest(col.all))
	}
	want := map[string]string{}
	for _, c := range lock.Cases {
		want[c.ID] = fmt.Sprintf("%d %s", c.Observations, c.Digest)
	}
	if lock.EngineVersion != "des-go-0.2.0" || !maps.Equal(got, want) {
		t.Errorf("observation digests\n got  %v\n want %v", got, want)
	}
}
