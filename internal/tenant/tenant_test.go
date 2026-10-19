package tenant_test

import (
	"context"
	"crypto/rand"
	"errors"
	"maps"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/contracttest"
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
		if err := d.RunOwners(context.Background(), s, func(context.Context, pgx.Tx, tenant.Membership, []string) error { called = true; return nil }); !errors.Is(err, tenant.ErrInvalidScope) {
			t.Errorf("owners, %+v: %v", s, err)
		}
	}
	if called {
		t.Error("fn ran for an invalid scope")
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
			END IF;
			EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
			END $$`)
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

type org struct{ id, user, project, subject string }

// newOrg commits an organization with one owner and one project, as the
// provisioning path would (D-008: controlled bootstrap, never the API).
func newOrg(t *testing.T, p *pgxpool.Pool) org {
	t.Helper()
	o := org{id: newID(t, "org"), user: newID(t, "usr"), project: newID(t, "prj"), subject: newID(t, "sub")}
	subject := o.subject
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
		{as(t, ownerLogin, 1, false), tenant.APIRole, "is also a member of twin_owner"},
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

// The named transaction modes are what they say: services pass ReadOnly
// for reads, and the database enforces it.
func TestRunModes(t *testing.T) {
	adm := admin(t)
	a := newOrg(t, adm)
	d := tenant.New(as(t, apiLogin, 2, true))
	sc := tenant.Scope{OrganizationID: a.id, UserID: a.user}
	for name, c := range map[string]struct {
		opts tenant.TxOptions
		want string
	}{"read-only": {tenant.ReadOnly, "on"}, "read-write": {tenant.ReadWrite, "off"}} {
		var got string
		err := d.Run(context.Background(), sc, c.opts, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			return tx.QueryRow(ctx, `SHOW transaction_read_only`).Scan(&got)
		})
		if err != nil || got != c.want {
			t.Errorf("%s: transaction_read_only %q, want %q (%v)", name, got, c.want, err)
		}
	}
}

// ReadSnapshot is one snapshot: a row another transaction commits between
// two statements is not seen, while ReadOnly sees it; and it is read-only
// (T-012 review 2 N2).
func TestReadSnapshot(t *testing.T) {
	adm := admin(t)
	a := newOrg(t, adm)
	d := tenant.New(as(t, apiLogin, 2, true))
	sc := tenant.Scope{OrganizationID: a.id, UserID: a.user}
	count := func(ctx context.Context, tx tenant.Tx) int {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM twin.projects WHERE organization_id=$1`, a.id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for name, c := range map[string]struct {
		opts tenant.TxOptions
		sees bool
	}{"snapshot": {tenant.ReadSnapshot, false}, "read-only": {tenant.ReadOnly, true}} {
		err := d.Run(context.Background(), sc, c.opts, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			before := count(ctx, tx)
			if _, err := adm.Exec(ctx, `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, newID(t, "prj"), a.id, a.user); err != nil {
				return err
			}
			if after := count(ctx, tx); (after != before) != c.sees {
				t.Errorf("%s: %d projects before the other commit, %d after", name, before, after)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	err := d.Run(context.Background(), sc, tenant.ReadSnapshot, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, newID(t, "prj"), a.id, a.user)
		return err
	})
	if err == nil {
		t.Error("a write in a snapshot read")
	}
}

// A write and a revocation of its membership are ordered (review L3): a
// read-write run locks the membership row it checked, so a concurrent
// DELETE or role change waits for the write to commit instead of
// committing first while the write goes on. A read-only run takes no lock.
func TestRunLocksMembership(t *testing.T) {
	adm := admin(t)
	a := newOrg(t, adm)
	d := tenant.New(as(t, apiLogin, 2, true))
	sc := tenant.Scope{OrganizationID: a.id, UserID: a.user}
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		opts    tenant.TxOptions
		blocked bool
	}{{"read-write", tenant.ReadWrite, true}, {"read-only", tenant.ReadOnly, false}} {
		inside, release, runDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			runDone <- d.Run(ctx, sc, c.opts, func(context.Context, tenant.Tx, tenant.Membership) error {
				close(inside)
				<-release
				return nil
			})
		}()
		<-inside
		revoked := make(chan error, 1)
		go func() {
			_, err := adm.Exec(ctx, `UPDATE twin.memberships SET role='viewer' WHERE organization_id=$1 AND user_id=$2`, a.id, a.user)
			revoked <- err
		}()
		var revokeErr error
		waited := false
		select {
		case revokeErr = <-revoked:
			if c.blocked {
				t.Errorf("%s: the role change committed while the run was in progress", c.name)
			}
		case <-time.After(500 * time.Millisecond):
			waited = true
			if !c.blocked {
				t.Errorf("%s: the role change waited for a read-only run", c.name)
			}
		}
		close(release)
		if err := <-runDone; err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if waited {
			revokeErr = <-revoked
		}
		if revokeErr != nil {
			t.Errorf("%s: the role change: %v", c.name, revokeErr)
		}
		if _, err := adm.Exec(ctx, `UPDATE twin.memberships SET role='owner' WHERE organization_id=$1 AND user_id=$2`, a.id, a.user); err != nil {
			t.Fatal(err)
		}
	}
}

// The run errors callers map (internal/httpapi) are the ones Run returns.
func TestRunErrors(t *testing.T) {
	if tenant.ErrRetryable != postgres.ErrRetryable || tenant.ErrOutcomeUnknown != postgres.ErrOutcomeUnknown {
		t.Error("tenant's run errors are not postgres.Classify's")
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

// Review of T-007: any session state that could carry one request's data or
// privileges into the next checkout makes Guard discard the connection.
func TestGuardDiscardsSessionState(t *testing.T) {
	adm := admin(t)
	a := newOrg(t, adm)
	pool := as(t, apiLogin, 1, true)
	d := tenant.New(pool)
	ctx := context.Background()
	s := tenant.Scope{OrganizationID: a.id, UserID: a.user}
	// Warm the single connection so NewConnsCount only moves when Guard discards one.
	if _, err := pool.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		leak func() error
	}{
		{"session-level app.oidc_subject", func() error {
			_, err := pool.Exec(ctx, `SELECT set_config('app.oidc_subject', 'leaked-subject', false)`)
			return err
		}},
		{"session-level app.oidc_issuer", func() error {
			_, err := pool.Exec(ctx, `SELECT set_config('app.oidc_issuer', 'https://leak.invalid', false)`)
			return err
		}},
		{"session-level search_path", func() error {
			_, err := pool.Exec(ctx, `SET search_path = public, pg_catalog`)
			return err
		}},
		{"a cursor WITH HOLD over tenant rows", func() error {
			return d.Run(ctx, s, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx, _ tenant.Membership) error {
				_, err := tx.Exec(ctx, `DECLARE leak CURSOR WITH HOLD FOR SELECT id FROM twin.projects`)
				return err
			})
		}},
		{"a temporary table", func() error {
			_, err := pool.Exec(ctx, `CREATE TEMP TABLE leak AS SELECT 1 AS x`)
			return err
		}},
		{"a session advisory lock", func() error {
			_, err := pool.Exec(ctx, `SELECT pg_advisory_lock(42)`)
			return err
		}},
		{"a LISTEN channel", func() error {
			_, err := pool.Exec(ctx, `LISTEN leak`)
			return err
		}},
	} {
		before := pool.Stat().NewConnsCount()
		if err := c.leak(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var subject string
		var cursors int
		if err := pool.QueryRow(ctx, `SELECT coalesce(current_setting('app.oidc_subject', true), ''),
			(SELECT count(*) FROM pg_cursors WHERE is_holdable)`).Scan(&subject, &cursors); err != nil {
			t.Fatal(err)
		}
		if subject != "" || cursors != 0 || pool.Stat().NewConnsCount() != before+1 {
			t.Errorf("%s: the connection was reused (subject=%q cursors=%d, new connections %d -> %d)",
				c.name, subject, cursors, before, pool.Stat().NewConnsCount())
		}
	}
}

// Guard checks every app.* setting the migrations read.
func TestGuardCoversEverySetting(t *testing.T) {
	re := regexp.MustCompile(`current_setting\('(app\.[a-z_]+)'`)
	used := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(contracttest.Root(t), "migrations", "*.up.sql"))
	if len(files) == 0 {
		t.Fatal("no migrations found")
	}
	for _, f := range files {
		for _, m := range re.FindAllStringSubmatch(string(contracttest.ReadFile(t, f)), -1) {
			used[m[1]] = true
		}
	}
	got := map[string]bool{}
	for _, s := range tenant.ScopeSettings {
		got[s] = true
	}
	if !maps.Equal(used, got) {
		t.Errorf("Guard checks %v; the migrations read %v", tenant.ScopeSettings, slices.Sorted(maps.Keys(used)))
	}
}

// Review of T-007: logins that pass a current_user-only check but can still
// escape RLS or the grant model are refused.
func TestUnsafeRuntimeLogins(t *testing.T) {
	adm := admin(t)
	ctx := context.Background()
	setup := []string{
		`DO $$ BEGIN
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='dt_test_unsafe_super_role') THEN CREATE ROLE dt_test_unsafe_super_role NOLOGIN SUPERUSER; END IF;
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='dt_test_unsafe_bypass_role') THEN CREATE ROLE dt_test_unsafe_bypass_role NOLOGIN BYPASSRLS; END IF;
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='dt_test_unsafe_setsuper_login') THEN
		    CREATE ROLE dt_test_unsafe_setsuper_login LOGIN IN ROLE twin_api;
		    GRANT dt_test_unsafe_super_role TO dt_test_unsafe_setsuper_login WITH INHERIT FALSE, SET TRUE; END IF;
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='dt_test_unsafe_bypass_login') THEN
		    CREATE ROLE dt_test_unsafe_bypass_login LOGIN IN ROLE twin_api, dt_test_unsafe_bypass_role; END IF;
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='dt_test_unsafe_repl_login') THEN
		    CREATE ROLE dt_test_unsafe_repl_login LOGIN REPLICATION IN ROLE twin_api; END IF;
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='dt_test_unsafe_write_login') THEN
		    CREATE ROLE dt_test_unsafe_write_login LOGIN IN ROLE twin_worker, pg_write_all_data; END IF;
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='dt_test_unsafe_program_login') THEN
		    CREATE ROLE dt_test_unsafe_program_login LOGIN IN ROLE twin_worker, pg_execute_server_program; END IF;
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='dt_test_unsafe_fnowner_login') THEN
		    CREATE ROLE dt_test_unsafe_fnowner_login LOGIN IN ROLE twin_worker; END IF;
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='dt_test_unsafe_grant_login') THEN
		    CREATE ROLE dt_test_unsafe_grant_login LOGIN IN ROLE twin_worker; END IF;
		END $$`,
		`CREATE OR REPLACE FUNCTION public.dt_test_owned_fn() RETURNS int LANGUAGE sql AS 'SELECT 1'`,
		`ALTER FUNCTION public.dt_test_owned_fn() OWNER TO dt_test_unsafe_fnowner_login`,
		`GRANT UPDATE ON twin.imports TO dt_test_unsafe_grant_login`,
	}
	for _, q := range setup {
		if _, err := adm.Exec(ctx, q); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	// A superuser that starts its session as twin_api via the startup options.
	u, err := url.Parse(adminURL(t))
	if err != nil {
		t.Fatal(err)
	}
	// Appended by hand: url.Values would encode the space as "+".
	if u.RawQuery != "" {
		u.RawQuery += "&"
	}
	u.RawQuery += "options=-c%20role%3Dtwin_api"
	for _, c := range []struct {
		pool      *pgxpool.Pool
		want, why string
	}{
		{open(t, u.String(), 1, false), tenant.APIRole, "is a superuser"},
		{as(t, "dt_test_unsafe_setsuper_login", 1, false), tenant.APIRole, "dt_test_unsafe_super_role"},
		{as(t, "dt_test_unsafe_bypass_login", 1, false), tenant.APIRole, "dt_test_unsafe_bypass_role"},
		{as(t, "dt_test_unsafe_repl_login", 1, false), tenant.APIRole, "has REPLICATION"},
		{as(t, "dt_test_unsafe_write_login", 1, false), tenant.WorkerRole, "pg_write_all_data"},
		{as(t, "dt_test_unsafe_program_login", 1, false), tenant.WorkerRole, "pg_execute_server_program"},
		{as(t, "dt_test_unsafe_fnowner_login", 1, false), tenant.WorkerRole, "owns database objects"},
		{as(t, "dt_test_unsafe_grant_login", 1, false), tenant.WorkerRole, "has privileges granted directly"},
	} {
		if err := tenant.CheckRuntimeRole(ctx, c.pool, c.want); !errors.Is(err, tenant.ErrUnsafeRole) || !strings.Contains(err.Error(), c.why) {
			t.Errorf("want ErrUnsafeRole (%s), got %v", c.why, err)
		}
	}
}

// A cluster upgraded from PG14 or earlier still grants CREATE on schema
// public to PUBLIC. That grant is not recorded per role, so it is checked
// separately: a runtime login that can create objects anywhere is refused.
func TestRuntimeLoginCannotCreate(t *testing.T) {
	adm := admin(t)
	ctx := context.Background()
	if _, err := adm.Exec(ctx, `GRANT CREATE ON SCHEMA public TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	revoked := false
	revoke := func() {
		if !revoked {
			if _, err := adm.Exec(ctx, `REVOKE CREATE ON SCHEMA public FROM PUBLIC`); err != nil {
				t.Fatal(err)
			}
			revoked = true
		}
	}
	t.Cleanup(revoke)
	err := tenant.CheckRuntimeRole(ctx, as(t, apiLogin, 1, false), tenant.APIRole)
	if !errors.Is(err, tenant.ErrUnsafeRole) || !strings.Contains(err.Error(), "can create objects in schema public") {
		t.Errorf("PUBLIC CREATE on schema public: %v", err)
	}
	revoke()
	if err := tenant.CheckRuntimeRole(ctx, as(t, apiLogin, 1, false), tenant.APIRole); err != nil {
		t.Errorf("after the revoke: %v", err)
	}
}

