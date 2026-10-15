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
)

// ErrUnsafeRole: the login could bypass or escape the RLS/grant model.
var ErrUnsafeRole = errors.New("tenant: unsafe runtime database role")

// CheckRuntimeRole verifies, at startup, that the pool's login is a plain
// runtime login for want (APIRole or WorkerRole), so that RLS and the grant
// model are the only way it reaches data (AGENTS.md §2, TEST-011). A process
// must refuse to start when this fails. The login (session_user):
//   - runs as itself (no role switched at startup or later);
//   - is not SUPERUSER, BYPASSRLS, CREATEROLE, CREATEDB or REPLICATION;
//   - is a member of want, with its privileges inherited, and of no other
//     role in any way: no other runtime role, no twin_owner, no predefined
//     role such as pg_write_all_data, and no role it could only SET ROLE to;
//   - has no privileges granted to it directly and owns nothing.
//
// want itself must not be privileged and must own nothing.
func CheckRuntimeRole(ctx context.Context, pool *pgxpool.Pool, want string) error {
	if want != APIRole && want != WorkerRole {
		return fmt.Errorf("%w: unknown runtime role %q", ErrUnsafeRole, want)
	}
	var (
		login                                             string
		sameRole, super, bypass, createRole, createDB     bool
		replication, wantOK, inherits, direct, owns, wOwn bool
		others                                            []string
	)
	err := pool.QueryRow(ctx, `
		WITH l AS (SELECT * FROM pg_roles WHERE rolname = session_user),
		     w AS (SELECT * FROM pg_roles WHERE rolname = $1),
		     db AS (SELECT oid FROM pg_database WHERE datname = current_database()),
		     deps AS (SELECT * FROM pg_shdepend WHERE refclassid = 'pg_authid'::regclass)
		SELECT l.rolname, current_user = session_user,
		       l.rolsuper, l.rolbypassrls, l.rolcreaterole, l.rolcreatedb, l.rolreplication,
		       coalesce(NOT (w.rolsuper OR w.rolbypassrls OR w.rolcreaterole OR w.rolcreatedb OR w.rolreplication), false),
		       coalesce(pg_has_role(l.oid, w.oid, 'USAGE'), false),
		       ARRAY(SELECT r.rolname::text FROM pg_roles r
		             WHERE r.oid <> l.oid AND r.oid IS DISTINCT FROM w.oid AND pg_has_role(l.oid, r.oid, 'MEMBER')
		             ORDER BY r.rolname),
		       EXISTS (SELECT 1 FROM deps d, db WHERE d.refobjid = l.oid AND d.dbid = db.oid AND d.deptype <> 'o'),
		       EXISTS (SELECT 1 FROM deps d, db WHERE d.refobjid = l.oid AND d.deptype = 'o' AND d.dbid IN (0, db.oid)),
		       EXISTS (SELECT 1 FROM deps d, db WHERE d.refobjid = w.oid AND d.deptype = 'o' AND d.dbid IN (0, db.oid))
		FROM l LEFT JOIN w ON true`, want).
		Scan(&login, &sameRole, &super, &bypass, &createRole, &createDB, &replication, &wantOK, &inherits, &others, &direct, &owns, &wOwn)
	if err != nil {
		return fmt.Errorf("tenant: reading the runtime role: %w", err)
	}
	var problems []string
	for _, p := range []struct {
		bad  bool
		what string
	}{
		{!sameRole, "runs as a role other than its login"},
		{super, "is a superuser"}, {bypass, "has BYPASSRLS"}, {createRole, "has CREATEROLE"}, {createDB, "has CREATEDB"},
		{replication, "has REPLICATION"},
		{!wantOK, want + " is missing or privileged"}, {!inherits, "does not inherit " + want},
		{len(others) > 0, "is also a member of " + strings.Join(others, ", ")},
		{direct, "has privileges granted directly"}, {owns, "owns database objects"},
		{wOwn, want + " owns database objects"},
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
