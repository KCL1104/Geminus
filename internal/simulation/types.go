// Package simulation defines the SimulationEngine boundary and its run/result
// contract (schemas/simulation.schema.json). Engines live in subpackages
// (internal/simulation/engine); this package holds only types.
//
// The engine is pure: no DB, network, object storage, LLM or HTTP. Everything
// a run may read is frozen into RunInput by the worker before Run is called
// (ADR-012), so retries and replays see the same input.
package simulation

import (
	"context"
	"encoding/json/jsontext"

	"github.com/kcl1104/geminus/internal/model"
)

// Engine is the report's SimulationEngine (docs/ARCHITECTURE.md §3), with the
// dataset injection fixed as an explicit RunInput (ADR-012).
type Engine interface {
	// Validate reports engine-level issues for an IR; it never mutates it.
	Validate(ctx context.Context, ir model.ModelIR) ([]model.ValidationIssue, error)
	// Run executes all replications of a frozen input. It must honour ctx
	// cancellation at checkpoints and never return partial results as completed.
	Run(ctx context.Context, in RunInput, progress ProgressSink) (*Result, error)
}

// RunInput is the complete, immutable input of one logical run.
type RunInput struct {
	// Model is the effective IR: the pinned version with the scenario patch applied.
	Model model.ModelIR
	// ModelHash is the pinned ModelVersion's model_hash (before the patch). It
	// cannot be recomputed from Model, so it is recorded as given.
	ModelHash string
	// EffectiveModelHash is hashing.Model over model.Encode(Model); the engine
	// recomputes it and refuses a run whose value differs.
	EffectiveModelHash string
	Config             RunConfig
	Limits             ExecutionLimits
	// Datasets holds every empirical distribution the model references, keyed
	// by logical_ref ("dataset:..."), already fetched and hash-verified.
	Datasets map[string]ResolvedDataset
	// Manifest is the frozen binding list, ordered by logical_ref.
	Manifest []model.DatasetBinding
}

// ResolvedDataset is one bound empirical dataset with its samples in Binding.Unit.
type ResolvedDataset struct {
	Binding model.DatasetBinding
	Samples []float64
}

// ProgressSink receives best-effort progress; it is never a correctness input
// (docs/SIMULATION_SPEC.md §8). The worker adds run identity, status and revision.
// Engines call Report from one goroutine, never concurrently, and never wait
// for it: a slow sink sees only the latest snapshot (counts never go back),
// and a call may still be running just after Run returns, so a worker fences
// its progress writes by run status. A sink that panics receives no further
// reports; the run is unaffected.
type ProgressSink interface {
	Report(Progress)
}

// Progress is the engine-owned part of an http ProgressSnapshot.
type Progress struct {
	CompletedReplications int
	TotalReplications     int
	EventsProcessed       int64
}

// Duration is a non-negative amount of time (common.schema.json#/$defs/Duration).
type Duration struct {
	Value float64        `json:"value"`
	Unit  model.TimeUnit `json:"unit"`
}

// RunConfig is the frozen run configuration (#/$defs/RunConfig).
type RunConfig struct {
	Horizon      Duration   `json:"horizon"`
	Warmup       Duration   `json:"warmup"`
	Replications int        `json:"replications"`
	Seed         int64      `json:"seed"` // 0..2^53-1
	TraceLevel   TraceLevel `json:"trace_level"`
}

type TraceLevel string

const (
	TraceNone    TraceLevel = "none"
	TraceSampled TraceLevel = "sampled"
)

// ExecutionLimits are the budgets frozen at enqueue (#/$defs/ExecutionLimits, D-007).
type ExecutionLimits struct {
	WallTimeoutSeconds int   `json:"wall_timeout_seconds"`
	MaxEvents          int64 `json:"max_events"`
	MaxActiveEntities  int   `json:"max_active_entities"`
	MaxEntityVisits    int   `json:"max_entity_visits"`
	MaxTraceEvents     int   `json:"max_trace_events"`
}

// Result is a completed run's aggregate result (#/$defs/Result). Nullable
// members are pointers without omitzero: null means "unavailable", never 0.
type Result struct {
	RunID                string               `json:"run_id"`
	Status               string               `json:"status"` // always "completed"
	EngineVersion        string               `json:"engine_version"`
	Metrics              Metrics              `json:"metrics"`
	ReplicationSummaries []ReplicationSummary `json:"replication_summaries"`
	MeasurementWindow    MeasurementWindow    `json:"measurement_window"`
	Reproducibility      Reproducibility      `json:"reproducibility"`
	Trace                Trace                `json:"trace"`
}

