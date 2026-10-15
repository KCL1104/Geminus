// Package model holds the Generic Twin IR v0.1 types (schemas/model-ir.schema.json)
// and the JSON Patch profile (schemas/json-patch.schema.json).
//
// The schemas are the source of truth (AGENTS.md §1). These types mirror their
// structure only: contract tests in this package fail when a property is added,
// removed or renamed on either side, and when a fixture does not round-trip
// losslessly. Range, pattern and semantic rules are enforced by the schema and
// semantic validators (T-004), not by decoding into these types.
//
// This package must stay a pure leaf: standard library only, no persistence,
// HTTP, provider SDK or UI types. Services live in subpackages (internal/model/service).
package model

import "encoding/json/jsontext"

// SchemaVersion is the only IR schema_version this build accepts.
const SchemaVersion = "0.1"

// ModelIR is the canonical model interchange shared by UI, AI, import and engine.
type ModelIR struct {
	SchemaVersion string        `json:"schema_version"`
	ModelID       string        `json:"model_id"` // D-001 "mdl_" + ULID, assigned by the server
	Domain        string        `json:"domain"`
	Name          string        `json:"name"`
	TimeUnit      ModelTimeUnit `json:"time_unit"`
	EntityTypes   []EntityType  `json:"entity_types"`
	Resources     []Resource    `json:"resources"`
	Nodes         []Node        `json:"nodes"`
	Flows         []Flow        `json:"flows"`
	Constraints   []Constraint  `json:"constraints"`
	Metrics       []Metric      `json:"metrics"`
	Views         Views         `json:"views"`
	// Metadata is an untrusted data container, never code or expressions.
	Metadata jsontext.Value `json:"metadata,omitzero"`
}

// ModelTimeUnit is the time unit of every duration parameter in a model.
type ModelTimeUnit string

const (
	ModelSecond ModelTimeUnit = "second"
	ModelMinute ModelTimeUnit = "minute"
	ModelHour   ModelTimeUnit = "hour"
	ModelDay    ModelTimeUnit = "day"
)

// TimeUnit is the unit of a run duration or dataset sample (common.schema.json#/$defs/TimeUnit).
type TimeUnit string

const (
	Millisecond TimeUnit = "millisecond"
	Second      TimeUnit = "second"
	Minute      TimeUnit = "minute"
	Hour        TimeUnit = "hour"
	Day         TimeUnit = "day"
)

// EntityType names the kind of work item flowing through the model.
type EntityType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Resource is a capacity-limited pool that processes acquire.
type Resource struct {
	ID              string          `json:"id"`
	Type            ResourceType    `json:"type"`
	Role            string          `json:"role,omitzero"`
	Name            string          `json:"name"`
	Capacity        int             `json:"capacity"`
	QueueDiscipline QueueDiscipline `json:"queue_discipline"`
}

type ResourceType string

const (
	ResourceHuman   ResourceType = "human"
	ResourceMachine ResourceType = "machine"
	ResourceGeneric ResourceType = "generic"
)

type QueueDiscipline string

const FIFO QueueDiscipline = "fifo"

// Node is a discriminated union on Type; only the fields of that node type may
// be set. The schema's oneOf decides which combinations are valid.
type Node struct {
	ID   string   `json:"id"`
	Type NodeType `json:"type"`
	Name string   `json:"name,omitzero"`
	// DomainAttributes is an untrusted DomainPack data container.
	DomainAttributes jsontext.Value `json:"domain_attributes,omitzero"`

	EntityType  string                `json:"entity_type,omitzero"`  // source
	Arrival     *Arrival              `json:"arrival,omitzero"`      // source
	ServiceTime *TimedDistribution    `json:"service_time,omitzero"` // process
	Requires    []ResourceRequirement `json:"requires,omitzero"`     // process; may be empty
	DelayTime   *TimedDistribution    `json:"delay_time,omitzero"`   // delay
	Outcome     SinkOutcome           `json:"outcome,omitzero"`      // sink
	Reason      string                `json:"reason,omitzero"`       // failure
}

type NodeType string

const (
	NodeSource   NodeType = "source"
	NodeQueue    NodeType = "queue"
	NodeProcess  NodeType = "process"
	NodeDecision NodeType = "decision"
	NodeDelay    NodeType = "delay"
	NodeSink     NodeType = "sink"
	NodeFailure  NodeType = "failure"
	NodeRework   NodeType = "rework"
)

// NodeTypes lists every node type, in schema order.
var NodeTypes = []NodeType{NodeSource, NodeQueue, NodeProcess, NodeDecision, NodeDelay, NodeSink, NodeFailure, NodeRework}

type SinkOutcome string

const (
	SinkCompleted SinkOutcome = "completed"
	SinkAbandoned SinkOutcome = "abandoned"
)

// Arrival configures a source's interarrival distribution.
type Arrival struct {
	Distribution Distribution `json:"distribution"`
	MaxEntities  int          `json:"max_entities,omitzero"`
}

// TimedDistribution wraps the service or delay time distribution of a node.
type TimedDistribution struct {
	Distribution Distribution `json:"distribution"`
}

// NodeDistribution is one distribution of a node and the member holding it.
type NodeDistribution struct {
	Member       string // "arrival", "service_time" or "delay_time"
	Distribution *Distribution
}

