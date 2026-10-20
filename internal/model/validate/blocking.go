package validate

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kcl1104/geminus/internal/model"
)

// blockingCycles is D-050 R1 (ADR-026; TWIN_IR_SPEC §4, graph stage):
// BLOCKING_CYCLE at the capacity of every finite queue on a cycle of
// blocking edges between shared-resource components.
//
// A process that needs at least one resource (a holder) keeps its whole
// bundle while one of its entities is blocked (D-049); a source, a delay or
// a process without resources holds nothing. A holder A has a blocking
// edge to process P when A's route can reach P's finite buffer: over flows
// with p > 0, through decisions, failure and rework nodes, then into P
// directly or through the queue that names P's buffer; a delay, a sink or
// an unlimited buffer ends the route. Processes needing a common resource
// form one component (union-find; an entity blocked holding a unit of the
// component can stop every request of the component). A cycle of the edges
// between components, a component's edge to itself included, can leave
// every entity on it waiting for good, so it is refused, whether a source
// reaches it or not: the rule is the approved one, a conservative check
// that needs no simulation (D-050).
//
// The check is a strongly connected component search over a graph whose
// vertices are the route-through nodes, the queues and one vertex per
// component; its edges are the p > 0 flows out of each component's
// holders and out of the route-through nodes, and from each finite queue
// to its process's component. An edge into a component is a blocking edge,
// labeled with the finite queue of the process it reaches; it lies on a
// cycle when both ends are in one strongly connected component.
func blockingCycles(ir *model.ModelIR, g *graph) issues {
	n := len(ir.Nodes)
	// finite[p]: the queue node with a capacity that names process p's
	// buffer, or -1.
	finite := make([]int, n)
	for i := range finite {
		finite[i] = -1
	}
	any := false
	for q, node := range ir.Nodes {
		if node.Type != model.NodeQueue || node.Capacity == 0 || len(g.out[q]) != 1 {
			continue
		}
		f := ir.Flows[g.out[q][0]]
		if p, ok := g.node[f.To]; ok && ir.Nodes[p].Type == model.NodeProcess && f.Routing.Probability > 0 {
			finite[p], any = q, true
		}
	}
	if !any {
		return nil // no finite buffer, nothing to check (D-047)
	}

	// Components of processes that need a common resource, by node index;
	// the representative is the member with the smallest ID.
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if ir.Nodes[rb].ID < ir.Nodes[ra].ID {
			ra, rb = rb, ra
		}
		parent[rb] = ra
	}
	firstUser := map[string]int{} // resource ID -> the first process needing it
	for i, node := range ir.Nodes {
		if node.Type != model.NodeProcess {
			continue
		}
		for _, r := range node.Requires {
			if j, ok := firstUser[r.ResourceID]; ok {
				union(i, j)
			} else {
				firstUser[r.ResourceID] = i
			}
		}
	}

	routeThrough := func(i int) bool {
		switch ir.Nodes[i].Type {
		case model.NodeDecision, model.NodeFailure, model.NodeRework:
			return true
		}
		return false
	}
	// target is the vertex a flow into node j leads to, and the label of a
	// blocking edge (the finite queue), or -1 for none.
	target := func(j int) (int, int) {
		switch {
		case routeThrough(j):
			return j, -1
		case ir.Nodes[j].Type == model.NodeQueue:
			return j, -1
		case ir.Nodes[j].Type == model.NodeProcess && finite[j] >= 0:
			return find(j), finite[j]
		}
		return -1, -1 // a delay, a sink, a source or an unlimited buffer
	}
	type edge struct{ to, label int }
	adj := make([][]edge, n)
	addFlows := func(from, node int) {
		for _, f := range g.out[node] {
			fl := ir.Flows[f]
			j, ok := g.node[fl.To]
			if !ok || fl.Routing.Probability <= 0 {
				continue
			}
			if v, label := target(j); v >= 0 {
				adj[from] = append(adj[from], edge{v, label})
			}
		}
	}
	for i, node := range ir.Nodes {
		switch {
		case node.Type == model.NodeProcess && len(node.Requires) > 0:
			addFlows(find(i), i) // a holder's edges are its component's
		case routeThrough(i):
			addFlows(i, i)
		case node.Type == model.NodeQueue && node.Capacity > 0 && len(g.out[i]) == 1:
			if p, ok := g.node[ir.Flows[g.out[i][0]].To]; ok && finite[p] == i {
				adj[i] = append(adj[i], edge{find(p), i})
			}
		}
	}

	scc := make([]int, n)
	for k, c := range components(n, func(v int) []int {
		out := make([]int, len(adj[v]))
		for x, e := range adj[v] {
			out[x] = e.to
		}
		return out
	}) {
		for _, v := range c {
			scc[v] = k
		}
	}
	onCycle := map[int]int{} // finite queue -> the strongly connected component of its cycle
	for v := range adj {
		for _, e := range adj[v] {
			if e.label >= 0 && scc[v] == scc[e.to] {
				onCycle[e.label] = scc[v]
			}
		}
	}
	if len(onCycle) == 0 {
		return nil
	}

	var out issues
	queues := slices.Sorted(func(yield func(int) bool) {
		for q := range onCycle {
			if !yield(q) {
				return
			}
		}
	})
	for _, q := range queues {
		k := onCycle[q]
		var procs, buffers []string
		used := map[string]int{}
		for i, node := range ir.Nodes {
			if node.Type == model.NodeProcess && scc[find(i)] == k {
				procs = append(procs, node.ID)
				for _, r := range node.Requires {
					used[r.ResourceID]++
				}
			}
			if node.Type == model.NodeQueue && onCycle[i] == k && slices.Contains(queues, i) {
				buffers = append(buffers, node.ID)
			}
		}
		var shared []string
		for r, c := range used {
			if c > 1 {
				shared = append(shared, r)
			}
		}
		slices.Sort(procs)
		slices.Sort(buffers)
		slices.Sort(shared)
		msg := fmt.Sprintf("the full buffer %s can stop a cycle of blocked work for good: finite buffers %s, processes %s",
			ir.Nodes[q].ID, list(buffers), list(procs))
		if len(shared) > 0 {
			msg += ", which share the resources " + list(shared)
		}
		msg += "; make at least one buffer on the cycle unlimited, or do not let a blocked process share a resource with the process that blocks it"
		out.add(BlockingCycle, Pointer("nodes", idx(q), "capacity"), msg)
	}
	return out
}

// list joins IDs for a message, at most 20 of them.
func list(ids []string) string {
	const most = 20
	if len(ids) <= most {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:most], ", ") + " and " + strconv.Itoa(len(ids)-most) + " more"
}
