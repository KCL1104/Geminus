package validate

import (
	"slices"

	"github.com/kcl1104/geminus/internal/model"
)

// Primitive is one line-flow feature of the IR: the members ADR-026 adds
// (D-049–D-056) and the declared metrics of D-058. The schema accepts them
// all (T-046 phase 2); a build checks and runs a primitive only once the task
// that implements it has added its rules here and its kernel to the engine.
type Primitive string

const (
	FiniteBuffer     Primitive = "finite_buffer"     // queue capacity (D-049), T-047
	Breakdowns       Primitive = "breakdowns"        // resource breakdowns (D-051), T-048
	Lognormal        Primitive = "lognormal"         // distribution (D-056), T-048
	Weibull          Primitive = "weibull"           // distribution (D-056), T-048
	Calendars        Primitive = "calendars"         // calendars, calendar_id, shift_capacity (D-052), T-049
	ReleaseSchedule  Primitive = "release_schedule"  // arrival.schedule (D-053), T-049
	Batch            Primitive = "batch"             // process batch (D-054), T-050
	Setups           Primitive = "setups"            // resource setups (D-055), T-050
	SystemWIP        Primitive = "system_wip"        // declared metric (D-058), T-051
	FlowCount        Primitive = "flow_count"        // declared metric (D-058), T-051
	SinkCount        Primitive = "sink_count"        // declared metric (D-058), T-051
	PeriodThroughput Primitive = "period_throughput" // declared metric (D-058), T-051
)

// Primitives lists every line-flow primitive in a fixed order.
var Primitives = []Primitive{FiniteBuffer, Breakdowns, Lognormal, Weibull, Calendars, ReleaseSchedule, Batch, Setups,
	SystemWIP, FlowCount, SinkCount, PeriodThroughput}

// checked holds the primitives whose semantic rules (TWIN_IR_SPEC §4) this
// validator implements: finite buffers (T-047: D-050 R1, BLOCKING_CYCLE, in
// the graph stage); breakdowns, lognormal and Weibull (T-048: D-050 R3 in
// the bundle feasibility stage, a time to failure that cannot be 0, the
// parameter ranges of D-056 and D-075, the D-029 limits and the binding
// stage of resource distributions). Each of T-049–T-051 adds its own with
// the rules, never before. It is only read, never iterated.
var checked = map[Primitive]bool{FiniteBuffer: true, Breakdowns: true, Lognormal: true, Weibull: true}

// Checked reports whether this validator implements p's semantic rules.
func Checked(p Primitive) bool { return checked[p] }

// what names each primitive in its fixed issue message.
var what = map[Primitive]string{
	FiniteBuffer:     "finite queue capacity (ADR-026 D-049)",
	Breakdowns:       "resource breakdowns (ADR-026 D-051)",
	Lognormal:        "the lognormal distribution (ADR-026 D-056)",
	Weibull:          "the Weibull distribution (ADR-026 D-056)",
	Calendars:        "shift calendars (ADR-026 D-052)",
	ReleaseSchedule:  "release schedules (ADR-026 D-053)",
	Batch:            "batch processes (ADR-026 D-054)",
	Setups:           "changeovers (ADR-026 D-055)",
	SystemWIP:        "the system_wip metric (D-058)",
	FlowCount:        "the flow_count metric (D-058)",
	SinkCount:        "the sink_count metric (D-058)",
	PeriodThroughput: "the period_throughput metric (D-058)",
}

// Use is one place an IR uses a primitive: the JSON Pointer of the member
// that turns it on.
type Use struct {
	Primitive Primitive
	Path      string
}

// Uses returns every use of a line-flow primitive in ir, in D-023 path
// order. A model with none of the members has none (D-047).
func Uses(ir *model.ModelIR) []Use {
	var out []Use
	use := func(p Primitive, tokens ...string) { out = append(out, Use{p, Pointer(tokens...)}) }
	distribution := func(d *model.Distribution, tokens ...string) {
		switch d.Type {
		case model.DistLognormal:
			use(Lognormal, tokens...)
		case model.DistWeibull:
			use(Weibull, tokens...)
		}
	}
	if ir.Calendars != nil {
		use(Calendars, "calendars")
	}
	for i, r := range ir.Resources {
		at := func(tokens ...string) []string { return append([]string{"resources", idx(i)}, tokens...) }
		if r.CalendarID != "" {
			use(Calendars, at("calendar_id")...)
		}
		if r.ShiftCapacity != nil {
			use(Calendars, at("shift_capacity")...)
		}
		if b := r.Breakdowns; b != nil {
			use(Breakdowns, at("breakdowns")...)
			distribution(&b.TimeToFailure.Distribution, at("breakdowns", "time_to_failure", "distribution")...)
			distribution(&b.TimeToRepair.Distribution, at("breakdowns", "time_to_repair", "distribution")...)
		}
		if s := r.Setups; s != nil {
			use(Setups, at("setups")...)
			if s.Default != nil {
				distribution(s.Default, at("setups", "default")...)
			}
			for k := range s.Transitions {
				distribution(&s.Transitions[k].Distribution, at("setups", "transitions", idx(k), "distribution")...)
			}
		}
	}
	for i := range ir.Nodes {
		n := &ir.Nodes[i]
		if n.Capacity != 0 {
			use(FiniteBuffer, "nodes", idx(i), "capacity")
		}
		if n.Batch != nil {
			use(Batch, "nodes", idx(i), "batch")
		}
		if n.Arrival != nil && n.Arrival.Schedule != nil {
			use(ReleaseSchedule, "nodes", idx(i), "arrival", "schedule")
		}
		for _, d := range n.Distributions() {
			distribution(d.Distribution, "nodes", idx(i), d.Member, "distribution")
		}
	}
	for i, m := range ir.Metrics {
		switch m.Type {
		case model.MetricSystemWIP:
			use(SystemWIP, "metrics", idx(i), "type")
		case model.MetricFlowCount:
			use(FlowCount, "metrics", idx(i), "type")
		case model.MetricSinkCount:
			use(SinkCount, "metrics", idx(i), "type")
		case model.MetricPeriodThroughput:
			use(PeriodThroughput, "metrics", idx(i), "type")
		}
	}
	// D-023 pointer order. Each path turns on one primitive and paths are
	// unique, so the order is total.
	slices.SortFunc(out, func(a, b Use) int { return comparePointers(a.Path, b.Path) })
	return out
}

// NotSupported is the issue for a use of a primitive this build does not
// implement: an error at the member, with a fixed message that quotes
// nothing from the model.
func NotSupported(u Use) model.ValidationIssue {
	return model.NewIssue(PrimitiveNotSupported, u.Path,
		"this build does not support "+what[u.Primitive]+" yet; remove it to publish or run the model", model.SeverityError)
}

// support is the primitive support stage, right after the JSON Schema
// (TWIN_IR_SPEC §4, D-063 PROPOSED): a use of a primitive whose rules this
// validator does not implement is an error, and the later stages, which do
// not read the line-flow members, do not run.
func support(ir *model.ModelIR, _ *graph) issues {
	var out issues
	for _, u := range Uses(ir) {
		if !Checked(u.Primitive) {
			out = append(out, NotSupported(u))
		}
	}
	return out
}
