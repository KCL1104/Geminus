// Package validate is the deterministic IR validator (docs/TWIN_IR_SPEC.md §4).
//
// Stages run in the order JSON parse → JSON Schema → primitive support →
// limits → IDs → references → graph → distributions → bundle feasibility
// (D-050 R3 since T-048; R2 and R4 with calendars) → constraints. Primitive
// support (D-063 PROPOSED) refuses each line-flow member (ADR-026, D-058)
// whose rules this build does not implement yet, so the later stages only
// ever see models they fully check. A stage reports every issue it finds;
// when any of them is an error the later stages do not run (D-023), because
// their findings would only be consequences of the earlier ones. Issues are
// sorted by JSON Pointer (array indices compared numerically) and then code.
//
// The dataset binding stage is Bindings, which internal/storage/datasets runs
// once snapshots are resolved. Scenario/patch policy (internal/model/patch) and
// risk classification (T-025) are later stages owned elsewhere.
package validate

import "github.com/kcl1104/geminus/internal/model"

// Issue codes. Families come from TWIN_IR_SPEC §4; D-023 added the rest.
const (
	JSONInvalid                = "JSON_INVALID"      // malformed JSON, duplicate member, invalid UTF-8, trailing data
	IRSchemaInvalid            = "IR_SCHEMA_INVALID" // JSON Schema: shape, type, enum, unknown member, version
	LimitExceeded              = "LIMIT_EXCEEDED"    // D-029 size, depth and duration caps
	DuplicateID                = "DUPLICATE_ID"      // repeated ID in a catalog, or a node and resource sharing one
	UnknownReference           = "UNKNOWN_REFERENCE" // reference to an ID that does not exist
	ViewReferenceInvalid       = "VIEW_REFERENCE_INVALID"
	RoutingInvalid             = "ROUTING_INVALID"
	NoReachableSink            = "NO_REACHABLE_SINK"
	ZeroTimeCycle              = "ZERO_TIME_CYCLE"
	UnreachableNode            = "UNREACHABLE_NODE" // warning only: dormant node no source reaches
	ResourceRequirementInvalid = "RESOURCE_REQUIREMENT_INVALID"
	DistributionInvalid        = "DISTRIBUTION_INVALID"
	ConstraintFailed           = "CONSTRAINT_FAILED"
	DatasetBindingInvalid      = "DATASET_BINDING_INVALID" // binding stage: missing, unauthorized or inconsistent snapshot

	// Line-flow rules (ADR-026; their stages are TWIN_IR_SPEC §4). T-047–T-050
	// implement them; until then the primitive support stage refuses the
	// members they check, so no model reaches them.
	BlockingCycle          = "BLOCKING_CYCLE"           // D-050 R1, graph stage
	CalendarInvalid        = "CALENDAR_INVALID"         // D-052 calendar shape; D-050 R2, feasibility stage
	ReleaseScheduleInvalid = "RELEASE_SCHEDULE_INVALID" // D-053
	BatchInvalid           = "BATCH_INVALID"            // D-054
	SetupInvalid           = "SETUP_INVALID"            // D-055
	StallRisk              = "STALL_RISK"               // D-050 R4, an error (D-050 A), feasibility stage
	// PrimitiveNotSupported: the model uses a line-flow primitive this
	// build does not implement yet (D-063 PROPOSED).
	PrimitiveNotSupported = "PRIMITIVE_NOT_SUPPORTED"
)

// Limits approved in D-029 (candidates subject to the D-007 benchmark).
const (
	MaxIRBytes         = 2 << 20 // 2 MiB
	MaxContainerDepth  = 8       // metadata / domain_attributes nesting
	MaxContainerBytes  = 16 << 10
	MaxDurationTicks   = model.MaxDurationTicks // 365 days of 1 ms ticks (D-005)
	ProbabilityEpsilon = 1e-9                   // TWIN_IR_SPEC §4 routing tolerance
)
