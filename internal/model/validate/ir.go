package validate

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kcl1104/geminus/internal/model"
)

// Result is the outcome of validating one IR document.
type Result struct {
	// IR is the decoded model; nil when parsing or schema validation failed.
	IR     *model.ModelIR
	Issues []model.ValidationIssue
}

// Valid reports whether no issue has error severity.
func (r Result) Valid() bool { return !issues(r.Issues).hasError() }

// IR validates a Model IR document. The error is reserved for internal
// failures (e.g. an embedded schema that does not compile); every problem
// with the document itself is an issue.
func IR(data []byte) (Result, error) {
	var out issues
	if len(data) > MaxIRBytes {
		out.add(LimitExceeded, "", fmt.Sprintf("document is %d bytes; the limit is %d", len(data), MaxIRBytes))
		return Result{Issues: out}, nil
	}
	if issue, ok := parse(data); !ok {
		return Result{Issues: []model.ValidationIssue{issue}}, nil
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("re-reading a parsed document: %w", err)
	}
	schemaIssues, err := Schema("model-ir.schema.json", IRSchemaInvalid, inst)
	if err != nil || len(schemaIssues) > 0 {
		return Result{Issues: schemaIssues}, err
	}
	var ir model.ModelIR
	if err := model.Decode(data, &ir); err != nil {
		// Schema and Go types are kept in parity by contract tests; reaching
		// this means they drifted, which is an internal error, not user input.
		return Result{}, fmt.Errorf("schema-valid IR does not decode: %w", err)
	}
	return Result{IR: &ir, Issues: Semantic(&ir)}, nil
}

// parse rejects malformed JSON, duplicate member names, invalid UTF-8 and
// trailing data before anything else reads the document.
func parse(data []byte) (model.ValidationIssue, bool) {
	var v any
	err := json.Unmarshal(data, &v)
	if err == nil {
		return model.ValidationIssue{}, true
	}
	path := ""
	var se *jsontext.SyntacticError
	if errors.As(err, &se) {
		path = string(se.JSONPointer)
	}
	return model.ValidationIssue{Code: JSONInvalid, Path: path, Message: err.Error(), Severity: model.SeverityError}, false
}

// Semantic runs the stages after the schema on a schema-valid IR.
func Semantic(ir *model.ModelIR) []model.ValidationIssue {
	g := newGraph(ir)
	var all issues
	for _, stage := range []func(*model.ModelIR, *graph) issues{limits, ids, refs, routing, distributions, constraints} {
		found := stage(ir, g)
		all = append(all, found...)
		if found.hasError() {
			break
		}
	}
	return Sort(all)
}

// graph indexes an IR once for the stages.
type graph struct {
	node    map[string]int // node ID -> index
	out, in [][]int        // flow indices per node index, in document order
}

func newGraph(ir *model.ModelIR) *graph {
	g := &graph{node: map[string]int{}, out: make([][]int, len(ir.Nodes)), in: make([][]int, len(ir.Nodes))}
	for i, n := range ir.Nodes {
		if _, dup := g.node[n.ID]; !dup {
			g.node[n.ID] = i
		}
	}
	for f, fl := range ir.Flows {
		if i, ok := g.node[fl.From]; ok {
			g.out[i] = append(g.out[i], f)
		}
		if i, ok := g.node[fl.To]; ok {
			g.in[i] = append(g.in[i], f)
		}
	}
	return g
}

var unitTicks = map[model.ModelTimeUnit]float64{
	model.ModelSecond: 1000, model.ModelMinute: 60_000, model.ModelHour: 3_600_000, model.ModelDay: 86_400_000,
}

func limits(ir *model.ModelIR, _ *graph) issues {
	var out issues
	container := func(v jsontext.Value, path string) {
		if len(v) == 0 {
			return
		}
		if len(v) > MaxContainerBytes {
			out.add(LimitExceeded, path, fmt.Sprintf("%d bytes; the limit is %d", len(v), MaxContainerBytes))
		}
		if d := depth(v); d > MaxContainerDepth {
			out.add(LimitExceeded, path, fmt.Sprintf("nesting depth %d; the limit is %d", d, MaxContainerDepth))
		}
	}
	container(ir.Metadata, "/metadata")
	scale := unitTicks[ir.TimeUnit]
	for i, n := range ir.Nodes {
		base := Pointer("nodes", idx(i))
		container(n.DomainAttributes, base+"/domain_attributes")
		forEachDistribution(n, func(member string, d *model.Distribution) {
			for _, p := range []struct {
				name string
				v    *float64
			}{{"value", d.Value}, {"mean", d.Mean}, {"min", d.Min}, {"mode", d.Mode}, {"max", d.Max}} {
				if p.v != nil && *p.v*scale > MaxDurationTicks {
					out.add(LimitExceeded, base+"/"+member+"/distribution/"+p.name,
						fmt.Sprintf("%g %s exceeds the 365 day duration limit", *p.v, ir.TimeUnit))
				}
			}
		})
	}
	return out
}

