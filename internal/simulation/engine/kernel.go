package engine

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"strconv"

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
	// ErrDeadlock: a circular wait (ADR-026 D-050 B1): entities are blocked
	// by full buffers or wait for units that only blocked entities hold, and
	// nothing in service or in a delay can ever release one. A valid model
	// never reaches it (D-050 R1); it fails the run with a *DeadlockError.
	ErrDeadlock = errors.New("engine: deadlock")
	// ErrStalled: a bundle that can never be filled (ADR-026 D-050 B2): a
	// waiting request at the front of every FIFO it is in, none of its
	// bundle in use and not fitting the units that are up, has been so
	// longer than the longest repair of its resources (TTRmax) at the end
	// of a tick (D-074 (c)). A valid model never reaches it (D-050 R3); it
	// fails the run with a *StalledError. A bundle whose repair time has no
	// bound (exponential, lognormal, Weibull) is never checked.
	ErrStalled = errors.New("engine: stalled")
)

// StalledError is ErrStalled with what the run failed on (D-050 B2): the
// tick it was found at, the earliest such request (by queue order), its
// process, the tick it has been in that state since and the deadline
// Since + TTRmax it passed.
type StalledError struct {
	Tick     int64  // the end of the tick it was found at
	Entity   string // r<replication>/<source>/<n>
	Node     string // the process it waits for
	Since    int64  // the first tick at whose end it was in the state
	Deadline int64  // Since + TTRmax of its bundle
}

func (e *StalledError) Error() string { return "engine: " + e.Reason() }
func (e *StalledError) Unwrap() error { return ErrStalled }

// Reason is the failure's message (internal/runs/runner records it).
func (e *StalledError) Reason() string {
	return fmt.Sprintf("the replication stalled at tick %d: %s waits for node %s since tick %d with none of its bundle in use; it was due by tick %d",
		e.Tick, e.Entity, e.Node, e.Since, e.Deadline)
}

// DeadlockError is ErrDeadlock with what the run failed on (D-050 B1): the
// tick it was found at and the earliest entity still blocked (or, without
// one, the earliest waiting request), its node and the tick it has waited
// since.
type DeadlockError struct {
	Tick   int64  // when the circular wait was found
	Entity string // r<replication>/<source>/<n>
	Node   string // where the entity is blocked, or the process it waits for
	Since  int64  // the tick it was blocked, or queued, at
	Queued bool   // no entity was blocked: Entity is a waiting request
}

func (e *DeadlockError) Error() string { return "engine: " + e.Reason() }
func (e *DeadlockError) Unwrap() error { return ErrDeadlock }

// Reason is the failure's message (internal/runs/runner records it).
func (e *DeadlockError) Reason() string {
	verb := "is blocked at"
	if e.Queued {
		verb = "waits for"
	}
	return fmt.Sprintf("the replication deadlocked at tick %d: %s %s node %s since tick %d", e.Tick, e.Entity, verb, e.Node, e.Since)
}

// ObservationKind says what an Observation records.
type ObservationKind uint8

const (
	ObsArrival      ObservationKind = iota + 1 // Entity was created at source Node; Value is its arrival number there
	ObsQueueEnter                              // Entity joined the request buffer of process Node
	ObsServiceStart                            // Entity's request was granted and its service at Node began
	ObsServiceEnd                              // Entity's service at Node ended (and, unless it is blocked, its bundle was released)
	ObsResource                                // Resource's units in use changed to Value
	ObsDelayStart                              // Entity entered delay Node
	ObsDelayEnd                                // Entity left delay Node
	ObsFailure                                 // generic failure observation at Node (not DORA)
	ObsRework                                  // generic rework observation at Node
	ObsComplete                                // Entity reached sink Node; Value 1 completed, 0 abandoned
	// ADR-026 D-049 (T-047): numbered after the existing kinds, which keep
	// their values (D-047), and only ever observed in a model with a finite
	// buffer.
	ObsBlocked   // Entity's route from Node (a source, process or delay) reached the full buffer of At's process; it stays at Node
	ObsUnblocked // Entity, blocked at Node, moves into the buffer it was blocked by (an ObsQueueEnter follows)
	// ADR-026 D-051 (T-048): only ever observed in a model with a resource
	// that breaks down. At is the failure clock (unit index) of Resource.
	ObsResourceDown     // a unit of Resource went down for failure clock At; Value is Resource's units down
	ObsResourceUp       // the unit down for failure clock At was repaired; Value is Resource's units down
	ObsBreakdownPending // failure clock At fired with no unit idle: it waits for the next release (finish-first); Value is the failures waiting
)

