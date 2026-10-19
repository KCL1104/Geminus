// Package idpool is the D-046 ID allocation pool (approved, H-010): when a
// proposal is created the server generates, for each kind of entity a
// proposal may add (node, flow, resource), 16 IDs "<kind>_<lowercase
// ULID>". The pool is stored with the proposal and never changes
// (ai_proposals.id_pool, migration 0010), the provider context lists it, and
// validation and approval accept a new entity only with an ID from the list
// of its own kind that the base IR does not use. A provider that needs more
// IDs answers needs_input. The provider never mints IDs (D-016).
//
// Lowercase ULIDs match the IR's ID pattern and never look like a server
// resource ID (uppercase, D-001), so the context builder's server-ID
// redaction leaves them alone.
package idpool

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
)

// Kind is an entity kind a proposal may add.
type Kind string

const (
	Node     Kind = "node"
	Flow     Kind = "flow"
	Resource Kind = "resource"
)

// Kinds lists the kinds in the schema's member order.
var Kinds = []Kind{Node, Flow, Resource}

// PerKind is the number of IDs of each kind.
const PerKind = 16

// Pool is common.schema.json#/$defs/IdAllocationPool.
type Pool struct {
	Node     []string `json:"node"`
	Flow     []string `json:"flow"`
	Resource []string `json:"resource"`
}

// IDs returns the list of kind k (nil for another kind).
func (p Pool) IDs(k Kind) []string {
	switch k {
	case Node:
		return p.Node
	case Flow:
		return p.Flow
	case Resource:
		return p.Resource
	}
	return nil
}

func (p Pool) clone() Pool {
	return Pool{Node: slices.Clone(p.Node), Flow: slices.Clone(p.Flow), Resource: slices.Clone(p.Resource)}
}

var idPattern = regexp.MustCompile(`^(node|flow|resource)_[0-7][0-9a-hjkmnp-tv-z]{25}$`)

// ErrExhausted: the generator kept producing used or repeated IDs.
var ErrExhausted = errors.New("idpool: could not draw unused IDs")

// New generates a pool for a proposal on base: fresh IDs that base does not
// use anywhere.
func New(base *model.ModelIR) (Pool, error) {
	return newWith(base, func() string { return strings.ToLower(ids.ULID(time.Now())) })
}

// newWith draws ULIDs from next. An ID the base uses or that was drawn
// before is skipped; a generator that keeps repeating is an error.
func newWith(base *model.ModelIR, next func() string) (Pool, error) {
	if base == nil {
		return Pool{}, errors.New("idpool: no base IR")
	}
	used := entityIDs(base)
	var p Pool
	for _, k := range Kinds {
		list := make([]string, 0, PerKind)
		for tries := 0; len(list) < PerKind; tries++ {
			if tries >= 4*PerKind {
				return Pool{}, ErrExhausted
			}
			id := string(k) + "_" + next()
			if used[id] || !idPattern.MatchString(id) {
				continue
			}
			used[id] = true
			list = append(list, id)
		}
		switch k {
		case Node:
			p.Node = list
		case Flow:
			p.Flow = list
		case Resource:
			p.Resource = list
		}
	}
	return p, nil
}

// Error is a pool that does not have the schema's shape; Path is a JSON
// Pointer into the pool.
type Error struct {
	Path string
	Msg  string
}

func (e *Error) Error() string { return "idpool: " + e.Path + ": " + e.Msg }

// Validate checks the shape of common.schema.json#/$defs/IdAllocationPool:
// exactly PerKind IDs of each kind, each "<kind>_<lowercase ULID>", none
// twice in the pool.
func (p Pool) Validate() error {
	seen := map[string]bool{}
	for _, k := range Kinds {
		list := p.IDs(k)
		if len(list) != PerKind {
			return &Error{"/" + string(k), fmt.Sprintf("%d IDs; a pool has %d of each kind", len(list), PerKind)}
		}
		for i, id := range list {
			path := "/" + string(k) + "/" + strconv.Itoa(i)
			if m := idPattern.FindStringSubmatch(id); m == nil || m[1] != string(k) {
				return &Error{path, "not a " + string(k) + " ID: <kind>_<lowercase ULID>"}
			}
			if seen[id] {
				return &Error{path, "repeated in the pool"}
			}
			seen[id] = true
		}
	}
	return nil
}

// Reason says why a new entity's ID is refused.
type Reason string

const (
	// NotInPool: the ID is in no list of the pool.
	NotInPool Reason = "not_in_pool"
	// WrongKind: the ID is in the pool, in another kind's list.
	WrongKind Reason = "wrong_kind"
	// UsedInBase: the ID belongs to another entity of the base IR.
	UsedInBase Reason = "used_in_base"
	// Missing: no base or candidate IR to compare.
	Missing Reason = "missing_ir"
)

// Violation is a new node, flow or resource whose ID the pool does not
// allow. Path points at the ID in the candidate IR.
type Violation struct {
	Kind   Kind
	ID     string
	Path   string
	Reason Reason
}

// entities lists an IR's IDs of kind k with their pointers.
func entities(ir *model.ModelIR, k Kind) (ids []string, paths []string) {
	add := func(member string, i int, id string) {
		ids = append(ids, id)
		paths = append(paths, "/"+member+"/"+strconv.Itoa(i)+"/id")
	}
	switch k {
	case Node:
		for i, n := range ir.Nodes {
			add("nodes", i, n.ID)
		}
	case Flow:
		for i, f := range ir.Flows {
			add("flows", i, f.ID)
		}
	case Resource:
		for i, r := range ir.Resources {
			add("resources", i, r.ID)
		}
	}
	return ids, paths
}

// entityIDs is every entity ID of an IR, of any kind.
func entityIDs(ir *model.ModelIR) map[string]bool {
	out := map[string]bool{}
	for _, k := range Kinds {
		ids, _ := entities(ir, k)
		for _, id := range ids {
			out[id] = true
		}
	}
	for _, e := range ir.EntityTypes {
		out[e.ID] = true
	}
	for _, m := range ir.Metrics {
		out[m.ID] = true
	}
	return out
}

// Check compares a proposal's candidate IR (the base with the patch
// applied) with its base: every node, flow and resource of the candidate
// whose ID no entity of that kind has in the base is new, and must take an
// ID from the pool's list of its kind that the base does not use. A changed
// ID counts as a new entity. Violations are sorted by path. Check does not
// validate the candidate otherwise; duplicate IDs are the validator's.
func (p Pool) Check(base, candidate *model.ModelIR) []Violation {
	if base == nil || candidate == nil {
		return []Violation{{Reason: Missing}}
	}
	baseAny := entityIDs(base)
	var out []Violation
	for _, k := range Kinds {
		baseIDs, _ := entities(base, k)
		ids, paths := entities(candidate, k)
		for i, id := range ids {
			if slices.Contains(baseIDs, id) {
				continue
			}
			v := Violation{Kind: k, ID: id, Path: paths[i]}
			switch {
			case baseAny[id]:
				v.Reason = UsedInBase
			case slices.Contains(p.IDs(k), id):
				continue
			case slices.Contains(p.Node, id) || slices.Contains(p.Flow, id) || slices.Contains(p.Resource, id):
				v.Reason = WrongKind
			default:
				v.Reason = NotInPool
			}
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b Violation) int { return strings.Compare(a.Path, b.Path) })
	return out
}
