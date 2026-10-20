package engine

// The des-go-0.1.0 kernel, frozen for the D-047 differential (ADR-026 D-047
// step 1; T-047): kernel.go, replication.go and events.go as they were at
// 0ee3d2a, with their identifiers renamed (kernel010, replication010, ...),
// test-only. The shared pieces are the compiled Program, the samplers and
// streams (the numerical profile, unchanged by T-047), Budget and the
// Observation types; a model without a finite buffer must produce exactly
// the same observations, outcome and error on both kernels. Never edit this
// file to make a differential pass: a difference is a finding.

import (
	"container/heap"
	"context"
	"fmt"

	"github.com/kcl1104/geminus/internal/model"
)

// request is an entity waiting in a process's buffer for the process's
// bundle. Requests are numbered as they queue, so seq order is the
// (queue_enter_tick, request_sequence) FIFO order of SIMULATION_SPEC §3.
type request010 struct {
	entity  uint64
	process int
	seq     uint64
	heads   int  // how many of its resources' queues it is at the front of
	held    bool // a candidate whose bundle did not fit; waits for a release
}

// resourceQueue holds, in FIFO order, the waiting requests that need one
// resource. Requests needing a resource are granted in FIFO order among
// themselves (an earlier waiting request holds the resource back from later
// ones), so a grant always removes the front.
type resourceQueue010 struct {
	items []*request010
	head  int
}

func (q *resourceQueue010) front() *request010 {
	if q.head == len(q.items) {
		return nil
	}
	return q.items[q.head]
}

// push appends r and reports whether r is now the front.
func (q *resourceQueue010) push(r *request010) bool {
	q.items = append(q.items, r)
	return q.head == len(q.items)-1
}

func (q *resourceQueue010) pop() *request010 {
	r := q.items[q.head]
	q.items[q.head] = nil
	q.head++
	if q.head == len(q.items) {
		q.items, q.head = q.items[:0], 0
	} else if q.head >= 64 && q.head*2 >= len(q.items) {
		q.items = append(q.items[:0], q.items[q.head:]...)
		q.head = 0
	}
	return r
}

// candidates are the waiting requests at the front of every queue they are
// in: by the resource-wise FIFO rule no other request can be granted. There
// is at most one per resource, plus resource-free requests (granted at the
// next dispatch), and a held one is decided again only after a release of
// one of its resources, so a dispatch costs O(candidates touched), not
// O(waiting requests) (T-021 review). A min-heap by seq.
type candidates010 []*request010

func (c candidates010) Len() int           { return len(c) }
func (c candidates010) Less(i, j int) bool { return c[i].seq < c[j].seq }
func (c candidates010) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }
func (c *candidates010) Push(x any)        { *c = append(*c, x.(*request010)) }
func (c *candidates010) Pop() any {
	old := *c
	r := old[len(old)-1]
	old[len(old)-1] = nil
	*c = old[:len(old)-1]
	return r
}

// kernel adds queues, resources, service and routing to a replication
// (docs/SIMULATION_SPEC.md §3, D-005, D-032).
type kernel010 struct {
	r               *replication010
	obs             Observer
	inUse           []int
	queues          []resourceQueue010 // per resource
	cands           candidates010      // candidates to decide at the next dispatch
	reqSeq          uint64
	active          int64
	peak            int64
	exited          int64
	events          int64
	examined        int64 // requests the dispatcher looked at (a cost measure for tests)
	dispatchPending bool
}

// runKernel is RunReplication that also returns the finished kernel.
func runKernel010(ctx context.Context, p *Program, index uint32, b *Budget, obs Observer) (*kernel010, ReplicationOutcome, error) {
	if int(index) >= p.replications {
		return nil, ReplicationOutcome{Index: index}, fmt.Errorf("%w: replication %d of a %d-replication program", ErrInternal, index, p.replications)
	}
	r := newReplication010(p, index)
	k := &kernel010{r: r, obs: obs, inUse: make([]int, len(p.resources)), queues: make([]resourceQueue010, len(p.resources))}
	r.onArrival = k.arrived
	r.onEvent = k.handle
	r.onProcessed = func() { k.events++ }
	err := r.run(ctx, b)
	if err == nil && int64(len(r.entities)) != k.active {
		err = fmt.Errorf("%w: %d entity records for %d active entities", ErrInternal, len(r.entities), k.active)
	}
	out := ReplicationOutcome{Index: index, Created: int64(r.created), Exited: k.exited, Censored: k.active, PeakActive: k.peak, Events: k.events}
	return k, out, err
}

