package electronicsline

import (
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/results/observed"
	"github.com/kcl1104/geminus/internal/simulation"
)

// ObservedWindow is where a station log is compared with a run
// (ELECTRONICS_LINE §6, §7): Anchor is the moment of model time 0 (the
// user's anchor: the start of the model's period, e.g. a Monday 00:00 with
// its offset), and the log is observed in [From, To).
type ObservedWindow struct {
	Anchor, From, To time.Time
}

// ObservedComparison is one quantity of §7 compared (D-060).
type ObservedComparison struct {
	Key string
	observed.Comparison
}

// Units of the compared quantities (D-117 (d)'s units).
const (
	unitEntity = "entity"
	unitRatio  = "ratio"
	unitMinute = "minute"
)

// The periods of the line's output (§7: per day, per week).
var outputPeriods = []struct {
	key   string
	ticks int64
}{
	{"line_output_per_week", 7 * 24 * 60 * 60 * 1000},
	{"line_output_per_day", 24 * 60 * 60 * 1000},
}

// logEvent is an operation of the log with its times in ticks (1 ms) since
// the anchor; end -1 when it has none.
type logEvent struct {
	e          *domainpack.CanonicalEvent
	start, end int64
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, strings.ToUpper(s)) // the canonical form is ASCII
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func lcm(a, b int64) int64 {
	if a == 0 || b == 0 {
		return max(a, b)
	}
	return a / gcd(a, b) * b
}

func mod(a, m int64) int64 { return ((a % m) + m) % m }

// cycle is the period the model repeats (in ticks): the least common
// multiple of its calendars' and release schedules' periods; 0 when it has
// none.
func cycle(ir *model.ModelIR) int64 {
	unit := model.TimeUnit(ir.TimeUnit)
	var c int64
	add := func(period float64) {
		if t, err := model.Ticks(period, unit); err == nil && t > 0 {
			c = lcm(c, t)
		}
	}
	for _, cal := range ir.Calendars {
		add(cal.Period)
	}
	for _, n := range ir.Nodes {
		if n.Arrival != nil && n.Arrival.Schedule != nil {
			add(n.Arrival.Schedule.Period)
		}
	}
	return c
}

// mean is the mean of d in minutes, from its parameters, when the
// numerical profile gives it without a transcendental function (a Weibull
// mean needs Γ, an empirical one its dataset): ok false otherwise.
func mean(d model.Distribution, unit model.TimeUnit) (float64, bool) {
	ms, known := unit.Millis()
	if !known {
		return 0, false
	}
	var v float64
	switch {
	case d.Type == model.DistConstant && d.Value != nil:
		v = *d.Value
	case (d.Type == model.DistExponential || d.Type == model.DistLognormal) && d.Mean != nil:
		v = *d.Mean
	case d.Type == model.DistTriangular && d.Min != nil && d.Mode != nil && d.Max != nil:
		v = (*d.Min + *d.Mode + *d.Max) / 3
	default:
		return 0, false
	}
	return float64(v*float64(ms)) / 60000, true
}

func sameDistribution(a, b *model.TimedDistribution) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, err1 := json.Marshal(a.Distribution, json.Deterministic(true))
	y, err2 := json.Marshal(b.Distribution, json.Deterministic(true))
	return err1 == nil && err2 == nil && string(x) == string(y)
}

