package electronicsline

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strconv"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// stationKind is one value of a station process's `station` attribute
// (ELECTRONICS_LINE §3.3) and the role of its primary resource (§3.2,
// §3.4 rule 1). A test station's first test has a test result decision,
// so it has a first-pass yield (§5).
type stationKind struct {
	name, role string
	test       bool
}

// stationKinds are the main-line stations of §2, in line order.
var stationKinds = []stationKind{
	{"smt", "smt_line", false},
	{"aoi", "aoi", true},
	{"ict", "ict_tester", true},
	{"fct", "fct_tester", true},
	{"burn_in", "burn_in_chamber", true},
	{"pack", "pack_station", false},
}

func kindNamed(name string) *stationKind {
	for i := range stationKinds {
		if stationKinds[i].name == name {
			return &stationKinds[i]
		}
	}
	return nil
}

// The attribute values of §3.3.
const (
	passFirst  = "first"
	passRetest = "retest"
)

// stationNode is a process whose attributes name a station and a pass.
type stationNode struct {
	index   int
	kind    *stationKind
	retest  bool
	primary int // index of the one resource of the station's role; -1 when there is not exactly one
	matches int // how many of its requirements have the station's role
}

// resultNode is a decision whose result_of names a station process that
// routes to it: the test result of that process (§3.3).
type resultNode struct {
	index, of int // node indexes of the decision and the tested process
	fails     int // flows to failure nodes; exactly one for a usable result (rule 2)
	pass      float64
}

// station is a first test and the retests that share its primary
// resource: the unit the station metrics of §5 describe.
type station struct {
	first   *stationNode
	retests []*stationNode
	result  *resultNode // the first test's result, if it has one
}

// line is the pack's reading of an IR: its stations and test results,
// and the warnings about attributes it cannot use. It never changes ir.
type line struct {
	ir       *model.ModelIR
	nodes    map[string]int // first index of each node ID
	res      map[string]int // first index of each resource ID
	nodesAt  []*stationNode // station processes, in node order
	results  []*resultNode  // test results, in node order
	stations []*station     // first tests, in node order
	warnings []model.ValidationIssue
}

func attributePath(i int, member string) string {
	return validate.Pointer("nodes", strconv.Itoa(i), "domain_attributes", member)
}

// stringAttribute decodes one attribute: present tells whether the member
// exists, ok whether it is a JSON string.
func stringAttribute(attrs map[string]jsontext.Value, name string) (value string, present, ok bool) {
	raw, present := attrs[name]
	if !present {
		return "", false, false
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, false
	}
	return value, true, true
}

func read(ir *model.ModelIR) *line {
	l := &line{ir: ir, nodes: make(map[string]int, len(ir.Nodes)), res: make(map[string]int, len(ir.Resources))}
	for i, n := range ir.Nodes {
		if _, dup := l.nodes[n.ID]; !dup {
			l.nodes[n.ID] = i
		}
	}
	for i, r := range ir.Resources {
		if _, dup := l.res[r.ID]; !dup {
			l.res[r.ID] = i
		}
	}
	warn := func(i int, member, message string) {
		l.warnings = append(l.warnings, model.NewIssue(DomainAttributesInvalid, attributePath(i, member), message, model.SeverityWarning))
	}
	type pending struct{ index, of int }
	var results []pending
	for i := range ir.Nodes {
		n := &ir.Nodes[i]
		if len(n.DomainAttributes) == 0 {
			continue
		}
		var attrs map[string]jsontext.Value
		if err := json.Unmarshal(n.DomainAttributes, &attrs); err != nil {
			l.warnings = append(l.warnings, model.NewIssue(DomainAttributesInvalid, validate.Pointer("nodes", strconv.Itoa(i), "domain_attributes"),
				"the domain attributes are not a JSON object the pack can read", model.SeverityWarning))
			continue
		}
		if name, present, ok := stringAttribute(attrs, "station"); present {
			kind := kindNamed(name)
			switch {
			case n.Type != model.NodeProcess:
				warn(i, "station", "a station belongs on a process step; the pack ignores it here")
			case !ok || kind == nil:
				warn(i, "station", "the station is not one of smt, aoi, ict, fct, burn_in, pack; the step is not a station")
			default:
				pass, present, ok := stringAttribute(attrs, "pass")
				if !present || !ok || (pass != passFirst && pass != passRetest) {
					warn(i, "pass", "a station step needs pass first or retest; the step is not a station")
					break
				}
				l.nodesAt = append(l.nodesAt, l.stationNode(i, kind, pass == passRetest))
			}
		}
		if of, present, ok := stringAttribute(attrs, "result_of"); present {
			target, known := l.nodes[of]
			switch {
			case n.Type != model.NodeDecision:
				warn(i, "result_of", "result_of belongs on a decision; the pack ignores it here")
			case !ok || !known:
				warn(i, "result_of", "result_of does not name a step of the model")
			default:
				results = append(results, pending{i, target})
			}
		}
	}
	// A test result names a station process that routes to it.
	for _, p := range results {
		if l.station(p.of) == nil || !l.routes(p.of, p.index) {
			warn(p.index, "result_of", "result_of names no station step that routes to this decision")
			continue
		}
		r := &resultNode{index: p.index, of: p.of}
		for _, f := range ir.Flows {
			if f.From != ir.Nodes[p.index].ID {
				continue
			}
			if to, ok := l.nodes[f.To]; ok && ir.Nodes[to].Type == model.NodeFailure {
				r.fails++
			} else {
				r.pass += f.Routing.Probability
			}
		}
		l.results = append(l.results, r)
	}
	// A station is a first test and the retests of its kind that share its
	// primary resource; a retest goes with the first such first test.
	for _, s := range l.nodesAt {
		if !s.retest {
			l.stations = append(l.stations, &station{first: s, result: l.resultOf(s.index)})
		}
	}
	for _, s := range l.nodesAt {
		if !s.retest || s.primary < 0 {
			continue
		}
		for _, st := range l.stations {
			if st.first.kind == s.kind && st.first.primary == s.primary {
				st.retests = append(st.retests, s)
				break
			}
		}
	}
	return l
}

func (l *line) stationNode(i int, kind *stationKind, retest bool) *stationNode {
	s := &stationNode{index: i, kind: kind, retest: retest, primary: -1}
	for _, req := range l.ir.Nodes[i].Requires {
		if r, ok := l.res[req.ResourceID]; ok && l.ir.Resources[r].Role == kind.role {
			s.matches++
			s.primary = r
		}
	}
	if s.matches != 1 {
		s.primary = -1
	}
	return s
}

// station is the station process at node index i, or nil.
func (l *line) station(i int) *stationNode {
	for _, s := range l.nodesAt {
		if s.index == i {
			return s
		}
	}
	return nil
}

// routes reports whether node from has a flow to node to.
func (l *line) routes(from, to int) bool {
	for _, f := range l.ir.Flows {
		if f.From == l.ir.Nodes[from].ID && f.To == l.ir.Nodes[to].ID {
			return true
		}
	}
	return false
}

func (l *line) resultOf(i int) *resultNode {
	for _, r := range l.results {
		if r.of == i {
			return r
		}
	}
	return nil
}

// processes are the node indexes of a station's steps: its first test,
// then its retests.
func (s *station) processes() []int {
	out := []int{s.first.index}
	for _, r := range s.retests {
		out = append(out, r.index)
	}
	return out
}
