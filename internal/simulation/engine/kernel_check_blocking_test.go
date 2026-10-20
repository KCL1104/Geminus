package engine

// An observation-only model of finite buffers and blocking (ADR-026 D-049,
// D-050 B1; T-047), in the style of kernel_check_test.go. It rebuilds the
// waiting requests, each buffer's occupancy, the blocked entities of each
// buffer in FIFO order and the resource levels from the Observations and
// the IR alone, and checks D-049 against them:
//   - an entity enters a finite buffer only below its capacity, and is
//     blocked only when the buffer is full; a buffer with blocked entities
//     is full at every tick boundary;
//   - a blocked entity keeps its departing process's whole bundle until it
//     is unblocked, and has no place in any resource FIFO meanwhile;
//   - every dispatch is the literal fixpoint: grant the first waiting
//     request (in request order) whose bundle fits and that no earlier
//     ungranted request shares a resource with; after a grant at a full
//     buffer with blocked entities, move the first one in (its bundle's
//     levels, Unblocked, QueueEnter at the end of the FIFO); repeat;
//   - no tick ends with a grantable request;
//   - a run that ends in ErrDeadlock ends in a real circular wait (nothing
//     in service or in a delay, blocked entities, nothing grantable), and
//     no run goes past a tick that ended in one.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

type bentry struct {
	entity   uint64
	from, at int
}

type bchecker struct {
	p        *Program
	capacity []int // per process node: finite buffer capacity, 0 unlimited (from the IR)
	group    []Observation
	waiting  []wreq
	occupied []int
	blocked  [][]bentry // per process node, FIFO
	inUse    []int
	state    map[uint64]string
	ids      map[uint64]string // entity -> its ID, from its arrival
	rep      uint32
	where    map[uint64]int // service: the process; delay: the delay; blocked: the node blocked at
	nService int
	nDelay   int
	lastTick int64
	started  bool
	errs     []string
	stats    struct{ blocks, unblocks, grants, chains int }
}

func newBChecker(p *Program, ir *model.ModelIR, rep uint32) *bchecker {
	c := &bchecker{p: p, rep: rep, ids: map[uint64]string{}, capacity: make([]int, len(p.nodes)), occupied: make([]int, len(p.nodes)), blocked: make([][]bentry, len(p.nodes)),
		inUse: make([]int, len(p.resources)), state: map[uint64]string{}, where: map[uint64]int{}}
	index := map[string]int{}
	for i, n := range p.nodes {
		index[n.id] = i
	}
	// A queue's capacity bounds the buffer of the process its flow feeds.
	for _, n := range ir.Nodes {
		if n.Type != model.NodeQueue || n.Capacity == 0 {
			continue
		}
		for _, f := range ir.Flows {
			if f.From == n.ID && f.Routing.Probability > 0 {
				c.capacity[index[f.To]] = n.Capacity
			}
		}
	}
	return c
}

func (c *bchecker) fail(format string, a ...any) {
	if len(c.errs) < 20 {
		c.errs = append(c.errs, fmt.Sprintf(format, a...))
	}
}

func (c *bchecker) Observe(o Observation) {
	if len(c.group) > 0 && (c.group[0].Tick != o.Tick || c.group[0].Seq != o.Seq) {
		c.finishGroup()
		if o.Tick > c.lastTick {
			c.tickBoundary()
		}
	}
	if c.started && o.Tick < c.lastTick {
		c.fail("time went back: %+v", o)
	}
	c.started, c.lastTick = true, o.Tick
	c.group = append(c.group, o)
}

func (c *bchecker) finish() {
	if len(c.group) > 0 {
		c.finishGroup()
	}
	c.tickBoundary()
}

func (c *bchecker) fits(process int) bool {
	for _, q := range c.p.nodes[process].requires {
		if c.inUse[q.resource]+q.units > c.p.resources[q.resource].capacity {
			return false
		}
	}
	return true
}

func (c *bchecker) shares(a, b int) bool {
	for _, x := range c.p.nodes[a].requires {
		for _, y := range c.p.nodes[b].requires {
			if x.resource == y.resource {
				return true
			}
		}
	}
	return false
}

