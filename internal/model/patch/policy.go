package patch

import (
	"encoding/json/v2"
	"slices"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// pattern is an allowlist entry over decoded tokens; "*" matches one array
// index. A path is allowed when its tokens start with a pattern (D-020).
type pattern []string

var allow = map[Mode][]pattern{
	ManualDraft: {{"name"}, {"nodes"}, {"flows"}, {"resources"}, {"entity_types"}, {"constraints"},
		{"metrics"}, {"views"}, {"metadata"}},
	AIEdit: {{"name"}, {"nodes"}, {"flows"}, {"resources"}, {"entity_types"}, {"constraints"}, {"metrics"}},
	Scenario: {{"resources", "*", "capacity"}, {"nodes", "*", "arrival", "distribution"},
		{"nodes", "*", "service_time", "distribution"}, {"nodes", "*", "delay_time", "distribution"},
		{"flows", "*", "routing", "probability"}},
}

func (p pattern) matches(tokens []string) bool {
	if len(tokens) < len(p) {
		return false
	}
	for i, want := range p {
		if want == "*" {
			if _, err := arrayIndex(tokens[i]); err != nil {
				return false
			}
		} else if tokens[i] != want {
			return false
		}
	}
	return true
}

// identity is the member that identifies an element of each top-level array.
// Constraints have no id; their node_id identifies them (T-004 interpretation).
var identity = map[string]string{"nodes": "id", "flows": "id", "resources": "id", "entity_types": "id",
	"metrics": "id", "constraints": "node_id"}

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