func (k *kernel010) observe(o Observation) {
	if k.obs != nil {
		o.Tick, o.Seq = k.r.now, k.r.cur.Seq
		k.obs.Observe(o)
	}
}

func (k *kernel010) arrived(r *replication010, e uint64) error {
	k.active++
	k.peak = max(k.peak, k.active)
	if lim := int64(r.p.limits.MaxActiveEntities); k.active > lim {
		return fmt.Errorf("%w: %s arrived at tick %d with %d already in the system", ErrActiveEntities, r.EntityID(e), r.now, k.active-1)
	}
	en := r.entities[e]
	en.visits = 1 // the source is its first visit
	k.observe(Observation{Kind: ObsArrival, Entity: e, Node: en.source, Value: int64(en.n)})
	return k.route(e, en.source)
}

func (k *kernel010) handle(r *replication010, ev event010) error {
	switch ev.Kind {
	case EventServiceComplete:
		// Release the whole bundle atomically, then route on in the same
		// event; the dispatcher runs at priority 20 later in this tick (D-032).
		for _, q := range r.p.nodes[ev.Node].requires {
			k.inUse[q.resource] -= q.units
			if k.inUse[q.resource] < 0 {
				return fmt.Errorf("%w: resource %s below zero", ErrInternal, r.p.resources[q.resource].id)
			}
			k.observe(Observation{Kind: ObsResource, Resource: q.resource, Value: int64(k.inUse[q.resource])})
			// A held candidate at the front of this queue may fit now.
			if f := k.queues[q.resource].front(); f != nil && f.held {
				f.held = false
				heap.Push(&k.cands, f)
			}
		}
		k.observe(Observation{Kind: ObsServiceEnd, Entity: ev.Entity, Node: ev.Node})
		if len(r.p.nodes[ev.Node].requires) > 0 {
			if err := k.needDispatch(); err != nil {
				return err
			}
		}
		return k.route(ev.Entity, ev.Node)
	case EventDelayComplete:
		k.observe(Observation{Kind: ObsDelayEnd, Entity: ev.Entity, Node: ev.Node})
		return k.route(ev.Entity, ev.Node)
	case EventDispatch:
		return k.dispatch()
	}
	return fmt.Errorf("%w: unknown event kind %d", ErrInternal, ev.Kind)
}

// next picks the outgoing route of node n: a decision draws from its own
// routing stream and takes the first route with U < cumulative probability,
// the last route absorbing floating-point slack; any other node has exactly
// one route.
func (k *kernel010) next(n int) (int, error) {
	node := &k.r.p.nodes[n]
	if len(node.routes) == 0 {
		return 0, fmt.Errorf("%w: %s has no route", ErrInternal, node.id)
	}
	if node.typ != model.NodeDecision {
		return node.routes[0].to, nil
	}
	u := k.r.stream(n, PurposeRouting).Uniform()
	for _, rt := range node.routes {
		if u < rt.cumulative {
			return rt.to, nil
		}
	}
	return node.routes[len(node.routes)-1].to, nil
}

