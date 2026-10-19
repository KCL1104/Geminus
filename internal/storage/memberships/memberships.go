// Package memberships is the SQL of twin.memberships for membership
// management (T-010) and for the user's own memberships (D-045). Every
// statement runs in a tenant.DB transaction (RLS) and also filters by
// organization, except OfUser, which filters by the user its RunSelf
// transaction is scoped to. The D-036 owner lock is taken by
// tenant.DB.RunOwners before any of these run.
package memberships

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Membership is one row.
type Membership struct {
	OrganizationID string
	UserID         string
	Role           string
	CreatedAt      time.Time
}

var (
	// ErrNotFound: the user is not a member of the organization.
	ErrNotFound = errors.New("memberships: not found")
	// ErrExists: the user is already a member.
	ErrExists = errors.New("memberships: already a member")
	// ErrUnknownUser: no user has the ID. Users are provisioned by an
	// operator (D-008); the API role cannot see other users' rows, only
	// the foreign key can tell.
	ErrUnknownUser = errors.New("memberships: no such user")
)

// Get returns user's membership in org, locked FOR UPDATE when lock is set.
func Get(ctx context.Context, tx pgx.Tx, org, user string, lock bool) (Membership, error) {
	q := `SELECT role, created_at FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`
	if lock {
		q += ` FOR UPDATE`
	}
	m := Membership{OrganizationID: org, UserID: user}
	err := tx.QueryRow(ctx, q, org, user).Scan(&m.Role, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	return m, err
}

// List returns up to limit memberships of org in user_id order, after the
// user ID if one is given.
func List(ctx context.Context, tx pgx.Tx, org string, after *string, limit int) ([]Membership, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = tx.Query(ctx, `SELECT user_id, role, created_at FROM twin.memberships WHERE organization_id=$1
			ORDER BY user_id LIMIT $2`, org, limit)
	} else {
		rows, err = tx.Query(ctx, `SELECT user_id, role, created_at FROM twin.memberships WHERE organization_id=$1
			AND user_id > $2 ORDER BY user_id LIMIT $3`, org, *after, limit)
	}
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Membership, error) {
		m := Membership{OrganizationID: org}
		err := r.Scan(&m.UserID, &m.Role, &m.CreatedAt)
		return m, err
	})
}

// Insert adds m; the database sets CreatedAt.
func Insert(ctx context.Context, tx pgx.Tx, m Membership) (Membership, error) {
	err := tx.QueryRow(ctx, `INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ($1,$2,$3)
		ON CONFLICT (organization_id,user_id) DO NOTHING RETURNING created_at`, m.OrganizationID, m.UserID, m.Role).Scan(&m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrExists
	}
	if pg, ok := errors.AsType[*pgconn.PgError](err); ok && pg.Code == "23503" && pg.ConstraintName == "memberships_user_id_fkey" {
		return Membership{}, ErrUnknownUser
	}
	return m, err
}

// SetRole changes user's role in org.
func SetRole(ctx context.Context, tx pgx.Tx, org, user, role string) (Membership, error) {
	m := Membership{OrganizationID: org, UserID: user, Role: role}
	err := tx.QueryRow(ctx, `UPDATE twin.memberships SET role=$3 WHERE organization_id=$1 AND user_id=$2 RETURNING created_at`,
		org, user, role).Scan(&m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	return m, err
}

// Own is one of a user's memberships with its organization's name (D-045).
type Own struct {
	OrganizationID   string
	OrganizationName string
	Role             string
}

// OfUser returns up to limit of user's memberships with their
// organizations' names, ordered by organization name, then organization
// ID, in byte order (COLLATE "C": the same order whatever the database's
// collation, and the order Go compares strings in). It runs in
// tenant.DB.RunSelf, where RLS shows only the user's own memberships and
// their organizations (migration 0008); the user filter is the
// application's own, next to it.
func OfUser(ctx context.Context, tx pgx.Tx, user string, limit int) ([]Own, error) {
	rows, err := tx.Query(ctx, `SELECT m.organization_id, o.name, m.role
		FROM twin.memberships m JOIN twin.organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1
		ORDER BY o.name COLLATE "C", m.organization_id COLLATE "C" LIMIT $2`, user, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Own, error) {
		var m Own
		err := r.Scan(&m.OrganizationID, &m.OrganizationName, &m.Role)
		return m, err
	})
}

// Delete removes user's membership in org.
func Delete(ctx context.Context, tx pgx.Tx, org, user string) error {
	tag, err := tx.Exec(ctx, `DELETE FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, org, user)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return err
}
