package patch

import (
	"encoding/json/v2"
	"maps"
	"slices"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// pattern is an allowlist entry over decoded tokens; "*" matches one array
// index and "+" any one member name. A path is allowed when its tokens start
// with a pattern (D-020).
type pattern []string

var allow = map[Mode][]pattern{
	ManualDraft: {{"name"}, {"nodes"}, {"flows"}, {"resources"}, {"entity_types"}, {"constraints"},
		{"metrics"}, {"calendars"}, {"views"}, {"metadata"}},
	AIEdit: {{"name"}, {"nodes"}, {"flows"}, {"resources"}, {"entity_types"}, {"constraints"}, {"metrics"}, {"calendars"}},
	Scenario: {{"resources", "*", "capacity"}, {"nodes", "*", "arrival", "distribution"},
		{"nodes", "*", "service_time", "distribution"}, {"nodes", "*", "delay_time", "distribution"},
		{"flows", "*", "routing", "probability"},
		// D-057: a queue's capacity may change, be added (unlimited to
		// finite) or be removed (back to unlimited).
		{"nodes", "*", "capacity"}},
}

// replaceOnly are the D-057 scenario parameters that take only "replace": a
// scenario changes the value of what the version has and never adds or
// removes a breakdown, changeover, calendar, schedule or batch (those go
// through draft and publish). Every distribution under them may become any
// distribution, including lognormal and Weibull (D-056).
var replaceOnly = map[Mode][]pattern{
	Scenario: {
		{"resources", "*", "breakdowns", "time_to_failure", "distribution"},
		{"resources", "*", "breakdowns", "time_to_repair", "distribution"},
		{"nodes", "*", "arrival", "schedule", "segments", "*", "rate"},
		{"nodes", "*", "arrival", "schedule", "releases", "*", "quantity"},
		{"nodes", "*", "batch", "min_size"}, {"nodes", "*", "batch", "max_size"}, {"nodes", "*", "batch", "max_wait"},
		{"resources", "*", "setups", "transitions", "*", "distribution"}, {"resources", "*", "setups", "default"},
		{"resources", "*", "calendar_id"}, // the candidate must name an existing calendar
		{"resources", "*", "shift_capacity", "+"},
		{"calendars", "*", "shifts"}, {"calendars", "*", "exceptions"},
	},
}

func (p pattern) matches(tokens []string) bool {
	if len(tokens) < len(p) {
		return false
	}
	for i, want := range p {
		switch want {
		case "*":
			if _, err := arrayIndex(tokens[i]); err != nil {
				return false
			}
		case "+":
		default:
			if tokens[i] != want {
				return false
			}
		}
	}
	return true
}

// identity is the member that identifies an element of each top-level array.
// Constraints have no id; their node_id identifies them (T-004 interpretation).
var identity = map[string]string{"nodes": "id", "flows": "id", "resources": "id", "entity_types": "id",
	"metrics": "id", "constraints": "node_id", "calendars": "id"}

// check enforces the static rules: decodable pointers, the mode allowlist and
// the test-before-modify rule. It needs no base document; apply then verifies
// every test against the actual values.
func check(ops model.Patch, mode Mode) []model.ValidationIssue {
	var out []model.ValidationIssue
	paths := make([][]string, len(ops))
	for i, op := range ops {
		tokens, err := parsePointer(op.Path)
		if err != nil {
			code := PathInvalid
			if err == errTooDeep {
				code = validate.LimitExceeded
			}
			out = append(out, issue(code, i, "path", err.Error()))
			continue
		}
		paths[i] = tokens
	}
	if len(out) > 0 {
		return out
	}
	removedNodes := removedIDs(ops, paths, "nodes")
	// tested[catalog][index] is set by "test /catalog/i/<identity>" and
	// cleared by any op that shifts that catalog's indices.
	tested := map[string]map[string]bool{}
	for i, op := range ops {
		tokens := paths[i]
		if op.Op == model.PatchTest {
			if len(tokens) == 3 && identity[tokens[0]] == tokens[2] {
				if tested[tokens[0]] == nil {
					tested[tokens[0]] = map[string]bool{}
				}
				tested[tokens[0]][tokens[1]] = true
			}
			continue // tests are not mutations and are not allowlisted (D-020)
		}
		if !allowed(mode, op, tokens, removedNodes) {
			out = append(out, issue(PathForbidden, i, "path",
				"the "+mode.String()+" allowlist does not include "+op.Path))
			continue
		}
		catalog := tokens[0]
		if _, ok := identity[catalog]; !ok {
			continue
		}
		if len(tokens) >= 2 && tokens[1] != "-" {
			if _, err := arrayIndex(tokens[1]); err != nil {
				out = append(out, issue(PathInvalid, i, "path", err.Error()))
				continue
			}
		}
		switch {
		case len(tokens) == 1: // the whole array: indices are meaningless afterwards
			delete(tested, catalog)
		case op.Op == model.PatchAdd && len(tokens) == 2: // inserting a new element
			if tokens[1] != "-" {
				delete(tested, catalog)
			}
		case tokens[1] == "-":
			out = append(out, issue(PathInvalid, i, "path", `"-" is only valid when adding an element`))
		case !tested[catalog][tokens[1]]:
			out = append(out, issue(TestRequired, i, "path", "test "+validate.Pointer(catalog, tokens[1],
				identity[catalog])+" before changing that element (D-020)"))
		case op.Op == model.PatchRemove && len(tokens) == 2:
			delete(tested, catalog)
		}
	}
	return out
}

func allowed(mode Mode, op model.PatchOp, tokens []string, removedNodes []string) bool {
	if slices.ContainsFunc(allow[mode], func(p pattern) bool { return p.matches(tokens) }) {
		return true
	}
	if op.Op == model.PatchReplace && slices.ContainsFunc(replaceOnly[mode], func(p pattern) bool { return p.matches(tokens) }) {
		return true
	}
	// D-022: an AI edit may drop the 2D position of a node the same patch deletes.
	return mode == AIEdit && op.Op == model.PatchRemove && len(tokens) == 4 &&
		tokens[0] == "views" && tokens[1] == "2d" && tokens[2] == "positions" &&
		slices.Contains(removedNodes, tokens[3])
}

// removedIDs returns the IDs of catalog elements the patch removes, as
// established by the test preceding each removal. Apply verifies the tests,
// so a patch whose claimed IDs are wrong never applies.
func removedIDs(ops model.Patch, paths [][]string, catalog string) []string {
	var out []string
	last := map[string]string{} // index -> tested id
	for i, op := range ops {
		t := paths[i]
		if len(t) == 0 || t[0] != catalog {
			continue
		}
		switch {
		case op.Op == model.PatchTest && len(t) == 3 && t[2] == identity[catalog]:
			var id string
			if json.Unmarshal(op.Value, &id) == nil {
				last[t[1]] = id
			}
		case op.Op == model.PatchRemove && len(t) == 2:
			if id, ok := last[t[1]]; ok {
				out = append(out, id)
			}
			last = map[string]string{}
		case len(t) <= 2 && op.Op != model.PatchTest:
			last = map[string]string{}
		}
	}
	return out
}

// wholeCatalog refuses an op on a whole identity catalog (e.g. "replace
// /resources") while that array still has elements: it would change every
// existing element without the id tests D-020 requires. Creating a catalog
// that is absent or empty is allowed. It runs against the document as
// patched so far.
func wholeCatalog(tree any, op model.PatchOp, i int) *model.ValidationIssue {
	tokens, _ := parsePointer(op.Path)
	if op.Op == model.PatchTest || len(tokens) != 1 {
		return nil
	}
	if _, ok := identity[tokens[0]]; !ok {
		return nil
	}
	root, _ := tree.(map[string]any)
	if current, _ := root[tokens[0]].([]any); len(current) > 0 {
		is := issue(TestRequired, i, "path", "change the elements of "+op.Path+
			" one by one, each after a test of its "+identity[tokens[0]]+" (D-020)")
		return &is
	}
	return nil
}

// afterApply checks the rules that depend on the patched document:
//   - D-022: an AI edit may drop the position of a node only if no node with
//     that id exists afterwards (a removed and re-added id keeps its position);
//   - D-021: a scenario may not bind a dataset the base model does not
//     already bind (TWIN_IR_SPEC §6).
//
// A scenario's shift labels are no rule here (D-063 (b) option B, H-019;
// T-049): it may add, drop or rename them, and a label that shift_capacity
// refers to and the candidate no longer has is the candidate validation's
// CALENDAR_INVALID (D-052). Phase 2 kept each calendar's set of labels.
func afterApply(tree any, baseRefs map[string]bool, ops model.Patch, mode Mode) []model.ValidationIssue {
	var out []model.ValidationIssue
	switch mode {
	case AIEdit:
		ids := nodeIDs(tree)
		for i, op := range ops {
			t, _ := parsePointer(op.Path)
			if op.Op == model.PatchRemove && len(t) == 4 && t[0] == "views" && ids[t[3]] {
				out = append(out, issue(PathForbidden, i, "path",
					"node "+t[3]+" still exists after this patch, so its position stays (D-022)"))
			}
		}
	case Scenario:
		after := samplesRefs(tree)
		for i, op := range ops {
			for _, ref := range opRefs(op) {
				if after[ref] && !baseRefs[ref] {
					out = append(out, issue(PathForbidden, i, "value",
						"a scenario cannot bind a dataset the model does not bind (D-021)"))
					break
				}
			}
		}
	}
	return out
}

func nodeIDs(tree any) map[string]bool {
	out := map[string]bool{}
	root, _ := tree.(map[string]any)
	nodes, _ := root["nodes"].([]any)
	for _, n := range nodes {
		if m, ok := n.(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				out[id] = true
			}
		}
	}
	return out
}

