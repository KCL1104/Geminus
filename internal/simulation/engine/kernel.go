package engine

import (
	"container/heap"
	"context"
	"errors"
	"fmt"

	"github.com/kcl1104/geminus/internal/model"
)

// Typed limit failures (docs/SIMULATION_SPEC.md §8); like ErrEventBudget they
// fail the run.
var (
	// ErrActiveEntities: more entities were in the system at once than
	// max_active_entities allows.
	ErrActiveEntities = errors.New("engine: active entity limit reached")
	// ErrEntityVisits: one entity entered more nodes than max_entity_visits
	// allows (a runtime guard behind the compile-time zero-time-cycle check).
	ErrEntityVisits = errors.New("engine: entity visit limit reached")
)

// ObservationKind says what an Observation records.
type ObservationKind uint8

const (
	ObsArrival      ObservationKind = iota + 1 // Entity was created at source Node; Value is its arrival number there
	ObsQueueEnter                              // Entity joined the request buffer of process Node
	ObsServiceStart                            // Entity's request was granted and its service at Node began
	ObsServiceEnd                              // Entity's service at Node ended and its bundle was released
	ObsResource                                // Resource's units in use changed to Value
	ObsDelayStart                              // Entity entered delay Node
	ObsDelayEnd                                // Entity left delay Node
	ObsFailure                                 // generic failure observation at Node (not DORA)
	ObsRework                                  // generic rework observation at Node
	ObsComplete                                // Entity reached sink Node; Value 1 completed, 0 abandoned
)

// Observation is one fact the kernel reports, in processing order. Seq is
// the sequence of the event during which it happened, so (Tick, Seq) and
// the call order reproduce the engine's event order for traces.
type Observation struct {
	Kind     ObservationKind
	Tick     int64
	Seq      uint64
	Entity   uint64
	Node     int
	Resource int
	Value    int64
}

// Observer receives every observation of one replication. It must not
// retain the replication or call back into the engine.
type Observer interface{ Observe(Observation) }

// ReplicationOutcome summarizes a finished replication.
type ReplicationOutcome struct {
	Index      uint32
	Created    int64 // entities created by sources
	Exited     int64 // entities that reached a sink (completed or abandoned)
	Censored   int64 // entities still in the system at the horizon
	PeakActive int64 // most entities in the system at once
	Events     int64 // events this replication processed
}

// request is an entity waiting in a process's buffer for the process's
// bundle. Requests are numbered as they queue, so seq order is the
// (queue_enter_tick, request_sequence) FIFO order of SIMULATION_SPEC §3.
type request struct {
	entity  uint64
	process int
	seq     uint64
	heads   int // how many of its resources' queues it is at the front of
}

// resourceQueue holds, in FIFO order, the waiting requests that need one
// resource. Requests needing a resource are granted in FIFO order among
// themselves (an earlier waiting request holds the resource back from later
// ones), so a grant always removes the front.
type resourceQueue struct {
	items []*request
	head  int
}

func (q *resourceQueue) front() *request {
	if q.head == len(q.items) {
		return nil
	}
	return q.items[q.head]
}

// push appends r and reports whether r is now the front.
func (q *resourceQueue) push(r *request) bool {
	q.items = append(q.items, r)
	return q.head == len(q.items)-1
}

func (q *resourceQueue) pop() *request {
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
// next dispatch), so a dispatch costs O(resources), not O(waiting requests)
// (T-021 review). A min-heap by seq.
type candidates []*request

func (c candidates) Len() int           { return len(c) }
func (c candidates) Less(i, j int) bool { return c[i].seq < c[j].seq }
func (c candidates) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }
func (c *candidates) Push(x any)        { *c = append(*c, x.(*request)) }
func (c *candidates) Pop() any {
	old := *c
	r := old[len(old)-1]
	old[len(old)-1] = nil
	*c = old[:len(old)-1]
	return r
}

// kernel adds queues, resources, service and routing to a replication
// (docs/SIMULATION_SPEC.md §3, D-005, D-032).
type kernel struct {
	r               *replication
	obs             Observer
	inUse           []int
	queues          []resourceQueue // per resource
	cands           candidates
	held            []*request // dispatch scratch: candidates whose bundle does not fit now
	reqSeq          uint64
	active          int64
	peak            int64
	exited          int64
	events          int64
	examined        int64 // requests the dispatcher looked at (a cost measure for tests)
	dispatchPending bool
}

// RunReplication executes replication index of p up to and including the
// horizon, reporting every observation to obs (nil for none). It spends the
// run's shared budget and checks ctx before every event.
func RunReplication(ctx context.Context, p *Program, index uint32, b *Budget, obs Observer) (ReplicationOutcome, error) {
	_, out, err := runKernel(ctx, p, index, b, obs)
	return out, err
}

// runKernel is RunReplication that also returns the finished kernel.
func runKernel(ctx context.Context, p *Program, index uint32, b *Budget, obs Observer) (*kernel, ReplicationOutcome, error) {
	if int(index) >= p.replications {
		return nil, ReplicationOutcome{Index: index}, fmt.Errorf("%w: replication %d of a %d-replication program", ErrInternal, index, p.replications)
	}
	r := newReplication(p, index)
	k := &kernel{r: r, obs: obs, inUse: make([]int, len(p.resources)), queues: make([]resourceQueue, len(p.resources))}
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

func (k *kernel) observe(o Observation) {
	if k.obs != nil {
		o.Tick, o.Seq = k.r.now, k.r.cur.Seq
		k.obs.Observe(o)
	}
}

func (k *kernel) arrived(r *replication, e uint64) error {
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

func (k *kernel) handle(r *replication, ev event) error {
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
func (k *kernel) next(n int) (int, error) {
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
func (k *kernel) route(e uint64, from int) error {
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
			return k.enqueue(e, node.buffer)
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

func (k *kernel) enqueue(e uint64, process int) error {
	if process < 0 || k.r.p.nodes[process].typ != model.NodeProcess {
		return fmt.Errorf("%w: buffer %d is not a process", ErrInternal, process)
	}
	k.reqSeq++
	req := &request{entity: e, process: process, seq: k.reqSeq}
	reqs := k.r.p.nodes[process].requires
	for _, q := range reqs {
		if k.queues[q.resource].push(req) {
			req.heads++
		}
	}
	if req.heads == len(reqs) {
		heap.Push(&k.cands, req)
	}
	k.observe(Observation{Kind: ObsQueueEnter, Entity: e, Node: process})
	return k.needDispatch()
}

// needDispatch makes sure a dispatcher event runs at the end of this tick.
func (k *kernel) needDispatch() error {
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
// whose bundle does not fit stays at the front, holding its resources back,
// until a later dispatch. Each grant reports its bundle's new levels and
// starts its service, sampling its process's service stream, in grant order.
func (k *kernel) dispatch() error {
	k.dispatchPending = false
	p := k.r.p
	held := k.held[:0]
	for k.cands.Len() > 0 {
		req := heap.Pop(&k.cands).(*request)
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
			held = append(held, req)
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
	for _, req := range held {
		heap.Push(&k.cands, req)
	}
	clear(held)
	k.held = held[:0]
	return nil
}
