package tenant

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/tenant/internal/scope"
)

// RunOwners executes fn in one read-write transaction in which an owner
// changes the organization's memberships (D-036, ADR-023):
//
//  1. It reads the caller's role without a lock and refuses a non-member
//     (ErrNoMembership) or a non-owner (ErrForbidden) before taking any
//     lock, so a refused request never waits for, or holds up, a write
//     (T-010 review M1).
//  2. It takes the organization's membership lock, a transaction-level
//     advisory lock (released at commit or rollback), so membership changes
//     in one organization run one at a time: none can lock rows in an order
//     another one would cycle with (T-010 review M2).
//  3. It locks every owner membership in user_id order FOR UPDATE (D-036),
//     which waits for owners' writes in progress. That statement starts
//     after the previous change committed, so the set is current (T-010
//     review M3), and it cannot change until this transaction ends.
//  4. A caller who lost the owner role or the membership while waiting is
//     refused the same way as in 1.
//
// fn gets the locked owners' user IDs, so it can refuse to leave the
// organization without one; m.Role is always Owner.
func (d *DB) RunOwners(ctx context.Context, s Scope, fn func(ctx context.Context, tx pgx.Tx, m Membership, owners []string) error) error {
	if !orgID.MatchString(s.OrganizationID) || !userID.MatchString(s.UserID) {
		return ErrInvalidScope
	}
	return d.db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := scope.Set(ctx, tx, s.OrganizationID, s.UserID); err != nil {
			return err
		}
		if err := requireOwner(ctx, tx, s); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('twin.memberships:' || $1, 0))`, s.OrganizationID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT user_id FROM twin.memberships WHERE organization_id=$1 AND role='owner'
			ORDER BY user_id FOR UPDATE`, s.OrganizationID)
		if err != nil {
			return err
		}
		owners, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if err := requireOwner(ctx, tx, s); err != nil {
			return err
		}
		return fn(ctx, tx, Membership{OrganizationID: s.OrganizationID, UserID: s.UserID, Role: Owner}, owners)
	})
}

// requireOwner reads the caller's role without a lock.
func requireOwner(ctx context.Context, tx pgx.Tx, s Scope) error {
	var role Role
	err := tx.QueryRow(ctx, `SELECT role FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, s.OrganizationID, s.UserID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoMembership
	}
	if err != nil {
		return err
	}
	if role != Owner {
		return ErrForbidden
	}
	return nil
}