// samplesRefs returns every empirical samples_ref the nodes and resources of
// tree use (resources hold distributions since ADR-026: breakdowns, setups).
func samplesRefs(tree any) map[string]bool {
	out := map[string]bool{}
	root, _ := tree.(map[string]any)
	for _, catalog := range []string{"nodes", "resources"} {
		items, _ := root[catalog].([]any)
		for _, n := range items {
			collectRefs(n, out)
		}
	}
	return out
}

func collectRefs(v any, out map[string]bool) {
	switch v := v.(type) {
	case map[string]any:
		for k, c := range v {
			if s, ok := c.(string); ok && k == "samples_ref" {
				out[s] = true
			}
			collectRefs(c, out)
		}
	case []any:
		for _, c := range v {
			collectRefs(c, out)
		}
	}
}

// opRefs returns the samples_refs an op writes: its value when the path ends
// at samples_ref, otherwise any samples_ref members inside its value.
func opRefs(op model.PatchOp) []string {
	if op.Op != model.PatchAdd && op.Op != model.PatchReplace {
		return nil
	}
	var value any
	if json.Unmarshal(op.Value, &value) != nil {
		return nil
	}
	if t, _ := parsePointer(op.Path); len(t) > 0 && t[len(t)-1] == "samples_ref" {
		if s, ok := value.(string); ok {
			return []string{s}
		}
		return nil
	}
	refs := map[string]bool{}
	collectRefs(value, refs)
	return slices.Sorted(maps.Keys(refs))
}