// Observation is one fact the kernel reports, in processing order: by tick,
// then event priority, then sequence. Seq is the sequence of the event
// during which it happened; several observations share one, and within a
// tick Seq can go back when a higher-priority event was scheduled later, so
// the call order, not (Tick, Seq), is the engine's order.
type Observation struct {
	Kind     ObservationKind
	Tick     int64
	Seq      uint64
	Entity   uint64
	Node     int
	Resource int
	Value    int64
	// At is, for ObsQueueEnter, the node the entity entered: an explicit
	// queue, or the process itself when routed straight into it (Node is
	// always the process whose buffer it waits in). For ObsBlocked and
	// ObsUnblocked it is the node its route reached (that queue or that
	// process). For ObsResourceDown, ObsResourceUp and ObsBreakdownPending
	// it is the failure clock. Unused otherwise.
	At int
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
	entered int64 // tick it queued at
	heads   int   // how many of its resources' queues it is at the front of
	held    bool  // a candidate whose bundle did not fit; waits for a release (or a repair)
	// stalled is the first tick at whose end the request was in D-050 B2's
	// state (held, none of its bundle in use), -1 when it is not.
	stalled int64
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
// next dispatch), and a held one is decided again only after a release of
// one of its resources, so a dispatch costs O(candidates touched), not
// O(waiting requests) (T-021 review). A min-heap by seq.
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

// blockedEntity is an entity whose route reached a full buffer (D-049): it
// stays at its departing node, a process keeping its whole bundle, until a
// grant at that buffer makes room.
type blockedEntity struct {
	entity uint64
	from   int    // the departing node: a source, a process or a delay
	at     int    // the node its route reached: the buffer's queue or its process
	order  uint64 // blocking order, over all buffers
	since  int64  // tick
}

// blockedQueue is one buffer's blocked entities, in the order they were
// blocked (the blocked FIFO of D-049).
type blockedQueue struct {
	items []blockedEntity
	head  int
}

func (q *blockedQueue) len() int { return len(q.items) - q.head }

func (q *blockedQueue) pop() blockedEntity {
	b := q.items[q.head]
	q.head++
	if q.head == len(q.items) {
		q.items, q.head = q.items[:0], 0
	} else if q.head >= 64 && q.head*2 >= len(q.items) {
		q.items = append(q.items[:0], q.items[q.head:]...)
		q.head = 0
	}
	return b
}

// kernel adds queues, resources, service and routing to a replication
// (docs/SIMULATION_SPEC.md §3, D-005, D-032), and finite buffers with
// blocking after service (ADR-026 D-049, T-047).
type kernel struct {
	r               *replication
	obs             Observer
	inUse           []int
	queues          []resourceQueue // per resource
	cands           candidates      // candidates to decide at the next dispatch
	reqSeq          uint64
	active          int64
	peak            int64
	exited          int64
	events          int64
	examined        int64 // requests the dispatcher looked at (a cost measure for tests)
	dispatchPending bool

	// Finite buffers (D-049). waiting counts each process's waiting
	// requests; blocked is allocated only when the program has a finite
	// buffer. inService and inDelay count entities whose SERVICE_COMPLETE or
	// DELAY_COMPLETE is due, scheduled or past the horizon (D-050 B1).
	waiting    []int
	blocked    []blockedQueue // per process node
	nBlocked   int
	blockOrder uint64
	held       int // held candidates
	inService  int64
	inDelay    int64
	inDispatch bool
	path       []int // the failure and rework nodes of the route being resolved

	// Breakdowns (D-051, T-048). down counts each resource's units down
	// (all 0 without breakdowns); pending, allocated only when the program
	// has breakdowns, holds per resource the failure clocks waiting for a
	// release, in the order they fired; clocks holds per resource that
	// breaks down and per unit its TTF and TTR streams, created on first
	// use.
	down    []int
	pending [][]int
	clocks  [][]clockStreams
}

// clockStreams are one failure clock's streams.
type clockStreams struct{ ttf, ttr *SplitMix64 }

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
	k := &kernel{r: r, obs: obs, inUse: make([]int, len(p.resources)), queues: make([]resourceQueue, len(p.resources)),
		waiting: make([]int, len(p.nodes)), down: make([]int, len(p.resources))}
	if p.finite {
		k.blocked = make([]blockedQueue, len(p.nodes))
	}
	if p.breakdowns {
		k.pending, k.clocks = make([][]int, len(p.resources)), make([][]clockStreams, len(p.resources))
		r.onStart = k.startClocks
	}
	r.onArrival = func(r *replication, e uint64) error { return k.after(k.arrived(r, e)) }
	r.onEvent = func(r *replication, ev event) error { return k.after(k.handle(r, ev)) }
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

// after runs at the end of every event. In a program with a finite buffer
// it is the D-050 B1 check, unless a dispatch is still due in this tick (it
// checks at its own end); in a program with breakdowns, at the end of a
// tick with a held candidate, the B2 check (D-074 (c)). A program with
// neither never checks, so it runs as des-go-0.1.0 did (D-047).
func (k *kernel) after(err error) error {
	if err != nil {
		return err
	}
	p := k.r.p
	if p.finite && !k.dispatchPending {
		if err := k.deadlock(); err != nil {
			return err
		}
	}
	if p.breakdowns && k.held > 0 && k.tickEnds() {
		return k.stall()
	}
	return nil
}

// tickEnds reports whether the event just processed is the last of its tick.
func (k *kernel) tickEnds() bool {
	next, ok := k.r.q.peek()
	return !ok || next.Tick > k.r.now
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
	return k.depart(e, en.source)
}

func (k *kernel) handle(r *replication, ev event) error {
	switch ev.Kind {
	case EventServiceComplete:
		return k.serviceComplete(ev.Entity, ev.Node)
	case EventDelayComplete:
		k.inDelay--
		k.observe(Observation{Kind: ObsDelayEnd, Entity: ev.Entity, Node: ev.Node})
		return k.depart(ev.Entity, ev.Node)
	case EventDispatch:
		return k.dispatch()
	case EventFailure:
		return k.failure(int(ev.Resource), int(ev.Unit))
	case EventRepair:
		return k.repair(int(ev.Resource), int(ev.Unit))
	}
	return fmt.Errorf("%w: unknown event kind %d", ErrInternal, ev.Kind)
}

// startClocks starts every failure clock at t=0, after the sources' first
// arrivals (D-048): resources in ID order, units in index order, each
// failing one TTF from now. A failure after the horizon is never
// scheduled (and takes no sequence number), so breakdowns whose first
// failure is beyond it leave the run exactly as it was without them.
func (k *kernel) startClocks(r *replication) error {
	for i := range r.p.resources {
		res := &r.p.resources[i]
		if res.ttf == nil {
			continue
		}
		k.clocks[i] = make([]clockStreams, res.capacity)
		for u := range res.capacity {
			if err := k.scheduleFailure(i, u); err != nil {
				return err
			}
		}
	}
	return nil
}

// streams returns failure clock u of resource i's streams, creating them
// on first use.
func (k *kernel) streams(i, u int) *clockStreams {
	c := &k.clocks[i][u]
	if c.ttf == nil {
		id, unit := k.r.p.resources[i].id, strconv.Itoa(u)
		c.ttf, c.ttr = Stream(k.r.seed, id, PurposeFailurePrefix+unit), Stream(k.r.seed, id, PurposeRepairPrefix+unit)
	}
	return c
}

// scheduleFailure draws clock u's next time to failure and schedules it.
func (k *kernel) scheduleFailure(i, u int) error {
	d := k.r.p.resources[i].ttf.draw(k.streams(i, u).ttf)
	return k.r.scheduleClock(d, EventFailure, i, u)
}

// failure is failure clock u of resource i firing (D-051, priority 15): a
// unit neither in use nor down goes down at once; with none, the failure
// waits for the next release (finish-first), behind the failures already
// waiting.
func (k *kernel) failure(i, u int) error {
	if k.r.p.resources[i].capacity-k.down[i]-k.inUse[i] > 0 {
		return k.goDown(i, u)
	}
	k.pending[i] = append(k.pending[i], u)
	k.observe(Observation{Kind: ObsBreakdownPending, Resource: i, At: u, Value: int64(len(k.pending[i]))})
	return nil
}

// goDown takes a unit of resource i down for clock u and schedules its
// repair one TTR (drawn now) later.
func (k *kernel) goDown(i, u int) error {
	k.down[i]++
	k.observe(Observation{Kind: ObsResourceDown, Resource: i, At: u, Value: int64(k.down[i])})
	d := k.r.p.resources[i].ttr.draw(k.streams(i, u).ttr)
	return k.r.scheduleClock(d, EventRepair, i, u)
}

// repair ends clock u's downtime (priority 15): the unit is up, the clock
// draws its next time to failure, and, since the capacity rose without a
// release, the held front of the resource's FIFO is decided again at a
// dispatch later in this tick. A waiting failure is not given the repaired
// unit: only a released unit satisfies it (D-074 (a)).
func (k *kernel) repair(i, u int) error {
	if k.down[i]--; k.down[i] < 0 {
		return fmt.Errorf("%w: resource %s has %d units down", ErrInternal, k.r.p.resources[i].id, k.down[i])
	}
	k.observe(Observation{Kind: ObsResourceUp, Resource: i, At: u, Value: int64(k.down[i])})
	if err := k.scheduleFailure(i, u); err != nil {
		return err
	}
	if f := k.queues[i].front(); f != nil && f.held {
		f.held = false
		k.held--
		heap.Push(&k.cands, f)
		return k.needDispatch()
	}
	return nil
}

// serviceComplete ends entity e's service at process n. The route is
// resolved first (D-049: routing draws and visits, no observation, no
// event); unless it ends at a full buffer, the bundle is released
// atomically, then the entity moves on in the same event, exactly as in
// des-go-0.1.0: ObsResource per unit type, ServiceEnd, the dispatch at
// priority 20 later in this tick (D-032), the route's observations, the
// entity's new place. When it ends at a full buffer the entity is blocked
// at n and keeps the bundle: ServiceEnd, the route's observations, Blocked,
// and no dispatch (nothing was released or queued).
func (k *kernel) serviceComplete(e uint64, n int) error {
	k.inService--
	to, err := k.resolve(e, n)
	block := err == nil && k.full(to)
	reqs := k.r.p.nodes[n].requires
	if !block {
		if rerr := k.release(reqs); rerr != nil {
			return rerr
		}
	}
	for _, q := range reqs {
		if k.inUse[q.resource] < 0 {
			return fmt.Errorf("%w: resource %s below zero", ErrInternal, k.r.p.resources[q.resource].id)
		}
	}
	k.observe(Observation{Kind: ObsServiceEnd, Entity: e, Node: n})
	if !block && len(reqs) > 0 {
		if derr := k.needDispatch(); derr != nil {
			return derr
		}
	}
	k.passed(e)
	if err != nil {
		return err
	}
	if block {
		k.block(e, n, to)
		return nil
	}
	return k.enter(e, to)
}

// release frees a bundle's units, reporting each level; the released units
// of a resource with waiting failures go down for them first, one unit per
// failure in the order they fired (D-051 finish-first, before any grant);
// a held candidate at the front of one of these resources may fit now.
func (k *kernel) release(reqs []requirement) error {
	for _, q := range reqs {
		k.inUse[q.resource] -= q.units
		k.observe(Observation{Kind: ObsResource, Resource: q.resource, Value: int64(k.inUse[q.resource])})
		if k.pending != nil {
			for n := min(q.units, len(k.pending[q.resource])); n > 0; n-- {
				u := k.pending[q.resource][0]
				k.pending[q.resource] = k.pending[q.resource][1:]
				if err := k.goDown(q.resource, u); err != nil {
					return err
				}
			}
		}
		if f := k.queues[q.resource].front(); f != nil && f.held {
			f.held = false
			k.held--
			heap.Push(&k.cands, f)
		}
	}
	return nil
}

// depart moves entity e on from a source (after its arrival) or a delay
// (after its delay): its route's observations, then its new place, or
// blocked at from when the route ends at a full buffer (D-049). Nothing is
// held at a source or a delay.
func (k *kernel) depart(e uint64, from int) error {
	to, err := k.resolve(e, from)
	k.passed(e)
	if err != nil {
		return err
	}
	if k.full(to) {
		k.block(e, from, to)
		return nil
	}
	return k.enter(e, to)
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

// resolve follows entity e's route from node `from` through decisions,
// failure and rework nodes to where it waits (a buffer or a delay) or leaves
// (a sink), drawing the decisions' routing streams and counting visits as
// it goes; it records the failure and rework nodes passed in k.path and
// neither observes nor schedules anything (D-049: resolve, then commit).
// On a visit limit the error names the node the entity was entering, and
// k.path holds the nodes passed before it. Routing is part of the event
// that released the entity, never an event of its own, so the entity is
// always in exactly one state.
func (k *kernel) resolve(e uint64, from int) (int, error) {
	k.path = k.path[:0]
	n := from
	en := k.r.entities[e]
	for {
		to, err := k.next(n)
		if err != nil {
			return -1, err
		}
		n = to
		node := &k.r.p.nodes[n]
		visits := 1
		if node.typ == model.NodeQueue {
			visits = 2 // the queue, and the process whose buffer the entity waits in
		}
		en.visits += visits
		if lim := k.r.p.limits.MaxEntityVisits; en.visits > lim {
			return -1, fmt.Errorf("%w: %s entering %s at tick %d", ErrEntityVisits, k.r.EntityID(e), node.id, k.r.now)
		}
		switch node.typ {
		case model.NodeQueue, model.NodeProcess, model.NodeDelay, model.NodeSink:
			return n, nil
		case model.NodeDecision:
		case model.NodeFailure, model.NodeRework:
			k.path = append(k.path, n)
		default:
			return -1, fmt.Errorf("%w: an entity was routed into %s %s", ErrInternal, node.typ, node.id)
		}
	}
}

// passed observes the failure and rework nodes of the resolved route.
func (k *kernel) passed(e uint64) {
	for _, n := range k.path {
		kind := ObsFailure
		if k.r.p.nodes[n].typ == model.NodeRework {
			kind = ObsRework
		}
		k.observe(Observation{Kind: kind, Entity: e, Node: n})
	}
}

// full reports whether node to (the end of a resolved route) is a buffer
// holding as many waiting entities as its capacity. A buffer with blocked
// entities is always full at an event's boundary (D-049).
func (k *kernel) full(to int) bool {
	if k.blocked == nil {
		return false
	}
	b := k.r.p.nodes[to].buffer
	if b < 0 {
		return false
	}
	c := k.r.p.nodes[b].capacity
	return c > 0 && k.waiting[b] >= c
}

// enter puts entity e where its resolved route ends: into a buffer, a delay
// (sampling it now) or a sink.
func (k *kernel) enter(e uint64, to int) error {
	node := &k.r.p.nodes[to]
	switch node.typ {
	case model.NodeQueue, model.NodeProcess:
		return k.enqueue(e, node.buffer, to)
	case model.NodeDelay:
		k.observe(Observation{Kind: ObsDelayStart, Entity: e, Node: to})
		k.inDelay++
		return k.r.schedule(node.delay.draw(k.r.stream(to, PurposeDelay)), PriorityImmediate, EventDelayComplete, to, e)
	case model.NodeSink:
		completed := int64(1)
		if node.outcome == model.SinkAbandoned {
			completed = 0
		}
		k.observe(Observation{Kind: ObsComplete, Entity: e, Node: to, Value: completed})
		k.active--
		k.exited++
		k.r.release(e)
		return nil
	}
	return fmt.Errorf("%w: an entity was routed into %s %s", ErrInternal, node.typ, node.id)
}

// block leaves entity e at node from: its route ended at the full buffer
// of `to` (D-049). It joins that buffer's blocked FIFO; nothing is released
// or scheduled.
func (k *kernel) block(e uint64, from, to int) {
	b := k.r.p.nodes[to].buffer
	k.blockOrder++
	k.blocked[b].items = append(k.blocked[b].items, blockedEntity{entity: e, from: from, at: to, order: k.blockOrder, since: k.r.now})
	k.nBlocked++
	k.observe(Observation{Kind: ObsBlocked, Entity: e, Node: from, At: to})
}

func (k *kernel) enqueue(e uint64, process, at int) error {
	if process < 0 || k.r.p.nodes[process].typ != model.NodeProcess {
		return fmt.Errorf("%w: buffer %d is not a process", ErrInternal, process)
	}
	if c := k.r.p.nodes[process].capacity; c > 0 && k.waiting[process] >= c {
		return fmt.Errorf("%w: %s entered the full buffer of %s", ErrInternal, k.r.EntityID(e), k.r.p.nodes[process].id)
	}
	k.reqSeq++
	req := &request{entity: e, process: process, seq: k.reqSeq, entered: k.r.now, stalled: -1}
	reqs := k.r.p.nodes[process].requires
	for _, q := range reqs {
		if k.queues[q.resource].push(req) {
			req.heads++
		}
	}
	if req.heads == len(reqs) {
		heap.Push(&k.cands, req)
	}
	k.waiting[process]++
	k.observe(Observation{Kind: ObsQueueEnter, Entity: e, Node: process, At: at})
	return k.needDispatch()
}

// needDispatch makes sure a dispatcher event runs at the end of this tick.
// During a dispatch it does nothing: that dispatch goes on until no
// candidate fits (D-049), so a release or a request it makes needs no
// other one, and takes no sequence number.
func (k *kernel) needDispatch() error {
	if k.dispatchPending || k.inDispatch {
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
// whose bundle does not fit (in use + units > C - D, D the units down,
// D-051) stays at the front, holding its resources back; only a release or
// a repair of one of its resources can make it fit (grants and failures
// only take units), so it is set aside as held and decided again after
// such a release or repair. Each grant reports its bundle's new levels and starts its
// service, sampling its process's service stream, in grant order.
//
// A grant at a full buffer with blocked entities makes room for the first
// of them (D-049): right after the grant it moves in, releasing its
// departing process's bundle (whose held fronts become candidates again),
// and queues at the end of the FIFO with a new sequence. The dispatch goes
// on, smallest sequence first, until no candidate fits, so a chain of
// grants and unblocks completes in one dispatch.
func (k *kernel) dispatch() error {
	k.dispatchPending = false
	k.inDispatch = true
	defer func() { k.inDispatch = false }()
	p := k.r.p
	for k.cands.Len() > 0 {
		req := heap.Pop(&k.cands).(*request)
		k.examined++
		reqs := p.nodes[req.process].requires
		fits := true
		for _, q := range reqs {
			// D-051: only units neither in use nor down can be granted.
			if k.inUse[q.resource]+q.units > p.resources[q.resource].capacity-k.down[q.resource] {
				fits = false
				break
			}
		}
		if !fits {
			req.held = true
			k.held++
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
		k.waiting[req.process]--
		k.inService++
		k.observe(Observation{Kind: ObsServiceStart, Entity: req.entity, Node: req.process})
		d := p.nodes[req.process].service.draw(k.r.stream(req.process, PurposeService))
		if err := k.r.schedule(d, PriorityServiceComplete, EventServiceComplete, req.process, req.entity); err != nil {
			return err
		}
		if k.blocked != nil && k.blocked[req.process].len() > 0 {
			if err := k.unblock(req.process); err != nil {
				return err
			}
		}
	}
	return nil
}

// unblock moves the first entity blocked by process's buffer into it.
func (k *kernel) unblock(process int) error {
	b := k.blocked[process].pop()
	k.nBlocked--
	if from := &k.r.p.nodes[b.from]; from.typ == model.NodeProcess {
		if err := k.release(from.requires); err != nil {
			return err
		}
	}
	k.observe(Observation{Kind: ObsUnblocked, Entity: b.entity, Node: b.from, At: b.at})
	return k.enqueue(b.entity, process, b.at)
}

// deadlock is the D-050 B1 backstop, run when no dispatch is due in this
// tick: blocked entities or held candidates, nothing in service or in a
// delay (the events that release units, counted whether or not they fall
// within the horizon), and every held candidate release-bound (some unit
// it needs is in use; nothing but a release frees one, and no release can
// come). The bound is the full capacity C, units down or not: a candidate
// that fits C but not C - D is capacity-bound, a repair lets it in, and it
// is no circular wait (T-048, G-K1). A valid model never reaches it (D-050
// R1).
func (k *kernel) deadlock() error {
	if k.inService > 0 || k.inDelay > 0 || (k.nBlocked == 0 && k.held == 0) {
		return nil
	}
	p := k.r.p
	var first *request
	for i := range k.queues {
		f := k.queues[i].front()
		if f == nil || !f.held {
			continue
		}
		bound := false
		for _, q := range p.nodes[f.process].requires {
			if k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {
				bound = true
			}
		}
		if !bound {
			// It fits the full capacity: units down keep it out, and their
			// repair will let it in (capacity-bound, G-K1). Without
			// breakdowns every held candidate is bound here: each release
			// wakes the held fronts it frees and asks for a dispatch.
			return nil
		}
		if first == nil || f.seq < first.seq {
			first = f
		}
	}
	de := &DeadlockError{Tick: k.r.now}
	if k.nBlocked > 0 {
		var earliest *blockedEntity
		for i := range k.blocked {
			q := &k.blocked[i]
			if q.len() > 0 && (earliest == nil || q.items[q.head].order < earliest.order) {
				earliest = &q.items[q.head]
			}
		}
		de.Entity, de.Node, de.Since = k.r.EntityID(earliest.entity), p.nodes[earliest.from].id, earliest.since
	} else {
		if first == nil {
			return fmt.Errorf("%w: %d held candidates, none at a front", ErrInternal, k.held)
		}
		de.Entity, de.Node, de.Since, de.Queued = k.r.EntityID(first.entity), p.nodes[first.process].id, first.entered, true
	}
	return de
}

// stall is the D-050 B2 backstop, run at the end of a tick with a held
// candidate in a program with breakdowns (D-074 (c): the state of the
// tick's last event, so a repair later in the deadline tick still counts).
// A held candidate none of whose bundle is in use waits only for repairs;
// from the first tick it ends in that state, every unit it needs is
// repaired within its bundle's TTRmax, after which a dispatch grants it
// (no other request can take those units: it is the front of each FIFO).
// One still in the state at Since + TTRmax can never be filled: two clocks
// keep its units down in turn (R3 refuses such a bundle). With no bound on
// the repair time (TTRmax -1) it is not checked.
func (k *kernel) stall() error {
	p := k.r.p
	var due *request
	for i := range k.queues {
		f := k.queues[i].front()
		if f == nil || !f.held {
			continue
		}
		idle := true
		for _, q := range p.nodes[f.process].requires {
			if k.inUse[q.resource] > 0 {
				idle = false
				break
			}
		}
		if !idle {
			f.stalled = -1
			continue
		}
		if f.stalled < 0 {
			f.stalled = k.r.now
		}
		if m := p.nodes[f.process].ttrMax; m >= 0 && k.r.now >= f.stalled+m && (due == nil || f.seq < due.seq) {
			due = f
		}
	}
	if due == nil {
		return nil
	}
	return &StalledError{Tick: k.r.now, Entity: k.r.EntityID(due.entity), Node: p.nodes[due.process].id,
		Since: due.stalled, Deadline: due.stalled + p.nodes[due.process].ttrMax}
}
