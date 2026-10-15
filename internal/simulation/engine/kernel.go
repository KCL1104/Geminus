package engine

import (
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

// request is a waiting entity in a process's buffer. All buffers form one
// list in (enter tick, sequence) order: requests are appended at the current
// tick with an increasing sequence, so appending keeps it sorted.
type request struct {
	entity  uint64
	process int
	enter   int64
	seq     uint64
}

// kernel adds queues, resources, service and routing to a replication
// (docs/SIMULATION_SPEC.md §3, D-005, D-032).
type kernel struct {
	r               *replication
	obs             Observer
	inUse           []int
	blocked         []bool // dispatch scratch: resources an earlier waiting request holds back
	waiting         []request
	reqSeq          uint64
	active          int64
	peak            int64
	exited          int64
	events          int64
	dispatchPending bool
}

// RunReplication executes replication index of p up to and including the
// horizon, reporting every observation to obs (nil for none). It spends the
// run's shared budget and checks ctx before every event.
func RunReplication(ctx context.Context, p *Program, index uint32, b *Budget, obs Observer) (ReplicationOutcome, error) {
	r := newReplication(p, index)
	k := &kernel{r: r, obs: obs, inUse: make([]int, len(p.resources)), blocked: make([]bool, len(p.resources))}
	r.onArrival = k.arrived
	r.onEvent = k.handle
	r.onProcessed = func() { k.events++ }
	err := r.run(ctx, b)
	if err == nil && int64(len(r.entities)) != k.active {
		err = fmt.Errorf("%w: %d entity records for %d active entities", ErrInternal, len(r.entities), k.active)
	}
	out := ReplicationOutcome{Index: index, Created: int64(r.created), Exited: k.exited, Censored: k.active, PeakActive: k.peak, Events: k.events}
	return out, err
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
		return ErrActiveEntities
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
		en.visits++
		if lim := k.r.p.limits.MaxEntityVisits; en.visits > lim {
			return ErrEntityVisits
		}
		node := &k.r.p.nodes[n]
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
	k.waiting = append(k.waiting, request{entity: e, process: process, enter: k.r.now, seq: k.reqSeq})
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

// dispatch scans the waiting requests in (enter tick, sequence) order and
// grants a request only when every unit of its bundle is free and no earlier
// ungranted request needs any of the same resources: resource-wise FIFO,
// all-or-nothing, never a partial acquire (SIMULATION_SPEC §3). A request
// that shares no resource with a blocked one can still proceed.
func (k *kernel) dispatch() error {
	k.dispatchPending = false
	clear(k.blocked)
	p := k.r.p
	kept := k.waiting[:0]
	var granted []request
	for _, req := range k.waiting {
		reqs := p.nodes[req.process].requires
		free := true
		for _, q := range reqs {
			if k.blocked[q.resource] || k.inUse[q.resource]+q.units > p.resources[q.resource].capacity {
				free = false
				break
			}
		}
		if !free {
			for _, q := range reqs {
				k.blocked[q.resource] = true
			}
			kept = append(kept, req)
			continue
		}
		for _, q := range reqs {
			k.inUse[q.resource] += q.units
		}
		granted = append(granted, req)
	}
	k.waiting = kept
	// Start the granted services in request order; each samples its own
	// process's service stream.
	for _, req := range granted {
		for _, q := range p.nodes[req.process].requires {
			k.observe(Observation{Kind: ObsResource, Resource: q.resource, Value: int64(k.inUse[q.resource])})
		}
		k.observe(Observation{Kind: ObsServiceStart, Entity: req.entity, Node: req.process})
		d := p.nodes[req.process].service.draw(k.r.stream(req.process, PurposeService))
		if err := k.r.schedule(d, PriorityServiceComplete, EventServiceComplete, req.process, req.entity); err != nil {
			return err
		}
	}
	return nil
}