// route moves entity e on from node `from` until it waits (a buffer or a
// delay) or leaves (a sink). Routing is part of the event that released the
// entity, never an event of its own, so the entity is always in exactly one
// state.
func (k *kernel010) route(e uint64, from int) error {
	n := from
	en := k.r.entities[e]
	for {
		to, err := k.next(n)
		if err != nil {
			return err
		}
		n = to
		node := &k.r.p.nodes[n]
		visits := 1
		if node.typ == model.NodeQueue {
			visits = 2 // the queue, and the process whose buffer the entity waits in
		}
		en.visits += visits
		if lim := k.r.p.limits.MaxEntityVisits; en.visits > lim {
			return fmt.Errorf("%w: %s entering %s at tick %d", ErrEntityVisits, k.r.EntityID(e), node.id, k.r.now)
		}
		switch node.typ {
		case model.NodeQueue, model.NodeProcess:
			return k.enqueue(e, node.buffer, n)
		case model.NodeDelay:
			k.observe(Observation{Kind: ObsDelayStart, Entity: e, Node: n})
			return k.r.schedule(node.delay.draw(k.r.stream(n, PurposeDelay)), PriorityImmediate, EventDelayComplete, n, e)
		case model.NodeDecision:
			continue
		case model.NodeFailure:
			k.observe(Observation{Kind: ObsFailure, Entity: e, Node: n})
		case model.NodeRework:
			k.observe(Observation{Kind: ObsRework, Entity: e, Node: n})
		case model.NodeSink:
			completed := int64(1)
			if node.outcome == model.SinkAbandoned {
				completed = 0
			}
			k.observe(Observation{Kind: ObsComplete, Entity: e, Node: n, Value: completed})
			k.active--
			k.exited++
			k.r.release(e)
			return nil
		default:
			return fmt.Errorf("%w: an entity was routed into %s %s", ErrInternal, node.typ, node.id)
		}
	}
}

func (k *kernel010) enqueue(e uint64, process, at int) error {
	if process < 0 || k.r.p.nodes[process].typ != model.NodeProcess {
		return fmt.Errorf("%w: buffer %d is not a process", ErrInternal, process)
	}
	k.reqSeq++
	req := &request010{entity: e, process: process, seq: k.reqSeq}
	reqs := k.r.p.nodes[process].requires
	for _, q := range reqs {
		if k.queues[q.resource].push(req) {
			req.heads++
		}
	}
	if req.heads == len(reqs) {
		heap.Push(&k.cands, req)
	}
	k.observe(Observation{Kind: ObsQueueEnter, Entity: e, Node: process, At: at})
	return k.needDispatch()
}

// needDispatch makes sure a dispatcher event runs at the end of this tick.
func (k *kernel010) needDispatch() error {
	if k.dispatchPending {
		return nil
	}
	k.dispatchPending = true
	return k.r.schedule(0, PriorityDispatch, EventDispatch, -1, 0)
}

// dispatch applies the resource-wise FIFO rule (SIMULATION_SPEC §3): in
// (enter tick, sequence) order, a waiting request is granted only when every
// unit of its bundle is free and no earlier ungranted request needs any of
// the same resources; all-or-nothing, never a partial acquire, and a request
// that shares no resource with a held-back one can still proceed.
//
// Only candidates (requests at the front of all their resource queues) can
// satisfy the second condition, so the dispatcher looks at those alone, in
// seq order. A grant pops the request from its queues; the requests that
// move to the front have larger seqs and join the candidates, so the order
// in which requests are decided is exactly the literal scan's. A candidate
// whose bundle does not fit stays at the front, holding its resources back;
// only a release of one of its resources can make it fit (grants only take
// units), so it is set aside as held and decided again after such a
// release. Each grant reports its bundle's new levels and starts its
// service, sampling its process's service stream, in grant order.
func (k *kernel010) dispatch() error {
	k.dispatchPending = false
	p := k.r.p
	for k.cands.Len() > 0 {
		req := heap.Pop(&k.cands).(*request010)
		k.examined++
		reqs := p.nodes[req.process].requires
		fits := true
		for _, q := range reqs {
			if k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {
				fits = false
				break
			}
		}
		if !fits {
			req.held = true
			continue
		}
		for _, q := range reqs {
			k.inUse[q.resource] += q.units
			k.observe(Observation{Kind: ObsResource, Resource: q.resource, Value: int64(k.inUse[q.resource])})
			queue := &k.queues[q.resource]
			if queue.pop() != req {
				return fmt.Errorf("%w: a grant out of FIFO order on %s", ErrInternal, p.resources[q.resource].id)
			}
			if next := queue.front(); next != nil {
				if next.heads++; next.heads == len(p.nodes[next.process].requires) {
					heap.Push(&k.cands, next)
				}
			}
		}
		k.observe(Observation{Kind: ObsServiceStart, Entity: req.entity, Node: req.process})
		d := p.nodes[req.process].service.draw(k.r.stream(req.process, PurposeService))
		if err := k.r.schedule(d, PriorityServiceComplete, EventServiceComplete, req.process, req.entity); err != nil {
			return err
		}
	}
	return nil
}

