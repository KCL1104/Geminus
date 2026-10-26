package metrics

import (
	"errors"
	"fmt"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
	"github.com/kcl1104/geminus/internal/simulation/engine"
)

// Reasons for unavailable metrics. Like the DORA reason, they are
// provisional codes in the style of testdata/contracts/valid-result.json
// until CB-23 settles reason codes.
const (
	// ReasonNoCompletedEntity: no entity arrived at or after the warmup and
	// reached a completed sink by the horizon.
	ReasonNoCompletedEntity = "NO_COMPLETED_ENTITY_IN_WINDOW"
	// ReasonReplicationWithoutCohort: some replication has no completed
	// lead-time cohort, so the mean of replication means is undefined.
	ReasonReplicationWithoutCohort = "REPLICATION_WITHOUT_COMPLETED_COHORT"
	// ReasonNoQueueVisit: no visit joined this queue at or after the warmup
	// and started service by the horizon.
	ReasonNoQueueVisit = "NO_COMPLETED_QUEUE_VISIT_IN_WINDOW"
)

// ReplicationMeanLeadTime is the additional_metrics key of the mean of
// replication-mean lead times, the estimand the lead-time CI describes
// (SIMULATION_SPEC §5).
const ReplicationMeanLeadTime = "replication_mean_lead_time_minutes"

// ErrInconsistent reports replications that cannot be combined: none, or
// not in replication index order 0..n-1.
var ErrInconsistent = errors.New("metrics: inconsistent replications")

const ticksPerMinute = 60_000.0

// integral is the area under a step function of time, restricted to the
// window [w, h] (ticks). Levels change at non-decreasing ticks.
type integral struct {
	level, since, area int64
}

func (g *integral) set(level, t, w, h int64) {
	g.area += g.level * overlap(g.since, t, w, h)
	g.level, g.since = level, t
}

// total closes the step function at the horizon.
func (g *integral) total(w, h int64) int64 {
	return g.area + g.level*overlap(g.since, h, w, h)
}

// overlap is the length of [a, b] ∩ [w, h], 0 when they do not meet.
func overlap(a, b, w, h int64) int64 {
	return max(min(b, h)-max(a, w), 0)
}

// Collector aggregates one replication's observations over the measurement
// window [W, H] (SIMULATION_SPEC §5): events at W <= t <= H count, time
// integrals cover exactly [W, H], and the system is never reset at W. It is
// an engine.Observer; it is not safe for concurrent use, and each
// replication has its own.
type Collector struct {
	p    *engine.Program
	w, h int64
	// arrived and entered hold the arrival tick of every active entity and
	// the queue-enter tick of every waiting one; they are bounded by the
	// active-entity limit and only ever indexed, never iterated.
	arrived   map[uint64]int64
	entered   map[uint64]int64
	completed int64 // completed-sink arrivals in the window
	failures  int64
	reworks   int64
	leads     []float64   // minutes, in completion order
	waits     [][]float64 // per process node: minutes, in service-start order
	queueLen  []int64     // per process node: requests waiting
	queue     []integral  // per process node: queue length
	busy      []integral  // per resource: units in use
	finished  bool

	// Finite buffers (D-049, D-058; only when the program has one). since
	// holds the tick each blocked entity was blocked at; like arrived it is
	// bounded by the active entities and only indexed.
	finite        bool
	since         map[uint64]int64
	heldUnits     []int64    // per resource: units held by blocked entities (K)
	held          []integral // per resource: ∫K
	nodeBlocked   []int64    // per node: entities blocked there
	blockedAt     []integral // per node: ∫ blocked entities
	blocksStarted []int64    // per node: blocks that started in [W, H]
	blocksDone    []int64    // per node: complete blocks (start >= W, end <= H)
	blockTicks    []int64    // per node: their total length
	full          []integral // per process node with a finite buffer: 1 while full

	// Breakdowns (D-051, D-058; only when the program has them): per
	// resource ∫D, the downtimes that started in [W, H], the complete ones
	// (started at or after W, repaired by H) and their total length; and
	// per resource that breaks down and per failure clock, the tick its
	// current downtime started.
	breakdowns   bool
	down         []integral
	downsStarted []int64
	repairsDone  []int64
	repairTicks  []int64
	downSince    [][]int64

	// Calendars (D-052, D-058; T-049; only when the program has one): per
	// resource on a calendar, its units on shift S and the integrals of
	// S, min(U, S), max(U - S, 0), A = min(S, C - D), max(0, A - U) and
	// min(P, S), P = U - K, kept as step functions of U, S, D and K
	// together (they cannot be derived from the separate totals).
	calendars  bool
	onShift    []int64
	scheduled  []integral
	schedInUse []integral
	overtime   []integral
	available  []integral
	idle       []integral
	processing []integral

	// Batch processes (D-054, D-058; T-050; only when the program has
	// one): per batch process the batches started in [W, H] and their
	// members; with a finite buffer, per (process, batch number) the
	// members still blocked, so that a batch's bundle counts once in K
	// however many of its members are blocked (only indexed).
	batched        bool
	batchesStarted []int64
	membersStarted []int64
	batchBlocked   map[[2]uint64]int64
}