func depth(v jsontext.Value) int {
	max, cur := 0, 0
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return max
		}
		switch tok.Kind() {
		case '{', '[':
			cur++
			max = maxInt(max, cur)
		case '}', ']':
			cur--
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// forEachDistribution visits the arrival, service and delay distributions of n.
func forEachDistribution(n model.Node, visit func(member string, d *model.Distribution)) {
	if n.Arrival != nil {
		visit("arrival", &n.Arrival.Distribution)
	}
	if n.ServiceTime != nil {
		visit("service_time", &n.ServiceTime.Distribution)
	}
	if n.DelayTime != nil {
		visit("delay_time", &n.DelayTime.Distribution)
	}
}

func ids(ir *model.ModelIR, _ *graph) issues {
	var out issues
	seen := func(catalog string, n int, id func(int) string) map[string]bool {
		set := map[string]bool{}
		for i := range n {
			if set[id(i)] {
				out.add(DuplicateID, Pointer(catalog, idx(i), "id"), fmt.Sprintf("%q is already used in %s", id(i), catalog))
			}
			set[id(i)] = true
		}
		return set
	}
	seen("entity_types", len(ir.EntityTypes), func(i int) string { return ir.EntityTypes[i].ID })
	nodes := seen("nodes", len(ir.Nodes), func(i int) string { return ir.Nodes[i].ID })
	seen("resources", len(ir.Resources), func(i int) string { return ir.Resources[i].ID })
	seen("flows", len(ir.Flows), func(i int) string { return ir.Flows[i].ID })
	seen("metrics", len(ir.Metrics), func(i int) string { return ir.Metrics[i].ID })
	// Nodes and resources share one namespace so a selected entity ID is unambiguous (D-014).
	for i, r := range ir.Resources {
		if nodes[r.ID] {
			out.add(DuplicateID, Pointer("resources", idx(i), "id"), fmt.Sprintf("%q is also a node ID", r.ID))
		}
	}
	return out
}

func refs(ir *model.ModelIR, g *graph) issues {
	var out issues
	entity, resource := set(ir.EntityTypes, func(e model.EntityType) string { return e.ID }),
		set(ir.Resources, func(r model.Resource) string { return r.ID })
	node := func(id, path string) {
		if _, ok := g.node[id]; !ok {
			out.add(UnknownReference, path, fmt.Sprintf("no node %q", id))
		}
	}
	for i, f := range ir.Flows {
		node(f.From, Pointer("flows", idx(i), "from"))
		node(f.To, Pointer("flows", idx(i), "to"))
	}
	for i, n := range ir.Nodes {
		if n.Type == model.NodeSource && !entity[n.EntityType] {
			out.add(UnknownReference, Pointer("nodes", idx(i), "entity_type"), fmt.Sprintf("no entity type %q", n.EntityType))
		}
		for k, r := range n.Requires {
			if !resource[r.ResourceID] {
				out.add(UnknownReference, Pointer("nodes", idx(i), "requires", idx(k), "resource_id"),
					fmt.Sprintf("no resource %q", r.ResourceID))
			}
		}
	}
	for i, m := range ir.Metrics {
		base := Pointer("metrics", idx(i))
		if m.SinkID != "" {
			node(m.SinkID, base+"/sink_id")
		}
		if m.NodeID != "" {
			node(m.NodeID, base+"/node_id")
		}
		if m.ResourceID != "" && !resource[m.ResourceID] {
			out.add(UnknownReference, base+"/resource_id", fmt.Sprintf("no resource %q", m.ResourceID))
		}
	}
	for i, c := range ir.Constraints {
		node(c.NodeID, Pointer("constraints", idx(i), "node_id"))
	}
	for id := range ir.Views.TwoD.Positions {
		if _, ok := g.node[id]; !ok {
			out.add(ViewReferenceInvalid, Pointer("views", "2d", "positions", id), fmt.Sprintf("no node %q", id))
		}
	}
	return out
}

func set[T any](items []T, id func(T) string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, it := range items {
		out[id(it)] = true
	}
	return out
}

func routing(ir *model.ModelIR, g *graph) issues {
	var out issues
	upstreamQueues := make([]int, len(ir.Nodes))
	for i, n := range ir.Nodes {
		base := Pointer("nodes", idx(i))
		outs := g.out[i]
		switch n.Type {
		case model.NodeSink:
			for _, f := range outs {
				out.add(RoutingInvalid, Pointer("flows", idx(f), "from"), "a sink has no outgoing flows")
			}
		case model.NodeDecision:
			if len(outs) == 0 {
				out.add(RoutingInvalid, base, "a decision needs at least one outgoing flow")
			} else if sum := probabilitySum(ir, outs); math.Abs(sum-1) > ProbabilityEpsilon {
				out.add(RoutingInvalid, base, fmt.Sprintf("outgoing probabilities sum to %g, not 1", sum))
			}
		default:
			if len(outs) != 1 {
				out.add(RoutingInvalid, base, fmt.Sprintf("a %s needs exactly one outgoing flow, has %d", n.Type, len(outs)))
			} else if p := ir.Flows[outs[0]].Routing.Probability; math.Abs(p-1) > ProbabilityEpsilon {
				out.add(RoutingInvalid, Pointer("flows", idx(outs[0]), "routing", "probability"),
					fmt.Sprintf("the only flow out of a %s must have probability 1, has %g", n.Type, p))
			}
			if n.Type == model.NodeQueue && len(outs) == 1 {
				if t := g.node[ir.Flows[outs[0]].To]; ir.Nodes[t].Type != model.NodeProcess {
					out.add(RoutingInvalid, Pointer("flows", idx(outs[0]), "to"), "a queue must feed a process")
				} else {
					upstreamQueues[t]++
				}
			}
		}
		if n.Type == model.NodeSource {
			for _, f := range g.in[i] {
				out.add(RoutingInvalid, Pointer("flows", idx(f), "to"), "a source does not accept incoming flows")
			}
		}
	}
	for i, q := range upstreamQueues {
		if q > 1 {
			out.add(RoutingInvalid, Pointer("nodes", idx(i)), fmt.Sprintf("a process may have one upstream queue, has %d", q))
		}
	}
	out = append(out, reachability(ir, g)...)
	return out
}

func probabilitySum(ir *model.ModelIR, flows []int) float64 {
	var sum float64
	for _, f := range flows {
		sum += ir.Flows[f].Routing.Probability
	}
	return sum
}

// reachability checks that every node a source can reach (over p>0 flows) can
// still reach a sink, warns about nodes no source reaches, and rejects
// reachable cycles that take zero time (D-033).
func reachability(ir *model.ModelIR, g *graph) issues {
	var out issues
	next := func(i int) []int {
		var to []int
		for _, f := range g.out[i] {
			if ir.Flows[f].Routing.Probability > 0 {
				to = append(to, g.node[ir.Flows[f].To])
			}
		}
		return to
	}
	reached := make([]bool, len(ir.Nodes))
	var sources []int
	for i, n := range ir.Nodes {
		if n.Type == model.NodeSource {
			sources = append(sources, i)
		}
	}
	if len(sources) == 0 {
		out.add(NoReachableSink, "/nodes", "the model has no source")
		return out
	}
	stack := slices.Clone(sources)
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if reached[i] {
			continue
		}
		reached[i] = true
		stack = append(stack, next(i)...)
	}
	// Reverse search from sinks over the same p>0 edges.
	reachesSink := make([]bool, len(ir.Nodes))
	prev := make([][]int, len(ir.Nodes))
	for i := range ir.Nodes {
		for _, j := range next(i) {
			prev[j] = append(prev[j], i)
		}
	}
	for i, n := range ir.Nodes {
		if n.Type == model.NodeSink {
			stack = append(stack, i)
		}
	}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if reachesSink[i] {
			continue
		}
		reachesSink[i] = true
		stack = append(stack, prev[i]...)
	}
	for i := range ir.Nodes {
		switch {
		case reached[i] && !reachesSink[i]:
			out.add(NoReachableSink, Pointer("nodes", idx(i)), "entities reaching this node can never reach a sink")
		case !reached[i]:
			out.warn(UnreachableNode, Pointer("nodes", idx(i)), "no source reaches this node")
		}
	}
	zero := func(i int) bool { return reached[i] && zeroTime(ir.Nodes[i]) }
	for _, scc := range components(len(ir.Nodes), func(i int) []int {
		if !zero(i) {
			return nil
		}
		return slices.DeleteFunc(next(i), func(j int) bool { return !zero(j) })
	}) {
		if len(scc) > 1 || (zero(scc[0]) && slices.Contains(next(scc[0]), scc[0])) {
			out.add(ZeroTimeCycle, Pointer("nodes", idx(slices.Min(scc))),
				fmt.Sprintf("a reachable cycle of %d node(s) takes zero time", len(scc)))
		}
	}
	return out
}

