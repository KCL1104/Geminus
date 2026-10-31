// Package scope sets the organization scope the RLS policies read, for
// internal/tenant (a member's request), internal/tenant/workerscope (an
// organization assigned to the worker) and internal/tenant/actingfor (the
// API's organization-only discovery of investigation calls, H-061). It is
// internal to internal/tenant: no other package sets a scope itself (T-019;
// ADR-019 R3b).
package scope

import (
	"context"
	"regexp"

	"github.com/jackc/pgx/v5"
)

// D-001 IDs of the scope.
var (
	OrgID           = regexp.MustCompile(`^org_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	UserID          = regexp.MustCompile(`^usr_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	InvestigationID = regexp.MustCompile(`^inv_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
)

// Set scopes tx to the organization and user ("" for none) with
// set_config(..., true): the scope ends with the transaction and never
// stays on a pooled connection.
func Set(ctx context.Context, tx pgx.Tx, org, user string) error {
	return SetActing(ctx, tx, org, user, "")
}

// SetActing is Set with the AI investigation the transaction acts for
// (app.investigation_id; "" for none, a person's own request), in the same
// statement (H-061, D-008 as amended; migration 0019's labels and deny
// triggers read it).
func SetActing(ctx context.Context, tx pgx.Tx, org, user, investigation string) error {
	_, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true), set_config('app.user_id', $2, true),
		set_config('app.investigation_id', $3, true)`, org, user, investigation)
	return err
}
