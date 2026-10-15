package tenant_test

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

func TestScopeIsValidatedBeforeTheDatabase(t *testing.T) {
	d := tenant.New(nil) // never reached: invalid scopes fail first
	called := false
	fn := func(context.Context, pgx.Tx, tenant.Membership) error { called = true; return nil }
	for _, s := range []tenant.Scope{
		{OrganizationID: "org_x", UserID: "usr_01M4YDQK00TA00000000000001"},
		{OrganizationID: "org_01M4YDQK00TA00000000000000", UserID: "usr_01m4ydqk00ta00000000000001"},
		{OrganizationID: "org_01M4YDQK00TA00000000000000'; RESET ROLE; --", UserID: "usr_01M4YDQK00TA00000000000001"},
		{},
	} {
		if err := d.Run(context.Background(), s, pgx.TxOptions{}, fn); !errors.Is(err, tenant.ErrInvalidScope) {
			t.Errorf("%+v: %v", s, err)
		}
	}
	if called {
		t.Error("fn ran for an invalid scope")
	}
	if _, err := tenant.NewWorker(d, []string{"org_01M4YDQK00TA00000000000000", "org_bad"}); !errors.Is(err, tenant.ErrInvalidScope) {
		t.Errorf("worker allowlist with a bad ID: %v", err)
	}
}

// --- Integration tests with real runtime logins on a disposable database.

const (
	apiLogin    = "dt_test_api_login"
	workerLogin = "dt_test_worker_login"
	ownerLogin  = "dt_test_unsafe_owner_login" // member of twin_owner: must be refused
	bothLogin   = "dt_test_unsafe_both_login"  // member of both runtime roles: must be refused
)

func adminURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("GEMINUS_TEST_DATABASE_URL")
	if u == "" {
		if os.Getenv("GEMINUS_REQUIRE_DB_TESTS") == "1" {
			t.Fatal("GEMINUS_REQUIRE_DB_TESTS=1 but GEMINUS_TEST_DATABASE_URL is not set")
		}
		t.Skip("NOT RUN: set GEMINUS_TEST_DATABASE_URL to a disposable dt_test_* database prepared by `make db-test`")
	}
	return u
}

