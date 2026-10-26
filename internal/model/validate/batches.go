package validate

import (
	"fmt"
	"strconv"

	"github.com/kcl1104/geminus/internal/model"
)

// The rules of batch processes (ADR-026 D-054) and changeovers (D-055),
// T-050, in the distribution and domain semantics stage; the entity types
// a resource's setups name are checked with the other references
// (setupRefs). The details the approved text leaves open are D-096 (i)
// and D-097 (g), PROPOSED.

// batchRules is D-054's BATCH_INVALID: min_size <= max_size (the schema
// keeps both in 1..10000); a batch that can start below max_size has a
// wait limit; the wait limit is a duration of at most 365 days once
// quantized with model.Ticks (D-096 (i), like a calendar's period); and a
// finite buffer holds a whole batch (capacity >= max_size), or a batch of
// max_size could never form and the buffer would block for good.
func batchRules(ir *model.ModelIR, g *graph, out *issues) {
	capacity := map[string]int{} // process ID -> the capacity of its explicit queue
	for _, n := range ir.Nodes {
		if n.Type != model.NodeQueue || n.Capacity == 0 {
			continue
		}
		// The routing stage has checked that a queue has one flow out, of
		// probability 1, into a process.
		for _, f := range g.out[g.node[n.ID]] {
			capacity[ir.Flows[f].To] = n.Capacity
		}
	}
	unit := model.TimeUnit(ir.TimeUnit)
	for i, n := range ir.Nodes {
		b := n.Batch
		if b == nil {
			continue
		}
		base := Pointer("nodes", idx(i), "batch")
		switch {
		case b.MinSize > b.MaxSize:
			out.add(BatchInvalid, base, fmt.Sprintf("a batch's min_size (%d) must be at most its max_size (%d)", b.MinSize, b.MaxSize))
		case b.MinSize < b.MaxSize && b.MaxWait == nil:
			out.add(BatchInvalid, base, fmt.Sprintf("a batch that may start with fewer than max_size (%d) members needs max_wait: "+
				"how long its oldest member waits before a batch of at least min_size (%d) starts", b.MaxSize, b.MinSize))
		}
		if b.MaxWait != nil {
			if _, err := model.Ticks(*b.MaxWait, unit); err != nil {
				out.add(BatchInvalid, base+"/max_wait", "a batch's max_wait must be at most 365 days")
			}
		}
		if c := capacity[n.ID]; c > 0 && c < b.MaxSize {
			out.add(BatchInvalid, base+"/max_size", fmt.Sprintf("the batch's max_size (%d) is larger than its buffer's capacity (%d): "+
				"a full batch could never form; raise the capacity or lower max_size", b.MaxSize, c))
		}
	}
}

// setupRefs checks that setups name entity types of the model (D-055,
// UNKNOWN_REFERENCE, the reference stage): its initial type and every
// transition's from and to.
func setupRefs(ir *model.ModelIR, entity map[string]bool, out *issues) {
	for r, res := range ir.Resources {
		s := res.Setups
		if s == nil {
			continue
		}
		base := Pointer("resources", idx(r), "setups")
		if s.Initial != "" && !entity[s.Initial] {
			out.add(UnknownReference, base+"/initial", fmt.Sprintf("no entity type %q", s.Initial))
		}
		for k, tr := range s.Transitions {
			for _, end := range []struct{ member, id string }{{"from", tr.From}, {"to", tr.To}} {
				if !entity[end.id] {
					out.add(UnknownReference, base+"/transitions/"+idx(k)+"/"+end.member, fmt.Sprintf("no entity type %q", end.id))
				}
			}
		}
	}
}

// setupRules is D-055's SETUP_INVALID: a transition between two different
// entity types, each pair at most once (the later one is reported); and
// D-055's P0 limits, at the requirement that breaks them (D-097 (g)): a
// process needs one unit of a resource with changeovers, at most one such
// resource (each later one in the bundle is reported), and none at all in
// a batch process, whose batch may mix entity types. The changeover
// distributions are resource distributions, checked with the others
// (DISTRIBUTION_INVALID, LIMIT_EXCEEDED, the dataset binding stage).
func setupRules(ir *model.ModelIR, out *issues) {
	changes := map[string]bool{}
	for r, res := range ir.Resources {
		s := res.Setups
		if s == nil {
			continue
		}
		changes[res.ID] = true
		seen := map[[2]string]bool{}
		for k, tr := range s.Transitions {
			path := Pointer("resources", idx(r), "setups", "transitions", idx(k))
			pair := [2]string{tr.From, tr.To}
			switch {
			case tr.From == tr.To:
				out.add(SetupInvalid, path, fmt.Sprintf("a transition's from and to must differ: a unit set up for %q never changes over to it", tr.From))
			case seen[pair]:
				out.add(SetupInvalid, path, fmt.Sprintf("the transition from %q to %q is listed twice", tr.From, tr.To))
			}
			seen[pair] = true
		}
	}
	for i, n := range ir.Nodes {
		first := ""
		for k, q := range n.Requires {
			if !changes[q.ResourceID] {
				continue
			}
			path := Pointer("nodes", idx(i), "requires", idx(k))
			switch {
			case n.Batch != nil:
				out.add(SetupInvalid, path, fmt.Sprintf("%q has changeovers, which a batch process cannot need (D-055): one batch may hold several entity types", q.ResourceID))
			case q.Units != 1:
				out.add(SetupInvalid, path, fmt.Sprintf("%q has changeovers, so a process needs one unit of it at a time (D-055), not %d", q.ResourceID, q.Units))
			case first != "":
				out.add(SetupInvalid, path, fmt.Sprintf("a process may need at most one resource with changeovers (D-055); it already needs %s", strconv.Quote(first)))
			}
			if first == "" {
				first = q.ResourceID
			}
		}
	}
}
