// Package memberships is an organization's membership management (T-010):
// an owner lists the members, adds a known user, changes a role or removes
// a member, and an organization always keeps at least one owner (D-036).
// Users and organizations themselves are provisioned by an operator
// (cmd/provision-tenant, D-008), never through the API.
//
// Every write runs in tenant.DB.RunOwners: it refuses a caller who is not
// an owner before taking any lock, runs one membership change of an
// organization at a time, and locks the owner memberships in user_id order
// before the change judges "the last owner" (D-036, ADR-023). SQL lives in
// internal/storage/memberships.
package memberships

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	store "github.com/kcl1104/geminus/internal/storage/memberships"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Membership is http.schema.json#/$defs/Membership.
type Membership struct {
	OrganizationID string      `json:"organization_id"`
	UserID         string      `json:"user_id"`
	Role           tenant.Role `json:"role"`
	CreatedAt      string      `json:"created_at"` // RFC 3339, UTC
}

// Page is http.schema.json#/$defs/Memberships.
type Page struct {
	Items      []Membership `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

// CreateRequest is http.schema.json#/$defs/MembershipWrite.
type CreateRequest struct {
	UserID string      `json:"user_id"`
	Role   tenant.Role `json:"role"`
}

// UpdateRequest is http.schema.json#/$defs/MembershipUpdate.
type UpdateRequest struct {
	Role tenant.Role `json:"role"`
}

var (
	// ErrNotFound: the user is not a member of the organization.
	ErrNotFound = errors.New("memberships: not found")
	// ErrExists: the user is already a member (409 LIFECYCLE_CONFLICT).
	ErrExists = errors.New("memberships: already a member")
	// ErrUnknownUser: no user has the ID (422 at /user_id). Telling it
	// apart from a member is unavoidable for an add; it reveals only that
	// an unguessable ID exists, not who it is (SECURITY §2: no user
	// enumeration).
	ErrUnknownUser = errors.New("memberships: no such user")
	// ErrLastOwner: the change would leave the organization without an
	// owner (409 LIFECYCLE_CONFLICT, D-036).
	ErrLastOwner = errors.New("memberships: the organization must keep an owner")
	// ErrInvalidCursor: the cursor is not one this service issued for the
	// organization's member list.
	ErrInvalidCursor = errors.New("memberships: invalid cursor")
	// ErrInvalidLimit: a page size outside 1–100 (openapi Limit).
	ErrInvalidLimit = errors.New("memberships: invalid page size")
	// ErrInvalidRequest: a user ID or role the contract refuses (the HTTP
	// layer's schema check refuses it first).
	ErrInvalidRequest = errors.New("memberships: invalid user ID or role")
)

// Service runs membership operations for a verified scope.
type Service struct {
	DB *tenant.DB
}

// manageRoles: only an owner manages memberships (x-required-roles,
// SECURITY §2). List checks it; the writes run in tenant.DB.RunOwners,
// which admits only owners, so a change here needs one there.
var manageRoles = []tenant.Role{tenant.Owner}

const (
	createOperation = "createMembership"
	maxLimit        = 100
)

var userID = regexp.MustCompile(`^usr_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// ValidUserID reports whether id is a D-001 user ID.
func ValidUserID(id string) bool { return userID.MatchString(id) }

func validRole(r tenant.Role) bool { return slices.Contains(tenant.Members, r) }

// List returns one page of the organization's members in user ID order.
func (s *Service) List(ctx context.Context, sc tenant.Scope, cursor string, limit int) (Page, error) {
	if limit < 1 || limit > maxLimit {
		return Page{}, ErrInvalidLimit
	}
	var after *string
	if cursor != "" {
		id, err := decodeCursor(cursor, sc.OrganizationID)
		if err != nil {
			return Page{}, err
		}
		after = &id
	}
	page := Page{Items: []Membership{}}
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(manageRoles...); err != nil {
			return err
		}
		rows, err := store.List(ctx, tx, m.OrganizationID, after, limit+1)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			c := encodeCursor(m.OrganizationID, rows[limit-1].UserID)
			page.NextCursor = &c
			rows = rows[:limit]
		}
		for _, r := range rows {
			page.Items = append(page.Items, public(r))
		}
		return nil
	})
	return page, err
}