// Distributions returns n's arrival, service and delay distributions that are
// set, in that order. The pointers alias n.
func (n *Node) Distributions() []NodeDistribution {
	var out []NodeDistribution
	if n.Arrival != nil {
		out = append(out, NodeDistribution{"arrival", &n.Arrival.Distribution})
	}
	if n.ServiceTime != nil {
		out = append(out, NodeDistribution{"service_time", &n.ServiceTime.Distribution})
	}
	if n.DelayTime != nil {
		out = append(out, NodeDistribution{"delay_time", &n.DelayTime.Distribution})
	}
	return out
}

// ResourceRequirement is one entry of a process's atomic resource bundle.
type ResourceRequirement struct {
	ResourceID string `json:"resource_id"`
	Units      int    `json:"units"`
}

// Distribution is a discriminated union on Type. Parameters are pointers
// because zero is a meaningful value (e.g. a constant of 0 or a min of 0).
type Distribution struct {
	Type       DistributionType `json:"type"`
	Value      *float64         `json:"value,omitzero"`       // constant
	Mean       *float64         `json:"mean,omitzero"`        // exponential
	Min        *float64         `json:"min,omitzero"`         // triangular
	Mode       *float64         `json:"mode,omitzero"`        // triangular
	Max        *float64         `json:"max,omitzero"`         // triangular
	SamplesRef string           `json:"samples_ref,omitzero"` // empirical, "dataset:<logical ref>"
}

type DistributionType string

const (
	DistConstant    DistributionType = "constant"
	DistExponential DistributionType = "exponential"
	DistTriangular  DistributionType = "triangular"
	DistEmpirical   DistributionType = "empirical"
)

// DistributionTypes lists every P0 distribution, in schema order.
var DistributionTypes = []DistributionType{DistConstant, DistExponential, DistTriangular, DistEmpirical}

// Flow is a directed edge between nodes with its routing rule.
type Flow struct {
	ID      string  `json:"id"`
	From    string  `json:"from"`
	To      string  `json:"to"`
	Routing Routing `json:"routing"`
}

type Routing struct {
	Type        RoutingType `json:"type"`
	Probability float64     `json:"probability"`
}

type RoutingType string

const RoutingProbability RoutingType = "probability"

// Constraint is a declarative model constraint; P0 has outgoing_probability_sum only.
type Constraint struct {
	Type   ConstraintType `json:"type"`
	NodeID string         `json:"node_id"`
	Equals float64        `json:"equals"`
}

type ConstraintType string

const ConstraintOutgoingProbabilitySum ConstraintType = "outgoing_probability_sum"

// Metric declares a metric of interest; a discriminated union on Type.
type Metric struct {
	ID         string     `json:"id"`
	Type       MetricType `json:"type"`
	SinkID     string     `json:"sink_id,omitzero"`     // sink_throughput (optional)
	ResourceID string     `json:"resource_id,omitzero"` // resource_utilization
	NodeID     string     `json:"node_id,omitzero"`     // queue_wait
}

type MetricType string

const (
	MetricEntityCycleTime     MetricType = "entity_cycle_time"
	MetricSinkThroughput      MetricType = "sink_throughput"
	MetricResourceUtilization MetricType = "resource_utilization"
	MetricQueueWait           MetricType = "queue_wait"
	MetricFailureCount        MetricType = "failure_count"
	MetricReworkCount         MetricType = "rework_count"
)

// MetricTypes lists every metric type, in schema order.
var MetricTypes = []MetricType{MetricEntityCycleTime, MetricSinkThroughput, MetricResourceUtilization,
	MetricQueueWait, MetricFailureCount, MetricReworkCount}

// Views holds renderer data. Positions are presentation, not domain semantics,
// and views never carry a second topology (2D authors, 3D only projects).
type Views struct {
	TwoD   View2D `json:"2d"`
	ThreeD View3D `json:"3d"`
}

type View2D struct {
	Positions map[string]Position `json:"positions,omitzero"` // keyed by node ID
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type View3D struct {
	LayoutMode string `json:"layout_mode,omitzero"` // only "derived_from_2d"
}

// Patch is an RFC 6902 document restricted to the P0 profile (D-014):
// add, remove, replace and test.
type Patch []PatchOp

// PatchOp is a discriminated union on Op. Value is raw JSON so that null,
// false and 0 survive; it is absent only for remove.
type PatchOp struct {
	Op    PatchOpKind    `json:"op"`
	Path  string         `json:"path"`
	Value jsontext.Value `json:"value,omitzero"`
}

type PatchOpKind string

const (
	PatchAdd     PatchOpKind = "add"
	PatchReplace PatchOpKind = "replace"
	PatchTest    PatchOpKind = "test"
	PatchRemove  PatchOpKind = "remove"
)

// PatchOpKinds lists every allowed op, in schema order.
var PatchOpKinds = []PatchOpKind{PatchAdd, PatchReplace, PatchTest, PatchRemove}

// DatasetBinding pins one empirical logical_ref to an immutable dataset revision
// (common.schema.json#/$defs/DatasetBinding).
type DatasetBinding struct {
	LogicalRef       string   `json:"logical_ref"`
	DatasetVersionID string   `json:"dataset_version_id"` // D-001 "dsv_" + ULID
	ContentHash      string   `json:"content_hash"`       // "sha256:<hex>" of the stored object bytes (D-019)
	Unit             TimeUnit `json:"unit"`
	SampleCount      int      `json:"sample_count"`
}

// ValidationIssue is one deterministic validator finding
// (common.schema.json#/$defs/ValidationIssue).
type ValidationIssue struct {
	Code     string   `json:"code"`
	Path     string   `json:"path"` // JSON Pointer into the validated document
	Message  string   `json:"message"`
	Severity Severity `json:"severity"`
}

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)
