package engine

// An observation-only model of the kernel, ported from the independent T-021
// review. It rebuilds waiting requests, resource levels and entity states from
// the Observations alone and checks the spec rules against them,
// independently of kernel.go's data structures:
//   - every dispatch grants exactly what the literal resource-wise FIFO rule
//     grants (SIMULATION_SPEC §3), and no tick ends with a grantable request;
//   - reported resource levels match the reconstruction, grant by grant, and
//     stay within [0, capacity];
//   - every event leaves each entity it touched queued, in service, in a
//     delay or gone (exactly one state), never routing;
//   - time never goes back.
// It runs on random models: bundles of 0-5 resources with 1-3 units, queue
// nodes plus direct routes into the same buffer, zero-time services and
// delays, decision loops with abandon and 0% flows, failure and rework.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/model"
)

type wreq struct {
	entity  uint64
	process int
}

type checker struct {
	t              *testing.T
	p              *Program
	group          []Observation
	waiting        []wreq
	inUse          []int
	state          map[uint64]string
	svc            map[uint64]int
	seen           map[uint64]bool // event seqs already finished
	lastTick       int64
	started        bool
	errs           []string
	grants         int
	dispatchGroups int
	boundaries     int
	maxWaiting     int
}

func newChecker(t *testing.T, p *Program) *checker {
	return &checker{t: t, p: p, inUse: make([]int, len(p.resources)), state: map[uint64]string{}, svc: map[uint64]int{}, seen: map[uint64]bool{}}
}

func (c *checker) fail(format string, a ...any) {
	if len(c.errs) < 20 {
		c.errs = append(c.errs, fmt.Sprintf(format, a...))
	}
}

func (c *checker) Observe(o Observation) {
	if len(c.group) > 0 && (c.group[0].Tick != o.Tick || c.group[0].Seq != o.Seq) {
		c.finishGroup()
		if o.Tick > c.lastTick {
			c.tickBoundary()
		}
	}
	if c.started && o.Tick < c.lastTick {
		c.fail("time went back: %+v after tick %d", o, c.lastTick)
	}
	if len(c.group) == 0 && c.seen[o.Seq] {
		c.fail("event seq %d observed in two separate groups", o.Seq)
	}
	c.started = true
	c.lastTick = o.Tick
	c.group = append(c.group, o)
}

func (c *checker) finish() {
	if len(c.group) > 0 {
		c.finishGroup()
	}
	c.tickBoundary()
}

func (c *checker) shares(a, b int) bool {
	for _, x := range c.p.nodes[a].requires {
		for _, y := range c.p.nodes[b].requires {
			if x.resource == y.resource {
				return true
			}
		}
	}
	return false
}

// naive applies the spec sentence literally: grant iff every resource has
// enough free units and no earlier ungranted request shares any resource.
func (c *checker) naive() []uint64 {
	use := slices.Clone(c.inUse)
	granted := make([]bool, len(c.waiting))
	var out []uint64
	for i, w := range c.waiting {
		ok := true
		for _, q := range c.p.nodes[w.process].requires {
			if use[q.resource]+q.units > c.p.resources[q.resource].capacity {
				ok = false
				break
			}
		}
		if ok {
			for j := 0; j < i; j++ {
				if !granted[j] && c.shares(c.waiting[j].process, w.process) {
					ok = false
					break
				}
			}
		}
		if ok {
			granted[i] = true
			for _, q := range c.p.nodes[w.process].requires {
				use[q.resource] += q.units
			}
			out = append(out, w.entity)
		}
	}
	return out
}

func (c *checker) tickBoundary() {
	c.boundaries++
	if g := c.naive(); len(g) > 0 {
		c.fail("tick %d ended with grantable waiting requests %v (stranded)", c.lastTick, g)
	}
}