type Metrics struct {
	Throughput        MetricSummary            `json:"throughput"`
	LeadTimeMinutes   MetricSummary            `json:"lead_time_minutes"`
	Resources         map[string]ResourceStats `json:"resources"`
	Queues            map[string]QueueStats    `json:"queues"`
	FailureCount      MetricSummary            `json:"failure_count"`
	ReworkCount       MetricSummary            `json:"rework_count"`
	DORA              map[string]MetricSummary `json:"dora"`
	AdditionalMetrics map[string]MetricSummary `json:"additional_metrics"`
	// Domain holds a DomainPack's model metrics (D-058); absent when none,
	// so a result without them keeps its digest (D-047).
	Domain map[string]MetricSummary `json:"domain,omitzero"`
}

// MetricSummary carries its own evidence: status, population and sample count (D-010).
type MetricSummary struct {
	Status                 MetricStatus        `json:"status"`
	Mean                   *float64            `json:"mean"`
	P50                    *float64            `json:"p50"`
	P95                    *float64            `json:"p95"`
	SampleCount            int64               `json:"sample_count"`
	Population             Population          `json:"population"`
	Unit                   string              `json:"unit"`
	ConfidenceIntervalMean *ConfidenceInterval `json:"confidence_interval_mean"`
	Reason                 *string             `json:"reason"`
}

type MetricStatus string

const (
	Available   MetricStatus = "available"
	Unavailable MetricStatus = "unavailable"
)

type Population string

const (
	PopulationReplication  Population = "replication"
	PopulationEntity       Population = "entity"
	PopulationStageVisit   Population = "stage_visit"
	PopulationTimeWeighted Population = "time_weighted"
)

type ConfidenceInterval struct {
	Level  float64 `json:"level"`
	Lower  float64 `json:"lower"`
	Upper  float64 `json:"upper"`
	Method string  `json:"method"`
}

type ResourceStats struct {
	UtilizationMean  *float64 `json:"utilization_mean"`
	ObservationTicks int64    `json:"observation_ticks"`
	Capacity         int      `json:"capacity"`
	Reason           *string  `json:"reason"`
}

type QueueStats struct {
	Status          MetricStatus `json:"status"`
	WaitMeanMinutes *float64     `json:"wait_mean_minutes"`
	WaitP50Minutes  *float64     `json:"wait_p50_minutes"`
	WaitP95Minutes  *float64     `json:"wait_p95_minutes"`
	QueueLengthMean float64      `json:"queue_length_mean"`
	SampleCount     int64        `json:"sample_count"`
	Reason          *string      `json:"reason"`
}

type ReplicationSummary struct {
	ReplicationIndex    int                `json:"replication_index"`
	CompletedCount      int64              `json:"completed_count"`
	MeanLeadTimeMinutes *float64           `json:"mean_lead_time_minutes"`
	ResourceUtilization map[string]float64 `json:"resource_utilization"`
	Failures            int64              `json:"failures"`
	Reworks             int64              `json:"reworks"`
	CensoredEntities    int64              `json:"censored_entities"`

	// Line-flow members (ADR-026, D-058): each appears only when the model
	// uses the feature it measures, so older results keep their bytes and
	// digest (D-047). Keys are resource, node, queue, process and metric IDs.
	ResourceStates map[string]ResourceState `json:"resource_states,omitzero"`
	Blocking       map[string]NodeBlocking  `json:"blocking,omitzero"`
	Buffers        map[string]BufferState   `json:"buffers,omitzero"`
	Batches        map[string]BatchCounts   `json:"batches,omitzero"`
	// Declared holds each declared metric's value in this replication: a
	// number (system_wip), a count (flow_count, sink_count), counts per
	// period (period_throughput) or null when unavailable.
	Declared map[string]jsontext.Value `json:"declared,omitzero"`
}

// ResourceState is one resource's integer-tick integrals and counts over
// [W, H] in one replication (D-058, ELECTRONICS_LINE §5). A group of
// members is present when its feature applies to the resource.
type ResourceState struct {
	InUseTicks               int64  `json:"in_use_ticks"`
	BlockedTicks             *int64 `json:"blocked_ticks,omitzero"` // the model has a finite buffer
	SetupTicks               *int64 `json:"setup_ticks,omitzero"`   // setups
	SetupsStarted            *int64 `json:"setups_started,omitzero"`
	SetupsCompleted          *int64 `json:"setups_completed,omitzero"`
	CompletedSetupTicks      *int64 `json:"completed_setup_ticks,omitzero"`
	DownTicks                *int64 `json:"down_ticks,omitzero"` // breakdowns
	BreakdownsStarted        *int64 `json:"breakdowns_started,omitzero"`
	RepairsCompleted         *int64 `json:"repairs_completed,omitzero"`
	CompletedRepairTicks     *int64 `json:"completed_repair_ticks,omitzero"`
	ScheduledTicks           *int64 `json:"scheduled_ticks,omitzero"` // a calendar
	ScheduledInUseTicks      *int64 `json:"scheduled_in_use_ticks,omitzero"`
	OvertimeTicks            *int64 `json:"overtime_ticks,omitzero"`
	AvailableTicks           *int64 `json:"available_ticks,omitzero"` // breakdowns or a calendar
	IdleTicks                *int64 `json:"idle_ticks,omitzero"`
	ScheduledProcessingTicks int64  `json:"scheduled_processing_ticks"`
}

