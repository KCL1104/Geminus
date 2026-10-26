package electronicsline

import (
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/calendar"
	"github.com/kcl1104/geminus/internal/results/metrics"
	"github.com/kcl1104/geminus/internal/simulation"
)

// Scope is where a metric of the pack applies: the whole line, or each
// station (a first test with its retests, keyed by the first test's node
// ID).
type Scope string

const (
	LineScope    Scope = "line"
	StationScope Scope = "station"
)

// MetricDefinition is one metric of ELECTRONICS_LINE §5 the pack reports
// in metrics.domain (D-058). A line metric's key is its Name; a station
// metric's is Name + "." + the station's first-test node ID (a name has
// no "."; a node ID may, so the key splits at its first ".").
type MetricDefinition struct {
	Name  string
	Scope Scope
	Unit  string
}

// metricDefs are the pack's metrics in §5's order. Every one is either
// computed from the engine's Result and the IR or reported unavailable
// with a reason (REQ-015); none is borrowed from another metric.
var metricDefs = []MetricDefinition{
	{"uph", LineScope, "entity_per_hour"},
	{"uph_scheduled", LineScope, "entity_per_hour"},
	{"rolled_throughput_yield_input", LineScope, "ratio"},
	{"scrap_rate", LineScope, "ratio"},
	{"line_wip", LineScope, "entity"},
	{"line_balance", LineScope, "ratio"},
	{"fpy_input", StationScope, "ratio"},
	{"simulated_first_pass_ratio", StationScope, "ratio"},
	{"retest_ratio", StationScope, "ratio"},
	{"blocking_ratio", StationScope, "ratio"},
	{"mean_blocking_minutes", StationScope, "minute"},
	{"down_ratio", StationScope, "ratio"},
	{"setup_ratio", StationScope, "ratio"},
	{"setups_started", StationScope, "count"},
	{"availability", StationScope, "ratio"},
	{"idle_ratio", StationScope, "ratio"},
	{"starved_ratio", StationScope, "ratio"},
	{"processing_ratio", StationScope, "ratio"},
	{"off_shift_ratio", StationScope, "ratio"},
	{"scheduled_utilization", StationScope, "ratio"},
	{"overtime_minutes", StationScope, "minute"},
	{"work_ratio", StationScope, "ratio"},
	{"batches_started", StationScope, "count"},
	{"mean_batch_size", StationScope, "entity"},
	{"batch_fill_ratio", StationScope, "ratio"},
}

// Metrics lists the pack's metric definitions (a copy).
func Metrics() []MetricDefinition { return append([]MetricDefinition(nil), metricDefs...) }

func unitOf(name string) string {
	for _, d := range metricDefs {
		if d.Name == name {
			return d.Unit
		}
	}
	panic("electronicsline: no metric " + name) // a typo in this file
}

// The reasons a metric is unavailable (ELECTRONICS_LINE §5, provisional
// like the generic ones, CB-23). The first eleven are §5's; the last
// three are the pack's: a batch metric of a station that is not a batch
// process, a batch metric of a window without a batch, and an engine
// Result without the member the model's features imply.
const (
	reasonNoCompletedSink         = "NO_COMPLETED_SINK"
	reasonNoCommonLineCalendar    = "NO_COMMON_LINE_CALENDAR"
	reasonNoScheduledTime         = "NO_SCHEDULED_TIME_IN_WINDOW"
	reasonTestResultNotIdentified = "TEST_RESULT_NOT_IDENTIFIED"
	reasonFlowCountNotDeclared    = "FLOW_COUNT_NOT_DECLARED"
	reasonSinkCountNotDeclared    = "SINK_COUNT_NOT_DECLARED"
	reasonWIPNotDeclared          = "WIP_NOT_DECLARED"
	reasonNoFiniteBuffers         = "NO_FINITE_BUFFERS"
	reasonNoCompletedBlock        = "NO_COMPLETED_BLOCK_IN_WINDOW"
	reasonNoBreakdownModel        = "NO_BREAKDOWN_MODEL"
	reasonNoSetupModel            = "NO_SETUP_MODEL"
	reasonStarvationShared        = "STARVATION_UNDEFINED_SHARED_RESOURCE"
	reasonStarvationMultiUnit     = "STARVATION_UNDEFINED_MULTI_UNIT"
	reasonStarvationBatch         = "STARVATION_UNDEFINED_BATCH"
	reasonTooFewStations          = "TOO_FEW_STATIONS"
	reasonNoProcessing            = "NO_PROCESSING_IN_WINDOW"
	reasonStationRoleMissing      = "STATION_ROLE_MISSING"
	reasonNoBatchModel            = "NO_BATCH_MODEL"
	reasonNoBatchInWindow         = "NO_BATCH_IN_WINDOW"
	reasonResultMemberMissing     = "RESULT_MEMBER_MISSING"
)