func (c *checker) finishGroup() {
	g := c.group
	c.group = nil
	c.seen[g[0].Seq] = true
	isDispatch := slices.ContainsFunc(g, func(o Observation) bool { return o.Kind == ObsServiceStart })
	if isDispatch {
		c.dispatchGroups++
		want := c.naive()
		var got []uint64
		for _, o := range g {
			switch o.Kind {
			case ObsServiceStart:
				got = append(got, o.Entity)
			case ObsResource:
			default:
				c.fail("dispatch group has a %d observation", o.Kind)
			}
		}
		if !slices.Equal(got, want) {
			c.fail("tick %d: dispatch granted %v, the spec rule grants %v (waiting %v)", g[0].Tick, got, want, c.waiting)
		}
		// Each grant reports its bundle's new in-use levels, then its
		// service start (step by step, T-021 review D3).
		var pending []Observation
		for _, o := range g {
			if o.Kind == ObsResource {
				pending = append(pending, o)
				continue
			}
			if o.Kind != ObsServiceStart {
				continue
			}
			i := slices.IndexFunc(c.waiting, func(w wreq) bool { return w.entity == o.Entity })
			if i < 0 || c.waiting[i].process != o.Node || c.state[o.Entity] != "queue" {
				c.fail("service start of %d at %d without a waiting request (state %q)", o.Entity, o.Node, c.state[o.Entity])
				continue
			}
			c.waiting = slices.Delete(c.waiting, i, i+1)
			reqs := c.p.nodes[o.Node].requires
			if len(pending) != len(reqs) {
				c.fail("tick %d: grant of %d reported %d resource levels for %d requirements", o.Tick, o.Entity, len(pending), len(reqs))
			}
			for k, q := range reqs {
				c.inUse[q.resource] += q.units
				if k < len(pending) && (pending[k].Resource != q.resource || pending[k].Value != int64(c.inUse[q.resource])) {
					c.fail("tick %d: grant of %d reported resource %d at %d, reconstructed resource %d at %d", o.Tick, o.Entity, pending[k].Resource, pending[k].Value, q.resource, c.inUse[q.resource])
				}
			}
			pending = pending[:0]
			c.state[o.Entity] = "service"
			c.svc[o.Entity] = o.Node
			c.grants++
		}
		if len(pending) > 0 {
			c.fail("tick %d: resource levels after the last grant: %v", g[0].Tick, pending)
		}
	} else {
		for _, o := range g {
			switch o.Kind {
			case ObsArrival:
				if _, ok := c.state[o.Entity]; ok {
					c.fail("entity %d arrived twice", o.Entity)
				}
				c.state[o.Entity] = "route"
			case ObsQueueEnter:
				if c.state[o.Entity] != "route" {
					c.fail("entity %d queued from state %q", o.Entity, c.state[o.Entity])
				}
				if c.p.nodes[o.Node].typ != model.NodeProcess {
					c.fail("queue enter at non-process %d", o.Node)
				}
				c.state[o.Entity] = "queue"
				c.waiting = append(c.waiting, wreq{o.Entity, o.Node})
				c.maxWaiting = max(c.maxWaiting, len(c.waiting))
			case ObsServiceEnd:
				if c.state[o.Entity] != "service" || c.svc[o.Entity] != o.Node {
					c.fail("entity %d ended service at %d from state %q", o.Entity, o.Node, c.state[o.Entity])
				}
				for _, q := range c.p.nodes[o.Node].requires {
					c.inUse[q.resource] -= q.units
				}
				c.state[o.Entity] = "route"
			case ObsDelayStart:
				if c.state[o.Entity] != "route" {
					c.fail("entity %d delay start from %q", o.Entity, c.state[o.Entity])
				}
				c.state[o.Entity] = "delay"
			case ObsDelayEnd:
				if c.state[o.Entity] != "delay" {
					c.fail("entity %d delay end from %q", o.Entity, c.state[o.Entity])
				}
				c.state[o.Entity] = "route"
			case ObsFailure, ObsRework:
				if c.state[o.Entity] != "route" {
					c.fail("entity %d failure/rework from %q", o.Entity, c.state[o.Entity])
				}
			case ObsComplete:
				if c.state[o.Entity] != "route" {
					c.fail("entity %d completed from %q", o.Entity, c.state[o.Entity])
				}
				c.state[o.Entity] = "gone"
			case ObsResource:
			}
		}
		for _, o := range g {
			if o.Kind == ObsResource && int64(c.inUse[o.Resource]) != o.Value {
				c.fail("tick %d: resource %d reported %d after release, reconstructed %d", o.Tick, o.Resource, o.Value, c.inUse[o.Resource])
			}
		}
	}
	for r, u := range c.inUse {
		if u < 0 || u > c.p.resources[r].capacity {
			c.fail("resource %d in use %d of %d", r, u, c.p.resources[r].capacity)
		}
	}
	for _, o := range g {
		if c.state[o.Entity] == "route" && o.Kind != ObsResource {
			c.fail("entity %d left an event still routing (not in exactly one state)", o.Entity)
			break
		}
	}
}

