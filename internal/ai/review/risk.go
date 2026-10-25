package review

import (
	"maps"
	"slices"

	"github.com/kcl1104/geminus/internal/model"
)

// Risk is a proposal's risk level (common.schema.json#/$defs/RiskLevel).
type Risk string

const (
	Low    Risk = "low"
	Medium Risk = "medium"
	High   Risk = "high"
)

var rank = map[Risk]int{Low: 1, Medium: 2, High: 3}

func (r Risk) valid() bool { return rank[r] > 0 }

// Max is the higher of two levels: the effective risk of a proposal is the
// server's and the provider's claim together, so a claim can raise the
// risk and never lower it (D-027).
func Max(a, b Risk) Risk {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// ServerRisk is the risk of changing base into candidate, computed from the
// two models by entity ID, never from what the patch or the provider says
// it does (D-016; AI_SPEC §5). The rules are domain-neutral:
//
//   - high: a node or a resource of the base is gone, or a node changed its
//     type (the node as it was is gone), or a node some source reached in
//     the base is still there but no source reaches it (a re-targeted or
//     removed flow cut it off, which is as good as its deletion);
//   - at least medium: a resource's capacity is lower; a process needs
//     another quantity (more or fewer units) of a resource, or another
//     bundle of resources; a flow was re-targeted; a routing probability
//     rose on a flow into a failure or rework node, or on a flow that lies
//     on a cycle (rework, retest); the candidate has a cycle the base does
//     not; an entity (node, flow, resource, entity type, metric) was added
//     or removed; a constraint was added, removed or changed; a queue's
//     buffer is smaller, or finite where it was unlimited (D-057); a
//     resource's breakdowns were added or removed, or its time to failure
//     or to repair changed, either way (D-057, T-048); a calendar was added
//     or removed or its period, shifts (their times and labels) or
//     exceptions changed, a resource is on another calendar or on none, or
//     its units on shift (shift_capacity) changed; a source's release
//     schedule was added or removed, or anything of it changed (a rate, a
//     quantity, its period, a segment or a release), either way (D-057,
//     T-049);
//   - low: anything else (a name, an arrival, service or delay
//     distribution, lognormal and Weibull included, a higher capacity, a
//     larger or an unlimited buffer, less failure routing).
//
// D-016 fixes the first two lines' deletions, lower capacity, new cycles
// and higher failure probability; the rest is D-062 (d), and the cut-off
// node is D-062 (i) option B, both approved by the owner (H-019); the
// buffers are D-057 (H-018), from T-047, which lifted their gate, and the
// breakdowns D-057 too, from T-048. A buffer that closes a blocking cycle,
// or a bundle R3 refuses, never gets here: the candidate is invalid (D-050
// R1, R3); the calendars and schedules D-057 lists, from T-049, which
// lifted their gates: a candidate whose bundle never fits (R2) or whose
// breakdown resource is on shift without its partners (R4) is invalid.
// Confidence plays no part.
func ServerRisk(base, candidate *model.ModelIR) Risk {
	level := Low
	raise := func(r Risk) { level = Max(level, r) }

	baseNodes, candNodes := byID(base.Nodes, func(n model.Node) string { return n.ID }), byID(candidate.Nodes, func(n model.Node) string { return n.ID })
	for id, n := range baseNodes {
		c, ok := candNodes[id]
		switch {
		case !ok:
			raise(High)
		case c.Type != n.Type:
			raise(High)
		case !sameRequirements(n.Requires, c.Requires):
			raise(Medium)
		}
		// D-057: a smaller buffer, or a finite one where the base had none
		// (capacity 0 is unlimited); a larger or an unlimited one is low.
		if ok && c.Type == n.Type && c.Capacity != 0 && (n.Capacity == 0 || c.Capacity < n.Capacity) {
			raise(Medium)
		}
		// D-057 (T-049): any change to a release schedule, either way (a
		// higher rate is as much an assumption as a lower one).
		if ok && c.Type == n.Type && !sameSchedule(arrivalSchedule(n), arrivalSchedule(c)) {
			raise(Medium)
		}
	}
	baseRes, candRes := byID(base.Resources, func(r model.Resource) string { return r.ID }), byID(candidate.Resources, func(r model.Resource) string { return r.ID })
	for id, r := range baseRes {
		c, ok := candRes[id]
		switch {
		case !ok:
			raise(High)
		case c.Capacity < r.Capacity:
			raise(Medium)
		}
		// D-057 (T-048): breakdowns added or removed, or another time to
		// failure or to repair, in either direction (a faster repair is
		// as much an assumption about the line as a slower one).
		if ok && !sameBreakdowns(r.Breakdowns, c.Breakdowns) {
			raise(Medium)
		}
		// D-057 (T-049): another calendar, or none, or other units on shift.
		if ok && (c.CalendarID != r.CalendarID || !sameShiftCapacity(r.ShiftCapacity, c.ShiftCapacity)) {
			raise(Medium)
		}
	}
	// D-057 (T-049): a calendar added, removed or changed (period, shifts
	// with their labels, exceptions); its name is presentation.
	baseCals, candCals := byID(base.Calendars, func(c model.Calendar) string { return c.ID }), byID(candidate.Calendars, func(c model.Calendar) string { return c.ID })
	if added(baseCals, candCals) || added(candCals, baseCals) {
		raise(Medium)
	}
	for id, c := range baseCals {
		if d, ok := candCals[id]; ok && !sameCalendar(c, d) {
			raise(Medium)
		}
	}
	if added(baseNodes, candNodes) || added(baseRes, candRes) {
		raise(Medium)
	}
	candReached := reached(candidate)
	for id := range reached(base) {
		if _, ok := candNodes[id]; ok && !candReached[id] {
			raise(High)
		}
	}

	baseFlows, candFlows := byID(base.Flows, func(f model.Flow) string { return f.ID }), byID(candidate.Flows, func(f model.Flow) string { return f.ID })
	if added(baseFlows, candFlows) || added(candFlows, baseFlows) {
		raise(Medium)
	}
	onCycle := cycleFlows(candidate)
	for id, f := range baseFlows {
		c, ok := candFlows[id]
		if !ok {
			continue
		}
		if c.From != f.From || c.To != f.To {
			raise(Medium)
			continue
		}
		if c.Routing.Probability > f.Routing.Probability {
			if t, ok := candNodes[c.To]; ok && (t.Type == model.NodeFailure || t.Type == model.NodeRework) || onCycle[id] {
				raise(Medium)
			}
		}
	}
	baseCycles := cycleEdges(base)
	for e := range cycleEdges(candidate) {
		if !baseCycles[e] {
			raise(Medium)
		}
	}

	if added(byID(base.EntityTypes, func(e model.EntityType) string { return e.ID }), byID(candidate.EntityTypes, func(e model.EntityType) string { return e.ID })) ||
		added(byID(candidate.EntityTypes, func(e model.EntityType) string { return e.ID }), byID(base.EntityTypes, func(e model.EntityType) string { return e.ID })) ||
		added(byID(base.Metrics, func(m model.Metric) string { return m.ID }), byID(candidate.Metrics, func(m model.Metric) string { return m.ID })) ||
		added(byID(candidate.Metrics, func(m model.Metric) string { return m.ID }), byID(base.Metrics, func(m model.Metric) string { return m.ID })) ||
		!sameConstraints(base.Constraints, candidate.Constraints) {
		raise(Medium)
	}
	return level
}

func byID[T any](items []T, id func(T) string) map[string]T {
	out := make(map[string]T, len(items))
	for _, it := range items {
		out[id(it)] = it
	}
	return out
}

// added reports whether to has a key from does not.
func added[T any](from, to map[string]T) bool {
	for id := range to {
		if _, ok := from[id]; !ok {
			return true
		}
	}
	return false
}

// sameRequirements reports whether a process needs the same resources, the
// same units of each: fewer units are a change too (D-062 (d): the
// resource or the quantity a process requires changing; H-003 review L1).
func sameRequirements(base, cand []model.ResourceRequirement) bool {
	units := map[string]int{}
	for _, r := range base {
		units[r.ResourceID] += r.Units
	}
	seen := map[string]int{}
	for _, r := range cand {
		seen[r.ResourceID] += r.Units
	}
	if len(units) != len(seen) {
		return false
	}
	for id, n := range seen {
		b, ok := units[id]
		if !ok || n != b {
			return false
		}
	}
	return true
}

// sameBreakdowns reports whether a resource has breakdowns in both or in
// neither, with the same distributions.
func sameBreakdowns(a, b *model.Breakdowns) bool {
	if a == nil || b == nil {
		return a == b
	}
	return sameDistribution(a.TimeToFailure.Distribution, b.TimeToFailure.Distribution) &&
		sameDistribution(a.TimeToRepair.Distribution, b.TimeToRepair.Distribution)
}

// arrivalSchedule is a node's release schedule, nil without one.
func arrivalSchedule(n model.Node) *model.ReleaseSchedule {
	if n.Arrival == nil {
		return nil
	}
	return n.Arrival.Schedule
}

// sameSchedule reports whether two release schedules are both absent or
// equal in every member.
func sameSchedule(a, b *model.ReleaseSchedule) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Process == b.Process && a.Period == b.Period && slices.Equal(a.Segments, b.Segments) && slices.Equal(a.Releases, b.Releases)
}