// ticksPerHour and ticksPerMinute convert the engine's 1 ms ticks.
const (
	ticksPerHour   = 3_600_000
	ticksPerMinute = 60_000
)

// value is one replication's value of a metric, or why it has none.
type value struct {
	v      float64
	reason string
}

func ok(v float64) value          { return value{v: v} }
func missing(reason string) value { return value{reason: reason} }

// states are one resource's integrals over [W, H] in one replication, in
// ticks (exact: C·T ≤ 100,000 units × 365 days < 2^53): U in use, K held by
// blocked entities, Z changing over, D down, S on shift, A available, I
// idle, US the on-shift part of U, OT the overtime and PS the on-shift
// processing (ELECTRONICS_LINE §5's symbols).
type states struct {
	CT, U, K, Z, D, S, A, I, US, OT, PS float64
}

// computer computes the metrics of one IR and Result.
type computer struct {
	l       *line
	r       *simulation.Result
	w, h, t int64
	finite  bool // the model has a finite buffer (D-049)
}

// ComputeModelMetrics derives ELECTRONICS_LINE §5's metrics from the
// effective IR and the engine's generic Result (D-058); it changes
// neither. Each metric is one value a replication, summarized across
// replications as every Result statistic is (population replication,
// metrics.Summarize); a metric some replication has no value for is
// unavailable with that replication's reason. Without a model, a Result,
// a replication or a window there is nothing to describe: no metrics.
func (Pack) ComputeModelMetrics(ir *model.ModelIR, r *simulation.Result) map[string]simulation.MetricSummary {
	if ir == nil || r == nil || len(r.ReplicationSummaries) == 0 || r.MeasurementWindow.HorizonTicks <= r.MeasurementWindow.WarmupTicks ||
		r.MeasurementWindow.WarmupTicks < 0 {
		return nil
	}
	c := &computer{l: read(ir), r: r, w: r.MeasurementWindow.WarmupTicks, h: r.MeasurementWindow.HorizonTicks}
	c.t = c.h - c.w
	for _, n := range ir.Nodes {
		if n.Type == model.NodeQueue && n.Capacity > 0 {
			c.finite = true
		}
	}
	out := map[string]simulation.MetricSummary{}
	put := func(key, name string, f func(rep *simulation.ReplicationSummary) value) {
		values := make([]float64, 0, len(r.ReplicationSummaries))
		for i := range r.ReplicationSummaries {
			v := f(&r.ReplicationSummaries[i])
			if v.reason != "" {
				reason := v.reason
				out[key] = simulation.MetricSummary{Status: simulation.Unavailable, Population: simulation.PopulationReplication, Unit: unitOf(name), Reason: &reason}
				return
			}
			values = append(values, v.v)
		}
		out[key] = metrics.Summarize(values, simulation.PopulationReplication, unitOf(name), reasonResultMemberMissing)
	}
	static := func(v value) func(*simulation.ReplicationSummary) value {
		return func(*simulation.ReplicationSummary) value { return v }
	}
	c.line(put, static)
	for _, s := range c.l.stations {
		c.station(s, put, static)
	}
	return out
}

func (c *computer) line(put func(string, string, func(*simulation.ReplicationSummary) value), static func(value) func(*simulation.ReplicationSummary) value) {
	completedSink := false
	for _, n := range c.l.ir.Nodes {
		if n.Type == model.NodeSink && n.Outcome != model.SinkAbandoned {
			completedSink = true
		}
	}
	put("uph", "uph", func(rep *simulation.ReplicationSummary) value {
		if !completedSink {
			return missing(reasonNoCompletedSink)
		}
		return ok(float64(rep.CompletedCount*ticksPerHour) / float64(c.t))
	})
	onShift, reason := c.lineCalendarTicks()
	put("uph_scheduled", "uph_scheduled", func(rep *simulation.ReplicationSummary) value {
		switch {
		case !completedSink:
			return missing(reasonNoCompletedSink)
		case reason != "":
			return missing(reason)
		}
		return ok(float64(rep.CompletedCount*ticksPerHour) / float64(onShift))
	})
	put("rolled_throughput_yield_input", "rolled_throughput_yield_input", static(c.rolledYield()))
	put("scrap_rate", "scrap_rate", static(missing(reasonSinkCountNotDeclared)))
	put("line_wip", "line_wip", static(missing(reasonWIPNotDeclared)))
	put("line_balance", "line_balance", c.lineBalance)
}

