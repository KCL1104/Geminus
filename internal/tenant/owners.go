package tenant

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
)

// RunOwners executes fn in one read-write transaction that changes the
// organization's memberships (D-036, ADR-023). It first locks every owner
// membership of s.OrganizationID in user_id order, then reads the caller's
// role. Unlike Run, it takes no share lock on the caller's own row before
// that: two owners changing memberships at once then queue on the same
// ordered locks instead of each holding its own row and waiting for the
// other's (T-009 re-review R1). fn gets the locked owners' user IDs, so it
// can refuse to leave the organization without one.
func (d *DB) RunOwners(ctx context.Context, s Scope, fn func(ctx context.Context, tx pgx.Tx, m Membership, owners []string) error) error {
	if !orgID.MatchString(s.OrganizationID) || !userID.MatchString(s.UserID) {
		return ErrInvalidScope
	}
	return d.db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := setScope(ctx, tx, s.OrganizationID, s.UserID); err != nil {
			return err
		}
		for attempt := 0; ; attempt++ {
			rows, err := tx.Query(ctx, `SELECT user_id FROM twin.memberships WHERE organization_id=$1 AND role='owner'
				ORDER BY user_id FOR UPDATE`, s.OrganizationID)
			if err != nil {
				return err
			}
			owners, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			var role Role
			err = tx.QueryRow(ctx, `SELECT role FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, s.OrganizationID, s.UserID).Scan(&role)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoMembership
			}
			if err != nil {
				return err
			}
			// Made an owner after the lock's snapshot was taken: the caller's
			// row is not held. Lock again, which sees it; never judge "the
			// last owner" from an incomplete set.
			if role == Owner && !slices.Contains(owners, s.UserID) {
				if attempt == 0 {
					continue
				}
				return ErrRetryable
			}
			return fn(ctx, tx, Membership{OrganizationID: s.OrganizationID, UserID: s.UserID, Role: role}, owners)
		}
	})
}
