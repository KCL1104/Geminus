package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
)

// Typed run failures (docs/SIMULATION_SPEC.md §8). A run that hits one is
// failed, never completed with partial results. Cancellation and deadlines
// are reported as the context's own errors.
var (
	// ErrEventBudget: the run processed max_events events, counting every
	// replication and the warmup.
	ErrEventBudget = errors.New("engine: event budget exhausted")
	// ErrInternal: an engine invariant broke (a bug, not bad input).
	ErrInternal = errors.New("engine: internal invariant violated")
)

// Budget counts processed events across all replications of one run. It is
// safe for replications running in parallel; which one hits the cap first
// may vary, but hitting it always fails the whole run, so no result depends
// on that order.
type Budget struct {
	max  int64
	used atomic.Int64
}

// NewBudget allows max events.
func NewBudget(max int64) *Budget { return &Budget{max: max} }

// Used reports how many events were processed.
func (b *Budget) Used() int64 { return min(b.used.Load(), b.max) }

func (b *Budget) spend() error {
	if b.used.Add(1) > b.max {
		return ErrEventBudget
	}
	return nil
}

// entity is one work item. Entity indices start at 1, so 0 means "none".
type entity struct {
	source  int    // node index of the source that created it
	n       uint64 // 1-based arrival number within that source
	created int64  // tick
}

type streamKey struct {
	node    int
	purpose string
}

// replication is the state of one replication: a single-threaded clock over
// one event queue (docs/SIMULATION_SPEC.md §1–§2). The kernel (T-021) adds
// queues, resources and routing through the two hooks.
type replication struct {
	p        *Program
	index    uint32
	seed     uint64
	now      int64
	q        eventQueue
	streams  map[streamKey]*SplitMix64 // looked up, never iterated
	arrived  []uint64                  // per node: entities its source created
	entities []entity

	// onArrival receives each new entity after its source has scheduled the
	// next arrival.
	onArrival func(r *replication, entity uint64) error
	// onEvent handles every event that is not an arrival.
	onEvent func(r *replication, e event) error
}

func newReplication(p *Program, index uint32) *replication {
	return &replication{
		p:        p,
		index:    index,
		seed:     ReplicationSeed(p.seed, index),
		streams:  map[streamKey]*SplitMix64{},
		arrived:  make([]uint64, len(p.nodes)),
		entities: []entity{{}},
	}
}

// stream returns the (node, purpose) stream of this replication, created on
// first use from its own seed; streams never share state.
func (r *replication) stream(node int, purpose string) *SplitMix64 {
	k := streamKey{node, purpose}
	s, ok := r.streams[k]
	if !ok {
		s = Stream(r.seed, r.p.nodes[node].id, purpose)
		r.streams[k] = s
	}
	return s
}

// EntityID is an entity's stable, human-readable ID:
// r<replication index>/<source node ID>/<n>. Node IDs cannot contain "/".
func (r *replication) EntityID(e uint64) string {
	en := r.entities[e]
	return "r" + strconv.FormatUint(uint64(r.index), 10) + "/" + r.p.nodes[en.source].id + "/" + strconv.FormatUint(en.n, 10)
}

// schedule adds an event at now+delay. Delays are at most beyondCap, so the
// tick cannot overflow; an event after the horizon is never processed and is
// not scheduled at all (the draw that produced it has been consumed).
func (r *replication) schedule(delay int64, priority uint8, kind EventKind, node int, ent uint64) error {
	if delay < 0 || delay > beyondCap {
		return fmt.Errorf("%w: delay %d ticks", ErrInternal, delay)
	}
	at := r.now + delay
	if at > r.p.horizon {
		return nil
	}
	r.q.schedule(event{Tick: at, Priority: priority, Kind: kind, Node: node, Entity: ent})
	return nil
}

// scheduleArrival draws the next interarrival of source src unless the
// source has created max_entities already.
func (r *replication) scheduleArrival(src int) error {
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
func (r *replication) run(ctx context.Context, b *Budget) error {
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
		r.now = e.Tick
		if err := b.spend(); err != nil {
			return err
		}
		if err := r.handle(e); err != nil {
			return err
		}
	}
}

func (r *replication) handle(e event) error {
	if e.Kind != EventArrival {
		if r.onEvent == nil {
			return fmt.Errorf("%w: no handler for event kind %d", ErrInternal, e.Kind)
		}
		return r.onEvent(r, e)
	}
	src := e.Node
	r.arrived[src]++
	r.entities = append(r.entities, entity{source: src, n: r.arrived[src], created: r.now})
	id := uint64(len(r.entities) - 1)
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