// lineCalendarTicks is L, the line calendar's on-shift ticks in [W, H)
// (not times its capacity): the one calendar the main-line stations with a
// calendar share; stations without one (a 24/7 burn-in) do not count.
func (c *computer) lineCalendarTicks() (int64, string) {
	ids := map[string]bool{}
	var id string
	for _, s := range c.l.nodesAt {
		if s.primary < 0 {
			continue
		}
		if cal := c.l.ir.Resources[s.primary].CalendarID; cal != "" && !ids[cal] {
			ids[cal], id = true, cal
		}
	}
	if len(ids) != 1 {
		return 0, reasonNoCommonLineCalendar
	}
	for _, cal := range c.l.ir.Calendars {
		if cal.ID != id {
			continue
		}
		p, err := calendar.FromModel(cal, model.TimeUnit(c.l.ir.TimeUnit), 1, nil)
		if err != nil {
			return 0, reasonNoCommonLineCalendar
		}
		on := p.OnTicks(c.h) - p.OnTicks(c.w)
		if on <= 0 {
			return 0, reasonNoScheduledTime
		}
		return on, ""
	}
	return 0, reasonNoCommonLineCalendar
}

// fpy is a test station's first-pass yield, a model input: the pass flows'
// probabilities of its first test's result (§5).
func (c *computer) fpy(s *station) value {
	if s.result == nil || s.result.fails != 1 {
		return missing(reasonTestResultNotIdentified)
	}
	return ok(s.result.pass)
}

// rolledYield is the product of the test stations' first-pass yields, in
// node order.
func (c *computer) rolledYield() value {
	product, tests := 1.0, 0
	for _, s := range c.l.stations {
		if !s.first.kind.test {
			continue
		}
		tests++
		y := c.fpy(s)
		if y.reason != "" {
			return y
		}
		product *= y.v
	}
	if tests == 0 {
		return missing(reasonTestResultNotIdentified)
	}
	return ok(product)
}

// lineBalance is mean(w)/max(w) over the stations' primary resources, each
// once, w being the on-shift processing over the on-shift time.
func (c *computer) lineBalance(rep *simulation.ReplicationSummary) value {
	var resources []int
	for _, s := range c.l.stations {
		if s.first.primary < 0 {
			return missing(reasonStationRoleMissing)
		}
		seen := false
		for _, r := range resources {
			seen = seen || r == s.first.primary
		}
		if !seen {
			resources = append(resources, s.first.primary)
		}
	}
	if len(resources) < 2 {
		return missing(reasonTooFewStations)
	}
	sum, most := 0.0, 0.0
	for _, r := range resources {
		w := c.workRatio(rep, r)
		if w.reason != "" {
			return w
		}
		sum += w.v
		most = max(most, w.v)
	}
	if most == 0 {
		return missing(reasonNoProcessing)
	}
	return ok(sum / float64(len(resources)) / most)
}

func (c *computer) workRatio(rep *simulation.ReplicationSummary, r int) value {
	st, why := c.states(rep, r)
	switch {
	case why != "":
		return missing(why)
	case st.S == 0:
		return missing(reasonNoScheduledTime)
	}
	return ok(st.PS / st.S)
}