// NewCollector starts the collector of one replication of p.
func NewCollector(p *engine.Program) *Collector {
	n, r := p.NodeCount(), p.ResourceCount()
	c := &Collector{
		p: p, w: p.WarmupTicks(), h: p.HorizonTicks(),
		arrived: map[uint64]int64{}, entered: map[uint64]int64{},
		waits: make([][]float64, n), queueLen: make([]int64, n), queue: make([]integral, n),
		busy: make([]integral, r),
	}
	if p.HasFiniteBuffers() {
		c.finite, c.since = true, map[uint64]int64{}
		c.heldUnits, c.held = make([]int64, r), make([]integral, r)
		c.nodeBlocked, c.blockedAt = make([]int64, n), make([]integral, n)
		c.blocksStarted, c.blocksDone, c.blockTicks = make([]int64, n), make([]int64, n), make([]int64, n)
		c.full = make([]integral, n)
	}
	if p.HasBreakdowns() {
		c.breakdowns = true
		c.down, c.downsStarted, c.repairsDone, c.repairTicks = make([]integral, r), make([]int64, r), make([]int64, r), make([]int64, r)
		c.downSince = make([][]int64, r)
		for i := range r {
			if p.ResourceHasBreakdowns(i) {
				c.downSince[i] = make([]int64, p.ResourceCapacity(i))
			}
		}
	}
	if p.HasBatches() {
		c.batched = true
		c.batchesStarted, c.membersStarted = make([]int64, n), make([]int64, n)
		c.batchBlocked = map[[2]uint64]int64{}
	}
	if p.HasCalendars() {
		c.calendars = true
		c.onShift = make([]int64, r)
		c.scheduled, c.schedInUse, c.overtime = make([]integral, r), make([]integral, r), make([]integral, r)
		c.available, c.idle, c.processing = make([]integral, r), make([]integral, r), make([]integral, r)
		for i := range r {
			if p.ResourceHasCalendar(i) {
				c.onShift[i] = int64(p.InitialCapacity(i)) // S(0), from the program (no observation)
				c.shift(i, 0)
			}
		}
	}
	return c
}

// shift keeps the calendar integrals of resource i (D-058) at tick t,
// after U, S, D or K changed.
func (c *Collector) shift(i int, t int64) {
	if !c.calendars || !c.p.ResourceHasCalendar(i) {
		return
	}
	u, s, d, k := c.busy[i].level, c.onShift[i], int64(0), int64(0)
	if c.breakdowns {
		d = c.down[i].level
	}
	if c.finite {
		k = c.heldUnits[i]
	}
	a := min(s, int64(c.p.ResourceCapacity(i))-d)
	c.scheduled[i].set(s, t, c.w, c.h)
	c.schedInUse[i].set(min(u, s), t, c.w, c.h)
	c.overtime[i].set(max(u-s, 0), t, c.w, c.h)
	c.available[i].set(a, t, c.w, c.h)
	c.idle[i].set(max(a-u, 0), t, c.w, c.h)
	c.processing[i].set(min(u-k, s), t, c.w, c.h)
}