func open(t *testing.T, rawURL string, maxConns int32, guard bool) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		t.Fatal("invalid database URL")
	}
	cfg.MaxConns = maxConns
	if guard {
		cfg.AfterRelease = tenant.Guard
	}
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// admin returns a pool on the provisioning login after checking the database
// is disposable and migrated, and makes sure the test login roles exist.
// Logins have no password: the disposable clusters use trust authentication.
func admin(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p := open(t, adminURL(t), 2, false)
	var name string
	var schema bool
	if err := p.QueryRow(context.Background(),
		`SELECT current_database(), EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='twin')`).Scan(&name, &schema); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "dt_test_") || !schema {
		t.Fatalf("refusing database %q (twin schema: %v)", name, schema)
	}
	for login, roles := range map[string]string{apiLogin: "twin_api", workerLogin: "twin_worker", ownerLogin: "twin_api, twin_owner", bothLogin: "twin_api, twin_worker"} {
		_, err := p.Exec(context.Background(), `DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+login+`') THEN
				CREATE ROLE `+login+` LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB IN ROLE `+roles+`;
			END IF; END $$`)
		if err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func as(t *testing.T, login string, maxConns int32, guard bool) *pgxpool.Pool {
	t.Helper()
	u, err := url.Parse(adminURL(t))
	if err != nil {
		t.Fatal("invalid GEMINUS_TEST_DATABASE_URL")
	}
	u.User = url.User(login)
	return open(t, u.String(), maxConns, guard)
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func newID(t *testing.T, prefix string) string {
	t.Helper()
	b := []byte{"01234567"[randInt(t, 8)]}
	for range 25 {
		b = append(b, crockford[randInt(t, 32)])
	}
	return prefix + "_" + string(b)
}

func randInt(t *testing.T, n int64) int64 {
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		t.Fatal(err)
	}
	return v.Int64()
}

type org struct{ id, user, project string }

// newOrg commits an organization with one owner and one project, as the
// provisioning path would (D-008: controlled bootstrap, never the API).
func newOrg(t *testing.T, p *pgxpool.Pool) org {
	t.Helper()
	o := org{id: newID(t, "org"), user: newID(t, "usr"), project: newID(t, "prj")}
	subject := newID(t, "sub")
	err := postgres.OpenPool(p).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`SET LOCAL ROLE twin_owner`, nil},
			{`SELECT set_config('app.oidc_issuer','https://test.invalid',true), set_config('app.oidc_subject',$1,true),
			  set_config('app.organization_id',$2,true), set_config('app.user_id',$3,true)`, []any{subject, o.id, o.user}},
			{`INSERT INTO twin.users(id,oidc_issuer,oidc_subject) VALUES ($1,'https://test.invalid',$2)`, []any{o.user, subject}},
			{`INSERT INTO twin.organizations(id,organization_id,name) VALUES ($1,$1,'Org')`, []any{o.id}},
			{`INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ($1,$2,'owner')`, []any{o.id, o.user}},
			{`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'Project',$3)`, []any{o.project, o.id, o.user}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("creating organization: %v", err)
	}
	return o
}

func code(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

// TEST-011: the runtime logins are plain, and cannot become the owner.
func TestRuntimeRoles(t *testing.T) {
	adm := admin(t)
	ctx := context.Background()
	if err := tenant.CheckRuntimeRole(ctx, as(t, apiLogin, 1, false), tenant.APIRole); err != nil {
		t.Errorf("API login: %v", err)
	}
	if err := tenant.CheckRuntimeRole(ctx, as(t, workerLogin, 1, false), tenant.WorkerRole); err != nil {
		t.Errorf("worker login: %v", err)
	}
	for _, c := range []struct {
		pool *pgxpool.Pool
		want string
		why  string
	}{
		{as(t, apiLogin, 1, false), tenant.WorkerRole, "does not inherit twin_worker"},
		{as(t, ownerLogin, 1, false), tenant.APIRole, "is a member of twin_owner"},
		{as(t, bothLogin, 1, false), tenant.APIRole, "is also a member of twin_worker"},
		{adm, tenant.APIRole, "is a superuser"},
	} {
		if err := tenant.CheckRuntimeRole(ctx, c.pool, c.want); !errors.Is(err, tenant.ErrUnsafeRole) || !strings.Contains(err.Error(), c.why) {
			t.Errorf("want ErrUnsafeRole (%s), got %v", c.why, err)
		}
	}

	api := as(t, apiLogin, 1, false)
	for _, role := range []string{"twin_owner", "twin_worker", "dt_admin", "postgres"} {
		_, err := api.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		if c := code(err); c != "42501" && c != "22023" && c != "42704" { // denied, or the role does not exist here
			t.Errorf("API login SET ROLE %s: %v", role, err)
		}
	}
	var n int
	if err := api.QueryRow(ctx, `SELECT count(*) FROM twin.projects`).Scan(&n); err != nil || n != 0 {
		t.Errorf("FORCE RLS: an unscoped API login reads %d projects (%v)", n, err)
	}
}

// TEST-010: membership is checked, scope confines reads and writes, and a
// cross-tenant parent is rejected.
func TestRunScopes(t *testing.T) {
	adm := admin(t)
	a, b := newOrg(t, adm), newOrg(t, adm)
	d := tenant.New(as(t, apiLogin, 2, true))
	ctx := context.Background()

	var seen []string
	err := d.Run(ctx, tenant.Scope{OrganizationID: a.id, UserID: a.user}, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx, m tenant.Membership) error {
		if m.Role != tenant.Owner {
			t.Errorf("role %q", m.Role)
		}
		rows, err := tx.Query(ctx, `SELECT id FROM twin.projects WHERE id = ANY($1)`, []string{a.project, b.project})
		if err != nil {
			return err
		}
		seen, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil || len(seen) != 1 || seen[0] != a.project {
		t.Fatalf("scoped read: %v %v", seen, err)
	}

	called := false
	err = d.Run(ctx, tenant.Scope{OrganizationID: b.id, UserID: a.user}, pgx.TxOptions{}, func(context.Context, pgx.Tx, tenant.Membership) error {
		called = true
		return nil
	})
	if !errors.Is(err, tenant.ErrNoMembership) || called {
		t.Errorf("wrong organization: %v (fn called: %v)", err, called)
	}

	write := func(sql string, args ...any) error {
		return d.Run(ctx, tenant.Scope{OrganizationID: a.id, UserID: a.user}, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx, _ tenant.Membership) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		})
	}
	if err := write(`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, newID(t, "prj"), b.id, a.user); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("write into another organization: %v", err)
	}
	if err := write(`INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by) VALUES ($1,$2,$3,'M','software_delivery',$4)`,
		newID(t, "mdl"), a.id, b.project, a.user); !errors.Is(err, postgres.ErrReference) {
		t.Errorf("cross-tenant parent: %v", err)
	}
	if err := write(`UPDATE twin.projects SET name='renamed' WHERE id=$1`, b.project); err != nil {
		t.Errorf("an update of an invisible row must be a no-op, not an error: %v", err)
	}
	var name string
	if err := postgres.OpenPool(adm).Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, b.id); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT name FROM twin.projects WHERE id=$1`, b.project).Scan(&name)
	}); err != nil || name != "Project" {
		t.Errorf("organization B's project changed: %q %v", name, err)
	}
}

func scopeOf(t *testing.T, p *pgxpool.Pool) (org, user string, sameRole bool) {
	t.Helper()
	if err := p.QueryRow(context.Background(), `SELECT coalesce(current_setting('app.organization_id', true), ''),
		coalesce(current_setting('app.user_id', true), ''), current_user = session_user`).Scan(&org, &user, &sameRole); err != nil {
		t.Fatal(err)
	}
	return org, user, sameRole
}

// TEST-010: after success, error, panic or cancellation the reused pooled
// connection carries no scope; a leaked session-level setting is discarded.
func TestPoolReuseLeavesNoScope(t *testing.T) {
	adm := admin(t)
	a := newOrg(t, adm)
	pool := as(t, apiLogin, 1, true)
	d := tenant.New(pool)
	s := tenant.Scope{OrganizationID: a.id, UserID: a.user}
	ctx := context.Background()
	clean := func(after string) {
		t.Helper()
		if org, user, same := scopeOf(t, pool); org != "" || user != "" || !same {
			t.Errorf("after %s: org=%q user=%q same role=%v", after, org, user, same)
		}
	}
	clean("connect")
	start := pool.Stat().NewConnsCount()
	_ = d.Run(ctx, s, pgx.TxOptions{}, func(context.Context, pgx.Tx, tenant.Membership) error { return nil })
	clean("success")
	_ = d.Run(ctx, s, pgx.TxOptions{}, func(context.Context, pgx.Tx, tenant.Membership) error { return errors.New("boom") })
	clean("error")
	func() {
		defer func() { _ = recover() }()
		_ = d.Run(ctx, s, pgx.TxOptions{}, func(context.Context, pgx.Tx, tenant.Membership) error { panic("kaboom") })
	}()
	clean("panic")
	// Run's scope is transaction-local by itself: Guard never had to discard a
	// connection (it is a safety net, not what keeps the scope from leaking).
	if n := pool.Stat().NewConnsCount(); n != start {
		t.Errorf("connections were replaced during ordinary runs (%d -> %d): scope outlived its transaction", start, n)
	}
	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	err := d.Run(cctx, s, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx, _ tenant.Membership) error {
		_, err := tx.Exec(ctx, `SELECT pg_sleep(5)`)
		return err
	})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cancelled run: %v", err)
	}
	clean("cancellation")

	// A bug that sets scope at session level is caught by Guard: the
	// connection is destroyed on release instead of being reused.
	before := pool.Stat().NewConnsCount()
	if _, err := pool.Exec(ctx, `SELECT set_config('app.organization_id', $1, false)`, a.id); err != nil {
		t.Fatal(err)
	}
	clean("a session-level leak")
	if pool.Stat().NewConnsCount() != before+1 {
		t.Errorf("the leaking connection was reused (new connections %d -> %d)", before, pool.Stat().NewConnsCount())
	}
	// The same for a session-level role switch (the login may SET ROLE to the
	// group it belongs to, which still differs from the session user).
	before = pool.Stat().NewConnsCount()
	if _, err := pool.Exec(ctx, `SET ROLE twin_api`); err != nil {
		t.Fatal(err)
	}
	clean("a session-level SET ROLE")
	if pool.Stat().NewConnsCount() != before+1 {
		t.Errorf("the connection with a leaked role was reused")
	}
}

// D-008: the worker serves assigned organizations only, one per transaction.
func TestWorkerAssignment(t *testing.T) {
	adm := admin(t)
	a, b := newOrg(t, adm), newOrg(t, adm)
	d := tenant.New(as(t, workerLogin, 1, true))
	w, err := tenant.NewWorker(d, []string{a.id})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := w.Run(ctx, b.id, pgx.TxOptions{}, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, tenant.ErrNotAssigned) {
		t.Errorf("unassigned organization: %v", err)
	}
	var n int
	if err := w.Run(ctx, a.id, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM twin.projects WHERE id = ANY($1)`, []string{a.project, b.project}).Scan(&n)
	}); err != nil || n != 1 {
		t.Errorf("assigned scope sees %d of the two projects (%v)", n, err)
	}
	if err := w.Run(ctx, a.id, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT count(*) FROM twin.memberships`)
		return err
	}); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("the worker reads memberships: %v", err)
	}
}