// zeroTime reports whether an entity can pass n in zero ticks (D-033). Only an
// exact 0 quantizes to 0 ticks; empirical samples are checked at binding time.
func zeroTime(n model.Node) bool {
	var d *model.Distribution
	switch n.Type {
	case model.NodeProcess:
		d = &n.ServiceTime.Distribution
	case model.NodeDelay:
		d = &n.DelayTime.Distribution
	case model.NodeSource:
		return false
	default:
		return true // queue, decision, sink, failure, rework take no time themselves
	}
	switch d.Type {
	case model.DistConstant:
		return *d.Value == 0
	case model.DistTriangular:
		return *d.Min == 0
	}
	return false
}

// components returns the strongly connected components of a graph given by
// adjacency, each sorted, in a deterministic order (Tarjan).
func components(n int, adj func(int) []int) [][]int {
	index, low := make([]int, n), make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	var out [][]int
	next := 0
	var visit func(int)
	visit = func(v int) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj(v) {
			if index[w] < 0 {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var scc []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			slices.Sort(scc)
			out = append(out, scc)
		}
	}
	for v := range n {
		if index[v] < 0 {
			visit(v)
		}
	}
	return out
}

func distributions(ir *model.ModelIR, _ *graph) issues {
	var out issues
	capacity := map[string]int{}
	for _, r := range ir.Resources {
		capacity[r.ID] = r.Capacity
	}
	for i, n := range ir.Nodes {
		base := Pointer("nodes", idx(i))
		forEachDistribution(n, func(member string, d *model.Distribution) {
			path := base + "/" + member + "/distribution"
			if d.Type == model.DistTriangular && !(*d.Min <= *d.Mode && *d.Mode <= *d.Max) {
				out.add(DistributionInvalid, path, fmt.Sprintf("triangular needs min ≤ mode ≤ max, got %g, %g, %g", *d.Min, *d.Mode, *d.Max))
				return
			}
			if member == "arrival" && ((d.Type == model.DistConstant && *d.Value <= 0) ||
				(d.Type == model.DistTriangular && *d.Min <= 0)) {
				out.add(DistributionInvalid, path, "interarrival times must be strictly positive")
			}
		})
		used := map[string]bool{}
		for k, r := range n.Requires {
			path := Pointer("nodes", idx(i), "requires", idx(k))
			if used[r.ResourceID] {
				out.add(ResourceRequirementInvalid, path+"/resource_id", fmt.Sprintf("%q is required twice", r.ResourceID))
			}
			used[r.ResourceID] = true
			if r.Units > capacity[r.ResourceID] {
				out.add(ResourceRequirementInvalid, path+"/units",
					fmt.Sprintf("needs %d units of %q, capacity is %d", r.Units, r.ResourceID, capacity[r.ResourceID]))
			}
		}
	}
	return out
}

func constraints(ir *model.ModelIR, g *graph) issues {
	var out issues
	for i, c := range ir.Constraints {
		if sum := probabilitySum(ir, g.out[g.node[c.NodeID]]); math.Abs(sum-c.Equals) > ProbabilityEpsilon {
			out.add(ConstraintFailed, Pointer("constraints", idx(i)),
				fmt.Sprintf("outgoing probabilities of %q sum to %g, not %g", c.NodeID, sum, c.Equals))
		}
	}
	return out
}