func (c *Collector) inWindow(t int64) bool { return c.w <= t && t <= c.h }

// Finish marks the replication as ended and releases the per-entity state
// (Build reads only totals, samples and integrals), so a run of many
// replications does not keep every replication's entity maps until Build
// (T-022 review). Build accepts only finished collectors; a collector
// observes nothing after Finish.
func (c *Collector) Finish() {
	c.arrived, c.entered, c.since, c.batchBlocked, c.finished = nil, nil, nil, nil, true
}

// Observe implements engine.Observer.
func (c *Collector) Observe(o engine.Observation) {
	switch o.Kind {
	case engine.ObsArrival:
		c.arrived[o.Entity] = o.Tick
	case engine.ObsQueueEnter:
		c.entered[o.Entity] = o.Tick
		c.queueLen[o.Node]++
		c.queue[o.Node].set(c.queueLen[o.Node], o.Tick, c.w, c.h)
		c.setFull(o.Node, o.Tick)
	case engine.ObsServiceStart:
		// A visit counts when it joined the queue at or after W and started
		// by H (the kernel processes nothing after H), zero waits included;
		// every rework visit is its own sample.
		if enter := c.entered[o.Entity]; enter >= c.w && o.Tick <= c.h {
			c.waits[o.Node] = append(c.waits[o.Node], float64(o.Tick-enter)/ticksPerMinute)
		}
		delete(c.entered, o.Entity)
		c.queueLen[o.Node]--
		c.queue[o.Node].set(c.queueLen[o.Node], o.Tick, c.w, c.h)
		c.setFull(o.Node, o.Tick)
	case engine.ObsResource:
		c.busy[o.Resource].set(o.Value, o.Tick, c.w, c.h)
		c.shift(o.Resource, o.Tick)
	case engine.ObsCapacity:
		// Value is the resource's units on shift (D-052).
		c.onShift[o.Resource] = o.Value
		c.shift(o.Resource, o.Tick)
	case engine.ObsFailure:
		if c.inWindow(o.Tick) {
			c.failures++
		}
	case engine.ObsRework:
		if c.inWindow(o.Tick) {
			c.reworks++
		}
	case engine.ObsBlocked:
		c.since[o.Entity] = o.Tick
		if c.inWindow(o.Tick) {
			c.blocksStarted[o.Node]++
		}
		c.block(o.Node, o.Tick, +1, o.Batch)
	case engine.ObsUnblocked:
		// A complete block: started at or after W, ended by H (the kernel
		// processes nothing after H).
		if since := c.since[o.Entity]; since >= c.w && o.Tick <= c.h {
			c.blocksDone[o.Node]++
			c.blockTicks[o.Node] += o.Tick - since
		}
		delete(c.since, o.Entity)
		c.block(o.Node, o.Tick, -1, o.Batch)
	case engine.ObsBatchStart:
		// D-054, D-058: a batch started in [W, H] and its members.
		if c.inWindow(o.Tick) {
			c.batchesStarted[o.Node]++
			c.membersStarted[o.Node] += o.Value
		}
	case engine.ObsResourceDown:
		// Value is the resource's units down (D); At the failure clock.
		c.down[o.Resource].set(o.Value, o.Tick, c.w, c.h)
		c.shift(o.Resource, o.Tick)
		c.downSince[o.Resource][o.At] = o.Tick
		if c.inWindow(o.Tick) {
			c.downsStarted[o.Resource]++
		}
	case engine.ObsResourceUp:
		c.down[o.Resource].set(o.Value, o.Tick, c.w, c.h)
		c.shift(o.Resource, o.Tick)
		// A complete repair: down at or after W, repaired by H (the kernel
		// processes nothing after H); a 0-tick downtime counts (D-074 (d)).
		if since := c.downSince[o.Resource][o.At]; since >= c.w && o.Tick <= c.h {
			c.repairsDone[o.Resource]++
			c.repairTicks[o.Resource] += o.Tick - since
		}
	case engine.ObsComplete:
		arrived := c.arrived[o.Entity]
		delete(c.arrived, o.Entity)
		if o.Value != 1 || !c.inWindow(o.Tick) {
			return // abandoned, or completed before W
		}
		c.completed++
		// The lead-time cohort: arrived at or after W, completed by H.
		if arrived >= c.w {
			c.leads = append(c.leads, float64(o.Tick-arrived)/ticksPerMinute)
		}
	}
}

