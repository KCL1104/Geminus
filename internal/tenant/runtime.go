package tenant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Runtime roles (db/bootstrap_roles.sql). An API login inherits twin_api, a
// worker login twin_worker; neither may be anything more.
const (
	APIRole    = "twin_api"
	WorkerRole = "twin_worker"
	ownerRole  = "twin_owner"
)

// ErrUnsafeRole: the login could bypass or escape the RLS/grant model.
var ErrUnsafeRole = errors.New("tenant: unsafe runtime database role")

// CheckRuntimeRole verifies, at startup, that the pool's login is a plain
// runtime login for want (APIRole or WorkerRole): not superuser, BYPASSRLS,
// CREATEROLE or CREATEDB; not a member of twin_owner or of the other runtime
// role; and owner of nothing in schema twin (AGENTS.md §2, TEST-011). A
// process must refuse to start when this fails.
func CheckRuntimeRole(ctx context.Context, pool *pgxpool.Pool, want string) error {
	other := map[string]string{APIRole: WorkerRole, WorkerRole: APIRole}[want]
	if other == "" {
		return fmt.Errorf("%w: unknown runtime role %q", ErrUnsafeRole, want)
	}
	var login string
	var super, bypass, createRole, createDB, owner, hasWant, hasOther, owns bool
	err := pool.QueryRow(ctx, `
		SELECT r.rolname, r.rolsuper, r.rolbypassrls, r.rolcreaterole, r.rolcreatedb,
		       pg_has_role(r.oid, $1::text, 'MEMBER'), pg_has_role(r.oid, $2::text, 'USAGE'), pg_has_role(r.oid, $3::text, 'MEMBER'),
		       EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		               WHERE n.nspname='twin' AND c.relowner=r.oid)
		       OR EXISTS (SELECT 1 FROM pg_namespace n WHERE n.nspname='twin' AND n.nspowner=r.oid)
		FROM pg_roles r WHERE r.rolname = current_user`, ownerRole, want, other).
		Scan(&login, &super, &bypass, &createRole, &createDB, &owner, &hasWant, &hasOther, &owns)
	if err != nil {
		return fmt.Errorf("tenant: reading the runtime role: %w", err)
	}
	var problems []string
	for _, p := range []struct {
		bad  bool
		what string
	}{
		{super, "is a superuser"}, {bypass, "has BYPASSRLS"}, {createRole, "has CREATEROLE"}, {createDB, "has CREATEDB"},
		{owner, "is a member of " + ownerRole}, {!hasWant, "does not inherit " + want}, {hasOther, "is also a member of " + other},
		{owns, "owns objects in schema twin"},
	} {
		if p.bad {
			problems = append(problems, p.what)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: login %q %s", ErrUnsafeRole, login, strings.Join(problems, ", "))
	}
	return nil
}
