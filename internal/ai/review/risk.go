package review

import "github.com/kcl1104/geminus/internal/model"

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
//     buffer is smaller, or finite where it was unlimited (D-057);
//   - low: anything else (a name, a distribution, a higher capacity, a
//     larger or an unlimited buffer, less failure routing).
//
// D-016 fixes the first two lines' deletions, lower capacity, new cycles
// and higher failure probability; the rest is D-062 (d), and the cut-off
// node is D-062 (i) option B, both approved by the owner (H-019); the
// buffers are D-057 (H-018), from T-047, which lifted their gate. A
// buffer that closes a blocking cycle never gets here: the candidate is
// invalid (D-050 R1). Confidence plays no part.
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