// CompareObserved compares a run of ir (result) with a station log
// observed in w (§7, D-060 option A): the line's output per week and per
// day (output: the operations at the line's last station, the one that
// routes to a completed sink, that end in the window, against each
// replication's completed boards over its measurement window), and for
// each station in node order its first-pass yield if it is a test station
// (input consistency: of the boards that entered the line in the window,
// at its first station, the share whose first operation at the station
// passed, against the model's input, §5) and its mean cycle time (input
// consistency: the operations' end − start against the mean of the first
// test's service time; approximate when a retest's differs). An operation
// is the station's when its stage is the station's name (one station of
// that name) or one of its steps' IDs; operations are the ones that start
// in the window (output: that end in it). Keys are D-117 (d)'s form.
//
// Both windows of an output are whole periods of the model's cycle (its
// calendars' and release schedules' periods) at the same phase, and the
// log has one UTC offset in the window; otherwise, and without events, a
// field or a simulated value a quantity needs, it is unavailable with
// D-060's reason (no number is ever made up). sample labels every row
// (D-060: in-sample or hold-out is the caller's, who knows the model's
// provenance). It reads ir, result and events and changes none; an event
// whose time does not parse is not canonical and is an error.
func CompareObserved(ir *model.ModelIR, result *simulation.Result, events []domainpack.CanonicalEvent, w ObservedWindow,
	sample observed.Sample) ([]ObservedComparison, error) {
	if ir == nil {
		return nil, errors.New("electronicsline: no model to compare with")
	}
	from, to := w.From.Sub(w.Anchor).Milliseconds(), w.To.Sub(w.Anchor).Milliseconds()
	var ops []logEvent
	offsets := map[int]bool{}
	for i := range events {
		e := &events[i]
		start, err := parseTime(e.Timestamp)
		if err != nil {
			return nil, errors.New("electronicsline: an event's timestamp is not RFC 3339")
		}
		ev := logEvent{e: e, start: start.Sub(w.Anchor).Milliseconds(), end: -1}
		in := ev.start >= from && ev.start < to
		if in {
			_, off := start.Zone()
			offsets[off] = true
		}
		if e.EndTimestamp != "" {
			end, err := parseTime(e.EndTimestamp)
			if err != nil {
				return nil, errors.New("electronicsline: an event's end_timestamp is not RFC 3339")
			}
			ev.end = end.Sub(w.Anchor).Milliseconds()
			if in {
				_, off := end.Zone()
				offsets[off] = true
			}
		}
		if e.EventType == EventOperation {
			ops = append(ops, ev)
		}
	}
	l := read(ir)
	var out []ObservedComparison
	put := func(key, unit string, approximate bool, c observed.Comparison) {
		c.Sample, c.Unit, c.Approximate = sample, unit, approximate && c.Status == simulation.Available
		out = append(out, ObservedComparison{Key: key, Comparison: c})
	}
	stages := func(s *station) []string {
		named := 0
		for _, o := range l.stations {
			if o.first.kind == s.first.kind {
				named++
			}
		}
		var st []string
		if named == 1 {
			st = append(st, s.first.kind.name)
		}
		st = append(st, ir.Nodes[s.first.index].ID)
		for _, r := range s.retests {
			st = append(st, ir.Nodes[r.index].ID)
		}
		return st
	}
	at := func(ev logEvent, st []string) bool { return slices.Contains(st, ev.e.Stage) }
	inWindow := func(ev logEvent) bool { return ev.start >= from && ev.start < to }

	// The line's output.
	var last *station
	for _, s := range l.stations {
		for _, f := range ir.Flows {
			for _, p := range append([]*stationNode{s.first}, s.retests...) {
				if f.From != ir.Nodes[p.index].ID {
					continue
				}
				if to, ok := l.nodes[f.To]; ok && ir.Nodes[to].Type == model.NodeSink && ir.Nodes[to].Outcome == model.SinkCompleted && last == nil {
					last = s
				}
			}
		}
	}
	c := cycle(ir)
	for _, period := range outputPeriods {
		unit := lcm(period.ticks, c)
		var w0, h0 int64
		if result != nil {
			w0, h0 = result.MeasurementWindow.WarmupTicks, result.MeasurementWindow.HorizonTicks
		}
		switch {
		case len(events) == 0:
			put(period.key, unitEntity, false, observed.Unavailable(observed.Output, observed.ReasonNoObservedData))
			continue
		case to-from < unit:
			put(period.key, unitEntity, false, observed.Unavailable(observed.Output, observed.ReasonWindowTooShort))
			continue
		case (to-from)%unit != 0 || mod(from, unit) != mod(w0, unit) || (h0-w0)%unit != 0 || h0-w0 < unit || len(offsets) > 1:
			put(period.key, unitEntity, false, observed.Unavailable(observed.Output, observed.ReasonWindowNotAligned))
			continue
		case result == nil || len(result.ReplicationSummaries) == 0:
			put(period.key, unitEntity, false, observed.Unavailable(observed.Output, observed.ReasonSimulatedUnavailable))
			continue
		case last == nil:
			put(period.key, unitEntity, false, observed.Unavailable(observed.Output, reasonNoCompletedSink))
			continue
		}
		st := stages(last)
		count, gap := 0, false
		for _, ev := range ops {
			switch {
			case !at(ev, st):
			case ev.end < 0:
				gap = gap || inWindow(ev)
			case ev.end >= from && ev.end < to:
				count++
			}
		}
		if gap {
			put(period.key, unitEntity, false, observed.Unavailable(observed.Output, observed.ReasonFieldMissing))
			continue
		}
		xs := make([]float64, len(result.ReplicationSummaries))
		for i, r := range result.ReplicationSummaries {
			xs[i] = float64(r.CompletedCount) / float64((h0-w0)/period.ticks)
		}
		put(period.key, unitEntity, false, observed.CompareOutput(float64(count)/float64((to-from)/period.ticks), xs))
	}

	// The stations: first-pass yield (test stations) and cycle time.
	var firstStation *station
	for _, k := range stationKinds {
		for _, s := range l.stations {
			if s.first.kind.name == k.name && firstStation == nil {
				firstStation = s
			}
		}
	}
	entered := map[string]bool{}
	if firstStation != nil {
		st := stages(firstStation)
		for _, ev := range ops {
			if at(ev, st) && inWindow(ev) {
				entered[ev.e.CaseID] = true
			}
		}
	}
	unit := model.TimeUnit(ir.TimeUnit)
	for _, s := range l.stations {
		st := stages(s)
		if s.first.kind.test {
			key := "first_pass_yield." + ir.Nodes[s.first.index].ID
			first := map[string]logEvent{}
			var order []string
			for _, ev := range ops {
				if !at(ev, st) || !inWindow(ev) || !entered[ev.e.CaseID] {
					continue
				}
				if prev, seen := first[ev.e.CaseID]; !seen || ev.start < prev.start {
					if !seen {
						order = append(order, ev.e.CaseID)
					}
					first[ev.e.CaseID] = ev
				}
			}
			pass, results := 0, true
			for _, serial := range order {
				switch first[serial].e.Outcome {
				case OutcomePass:
					pass++
				case OutcomeFail:
				default:
					results = false
				}
			}
			switch {
			case len(events) == 0 || len(order) == 0:
				put(key, unitRatio, false, observed.Unavailable(observed.InputConsistency, observed.ReasonNoObservedData))
			case !results:
				put(key, unitRatio, false, observed.Unavailable(observed.InputConsistency, observed.ReasonFieldMissing))
			case s.result == nil || s.result.fails != 1:
				put(key, unitRatio, false, observed.Unavailable(observed.InputConsistency, observed.ReasonSimulatedUnavailable))
			default:
				put(key, unitRatio, false, observed.CompareInput(float64(pass)/float64(len(order)), s.result.pass))
			}
		}
		key := "mean_cycle_minutes." + ir.Nodes[s.first.index].ID
		sum, n, gap := 0.0, 0, false
		for _, ev := range ops {
			if !at(ev, st) || !inWindow(ev) {
				continue
			}
			if ev.end < 0 {
				gap = true
				continue
			}
			sum += float64(ev.end-ev.start) / 60000
			n++
		}
		first := ir.Nodes[s.first.index].ServiceTime
		approximate := false
		for _, r := range s.retests {
			approximate = approximate || !sameDistribution(first, ir.Nodes[r.index].ServiceTime)
		}
		m, known := 0.0, false
		if first != nil {
			m, known = mean(first.Distribution, unit)
		}
		switch {
		case n == 0 && !gap:
			put(key, unitMinute, false, observed.Unavailable(observed.InputConsistency, observed.ReasonNoObservedData))
		case gap:
			put(key, unitMinute, false, observed.Unavailable(observed.InputConsistency, observed.ReasonFieldMissing))
		case !known:
			put(key, unitMinute, false, observed.Unavailable(observed.InputConsistency, observed.ReasonSimulatedUnavailable))
		default:
			put(key, unitMinute, approximate, observed.CompareInput(sum/float64(n), m))
		}
	}
	return out, nil
}
