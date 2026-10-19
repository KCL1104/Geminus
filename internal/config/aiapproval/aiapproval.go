// Package aiapproval holds the owner's recorded approvals of live AI and
// the token that proves one (T-024 review L5; H-001, H-017, D-018).
//
// An Approval can only be made by Recorded, from the list this build
// records; its zero value is no approval, and Valid checks the list again.
// internal/config reads AI_POLICY_APPROVAL through Recorded, and the live
// adapter (internal/ai/provider/anthropic) accepts nothing but a valid
// Approval, so no code path reaches the live provider with an approval the
// build does not record, whatever string it holds.
//
// The package is a leaf: it imports nothing of this module, so the adapter
// may depend on it without depending on the rest of internal/config
// (architecture rule ai-suggests-only).
package aiapproval

// recorded lists the owner's recorded approvals of live AI, by the ID of
// the decision that records them. Adding one is the owner's decision,
// recorded in docs/DECISIONS_AND_TBD.md (model, token quota, data policy,
// and both organizations' retention settings alike, D-018) and made in a
// reviewed change; an agent never adds one. It is empty: live AI is not
// approved (H-017). Tests of this package's importers add one through
// export_test.go, for the duration of a test only.
var recorded = map[string]bool{}

// Approval proves that an approval of live AI is recorded in this build.
type Approval struct{ id string }

// Recorded returns the Approval for id if this build records it.
func Recorded(id string) (Approval, bool) {
	if id == "" || !recorded[id] {
		return Approval{}, false
	}
	return Approval{id: id}, true
}

// ID is the decision that records the approval; empty for none.
func (a Approval) ID() string { return a.id }

// Valid reports whether a is a recorded approval: not the zero value, and
// still in the list.
func (a Approval) Valid() bool { return a.id != "" && recorded[a.id] }
