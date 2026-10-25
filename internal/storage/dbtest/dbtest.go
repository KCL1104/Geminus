// Package dbtest is test infrastructure for integration tests against the
// disposable database of `make db-test` (never linked into production; the
// architecture check enforces it). Without GEMINUS_TEST_DATABASE_URL the
// tests skip with NOT RUN, unless GEMINUS_REQUIRE_DB_TESTS=1 makes that a
// failure. It refuses any database whose name does not start with dt_test_.
package dbtest

import (
	"context"
	"encoding/json/v2"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model/hashing"
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
	return NewOrgNamed(t, admin, issuer, "Test org", roles...)
}

// RefuseAudit makes the database refuse every audit event of org, until
// the function it returns (run at the test's cleanup as well) drops the
// refusal: a BEFORE INSERT trigger on twin.audit_events, created as the
// table's owner, that raises for org's rows only, so other tests' events
// are untouched. T-035 (D-091 (c)): an action whose audit event cannot be
// written must commit nothing.
func RefuseAudit(t testing.TB, admin *pgxpool.Pool, org string) (stop func()) {
	t.Helper()
	if !strings.HasPrefix(org, "org_") || strings.ContainsFunc(org, func(r rune) bool {
		return !(r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) {
		t.Fatalf("not an organization ID: %q", org)
	}
	name := "twin.zz_test_refuse_audit_" + strings.ToLower(org)
	owner := func(sql string) error {
		return postgres.OpenPool(admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, sql)
			return err
		})
	}
	if err := owner(`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
		BEGIN
		  IF NEW.organization_id::text = '` + org + `' THEN
		    RAISE EXCEPTION 'the test refuses this audit event' USING ERRCODE = 'P0001';
		  END IF;
		  RETURN NEW;
		END $$`); err != nil {
		t.Fatal(err)
	}
	trigger := strings.TrimPrefix(name, "twin.")
	if err := owner(`CREATE TRIGGER ` + trigger + ` BEFORE INSERT ON twin.audit_events FOR EACH ROW EXECUTE FUNCTION ` + name + `()`); err != nil {
		t.Fatal(err)
	}
	stop = sync.OnceFunc(func() {
		if err := owner(`DROP TRIGGER IF EXISTS ` + trigger + ` ON twin.audit_events`); err != nil {
			t.Error(err)
		}
		if err := owner(`DROP FUNCTION IF EXISTS ` + name + `()`); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(stop)
	return stop
}

// Join commits user's membership of org with role, as provisioning or an
// owner would.
func Join(t testing.TB, admin *pgxpool.Pool, org, user string, role tenant.Role) {
	t.Helper()
	err := postgres.OpenPool(admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_owner`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, org); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ($1,$2,$3)`, org, user, string(role))
		return err
	})
	if err != nil {
		t.Fatalf("adding a membership: %v", err)
	}
}

// NewOrgNamed is NewOrg for an organization with the given name.
func NewOrgNamed(t testing.TB, admin *pgxpool.Pool, issuer, name string, roles ...tenant.Role) Org {
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
		if _, err := tx.Exec(ctx, `INSERT INTO twin.organizations(id,organization_id,name) VALUES ($1,$1,$2)`, o.ID, name); err != nil {
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

// NewImport inserts, as the provisioning login, a project, a model with its
// first version (the minimal contract IR, in canonical form with its hash)
// and an uploaded import in o, and returns the import's ID: a resource row
// for an import job. o needs an owner.
func NewImport(t testing.TB, admin *pgxpool.Pool, o Org) string {
	t.Helper()
	owner, ok := o.Members[tenant.Owner]
	if !ok {
		t.Fatal("dbtest.NewImport needs an organization with an owner")
	}
	prj, mdl, mv, imp := ids.New("prj"), ids.New("mdl"), ids.New("mv"), ids.New("imp")
	var doc map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", "valid-minimal-ir.json")), &doc); err != nil {
		t.Fatal(err)
	}
	doc["model_id"] = mdl
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	ir, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	err = postgres.OpenPool(admin).Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, []any{prj, o.ID, owner.UserID}},
			{`INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by) VALUES ($1,$2,$3,'M','software_delivery',$4)`, []any{mdl, o.ID, prj, owner.UserID}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,created_by) VALUES ($1,$2,$3,1,'0.1',$4,$5,$6)`,
				[]any{mv, o.ID, mdl, string(ir), hashing.Sum(ir), owner.UserID}},
			{`UPDATE twin.models SET latest_version_id=$1 WHERE id=$2`, []any{mv, mdl}},
			{`INSERT INTO twin.imports(id,organization_id,project_id,model_id,base_version_id,kind,filename,upload_object_key,upload_content_hash,size_bytes,status,created_by)
				VALUES ($1,$2,$3,$4,$5,'csv_events','events.csv',$6,$7,10,'UPLOADED',$8)`,
				[]any{imp, o.ID, prj, mdl, mv, "tenant/" + o.ID + "/imports/" + imp + ".csv", hashing.Sum([]byte(imp)), owner.UserID}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return imp
}