// sameShiftCapacity compares two shift_capacity maps (nil and empty alike).
func sameShiftCapacity(a, b map[string]int) bool { return maps.Equal(a, b) }

// sameCalendar compares two calendars' period, shifts (labels included)
// and exceptions; the name is not compared.
func sameCalendar(a, b model.Calendar) bool {
	return a.Period == b.Period && slices.Equal(a.Shifts, b.Shifts) && slices.Equal(a.Exceptions, b.Exceptions)
}

// sameDistribution compares two distributions by value.
func sameDistribution(a, b model.Distribution) bool {
	same := func(x, y *float64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.Type == b.Type && a.SamplesRef == b.SamplesRef && same(a.Value, b.Value) && same(a.Mean, b.Mean) &&
		same(a.Min, b.Min) && same(a.Mode, b.Mode) && same(a.Max, b.Max) && same(a.CV, b.CV) && same(a.Shape, b.Shape) && same(a.Scale, b.Scale)
}

func sameConstraints(a, b []model.Constraint) bool {
	set := func(cs []model.Constraint) map[model.Constraint]int {
		out := map[model.Constraint]int{}
		for _, c := range cs {
			out[c]++
		}
		return out
	}
	x, y := set(a), set(b)
	if len(x) != len(y) {
		return false
	}
	for c, n := range x {
		if y[c] != n {
			return false
		}
	}
	return true
}

// reached are the IDs of ir's nodes some source reaches over flows with a
// positive probability, the sources included: the nodes the validator does
// not warn about with UNREACHABLE_NODE (TWIN_IR_SPEC §4).
func reached(ir *model.ModelIR) map[string]bool {
	next := map[string][]string{}
	for _, f := range ir.Flows {
		if f.Routing.Probability > 0 {
			next[f.From] = append(next[f.From], f.To)
		}
	}
	out := map[string]bool{}
	var stack []string
	for _, n := range ir.Nodes {
		if n.Type == model.NodeSource {
			stack = append(stack, n.ID)
		}
	}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[v] {
			continue
		}
		out[v] = true
		stack = append(stack, next[v]...)
	}
	return out
}

