package engine

import "container/heap"

// Event priorities within one tick (D-005, D-032). Lower runs first.
const (
	// PriorityServiceComplete: release every unit of the bundle, then route
	// the entity onward in the same event.
	PriorityServiceComplete uint8 = 0
	// PriorityImmediate: arrivals, delay completions and other immediate
	// state transitions, each routing its entity in the same event.
	PriorityImmediate uint8 = 10
	// PriorityDispatch: the resource dispatcher, the last step of a tick.
	PriorityDispatch uint8 = 20
)

// EventKind says what an event does; the kernel dispatches on it.
type EventKind uint8

const (
	EventArrival EventKind = iota + 1
	EventServiceComplete
	EventDelayComplete
	EventDispatch
)

// event is one scheduled occurrence. Seq is unique and strictly increasing
// within a replication, so (Tick, Priority, Seq) is a total order and the
// processing order never depends on heap internals, map order, wall clock or
// goroutine scheduling.
type event struct {
	Tick     int64
	Priority uint8
	Seq      uint64
	Kind     EventKind
	Node     int    // compiled node index
	Entity   uint64 // index into the replication's entity table; 0 when none
}

func (a event) before(b event) bool {
	if a.Tick != b.Tick {
		return a.Tick < b.Tick
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	return a.Seq < b.Seq
}

// eventQueue is a binary min-heap by (Tick, Priority, Seq).
type eventQueue struct {
	items []event
	seq   uint64
}

func (q *eventQueue) Len() int           { return len(q.items) }
func (q *eventQueue) Less(i, j int) bool { return q.items[i].before(q.items[j]) }
func (q *eventQueue) Swap(i, j int)      { q.items[i], q.items[j] = q.items[j], q.items[i] }
func (q *eventQueue) Push(x any)         { q.items = append(q.items, x.(event)) }
func (q *eventQueue) Pop() any {
	last := q.items[len(q.items)-1]
	q.items = q.items[:len(q.items)-1]
	return last
}

// schedule adds e with the next sequence number and returns it.
func (q *eventQueue) schedule(e event) event {
	q.seq++
	e.Seq = q.seq
	heap.Push(q, e)
	return e
}

// next removes and returns the earliest event.
func (q *eventQueue) next() (event, bool) {
	if len(q.items) == 0 {
		return event{}, false
	}
	return heap.Pop(q).(event), true
}

// peek returns the earliest event without removing it.
func (q *eventQueue) peek() (event, bool) {
	if len(q.items) == 0 {
		return event{}, false
	}
	return q.items[0], true
}