// setFull keeps the full-time integral of process i's finite buffer.
func (c *Collector) setFull(i int, t int64) {
	if cap := c.p.BufferCapacity(i); c.finite && cap > 0 {
		full := int64(0)
		if c.queueLen[i] >= int64(cap) {
			full = 1
		}
		c.full[i].set(full, t, c.w, c.h)
	}
}

// block counts an entity blocked at (sign +1) or leaving (-1) node n; a
// process's blocked entity holds its whole bundle (D-049). The members of
// batch number batch (D-054; 0 for none) hold their batch's one bundle
// together: it counts from the first one blocked to the last one leaving.
func (c *Collector) block(n int, t int64, sign int64, batch uint64) {
	c.nodeBlocked[n] += sign
	c.blockedAt[n].set(c.nodeBlocked[n], t, c.w, c.h)
	if c.p.NodeType(n) != model.NodeProcess {
		return
	}
	if batch != 0 {
		key := [2]uint64{uint64(n), batch}
		c.batchBlocked[key] += sign
		left := c.batchBlocked[key]
		if left == 0 {
			delete(c.batchBlocked, key)
		}
		if (sign > 0 && left != 1) || (sign < 0 && left != 0) {
			return // the bundle is already held, or still is
		}
	}
	for _, q := range c.p.Requires(n) {
		c.heldUnits[q.Resource] += sign * int64(q.Units)
		c.held[q.Resource].set(c.heldUnits[q.Resource], t, c.w, c.h)
		c.shift(q.Resource, t)
	}
}

