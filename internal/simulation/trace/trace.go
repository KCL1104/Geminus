// Package trace records the sampled playback trace of a run
// (docs/SIMULATION_SPEC.md §9, T-022, D-039 PROPOSED): the events of the
// entities whose ID hashes below a fixed rate, in engine event order, for
// one replication, up to max_trace_events. Selection depends only on the
// entity ID, never on an RNG stream, so tracing cannot change a metric; a
// full trace stops recording, never the simulation.
package trace

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// Method and Rate describe the selection (TraceArtifact.sampling).
const (
	Method = "entity_id_sha256"
	Rate   = 0.125
	// threshold is 2^64 * Rate.
	threshold = uint64(1) << 61
	prefix    = "dt-trace-v1\x00"
)

// Selected reports whether the entity with this ID is traced: the first 8
// bytes, little-endian, of SHA-256("dt-trace-v1\x00" || entity ID) are below
// 2^64 * Rate. scripts/engine_reference.py implements the same rule.
func Selected(entityID string) bool {
	h := sha256.Sum256([]byte(prefix + entityID))
	return binary.LittleEndian.Uint64(h[:8]) < threshold
}

// Recorder is the engine.Observer that builds one replication's trace. It is
// not safe for concurrent use.
type Recorder struct {
	p         *engine.Program
	index     uint32
	max       int
	active    map[uint64]string // sampled entities still in the system: entity ID
	events    []simulation.TraceEvent
	truncated bool
}

// NewRecorder traces replication index of p, keeping at most max events.
func NewRecorder(p *engine.Program, index uint32, max int) *Recorder {
	return &Recorder{p: p, index: index, max: max, active: map[uint64]string{}}
}

var kinds = map[engine.ObservationKind]simulation.TraceEventType{
	engine.ObsArrival: simulation.TraceArrival, engine.ObsQueueEnter: simulation.TraceQueueEnter,
	engine.ObsServiceStart: simulation.TraceServiceStart, engine.ObsServiceEnd: simulation.TraceServiceEnd,
	engine.ObsDelayStart: simulation.TraceDelayStart, engine.ObsDelayEnd: simulation.TraceDelayEnd,
	engine.ObsFailure: simulation.TraceFailure, engine.ObsRework: simulation.TraceRework,
	// D-049 (T-047): at the node the entity is blocked at (Observation.Node).
	engine.ObsBlocked: simulation.TraceBlocked, engine.ObsUnblocked: simulation.TraceUnblocked,
}

// Observe implements engine.Observer.
func (r *Recorder) Observe(o engine.Observation) {
	switch o.Kind {
	case engine.ObsResource, engine.ObsResourceDown, engine.ObsResourceUp, engine.ObsBreakdownPending, engine.ObsCapacity:
		return // not an entity event (breakdowns: D-051, T-048; shifts: D-052, T-049)
	}
	if o.Kind == engine.ObsArrival {
		id := engine.FormatEntityID(r.index, r.p.NodeID(o.Node), uint64(o.Value))
		if !Selected(id) {
			return
		}
		r.active[o.Entity] = id
	}
	id, ok := r.active[o.Entity]
	if !ok {
		return
	}
	typ, node := kinds[o.Kind], r.p.NodeID(o.Node)
	switch o.Kind {
	case engine.ObsQueueEnter:
		// The node actually entered: the explicit queue, or the process for
		// an entity routed straight into its buffer (T-022 review), so a
		// playback never shows an entity at a node it did not visit.
		node = r.p.NodeID(o.At)
	case engine.ObsComplete:
		typ = simulation.TraceComplete
		if o.Value != 1 {
			typ = simulation.TraceAbandon
		}
		delete(r.active, o.Entity)
	}
	if r.truncated {
		return
	}
	if len(r.events) >= r.max {
		r.truncated = true
		return
	}
	r.events = append(r.events, simulation.TraceEvent{TimeTick: o.Tick, Sequence: o.Seq, Type: typ, EntityID: id, NodeID: node})
}

// Artifact returns the recorded trace.
func (r *Recorder) Artifact(engineVersion string) *simulation.TraceArtifact {
	events := r.events
	if events == nil {
		events = []simulation.TraceEvent{}
	}
	return &simulation.TraceArtifact{
		Format: simulation.TraceFormat, EngineVersion: engineVersion, ReplicationIndex: int(r.index),
		Sampling:  simulation.TraceSampling{Method: Method, Rate: Rate, MaxEvents: r.max},
		Truncated: r.truncated, Events: events,
	}
}