// states reads resource r's integrals from rep (D-058). A resource with
// none of the features is not in resource_states: it is never blocked,
// down, changing over or off shift, so P = U and idle = C·T − U, with U
// from its utilization.
func (c *computer) states(rep *simulation.ReplicationSummary, r int) (states, string) {
	res := &c.l.ir.Resources[r]
	if res.Capacity < 1 {
		return states{}, reasonResultMemberMissing
	}
	ct := float64(int64(res.Capacity) * c.t)
	onCalendar, breakdowns, setups := res.CalendarID != "", res.Breakdowns != nil, res.Setups != nil
	st, listed := rep.ResourceStates[res.ID]
	if !listed {
		u, has := rep.ResourceUtilization[res.ID]
		if c.finite || onCalendar || breakdowns || setups || !has {
			return states{}, reasonResultMemberMissing
		}
		in := float64(u * ct)
		return states{CT: ct, U: in, S: ct, A: ct, I: ct - in, US: in, PS: in}, ""
	}
	get := func(p *int64, needed bool, otherwise float64) (float64, bool) {
		switch {
		case !needed:
			return otherwise, true
		case p == nil:
			return 0, false
		}
		return float64(*p), true
	}
	s := states{CT: ct, U: float64(st.InUseTicks), PS: float64(st.ScheduledProcessingTicks)}
	var all [7]bool
	s.K, all[0] = get(st.BlockedTicks, c.finite, 0)
	s.Z, all[1] = get(st.SetupTicks, setups, 0)
	s.D, all[2] = get(st.DownTicks, breakdowns, 0)
	s.S, all[3] = get(st.ScheduledTicks, onCalendar, ct)
	s.A, all[4] = get(st.AvailableTicks, onCalendar || breakdowns, ct)
	s.I, all[5] = get(st.IdleTicks, onCalendar || breakdowns, ct-s.U)
	s.US, all[6] = get(st.ScheduledInUseTicks, onCalendar, s.U)
	if onCalendar {
		var has bool
		if s.OT, has = get(st.OvertimeTicks, true, 0); !has {
			return states{}, reasonResultMemberMissing
		}
	}
	for _, has := range all {
		if !has {
			return states{}, reasonResultMemberMissing
		}
	}
	return s, ""
}