// T-008: a verified identity resolves to its user and nothing else. The
// users RLS policy (identity_self) lets a transaction see only the row of
// the issuer and subject it set, and those settings do not outlive the
// lookup; there is no user for an unknown identity, and none is created.
func TestResolveUser(t *testing.T) {
	adm := admin(t)
	a, b := newOrg(t, adm), newOrg(t, adm)
	pool := as(t, apiLogin, 1, true)
	d := tenant.New(pool)
	ctx := context.Background()
	const issuer = "https://test.invalid"
	for _, o := range []org{a, b} {
		if id, err := d.ResolveUser(ctx, issuer, o.subject); err != nil || id != o.user {
			t.Errorf("resolve %s: %q %v", o.subject, id, err)
		}
	}
	for name, c := range map[string][2]string{
		"unknown subject":         {issuer, newID(t, "sub")},
		"same subject, other iss": {"https://other.invalid", a.subject},
		"issuer with a slash":     {issuer + "/", a.subject},
		"subject in another case": {issuer, strings.ToLower(a.subject)},
		"SQL in the subject":      {issuer, "x' OR '1'='1"},
		"LIKE pattern in subject": {issuer, "%"},
	} {
		if id, err := d.ResolveUser(ctx, c[0], c[1]); !errors.Is(err, tenant.ErrUnknownUser) || id != "" {
			t.Errorf("%s: %q %v", name, id, err)
		}
	}
	for name, c := range map[string][2]string{
		"empty issuer":   {"", a.subject},
		"empty subject":  {issuer, ""},
		"long subject":   {issuer, strings.Repeat("s", 256)},
		"long issuer":    {"https://" + strings.Repeat("i", 2048), a.subject},
		"NUL in subject": {issuer, a.subject + "\x00"},
		"NUL in issuer":  {issuer + "\x00", a.subject},
		"invalid UTF-8":  {issuer, "sub-\xff"},
		"invalid issuer": {"https://\xc3", a.subject},
	} {
		if _, err := d.ResolveUser(ctx, c[0], c[1]); !errors.Is(err, tenant.ErrInvalidIdentity) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The longest identity that fits is accepted (and unknown).
	if _, err := d.ResolveUser(ctx, "https://"+strings.Repeat("i", 2040), strings.Repeat("s", 255)); !errors.Is(err, tenant.ErrUnknownUser) {
		t.Errorf("longest identity: %v", err)
	}
	// The settings are transaction-local by themselves: on a pool without
	// the release guard the reused connection carries no identity and sees
	// no user (review #2).
	bare := as(t, apiLogin, 1, false)
	if id, err := tenant.New(bare).ResolveUser(ctx, issuer, a.subject); err != nil || id != a.user {
		t.Fatalf("resolve on the unguarded pool: %q %v", id, err)
	}
	var iss, sub string
	var n int
	if err := bare.QueryRow(ctx, `SELECT coalesce(current_setting('app.oidc_issuer', true), ''), coalesce(current_setting('app.oidc_subject', true), ''),
		(SELECT count(*) FROM twin.users)`).Scan(&iss, &sub, &n); err != nil || iss != "" || sub != "" || n != 0 {
		t.Errorf("after the lookup the reused connection has issuer %q, subject %q and sees %d users (%v), want none", iss, sub, n, err)
	}
	// The application filter holds without RLS too: a superuser, whom RLS
	// does not restrict, still resolves only the exact issuer and subject.
	super := tenant.New(adm)
	if id, err := super.ResolveUser(ctx, issuer, a.subject); err != nil || id != a.user {
		t.Errorf("superuser resolve: %q %v", id, err)
	}
	for name, c := range map[string][2]string{
		"same subject, other iss": {"https://other.invalid", a.subject},
		"same issuer, no subject": {issuer, newID(t, "sub")},
	} {
		if id, err := super.ResolveUser(ctx, c[0], c[1]); !errors.Is(err, tenant.ErrUnknownUser) {
			t.Errorf("superuser, %s: %q %v", name, id, err)
		}
	}
	// The policy itself: with a's identity set, every user row but a's is invisible.
	var seen []string
	err := postgres.OpenPool(pool).Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer', $1, true), set_config('app.oidc_subject', $2, true)`, issuer, a.subject); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM twin.users`)
		if err != nil {
			return err
		}
		seen, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil || len(seen) != 1 || seen[0] != a.user {
		t.Errorf("users visible to a's identity: %v %v", seen, err)
	}
	// No user is ever created by the API login, even in a read-write
	// transaction with a matching identity (review #10): users are
	// provisioned elsewhere.
	if err := postgres.OpenPool(pool).Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer', $1, true), set_config('app.oidc_subject', 'new', true)`, issuer); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO twin.users(id,oidc_issuer,oidc_subject) VALUES ($1,$2,'new')`, newID(t, "usr"), issuer)
		return err
	}); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("the API login inserting a user: %v, want permission denied", err)
	}
}
