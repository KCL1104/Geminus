// Package me is the caller's own identity and memberships (D-045, GET
// /v1/me): the UI picks an organization from it and learns the role it has
// there. The role only shapes the UI; every other operation authorizes its
// own request (D-008). The read runs in tenant.DB.RunSelf, a read-only
// transaction with no organization selected, where RLS shows only the
// user's own memberships and their organizations (migration 0008). SQL
// lives in internal/storage/memberships. Like every read, it writes no
// audit event.
package me

import (
	"context"

	store "github.com/kcl1104/geminus/internal/storage/memberships"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Me is http.schema.json#/$defs/Me.
type Me struct {
	UserID      string       `json:"user_id"`
	Memberships []Membership `json:"memberships"`
	Truncated   bool         `json:"truncated"`
}

// Membership is http.schema.json#/$defs/OwnMembership.
type Membership struct {
	OrganizationID   string      `json:"organization_id"`
	OrganizationName string      `json:"organization_name"`
	Role             tenant.Role `json:"role"`
}

// MaxMemberships is how many memberships GET /v1/me lists (D-045: at most
// 100 in P0). A user with more gets the first ones and Truncated.
const MaxMemberships = 100

// Service reads the caller's own memberships.
type Service struct {
	DB *tenant.DB
}

// Get returns user's ID and memberships, ordered by organization name, then
// organization ID (byte order), at most MaxMemberships. user is the ID
// tenant.DB.ResolveUser returned for the verified identity.
func (s *Service) Get(ctx context.Context, user string) (Me, error) {
	var rows []store.Own
	err := s.DB.RunSelf(ctx, user, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		// One more than the list holds tells whether there are more.
		rows, err = store.OfUser(ctx, tx, user, MaxMemberships+1)
		return err
	})
	if err != nil {
		return Me{}, err
	}
	list, truncated := page(rows)
	return Me{UserID: user, Memberships: list, Truncated: truncated}, nil
}

// page is the first MaxMemberships of rows, in their order, and whether
// rows had more; never nil, so an empty list encodes as [].
func page(rows []store.Own) ([]Membership, bool) {
	truncated := len(rows) > MaxMemberships
	if truncated {
		rows = rows[:MaxMemberships]
	}
	out := make([]Membership, 0, len(rows))
	for _, r := range rows {
		out = append(out, Membership{OrganizationID: r.OrganizationID, OrganizationName: r.OrganizationName, Role: tenant.Role(r.Role)})
	}
	return out, truncated
}