func (c *computer) station(s *station, put func(string, string, func(*simulation.ReplicationSummary) value), static func(value) func(*simulation.ReplicationSummary) value) {
	key := func(name string) string { return name + "." + c.l.ir.Nodes[s.first.index].ID }
	r := s.first.primary
	var res *model.Resource
	if r >= 0 {
		res = &c.l.ir.Resources[r]
	}
	// ratio is a metric of the primary resource's integrals: unavailable
	// with STATION_ROLE_MISSING without one, with the gate's reason when
	// the model lacks the feature, otherwise f of the replication's states.
	ratio := func(name, gate string, f func(st states, rep *simulation.ReplicationSummary) value) {
		put(key(name), name, func(rep *simulation.ReplicationSummary) value {
			if res == nil {
				return missing(reasonStationRoleMissing)
			}
			if gate != "" {
				return missing(gate)
			}
			st, why := c.states(rep, r)
			if why != "" {
				return missing(why)
			}
			return f(st, rep)
		})
	}
	gate := func(has bool, reason string) string {
		if res == nil || has {
			return ""
		}
		return reason
	}
	if s.first.kind.test {
		put(key("fpy_input"), "fpy_input", static(c.fpy(s)))
		put(key("simulated_first_pass_ratio"), "simulated_first_pass_ratio", static(missing(reasonFlowCountNotDeclared)))
		if len(s.retests) > 0 {
			put(key("retest_ratio"), "retest_ratio", static(missing(reasonFlowCountNotDeclared)))
		}
	}
	hasBreakdowns := res != nil && res.Breakdowns != nil
	hasSetups := res != nil && res.Setups != nil
	ratio("blocking_ratio", gate(c.finite, reasonNoFiniteBuffers), func(st states, _ *simulation.ReplicationSummary) value { return ok(st.K / st.CT) })
	put(key("mean_blocking_minutes"), "mean_blocking_minutes", func(rep *simulation.ReplicationSummary) value {
		if !c.finite {
			return missing(reasonNoFiniteBuffers)
		}
		var blocks, ticks int64
		for _, i := range s.processes() {
			b := rep.Blocking[c.l.ir.Nodes[i].ID]
			blocks += b.BlocksCompleted
			ticks += b.CompletedBlockTicks
		}
		if blocks == 0 {
			return missing(reasonNoCompletedBlock)
		}
		return ok(float64(ticks) / float64(blocks) / ticksPerMinute)
	})
	ratio("down_ratio", gate(hasBreakdowns, reasonNoBreakdownModel), func(st states, _ *simulation.ReplicationSummary) value { return ok(st.D / st.CT) })
	ratio("setup_ratio", gate(hasSetups, reasonNoSetupModel), func(st states, _ *simulation.ReplicationSummary) value { return ok(st.Z / st.CT) })
	ratio("setups_started", gate(hasSetups, reasonNoSetupModel), func(_ states, rep *simulation.ReplicationSummary) value {
		n := rep.ResourceStates[res.ID].SetupsStarted
		if n == nil {
			return missing(reasonResultMemberMissing)
		}
		return ok(float64(*n))
	})
	ratio("availability", gate(hasBreakdowns, reasonNoBreakdownModel), func(st states, _ *simulation.ReplicationSummary) value {
		if st.S == 0 {
			return missing(reasonNoScheduledTime)
		}
		return ok(st.A / st.S)
	})
	ratio("idle_ratio", "", func(st states, _ *simulation.ReplicationSummary) value { return ok(st.I / st.CT) })
	ratio("starved_ratio", c.starvation(s), func(st states, _ *simulation.ReplicationSummary) value { return ok(st.I / st.CT) })
	ratio("processing_ratio", "", func(st states, _ *simulation.ReplicationSummary) value { return ok((st.U - st.K - st.Z) / st.CT) })
	ratio("off_shift_ratio", "", func(st states, _ *simulation.ReplicationSummary) value {
		return ok((st.CT - st.U - st.D - st.I) / st.CT)
	})
	// Scheduled utilization and overtime are listed for a resource with a
	// calendar only: without one they are utilization_mean and 0 (§5).
	if res == nil || res.CalendarID != "" {
		ratio("scheduled_utilization", "", func(st states, _ *simulation.ReplicationSummary) value {
			if st.S == 0 {
				return missing(reasonNoScheduledTime)
			}
			return ok(st.US / st.S)
		})
		ratio("overtime_minutes", "", func(st states, _ *simulation.ReplicationSummary) value { return ok(st.OT / ticksPerMinute) })
	}
	ratio("work_ratio", "", func(_ states, rep *simulation.ReplicationSummary) value { return c.workRatio(rep, r) })
	batch := c.l.ir.Nodes[s.first.index].Batch
	counts := func(rep *simulation.ReplicationSummary) (simulation.BatchCounts, string) {
		if batch == nil {
			return simulation.BatchCounts{}, reasonNoBatchModel
		}
		b, listed := rep.Batches[c.l.ir.Nodes[s.first.index].ID]
		if !listed {
			return b, reasonResultMemberMissing
		}
		return b, ""
	}
	put(key("batches_started"), "batches_started", func(rep *simulation.ReplicationSummary) value {
		b, why := counts(rep)
		if why != "" {
			return missing(why)
		}
		return ok(float64(b.BatchesStarted))
	})
	meanBatch := func(rep *simulation.ReplicationSummary) value {
		b, why := counts(rep)
		switch {
		case why != "":
			return missing(why)
		case b.BatchesStarted == 0:
			return missing(reasonNoBatchInWindow)
		}
		return ok(float64(b.MembersStarted) / float64(b.BatchesStarted))
	}
	put(key("mean_batch_size"), "mean_batch_size", meanBatch)
	put(key("batch_fill_ratio"), "batch_fill_ratio", func(rep *simulation.ReplicationSummary) value {
		m := meanBatch(rep)
		if m.reason != "" {
			return m
		}
		return ok(m.v / float64(batch.MaxSize))
	})
}

// starvation is why idle is not starvation at station s, or "" when it is
// (§5, review L6): the primary resource is the station's own (its first
// test and retests only), each of them needs it alone, one unit at a time,
// and is not a batch process.
func (c *computer) starvation(s *station) string {
	r := s.first.primary
	if r < 0 {
		return ""
	}
	own := map[int]bool{}
	for _, i := range s.processes() {
		own[i] = true
	}
	id := c.l.ir.Resources[r].ID
	reason := ""
	for i, n := range c.l.ir.Nodes {
		uses := false
		for _, req := range n.Requires {
			uses = uses || req.ResourceID == id
		}
		switch {
		case !uses:
		case !own[i] || len(n.Requires) != 1:
			return reasonStarvationShared
		case n.Requires[0].Units != 1:
			reason = reasonStarvationMultiUnit
		case n.Batch != nil && reason == "":
			reason = reasonStarvationBatch
		}
	}
	return reason
}
