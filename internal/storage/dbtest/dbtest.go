// Package dbtest is test infrastructure for integration tests against the
// disposable database of `make db-test` (never linked into production; the
// architecture check enforces it). Without GEMINUS_TEST_DATABASE_URL the
// tests skip with NOT RUN, unless GEMINUS_REQUIRE_DB_TESTS=1 makes that a
// failure. It refuses any database whose name does not start with dt_test_.
package dbtest

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

// APILogin and WorkerLogin are plain logins in twin_api and twin_worker (no
// superuser, no BYPASSRLS).
const (
	APILogin    = "dt_test_api_login"
	WorkerLogin = "dt_test_worker_login"
)

// AdminURL returns the provisioning URL or skips the test.
func AdminURL(t testing.TB) string {
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

// Pool opens a pool on rawURL; guard installs tenant.Guard on release.
func Pool(t testing.TB, rawURL string, maxConns int32, guard bool) *pgxpool.Pool {
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

// Admin returns a pool on the provisioning login after checking that the
// database is disposable and migrated, with the runtime logins created.
func Admin(t testing.TB) *pgxpool.Pool {
	t.Helper()
	p := Pool(t, AdminURL(t), 4, false)
	var name string
	var schema bool
	if err := p.QueryRow(context.Background(),
		`SELECT current_database(), EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='twin')`).Scan(&name, &schema); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "dt_test_") || !schema {
		t.Fatalf("refusing database %q (twin schema: %v)", name, schema)
	}
	// Roles are cluster-wide: another test may create the login at the same
	// moment, which is not an error.
	for login, role := range map[string]string{APILogin: "twin_api", WorkerLogin: "twin_worker"} {
		if _, err := p.Exec(context.Background(), `DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+login+`') THEN
				CREATE ROLE `+login+` LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB IN ROLE `+role+`;
			END IF;
			EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
			END $$`); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// LoginURL is the provisioning URL with login as the user (the disposable
// clusters use trust authentication, so there is no password).
func LoginURL(t testing.TB, login string) string {
	t.Helper()
	u, err := url.Parse(AdminURL(t))
	if err != nil {
		t.Fatal("invalid GEMINUS_TEST_DATABASE_URL")
	}
	u.User = url.User(login)
	return u.String()
}

// Member is a provisioned user of an organization.
type Member struct {
	UserID, Subject string
	Role            tenant.Role
}

// Org is a provisioned organization.
type Org struct {
	ID      string
	Members map[tenant.Role]Member
}

// NewOrg commits an organization with one member per role, as the
// controlled provisioning path would (D-008: never through the API). Users
// have the given OIDC issuer and random subjects.
func NewOrg(t testing.TB, admin *pgxpool.Pool, issuer string, roles ...tenant.Role) Org {
	t.Helper()
	o := Org{ID: ids.New("org"), Members: map[tenant.Role]Member{}}
	err := postgres.OpenPool(admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		// The owner role is under FORCE RLS too: each row is written with
		// the scope settings its policy reads.
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, o.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO twin.organizations(id,organization_id,name) VALUES ($1,$1,'Test org')`, o.ID); err != nil {
			return err
		}
		for _, r := range roles {
			m := Member{UserID: ids.New("usr"), Subject: "sub-" + ids.New("x"), Role: r}
			if err := insertUser(ctx, tx, m, issuer); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ($1,$2,$3)`, o.ID, m.UserID, string(r)); err != nil {
				return err
			}
			o.Members[r] = m
		}
		return nil
	})
	if err != nil {
		t.Fatalf("provisioning an organization: %v", err)
	}
	return o
}

// NewUser commits a user without any membership.
func NewUser(t testing.TB, admin *pgxpool.Pool, issuer string) Member {
	t.Helper()
	m := Member{UserID: ids.New("usr"), Subject: "sub-" + ids.New("x")}
	err := postgres.OpenPool(admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		return insertUser(ctx, tx, m, issuer)
	})
	if err != nil {
		t.Fatalf("provisioning a user: %v", err)
	}
	return m
}

func insertUser(ctx context.Context, tx pgx.Tx, m Member, issuer string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer',$1,true), set_config('app.oidc_subject',$2,true), set_config('app.user_id',$3,true)`,
		issuer, m.Subject, m.UserID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO twin.users(id,oidc_issuer,oidc_subject) VALUES ($1,$2,$3)`, m.UserID, issuer, m.Subject)
	return err
}
