package patch

import (
	"encoding/json/v2"
	"reflect"

	"github.com/kcl1104/geminus/internal/model"
)

// apply performs one RFC 6902 operation on the decoded document. Callers
// discard the whole tree on the first issue, which makes a patch atomic.
func apply(doc *any, op model.PatchOp, i int) *model.ValidationIssue {
	tokens, _ := parsePointer(op.Path) // check() already rejected bad pointers
	var value any
	if op.Op != model.PatchRemove {
		if err := json.Unmarshal(op.Value, &value); err != nil {
			return newIssue(PathInvalid, i, "value", err.Error())
		}
	}
	updated, issue := at(*doc, tokens, op, value, i)
	if issue != nil {
		return issue
	}
	*doc = updated
	return nil
}

// at applies op to the value reached by tokens below node and returns the
// new node. Every level returns its (possibly reallocated) container to its
// parent, so array insertions and removals are never lost.
func at(node any, tokens []string, op model.PatchOp, value any, i int) (any, *model.ValidationIssue) {
	key, rest := tokens[0], tokens[1:]
	switch c := node.(type) {
	case map[string]any:
		current, exists := c[key]
		if len(rest) > 0 {
			if !exists {
				return nil, targetMissing(i, op.Path)
			}
			child, issue := at(current, rest, op, value, i)
			if issue != nil {
				return nil, issue
			}
			c[key] = child
			return c, nil
		}
		if !exists && op.Op != model.PatchAdd {
			return nil, targetMissing(i, op.Path)
		}
		switch op.Op {
		case model.PatchAdd, model.PatchReplace:
			c[key] = value
		case model.PatchRemove:
			delete(c, key)
		case model.PatchTest:
			if !reflect.DeepEqual(current, value) {
				return nil, newIssue(TestFailed, i, "value", "the value at "+op.Path+" differs")
			}
		}
		return c, nil
	case []any:
		if len(rest) == 0 && op.Op == model.PatchAdd && key == "-" {
			return append(c, value), nil
		}
		n, err := arrayIndex(key)
		if err != nil {
			return nil, newIssue(PathInvalid, i, "path", err.Error())
		}
		if len(rest) > 0 {
			if n >= len(c) {
				return nil, targetMissing(i, op.Path)
			}
			child, issue := at(c[n], rest, op, value, i)
			if issue != nil {
				return nil, issue
			}
			c[n] = child
			return c, nil
		}
		limit := len(c)
		if op.Op == model.PatchAdd {
			limit++ // index len(c) appends
		}
		if n >= limit {
			return nil, targetMissing(i, op.Path)
		}
		switch op.Op {
		case model.PatchAdd:
			return append(c[:n], append([]any{value}, c[n:]...)...), nil
		case model.PatchReplace:
			c[n] = value
		case model.PatchRemove:
			return append(c[:n], c[n+1:]...), nil
		case model.PatchTest:
			if !reflect.DeepEqual(c[n], value) {
				return nil, newIssue(TestFailed, i, "value", "the value at "+op.Path+" differs")
			}
		}
		return c, nil
	}
	return nil, targetMissing(i, op.Path) // a scalar has no members
}

func targetMissing(i int, path string) *model.ValidationIssue {
	return newIssue(TargetMissing, i, "path", path+" does not exist")
}

func newIssue(code string, op int, member, message string) *model.ValidationIssue {
	v := issue(code, op, member, message)
	return &v
}
