// Package scope sets the organization scope the RLS policies read, for
// internal/tenant (a member's request) and internal/tenant/workerscope (an
// organization assigned to the worker). It is internal to internal/tenant:
// no other package sets a scope itself (T-019; ADR-019 R3b).
package scope

import (
	"context"
	"regexp"

	"github.com/jackc/pgx/v5"
)

// D-001 IDs of the scope.
var (
	OrgID  = regexp.MustCompile(`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	UserID = regexp.MustCompile(`^usr_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// Set scopes tx to the organization and user ("" for none) with
// set_config(..., true): the scope ends with the transaction and never
// stays on a pooled connection.
func Set(ctx context.Context, tx pgx.Tx, org, user string) error {
	_, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true), set_config('app.user_id', $2, true)`, org, user)
	return err
}