// entity is one active work item. Handles start at 1 (0 means "none") and
// are never reused.
type entity010 struct {
	source  int    // node index of the source that created it
	n       uint64 // 1-based arrival number within that source
	created int64  // tick
	visits  int    // nodes entered, the source included (T-021)
}

type streamKey010 struct {
	node    int
	purpose string
}

// replication is the state of one replication: a single-threaded clock over
// one event queue (docs/SIMULATION_SPEC.md §1–§2). The kernel (T-021) adds
// queues, resources and routing through the two hooks.
type replication010 struct {
	p       *Program
	index   uint32
	seed    uint64
	now     int64
	q       eventQueue010
	streams map[streamKey010]*SplitMix64 // looked up, never iterated
	arrived []uint64                     // per node: entities its source created
	// entities holds only active entities: the kernel releases one when it
	// reaches a sink, so memory is bounded by max_active_entities, not by
	// every entity a long run creates (T-020 review). Looked up, never iterated.
	entities map[uint64]*entity010
	created  uint64 // the last entity handle

	// onArrival receives each new entity after its source has scheduled the
	// next arrival.
	onArrival func(r *replication010, entity010 uint64) error
	// onEvent handles every event that is not an arrival.
	onEvent func(r *replication010, e event010) error
	// onProcessed, if set, is called once per processed event.
	onProcessed func()
	// cur is the event being processed.
	cur event010
}

func newReplication010(p *Program, index uint32) *replication010 {
	return &replication010{
		p:        p,
		index:    index,
		seed:     ReplicationSeed(p.seed, index),
		streams:  map[streamKey010]*SplitMix64{},
		arrived:  make([]uint64, len(p.nodes)),
		entities: map[uint64]*entity010{},
	}
}

// stream returns the (node, purpose) stream of this replication, created on
// first use from its own seed; streams never share state.
func (r *replication010) stream(node int, purpose string) *SplitMix64 {
	k := streamKey010{node, purpose}
	s, ok := r.streams[k]
	if !ok {
		s = Stream(r.seed, r.p.nodes[node].id, purpose)
		r.streams[k] = s
	}
	return s
}

// EntityID is an active entity's stable, human-readable ID:
// r<replication index>/<source node ID>/<n>. Node IDs cannot contain "/".
// It is "" for an entity that is not in the system.
func (r *replication010) EntityID(e uint64) string {
	en, ok := r.entities[e]
	if !ok {
		return ""
	}
	return FormatEntityID(r.index, r.p.nodes[en.source].id, en.n)
}

// release forgets an entity that left the system.
func (r *replication010) release(e uint64) { delete(r.entities, e) }

// schedule adds an event at now+delay. Delays are at most beyondCap, so the
// tick cannot overflow; an event after the horizon is never processed and is
// not scheduled at all (the draw that produced it has been consumed).
func (r *replication010) schedule(delay int64, priority uint8, kind EventKind, node int, ent uint64) error {
	if delay < 0 || delay > beyondCap {
		return fmt.Errorf("%w: delay %d ticks", ErrInternal, delay)
	}
	at := r.now + delay
	if at > r.p.horizon {
		return nil
	}
	r.q.schedule(event010{Tick: at, Priority: priority, Kind: kind, Node: node, Entity: ent})
	return nil
}

