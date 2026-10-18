package tenant

import (
	"errors"
	"slices"
)

// ErrForbidden: the member's role does not allow the operation. The API
// answers 403 FORBIDDEN; a non-member gets ErrNoMembership (404) instead.
var ErrForbidden = errors.New("tenant: role does not allow this operation")

// Require returns ErrForbidden unless m's role is one of roles. Roles come
// only from the membership Run read in the same transaction (D-008), never
// from the request.
func (m Membership) Require(roles ...Role) error {
	if !slices.Contains(roles, m.Role) {
		return ErrForbidden
	}
	return nil
}

// Operation roles, as openapi.yaml x-required-roles lists them (a contract
// test compares them with planning/api-operations.json).
var (
	Members = []Role{Owner, Editor, Viewer} // any member: reads
	Writers = []Role{Owner, Editor}         // creates and edits
)