// NodeBlocking counts a node's blocked entities over [W, H] (D-049, D-058).
type NodeBlocking struct {
	BlocksStarted       int64 `json:"blocks_started"`
	BlocksCompleted     int64 `json:"blocks_completed"`
	CompletedBlockTicks int64 `json:"completed_block_ticks"`
	BlockedEntityTicks  int64 `json:"blocked_entity_ticks"`
}

// BufferState is a finite buffer's full time over [W, H] (D-049).
type BufferState struct {
	FullTicks int64 `json:"full_ticks"`
}

// BatchCounts are a batch process's batches over [W, H] (D-054, D-058).
type BatchCounts struct {
	BatchesStarted int64 `json:"batches_started"`
	MembersStarted int64 `json:"members_started"`
}

type MeasurementWindow struct {
	TickUnit     string `json:"tick_unit"` // "millisecond" (D-005)
	WarmupTicks  int64  `json:"warmup_ticks"`
	HorizonTicks int64  `json:"horizon_ticks"`
	TimeBoundary string `json:"time_boundary"`
}

type Reproducibility struct {
	Seed                int64  `json:"seed"`
	Replications        int    `json:"replications"`
	ModelHash           string `json:"model_hash"`
	EffectiveModelHash  string `json:"effective_model_hash"`
	ConfigHash          string `json:"config_hash"`
	DatasetManifestHash string `json:"dataset_manifest_hash"`
	ResultDigest        string `json:"result_digest"`
}

type Trace struct {
	Available     bool    `json:"available"`
	SampledEvents int     `json:"sampled_events"`
	ArtifactID    *string `json:"artifact_id"` // D-001 "art_" + ULID, or null
}

// TraceArtifact is the sampled playback trace of a run (#/$defs/TraceArtifact,
// SIMULATION_SPEC §9, D-039 PROPOSED). It is stored as a private object that
// Result.Trace.ArtifactID names; it is not part of result_digest.
type TraceArtifact struct {
	Format           string        `json:"format"` // TraceFormat
	EngineVersion    string        `json:"engine_version"`
	ReplicationIndex int           `json:"replication_index"`
	Sampling         TraceSampling `json:"sampling"`
	// Truncated reports that recording stopped at Sampling.MaxEvents; the
	// simulation itself ran to the horizon.
	Truncated bool         `json:"truncated"`
	Events    []TraceEvent `json:"events"`
}

// TraceFormat is TraceArtifact.Format.
const TraceFormat = "geminus.trace.v1"

// TraceSampling says how entities were chosen: those whose entity-ID hash
// falls below Rate, recording at most MaxEvents events.
type TraceSampling struct {
	Method    string  `json:"method"` // "entity_id_sha256"
	Rate      float64 `json:"rate"`
	MaxEvents int     `json:"max_events"`
}

// TraceEvent is one fact about a sampled entity, in engine event order.
type TraceEvent struct {
	TimeTick int64          `json:"time_tick"`
	Sequence uint64         `json:"sequence"`
	Type     TraceEventType `json:"type"`
	EntityID string         `json:"entity_id"`
	NodeID   string         `json:"node_id"`
	// ResourceID is the resource changing over, only on setup_start (D-055).
	ResourceID string `json:"resource_id,omitzero"`
	// BatchID is r<rep>/<process>/b<n> on a batch member's service_start and
	// service_end (D-054).
	BatchID string `json:"batch_id,omitzero"`
}

type TraceEventType string

const (
	TraceArrival      TraceEventType = "arrival"
	TraceQueueEnter   TraceEventType = "queue_enter"
	TraceServiceStart TraceEventType = "service_start"
	TraceServiceEnd   TraceEventType = "service_end"
	TraceDelayStart   TraceEventType = "delay_start"
	TraceDelayEnd     TraceEventType = "delay_end"
	TraceFailure      TraceEventType = "failure"
	TraceRework       TraceEventType = "rework"
	TraceComplete     TraceEventType = "complete"
	TraceAbandon      TraceEventType = "abandon"
	// ADR-026 (recorded from T-047 and T-050; des-go-0.1.0 writes none).
	TraceBlocked    TraceEventType = "blocked"     // D-049, at the node the entity is blocked in
	TraceUnblocked  TraceEventType = "unblocked"   // D-049
	TraceSetupStart TraceEventType = "setup_start" // D-055, with ResourceID
)

// TraceEventTypes lists every trace event type, in schema order.
var TraceEventTypes = []TraceEventType{TraceArrival, TraceQueueEnter, TraceServiceStart, TraceServiceEnd, TraceDelayStart,
	TraceDelayEnd, TraceFailure, TraceRework, TraceComplete, TraceAbandon, TraceBlocked, TraceUnblocked, TraceSetupStart}
