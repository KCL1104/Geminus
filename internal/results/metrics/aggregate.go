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
}

// NewCollector starts the collector of one replication of p.
func NewCollector(p *engine.Program) *Collector {
	n, r := p.NodeCount(), p.ResourceCount()
	return &Collector{
		p: p, w: p.WarmupTicks(), h: p.HorizonTicks(),
		arrived: map[uint64]int64{}, entered: map[uint64]int64{},
		waits: make([][]float64, n), queueLen: make([]int64, n), queue: make([]integral, n),
		busy: make([]integral, r),
	}
}

func (c *Collector) inWindow(t int64) bool { return c.w <= t && t <= c.h }

// Observe implements engine.Observer.
func (c *Collector) Observe(o engine.Observation) {
	switch o.Kind {
	case engine.ObsArrival:
		c.arrived[o.Entity] = o.Tick
	case engine.ObsQueueEnter:
		c.entered[o.Entity] = o.Tick
		c.queueLen[o.Node]++
		c.queue[o.Node].set(c.queueLen[o.Node], o.Tick, c.w, c.h)
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
	case engine.ObsResource:
		c.busy[o.Resource].set(o.Value, o.Tick, c.w, c.h)
	case engine.ObsFailure:
		if c.inWindow(o.Tick) {
			c.failures++
		}
	case engine.ObsRework:
		if c.inWindow(o.Tick) {
			c.reworks++
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