// grantable is the first waiting request the literal rule grants, or -1.
func (c *bchecker) grantable() int {
	for i, w := range c.waiting {
		if !c.fits(w.process) {
			continue
		}
		ok := true
		for j := 0; j < i; j++ {
			if c.shares(c.waiting[j].process, w.process) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func (c *bchecker) tickBoundary() {
	if i := c.grantable(); i >= 0 {
		c.fail("tick %d ended with a grantable request of %d at %d", c.lastTick, c.waiting[i].entity, c.waiting[i].process)
	}
	nblocked := 0
	for p, list := range c.blocked {
		nblocked += len(list)
		if len(list) > 0 && c.occupied[p] != c.capacity[p] {
			c.fail("tick %d: %d entities blocked at a buffer of %d holding %d of %d", c.lastTick, len(list), p, c.occupied[p], c.capacity[p])
		}
	}
	if nblocked > 0 && c.nService == 0 && c.nDelay == 0 {
		c.fail("tick %d ended in a circular wait (%d blocked) without ErrDeadlock", c.lastTick, nblocked)
	}
}

// deadlocked reports whether the reconstruction is a circular wait.
func (c *bchecker) deadlocked() bool {
	nblocked := 0
	for _, l := range c.blocked {
		nblocked += len(l)
	}
	return nblocked > 0 && c.nService == 0 && c.nDelay == 0 && c.grantable() < 0
}

// expect pops the next observation of the group and compares it.
type cursor struct {
	c  *bchecker
	g  []Observation
	at int
}

func (u *cursor) next(what string, ok func(Observation) bool) (Observation, bool) {
	if u.at >= len(u.g) {
		u.c.fail("tick %d: missing %s", u.g[0].Tick, what)
		return Observation{}, false
	}
	o := u.g[u.at]
	u.at++
	if !ok(o) {
		u.c.fail("tick %d: %+v, want %s", o.Tick, o, what)
		return o, false
	}
	return o, true
}

func (c *bchecker) release(u *cursor, process int, sign int) {
	for _, q := range c.p.nodes[process].requires {
		c.inUse[q.resource] += sign * q.units
		want := int64(c.inUse[q.resource])
		u.next(fmt.Sprintf("resource %d at %d", q.resource, want), func(o Observation) bool {
			return o.Kind == ObsResource && o.Resource == q.resource && o.Value == want
		})
	}
}

func (c *bchecker) finishGroup() {
	g := c.group
	c.group = nil
	u := &cursor{c: c, g: g}
	switch first := g[0]; {
	case first.Kind == ObsArrival:
		u.at++
		c.state[first.Entity] = "route"
		c.ids[first.Entity] = FormatEntityID(c.rep, c.p.nodes[first.Node].id, uint64(first.Value))
		c.route(u, first.Entity, first.Node)
	case first.Kind == ObsDelayEnd:
		u.at++
		if c.state[first.Entity] != "delay" {
			c.fail("delay end of %d from %q", first.Entity, c.state[first.Entity])
		}
		c.nDelay--
		c.state[first.Entity] = "route"
		c.route(u, first.Entity, first.Node)
	case first.Kind == ObsServiceEnd || (first.Kind == ObsResource && slices.ContainsFunc(g, func(o Observation) bool { return o.Kind == ObsServiceEnd })):
		end := g[slices.IndexFunc(g, func(o Observation) bool { return o.Kind == ObsServiceEnd })]
		if c.state[end.Entity] != "service" || c.where[end.Entity] != end.Node {
			c.fail("service end of %d at %d from %q", end.Entity, end.Node, c.state[end.Entity])
		}
		c.nService--
		// Blocked iff the event ends in Blocked; then nothing is released.
		if g[len(g)-1].Kind != ObsBlocked {
			c.release(u, end.Node, -1)
		}
		u.next("service end", func(o Observation) bool { return o == end })
		c.state[end.Entity] = "route"
		c.route(u, end.Entity, end.Node)
	default:
		c.dispatch(u)
	}
	if u.at != len(g) {
		c.fail("tick %d: %d unexplained observations from %+v", g[0].Tick, len(g)-u.at, g[u.at])
	}
	for r, n := range c.inUse {
		if n < 0 || n > c.p.resources[r].capacity {
			c.fail("resource %d in use %d", r, n)
		}
	}
}

// route reads the rest of an event: failure and rework observations, then
// how it ends (a buffer, a delay, a sink, or blocked at from).
func (c *bchecker) route(u *cursor, e uint64, from int) {
	for u.at < len(u.g) {
		o := u.g[u.at]
		u.at++
		if o.Entity != e && o.Kind != ObsResource {
			c.fail("event of %d has %+v", e, o)
			return
		}
		switch o.Kind {
		case ObsFailure, ObsRework:
			continue
		case ObsQueueEnter:
			p := o.Node
			if c.capacity[p] > 0 && (c.occupied[p] >= c.capacity[p] || len(c.blocked[p]) > 0) {
				c.fail("tick %d: %d entered a full buffer %d (%d of %d, %d blocked)", o.Tick, e, p, c.occupied[p], c.capacity[p], len(c.blocked[p]))
			}
			c.enter(e, p)
		case ObsDelayStart:
			c.state[e], c.where[e] = "delay", o.Node
			c.nDelay++
		case ObsComplete:
			c.state[e] = "gone"
		case ObsBlocked:
			p := c.p.nodes[o.At].buffer
			if o.Node != from || p < 0 || c.capacity[p] == 0 || c.occupied[p] != c.capacity[p] {
				c.fail("tick %d: %d blocked at %d (from %d) for buffer %d holding %d of %d", o.Tick, e, o.Node, from, p, c.occupied[max(p, 0)], c.capacity[max(p, 0)])
				return
			}
			c.blocked[p] = append(c.blocked[p], bentry{e, from, o.At})
			c.state[e], c.where[e] = "blocked", from
			c.stats.blocks++
		default:
			c.fail("event of %d ends with %+v", e, o)
		}
		return
	}
	c.fail("the event of %d left it routing", e)
}

func (c *bchecker) enter(e uint64, p int) {
	c.waiting = append(c.waiting, wreq{e, p})
	c.occupied[p]++
	c.state[e] = "queue"
}

// dispatch replays the literal fixpoint and compares it with the group.
func (c *bchecker) dispatch(u *cursor) {
	chain := 0
	for {
		i := c.grantable()
		if i < 0 {
			break
		}
		w := c.waiting[i]
		c.waiting = slices.Delete(c.waiting, i, i+1)
		c.occupied[w.process]--
		c.release(u, w.process, +1)
		u.next(fmt.Sprintf("service start of %d at %d", w.entity, w.process), func(o Observation) bool {
			return o.Kind == ObsServiceStart && o.Entity == w.entity && o.Node == w.process
		})
		c.state[w.entity], c.where[w.entity] = "service", w.process
		c.nService++
		c.stats.grants++
		if len(c.blocked[w.process]) > 0 {
			b := c.blocked[w.process][0]
			c.blocked[w.process] = c.blocked[w.process][1:]
			if c.p.nodes[b.from].typ == model.NodeProcess {
				c.release(u, b.from, -1)
			}
			u.next(fmt.Sprintf("unblocked %d at %d", b.entity, b.from), func(o Observation) bool {
				return o.Kind == ObsUnblocked && o.Entity == b.entity && o.Node == b.from
			})
			u.next(fmt.Sprintf("queue enter of %d at %d via %d", b.entity, w.process, b.at), func(o Observation) bool {
				return o.Kind == ObsQueueEnter && o.Entity == b.entity && o.Node == w.process && o.At == b.at
			})
			c.enter(b.entity, w.process)
			c.stats.unblocks++
			chain++
		}
	}
	if chain > 1 {
		c.stats.chains++
	}
}

// randomBlockingModel is randomModel with a capacity of 1–3 on some of its
// explicit queues, minus the capacities R1 refuses (BLOCKING_CYCLE), so
// that it is valid.
func randomBlockingModel(t *testing.T, rng *rand.Rand) (model.ModelIR, bool) {
	ir := randomModel(t, rng)
	for i := range ir.Nodes {
		if ir.Nodes[i].Type == model.NodeQueue && rng.IntN(3) > 0 {
			ir.Nodes[i].Capacity = 1 + rng.IntN(3)
		}
	}
	for range 10 {
		data, err := model.Encode(&ir)
		if err != nil {
			t.Fatal(err)
		}
		r, err := validate.IR(data)
		if err != nil {
			t.Fatal(err)
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
			return ir, r.Valid()
		}
	}
	return ir, false
}

func hasCapacity(ir model.ModelIR) bool {
	return slices.ContainsFunc(ir.Nodes, func(n model.Node) bool { return n.Capacity > 0 })
}

// The kernel agrees with the observation-only model on random valid models
// with finite buffers (R1 holds, so no run deadlocks, D-050), and runs the
// same replication identically in parallel.
func TestRandomBlockingModelsAgainstSpec(t *testing.T) {
	const n = 3000
	compiled, withBuffers := 0, 0
	var total bchecker
	for seed := range n {
		rng := rand.New(rand.NewPCG(uint64(seed), 4703))
		ir, ok := randomBlockingModel(t, rng)
		if !ok {
			continue
		}
		in := input(ir)
		in.Config = config(float64(60+rng.IntN(240)), model.Minute)
		in.Config.Replications = 3
		p, err := Compile(in)
		if err != nil {
			t.Fatalf("seed %d: a valid model does not compile: %v", seed, err)
		}
		compiled++
		if hasCapacity(ir) {
			withBuffers++
		}
		c := newBChecker(p, &ir, uint32(seed%3))
		_, err = RunReplication(context.Background(), p, uint32(seed%3), NewBudget(400_000), c)
		if errors.Is(err, ErrDeadlock) {
			t.Fatalf("seed %d: a valid model deadlocked: %v", seed, err)
		}
		if err != nil && !errors.Is(err, ErrEventBudget) && !errors.Is(err, ErrEntityVisits) {
			t.Fatalf("seed %d: %v", seed, err)
		}
		c.finish()
		if len(c.errs) > 0 {
			t.Fatalf("seed %d: %d problems, first: %v", seed, len(c.errs), c.errs)
		}
		total.stats.blocks += c.stats.blocks
		total.stats.unblocks += c.stats.unblocks
		total.stats.grants += c.stats.grants
		total.stats.chains += c.stats.chains
		if seed%5 == 0 {
			res := make([][]Observation, 3)
			done := make(chan int, 3)
			for i := range res {
				go func() {
					col := &collect{}
					RunReplication(context.Background(), p, uint32(seed%3), NewBudget(400_000), col)
					res[i] = col.all
					done <- i
				}()
			}
			for range res {
				<-done
			}
			for i := 1; i < len(res); i++ {
				if !slices.Equal(res[0], res[i]) {
					t.Fatalf("seed %d: concurrent run %d differs", seed, i)
				}
			}
		}
	}
	t.Logf("%d of %d random models compiled, %d with finite buffers; %d blocks, %d unblocks (%d dispatches with a chain), %d grants",
		compiled, n, withBuffers, total.stats.blocks, total.stats.unblocks, total.stats.chains, total.stats.grants)
	if withBuffers < n/5 || total.stats.blocks < 10_000 || total.stats.chains < 100 {
		t.Fatalf("generator too weak: %d with buffers, %+v", withBuffers, total.stats)
	}
}

// With R1 bypassed (test-only), random models may form circular waits: a
// run that fails with ErrDeadlock is really deadlocked at that tick, and
// every other run never passed a tick that ended in one (B1, no false
// positive, no miss).
func TestRandomCircularWaitsAreDetected(t *testing.T) {
	const n = 3000
	deadlocks, runs := 0, 0
	for seed := range n {
		rng := rand.New(rand.NewPCG(uint64(seed), 4704))
		base := randomModel(t, rng)
		in := input(base)
		if _, err := Compile(in); err != nil {
			continue // invalid for another reason
		}
		ir := base
		ir.Nodes = slices.Clone(base.Nodes)
		for i := range ir.Nodes {
			if ir.Nodes[i].Type == model.NodeQueue {
				ir.Nodes[i].Capacity = 1 + rng.IntN(2)
			}
		}
		if !hasCapacity(ir) {
			continue
		}
		in.Model = ir
		in.Config = config(float64(120+rng.IntN(600)), model.Minute)
		in.Config.Replications = 2
		var p *Program
		var err error
		withoutValidator(t, func() { p, err = Compile(in) })
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		runs++
		c := newBChecker(p, &ir, uint32(seed%2))
		_, err = RunReplication(context.Background(), p, uint32(seed%2), NewBudget(400_000), c)
		var de *DeadlockError
		switch {
		case errors.As(err, &de):
			deadlocks++
			if len(c.group) > 0 {
				c.finishGroup()
			}
			if !c.deadlocked() || de.Tick != c.lastTick {
				t.Fatalf("seed %d: ErrDeadlock at %d without a circular wait in the reconstruction (%v)", seed, de.Tick, c.errs)
			}
			if c.state[findEntity(t, c, de.Entity)] != "blocked" {
				t.Fatalf("seed %d: the reported entity %s is not blocked", seed, de.Entity)
			}
		case err == nil || errors.Is(err, ErrEventBudget) || errors.Is(err, ErrEntityVisits):
			c.finish()
		default:
			t.Fatalf("seed %d: %v", seed, err)
		}
		if len(c.errs) > 0 {
			t.Fatalf("seed %d: %d problems, first: %v", seed, len(c.errs), c.errs)
		}
	}
	t.Logf("%d runs with R1 bypassed, %d ended in ErrDeadlock", runs, deadlocks)
	if deadlocks < 50 || runs-deadlocks < 50 {
		t.Fatalf("too weak: %d runs, %d deadlocks", runs, deadlocks)
	}
}

// findEntity is the handle of the active entity with this ID.
func findEntity(t *testing.T, c *bchecker, id string) uint64 {
	t.Helper()
	for e, s := range c.ids {
		if s == id && c.state[e] != "gone" {
			return e
		}
	}
	t.Fatalf("no active entity %s", id)
	return 0
}