// lineFlow fills a replication summary's line-flow members (D-058),
// present only when the model uses the feature (D-047). With a finite
// buffer: every resource's in-use and blocked ticks (in_use_ticks −
// blocked_ticks is the processing time, ∫min(P, S) with S = C off a
// calendar and no changeover yet), every node that can be blocked, every
// finite buffer's full ticks. With breakdowns, every resource that breaks
// down (listed even without a finite buffer) adds ∫D, its downtimes
// started, its complete repairs and their ticks, ∫A = C·T − ∫D and
// ∫(A − U) (U + D <= C at every tick, so A − U >= 0). With calendars,
// every resource on one (listed even without a finite buffer, T-049) adds
// ∫S, ∫min(U, S), ∫max(U − S, 0) (overtime), and its ∫A, ∫max(0, A − U)
// and ∫min(P, S) are integrated as such, A = min(S, C − D). With batch
// processes (T-050), every one has batches[<process>]: the batches started
// in [W, H] and their members (a batch is a process feature: its chamber
// gets no resource_states entry for it; K counts a batch's bundle once).
func (c *Collector) lineFlow(s *simulation.ReplicationSummary) {
	p, w, h := c.p, c.w, c.h
	if c.batched {
		// D-054, D-058 (T-050): every batch process, zeros included.
		s.Batches = map[string]simulation.BatchCounts{}
		for i := range p.NodeCount() {
			if p.IsBatch(i) {
				s.Batches[p.NodeID(i)] = simulation.BatchCounts{BatchesStarted: c.batchesStarted[i], MembersStarted: c.membersStarted[i]}
			}
		}
	}
	if !c.finite && !c.breakdowns && !c.calendars {
		return
	}
	s.ResourceStates = make(map[string]simulation.ResourceState, p.ResourceCount())
	for i := range p.ResourceCount() {
		breaks := c.breakdowns && p.ResourceHasBreakdowns(i)
		onCalendar := c.calendars && p.ResourceHasCalendar(i)
		if !c.finite && !breaks && !onCalendar {
			continue // no line-flow feature applies to it
		}
		used := c.busy[i].total(w, h)
		st := simulation.ResourceState{InUseTicks: used, ScheduledProcessingTicks: used}
		if c.finite {
			held := c.held[i].total(w, h)
			st.BlockedTicks, st.ScheduledProcessingTicks = &held, used-held
		}
		if breaks {
			down := c.down[i].total(w, h)
			available := int64(p.ResourceCapacity(i))*(h-w) - down
			idle := available - used
			started, repairs, repairTicks := c.downsStarted[i], c.repairsDone[i], c.repairTicks[i]
			st.DownTicks, st.BreakdownsStarted, st.RepairsCompleted, st.CompletedRepairTicks = &down, &started, &repairs, &repairTicks
			st.AvailableTicks, st.IdleTicks = &available, &idle
		}
		if onCalendar {
			scheduled, inUse, overtime := c.scheduled[i].total(w, h), c.schedInUse[i].total(w, h), c.overtime[i].total(w, h)
			available, idle := c.available[i].total(w, h), c.idle[i].total(w, h)
			st.ScheduledTicks, st.ScheduledInUseTicks, st.OvertimeTicks = &scheduled, &inUse, &overtime
			st.AvailableTicks, st.IdleTicks = &available, &idle
			st.ScheduledProcessingTicks = c.processing[i].total(w, h)
		}
		s.ResourceStates[p.ResourceID(i)] = st
	}
	if !c.finite {
		return
	}
	s.Blocking = map[string]simulation.NodeBlocking{}
	s.Buffers = map[string]simulation.BufferState{}
	for i := range p.NodeCount() {
		if p.CanBlock(i) {
			s.Blocking[p.NodeID(i)] = simulation.NodeBlocking{BlocksStarted: c.blocksStarted[i], BlocksCompleted: c.blocksDone[i],
				CompletedBlockTicks: c.blockTicks[i], BlockedEntityTicks: c.blockedAt[i].total(w, h)}
		}
		if p.NodeType(i) == model.NodeProcess && p.BufferCapacity(i) > 0 {
			s.Buffers[p.QueueLabel(i)] = simulation.BufferState{FullTicks: c.full[i].total(w, h)}
		}
	}
}

// Replication is one finished replication: its collector and outcome.
type Replication struct {
	Collector *Collector
	Outcome   engine.ReplicationOutcome
}