// scheduleArrival draws the next interarrival of source src unless the
// source has created max_entities already.
func (r *replication010) scheduleArrival(src int) error {
	n := &r.p.nodes[src]
	if n.maxEntities > 0 && r.arrived[src] >= uint64(n.maxEntities) {
		return nil
	}
	return r.schedule(n.arrival.draw(r.stream(src, PurposeArrival)), PriorityImmediate, EventArrival, src, 0)
}

// run executes the replication up to and including the horizon tick.
// Sources start in node-ID order; the first arrival comes one interarrival
// after t=0. Every pop is a cancellation checkpoint and spends one event of
// the run's budget.
func (r *replication010) run(ctx context.Context, b *Budget) error {
	for _, src := range r.p.sources {
		if err := r.scheduleArrival(src); err != nil {
			return err
		}
	}
	done := ctx.Done()
	for {
		select {
		case <-done:
			return ctx.Err()
		default:
		}
		e, ok := r.q.next()
		if !ok {
			return nil
		}
		if e.Tick < r.now || e.Tick > r.p.horizon {
			return fmt.Errorf("%w: event at tick %d (now %d, horizon %d)", ErrInternal, e.Tick, r.now, r.p.horizon)
		}
		r.now, r.cur = e.Tick, e
		if err := b.spend(ctx); err != nil {
			return err
		}
		if r.onProcessed != nil {
			r.onProcessed()
		}
		if err := r.handle(e); err != nil {
			return err
		}
	}
}

func (r *replication010) handle(e event010) error {
	if e.Kind != EventArrival {
		if r.onEvent == nil {
			return fmt.Errorf("%w: no handler for event kind %d", ErrInternal, e.Kind)
		}
		return r.onEvent(r, e)
	}
	src := e.Node
	r.arrived[src]++
	r.created++
	id := r.created
	r.entities[id] = &entity010{source: src, n: r.arrived[src], created: r.now}
	// The next arrival is scheduled before the new entity moves on; the order
	// fixes sequence numbers and therefore same-tick ties.
	if err := r.scheduleArrival(src); err != nil {
		return err
	}
	if r.onArrival != nil {
		return r.onArrival(r, id)
	}
	return nil
}

// event is one scheduled occurrence. Seq is unique and strictly increasing
// within a replication, so (Tick, Priority, Seq) is a total order and the
// processing order never depends on heap internals, map order, wall clock or
// goroutine scheduling.
type event010 struct {
	Tick     int64
	Priority uint8
	Seq      uint64
	Kind     EventKind
	Node     int    // compiled node index
	Entity   uint64 // index into the replication's entity table; 0 when none
}

func (a event010) before(b event010) bool {
	if a.Tick != b.Tick {
		return a.Tick < b.Tick
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	return a.Seq < b.Seq
}

// eventQueue is a binary min-heap by (Tick, Priority, Seq).
type eventQueue010 struct {
	items []event010
	seq   uint64
}

func (q *eventQueue010) Len() int           { return len(q.items) }
func (q *eventQueue010) Less(i, j int) bool { return q.items[i].before(q.items[j]) }
func (q *eventQueue010) Swap(i, j int)      { q.items[i], q.items[j] = q.items[j], q.items[i] }
func (q *eventQueue010) Push(x any)         { q.items = append(q.items, x.(event010)) }
func (q *eventQueue010) Pop() any {
	last := q.items[len(q.items)-1]
	q.items = q.items[:len(q.items)-1]
	return last
}

// schedule adds e with the next sequence number and returns it.
func (q *eventQueue010) schedule(e event010) event010 {
	q.seq++
	e.Seq = q.seq
	heap.Push(q, e)
	return e
}

// next removes and returns the earliest event.
func (q *eventQueue010) next() (event010, bool) {
	if len(q.items) == 0 {
		return event010{}, false
	}
	return heap.Pop(q).(event010), true
}

// peek returns the earliest event without removing it.
func (q *eventQueue010) peek() (event010, bool) {
	if len(q.items) == 0 {
		return event010{}, false
	}
	return q.items[0], true
}
