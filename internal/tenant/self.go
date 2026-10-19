package tenant

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/tenant/internal/scope"
)

// RunSelf executes fn in one read-only transaction scoped to the user alone
// (D-045): no organization is selected, so RLS shows the transaction only
// the user's own memberships and the ID and name of the organizations they
// belong to (migration 0008), and no other tenant row. It serves reads of
// the caller's own identity (GET /v1/me); work in an organization goes
// through Run, which checks the membership. user must be a D-001 user ID,
// as ResolveUser returns it, or RunSelf returns ErrInvalidScope without
// touching the database. The scope is transaction-local, as Run's, and
// errors are classified the same way; the transaction rolls back on error,
// panic or cancellation, which also clears the scope.
func (d *DB) RunSelf(ctx context.Context, user string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if !userID.MatchString(user) {
		return ErrInvalidScope
	}
	return d.db.Tx(ctx, ReadOnly, func(ctx context.Context, tx pgx.Tx) error {
		if err := scope.Set(ctx, tx, "", user); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}