// Build combines replications 0..n-1, in index order, into a Result's
// metrics, replication summaries and measurement window. Every sum runs in
// that fixed order. DORA and other domain metrics are not the generic
// engine's; Metrics.DORA is left empty for the caller's domain pack.
func Build(p *engine.Program, reps []Replication) (simulation.Metrics, []simulation.ReplicationSummary, simulation.MeasurementWindow, error) {
	if len(reps) == 0 {
		return simulation.Metrics{}, nil, simulation.MeasurementWindow{}, fmt.Errorf("%w: no replications", ErrInconsistent)
	}
	for i, r := range reps {
		if r.Collector == nil || r.Collector.p != p || int(r.Outcome.Index) != i {
			return simulation.Metrics{}, nil, simulation.MeasurementWindow{}, fmt.Errorf("%w: entry %d is not replication %d of this program", ErrInconsistent, i, i)
		}
		if !r.Collector.finished {
			return simulation.Metrics{}, nil, simulation.MeasurementWindow{}, fmt.Errorf("%w: replication %d was not finished", ErrInconsistent, i)
		}
	}
	w, h := p.WarmupTicks(), p.HorizonTicks()
	span := float64(h - w)
	var throughput, failures, reworks, pooledLeads, repLeads []float64
	allCohorts := true
	util := make([][]float64, p.ResourceCount())
	queueLen := make([][]float64, p.NodeCount())
	waits := make([][]float64, p.NodeCount())
	summaries := make([]simulation.ReplicationSummary, 0, len(reps))
	for _, r := range reps {
		c := r.Collector
		s := simulation.ReplicationSummary{
			ReplicationIndex: int(r.Outcome.Index), CompletedCount: c.completed,
			Failures: c.failures, Reworks: c.reworks, CensoredEntities: r.Outcome.Censored,
			ResourceUtilization: make(map[string]float64, p.ResourceCount()),
		}
		throughput = append(throughput, float64(c.completed))
		failures = append(failures, float64(c.failures))
		reworks = append(reworks, float64(c.reworks))
		pooledLeads = append(pooledLeads, c.leads...)
		if len(c.leads) > 0 {
			m := mean(c.leads)
			s.MeanLeadTimeMinutes = &m
			repLeads = append(repLeads, m)
		} else {
			allCohorts = false
		}
		for i := range util {
			u := float64(c.busy[i].total(w, h)) / (float64(p.ResourceCapacity(i)) * span)
			util[i] = append(util[i], u)
			s.ResourceUtilization[p.ResourceID(i)] = u
		}
		for i := range queueLen {
			if p.NodeType(i) == model.NodeProcess {
				queueLen[i] = append(queueLen[i], float64(c.queue[i].total(w, h))/span)
				waits[i] = append(waits[i], c.waits[i]...)
			}
		}
		c.lineFlow(&s)
		summaries = append(summaries, s)
	}
	m := simulation.Metrics{
		// Every replication contributes a count, so these are available even
		// when the count is 0; a missing lead time is unavailable, never 0.
		Throughput:        summary(throughput, simulation.PopulationReplication, "count", ""),
		LeadTimeMinutes:   summary(pooledLeads, simulation.PopulationEntity, "minute", ReasonNoCompletedEntity),
		FailureCount:      summary(failures, simulation.PopulationReplication, "count", ""),
		ReworkCount:       summary(reworks, simulation.PopulationReplication, "count", ""),
		Resources:         make(map[string]simulation.ResourceStats, p.ResourceCount()),
		Queues:            map[string]simulation.QueueStats{},
		DORA:              map[string]simulation.MetricSummary{},
		AdditionalMetrics: map[string]simulation.MetricSummary{},
	}
	// Pooled entity quantiles carry no CI. The CI belongs to the mean of
	// replication means, which needs a cohort in every replication.
	switch {
	case len(repLeads) == 0:
		m.AdditionalMetrics[ReplicationMeanLeadTime] = summary(nil, simulation.PopulationReplication, "minute", ReasonNoCompletedEntity)
	case !allCohorts:
		m.AdditionalMetrics[ReplicationMeanLeadTime] = summary(nil, simulation.PopulationReplication, "minute", ReasonReplicationWithoutCohort)
	default:
		m.AdditionalMetrics[ReplicationMeanLeadTime] = summary(repLeads, simulation.PopulationReplication, "minute", "")
	}
	for i := range util {
		m.Resources[p.ResourceID(i)] = simulation.ResourceStats{
			UtilizationMean: ptr(mean(util[i])), ObservationTicks: h - w, Capacity: p.ResourceCapacity(i),
		}
	}
	for i := range queueLen {
		if p.NodeType(i) != model.NodeProcess {
			continue
		}
		q := simulation.QueueStats{QueueLengthMean: mean(queueLen[i]), SampleCount: int64(len(waits[i]))}
		if len(waits[i]) == 0 {
			q.Status, q.Reason = simulation.Unavailable, ptr(ReasonNoQueueVisit)
		} else {
			q.Status = simulation.Available
			q.WaitMeanMinutes = ptr(mean(waits[i]))
			q.WaitP50Minutes = ptr(quantile7(waits[i], 0.5))
			q.WaitP95Minutes = ptr(quantile7(waits[i], 0.95))
		}
		m.Queues[p.QueueLabel(i)] = q
	}
	window := simulation.MeasurementWindow{TickUnit: "millisecond", WarmupTicks: w, HorizonTicks: h, TimeBoundary: "warmup <= t <= horizon"}
	return m, summaries, window, nil
}