func (c *checker) active() int64 {
	var n int64
	for _, s := range c.state {
		if s != "gone" {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// random model generator
// ---------------------------------------------------------------------------

func randDist(rng *rand.Rand, allowZero bool) model.Distribution {
	switch rng.IntN(4) {
	case 0:
		v := float64(rng.IntN(4))
		if !allowZero && v == 0 {
			v = 1
		}
		return model.Distribution{Type: model.DistConstant, Value: &v}
	case 1:
		m := 0.25 + rng.Float64()*4
		return model.Distribution{Type: model.DistExponential, Mean: &m}
	case 2:
		lo := float64(rng.IntN(2))
		if !allowZero && lo == 0 {
			lo = 0.5
		}
		mo := lo + rng.Float64()*2
		hi := mo + rng.Float64()*3
		return model.Distribution{Type: model.DistTriangular, Min: &lo, Mode: &mo, Max: &hi}
	default:
		v := 0.0
		if !allowZero {
			v = 2
		}
		return model.Distribution{Type: model.DistConstant, Value: &v}
	}
}

func randomModel(t *testing.T, rng *rand.Rand) model.ModelIR {
	ir := minimal(t)
	ir.Resources = nil
	nres := 1 + rng.IntN(5)
	for i := 0; i < nres; i++ {
		ir.Resources = append(ir.Resources, model.Resource{ID: fmt.Sprintf("R%d", i), Type: model.ResourceMachine, Name: "r", Capacity: 1 + rng.IntN(3), QueueDiscipline: model.FIFO})
	}
	var nodes []model.Node
	var flows []model.Flow
	fid := 0
	addFlow := func(from, to string, p float64) {
		fid++
		flows = append(flows, model.Flow{ID: fmt.Sprintf("f%03d", fid), From: from, To: to, Routing: model.Routing{Type: model.RoutingProbability, Probability: p}})
	}
	nodes = append(nodes, model.Node{ID: "zz_done", Type: model.NodeSink}, model.Node{ID: "zz_gone", Type: model.NodeSink, Outcome: model.SinkAbandoned})
	nstage := 2 + rng.IntN(6)
	entry := make([]string, nstage) // where an entity entering stage i goes
	exit := make([]string, nstage)  // the node that leaves stage i
	kinds := make([]int, nstage)
	for i := 0; i < nstage; i++ {
		kinds[i] = rng.IntN(7)
		id := fmt.Sprintf("n%c%d", 'a'+rune(rng.IntN(26)), i)
		switch kinds[i] {
		case 0, 1: // process, maybe behind a queue
			n := model.Node{ID: id, Type: model.NodeProcess, ServiceTime: &model.TimedDistribution{Distribution: randDist(rng, true)}, Requires: []model.ResourceRequirement{}}
			for _, r := range rng.Perm(nres)[:rng.IntN(nres+1)] {
				n.Requires = append(n.Requires, model.ResourceRequirement{ResourceID: ir.Resources[r].ID, Units: 1 + rng.IntN(ir.Resources[r].Capacity)})
			}
			nodes = append(nodes, n)
			entry[i], exit[i] = id, id
			if kinds[i] == 1 {
				q := "q" + id
				nodes = append(nodes, model.Node{ID: q, Type: model.NodeQueue})
				addFlow(q, id, 1)
				entry[i] = q
			}
		case 2:
			nodes = append(nodes, model.Node{ID: id, Type: model.NodeDelay, DelayTime: &model.TimedDistribution{Distribution: randDist(rng, true)}})
			entry[i], exit[i] = id, id
		case 3:
			nodes = append(nodes, model.Node{ID: id, Type: model.NodeFailure})
			entry[i], exit[i] = id, id
		case 4:
			nodes = append(nodes, model.Node{ID: id, Type: model.NodeRework})
			entry[i], exit[i] = id, id
		default:
			nodes = append(nodes, model.Node{ID: id, Type: model.NodeDecision})
			entry[i], exit[i] = id, id
		}
	}
	for i := 0; i < nstage; i++ {
		next := "zz_done"
		if i+1 < nstage {
			next = entry[i+1]
		}
		if kinds[i] >= 5 { // decision: forward, back, abandon, a 0% flow
			pf := 0.5 + rng.Float64()*0.4
			pb := (1 - pf) * rng.Float64()
			addFlow(exit[i], next, pf)
			back := rng.IntN(i + 1)
			target := entry[back]
			if rng.IntN(2) == 0 {
				target = exit[back] // direct into the process, bypassing its queue
				if kinds[back] >= 5 || exit[back] == exit[i] {
					target = entry[back]
				}
			}
			if target == exit[i] {
				target = "zz_gone"
			}
			addFlow(exit[i], target, pb)
			addFlow(exit[i], "zz_gone", 1-pf-pb)
			addFlow(exit[i], "zz_done", 0)
		} else {
			addFlow(exit[i], next, 1)
		}
	}
	nsrc := 1 + rng.IntN(2)
	for s := 0; s < nsrc; s++ {
		id := fmt.Sprintf("s%d", s)
		nodes = append(nodes, model.Node{ID: id, Type: model.NodeSource, EntityType: "change", Arrival: &model.Arrival{Distribution: randDist(rng, false), MaxEntities: rng.IntN(150)}})
		addFlow(id, entry[rng.IntN(nstage)], 1)
	}
	ir.Nodes, ir.Flows = nodes, flows
	return ir
}

// The kernel agrees with the observation-only model on 300 random models,
// and gives the same observations when the same replication runs
// concurrently.
func TestRandomModelsAgainstSpec(t *testing.T) {
	const n = 300
	compiled, grants, dispatches, maxW := 0, 0, 0, 0
	for seed := range n {
		rng := rand.New(rand.NewPCG(uint64(seed), 99))
		in := input(randomModel(t, rng))
		in.Config = config(float64(60+rng.IntN(240)), model.Minute)
		in.Config.Replications = 7
		p, err := Compile(in)
		if err != nil {
			continue
		}
		compiled++
		c := newChecker(t, p)
		out, err := RunReplication(context.Background(), p, uint32(seed%7), NewBudget(400_000), c)
		if err != nil && !errors.Is(err, ErrEventBudget) && !errors.Is(err, ErrEntityVisits) {
			t.Fatalf("seed %d: %v", seed, err)
		}
		c.finish()
		if len(c.errs) > 0 {
			t.Fatalf("seed %d: %d problems, first: %v", seed, len(c.errs), c.errs)
		}
		if err == nil && (out.Created != out.Exited+out.Censored || c.active() != out.Censored) {
			t.Fatalf("seed %d: accounting %+v, reconstructed active %d", seed, out, c.active())
		}
		grants += c.grants
		dispatches += c.dispatchGroups
		maxW = max(maxW, c.maxWaiting)
		if seed%3 == 0 {
			var wg sync.WaitGroup
			res := make([][]Observation, 3)
			for i := range res {
				wg.Go(func() {
					col := &collect{}
					RunReplication(context.Background(), p, uint32(seed%7), NewBudget(400_000), col)
					res[i] = col.all
				})
			}
			wg.Wait()
			for i := 1; i < len(res); i++ {
				if !slices.Equal(res[0], res[i]) {
					t.Fatalf("seed %d: concurrent run %d differs", seed, i)
				}
			}
		}
	}
	t.Logf("%d of %d random models compiled; %d grants in %d dispatches, up to %d waiting", compiled, n, grants, dispatches, maxW)
	if compiled < n/4 || grants < 10_000 || maxW < 50 {
		t.Fatalf("generator too weak: %d compiled, %d grants, max waiting %d", compiled, grants, maxW)
	}
}