// Create adds the known user req.UserID with req.Role, or replays the
// response of the earlier request with the same Idempotency-Key. It returns
// the HTTP status and body to send.
func (s *Service) Create(ctx context.Context, sc tenant.Scope, requestID, key string, body []byte, req CreateRequest) (int, []byte, error) {
	if !ValidUserID(req.UserID) || !validRole(req.Role) {
		return 0, nil, ErrInvalidRequest
	}
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return 0, nil, err
	}
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: createOperation, Key: key}
	var status int
	var out []byte
	err = s.DB.RunOwners(ctx, sc, func(ctx context.Context, tx tenant.Tx, m tenant.Membership, _ []string) error {
		replay, err := idempotency.Claim(ctx, tx, k, hashing.Sum(canonical))
		if err != nil {
			return err
		}
		if replay != nil {
			status = replay.Status
			out, err = hashing.Canonical(replay.Body)
			return err
		}
		added, err := store.Insert(ctx, tx, store.Membership{OrganizationID: m.OrganizationID, UserID: req.UserID, Role: string(req.Role)})
		switch {
		case errors.Is(err, store.ErrExists):
			return ErrExists
		case errors.Is(err, store.ErrUnknownUser):
			return ErrUnknownUser
		case err != nil:
			return err
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "membership.create", TargetType: "membership", TargetID: added.UserID, RequestID: requestID,
			Metadata: map[string]any{"role": added.Role}}); err != nil {
			return err
		}
		data, err := json.Marshal(public(added))
		if err != nil {
			return err
		}
		if out, err = hashing.Canonical(data); err != nil {
			return err
		}
		status = 201
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}

// Update sets user's role. Taking the owner role from the only owner is
// refused. Setting the role a member already has changes nothing.
func (s *Service) Update(ctx context.Context, sc tenant.Scope, requestID, user string, req UpdateRequest) (Membership, error) {
	if !validRole(req.Role) {
		return Membership{}, ErrInvalidRequest
	}
	var out Membership
	err := s.DB.RunOwners(ctx, sc, func(ctx context.Context, tx tenant.Tx, m tenant.Membership, owners []string) error {
		cur, err := store.Get(ctx, tx, m.OrganizationID, user, true)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur.Role == string(req.Role) {
			out = public(cur)
			return nil
		}
		if cur.Role == string(tenant.Owner) && len(owners) == 1 {
			return ErrLastOwner
		}
		next, err := store.SetRole(ctx, tx, m.OrganizationID, user, string(req.Role))
		if err != nil {
			return err
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "membership.update", TargetType: "membership", TargetID: user, RequestID: requestID,
			Metadata: map[string]any{"role": next.Role, "previous_role": cur.Role}}); err != nil {
			return err
		}
		out = public(next)
		return nil
	})
	return out, err
}

// Delete removes user's membership; removing the only owner is refused.
// The removed member's next request is refused (every request checks its
// membership); one already writing finishes first, since its transaction
// share-locks the row this deletes (ADR-019).
func (s *Service) Delete(ctx context.Context, sc tenant.Scope, requestID, user string) error {
	return s.DB.RunOwners(ctx, sc, func(ctx context.Context, tx tenant.Tx, m tenant.Membership, owners []string) error {
		cur, err := store.Get(ctx, tx, m.OrganizationID, user, true)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur.Role == string(tenant.Owner) && len(owners) == 1 {
			return ErrLastOwner
		}
		if err := store.Delete(ctx, tx, m.OrganizationID, user); err != nil {
			return err
		}
		return auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "membership.delete", TargetType: "membership", TargetID: user, RequestID: requestID,
			Metadata: map[string]any{"role": cur.Role}})
	})
}

func public(m store.Membership) Membership {
	return Membership{OrganizationID: m.OrganizationID, UserID: m.UserID, Role: tenant.Role(m.Role), CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339Nano)}
}

// cursor is the user ID a page starts after, bound to the organization and
// the list (API_DATABASE §3). It is not signed: it only moves within the
// caller's own organization, which RLS and the organization filter confine.
type cursor struct {
	Version int    `json:"v"`
	Org     string `json:"o"`
	List    string `json:"l"`
	After   string `json:"i"`
}

const cursorList = "memberships"

func encodeCursor(org, after string) string {
	data, _ := json.Marshal(cursor{Version: 1, Org: org, List: cursorList, After: after})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(s, org string) (string, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return "", ErrInvalidCursor
	}
	var c cursor
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil {
		return "", ErrInvalidCursor
	}
	if c.Version != 1 || c.Org != org || c.List != cursorList || !ValidUserID(c.After) || encodeCursor(c.Org, c.After) != s {
		return "", ErrInvalidCursor
	}
	return c.After, nil
}