type edge struct{ from, to string }

// components returns the strongly connected component of each node of ir
// (Tarjan's algorithm, iterative order over the IR's own arrays, so the
// result never depends on map iteration).
func components(ir *model.ModelIR) map[string]int {
	next := map[string][]string{}
	for _, f := range ir.Flows {
		next[f.From] = append(next[f.From], f.To)
	}
	index, low, comp := map[string]int{}, map[string]int{}, map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	counter, comps := 0, 0
	var visit func(v string)
	visit = func(v string) {
		index[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range next[v] {
			if _, seen := index[w]; !seen {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp[w] = comps
				if w == v {
					break
				}
			}
			comps++
		}
	}
	for _, n := range ir.Nodes {
		if _, seen := index[n.ID]; !seen {
			visit(n.ID)
		}
	}
	return comp
}

// cycleFlows are the IDs of ir's flows that lie on a cycle: both ends in
// one strongly connected component (a flow to itself included).
func cycleFlows(ir *model.ModelIR) map[string]bool {
	comp := components(ir)
	out := map[string]bool{}
	for _, f := range ir.Flows {
		cf, okf := comp[f.From]
		ct, okt := comp[f.To]
		if okf && okt && cf == ct {
			out[f.ID] = true
		}
	}
	return out
}

// cycleEdges are the node pairs of ir's flows that lie on a cycle.
func cycleEdges(ir *model.ModelIR) map[edge]bool {
	ids := cycleFlows(ir)
	out := map[edge]bool{}
	for _, f := range ir.Flows {
		if ids[f.ID] {
			out[edge{f.From, f.To}] = true
		}
	}
	return out
}
