package provisioning_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/storage/provisioning"
	"github.com/kcl1104/geminus/internal/tenant"
)

const issuer = "https://test.invalid"

// Operator is a login that may act as twin_owner and nothing else: what an
// operator provisions with (db/README.md).
const Operator = "dt_test_provisioner_login"

func operator(t *testing.T) *postgres.DB {
	t.Helper()
	admin := dbtest.Admin(t)
	if _, err := admin.Exec(context.Background(), `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+Operator+`') THEN
			CREATE ROLE `+Operator+` LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB IN ROLE twin_provisioner;
		END IF;
		EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
		END $$`); err != nil {
		t.Fatal(err)
	}
	return postgres.OpenPool(dbtest.Pool(t, dbtest.LoginURL(t, Operator), 2, false))
}

// login creates (once) a login with the given attributes and IN ROLE list.
func login(t *testing.T, name, attrs string) *postgres.DB {
	t.Helper()
	if _, err := dbtest.Admin(t).Exec(context.Background(), `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+name+`') THEN
			CREATE ROLE `+name+` LOGIN `+attrs+`;
		END IF;
		EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
		END $$`); err != nil {
		t.Fatal(err)
	}
	return postgres.OpenPool(dbtest.Pool(t, dbtest.LoginURL(t, name), 1, false))
}

// Only an operator login provisions: one that may act as twin_provisioner
// and is nothing else. The schema owner, the API, the worker, a superuser
// (a member of every role), a provisioning login that is also one of them,
// one with more attributes than it needs and one without the role are
// refused (D-008, D-042, TEST-011; T-010 review L2, I3).
func TestCheckOperator(t *testing.T) {
	ctx := context.Background()
	if err := provisioning.CheckOperator(ctx, operator(t)); err != nil {
		t.Errorf("the operator login: %v", err)
	}
	if err := provisioning.CheckOperator(ctx, login(t, "dt_test_prov_inherit", "INHERIT IN ROLE twin_provisioner")); err != nil {
		t.Errorf("an INHERIT member of twin_provisioner: %v", err)
	}
	refused := map[string]*postgres.DB{
		"a superuser":                     postgres.OpenPool(dbtest.Admin(t)),
		"the API login":                   postgres.OpenPool(dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 1, false)),
		"the worker login":                login(t, "dt_test_worker_only", "NOINHERIT IN ROLE twin_worker"),
		"a login without the role":        login(t, "dt_test_plain_login", "NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB"),
		"a member of the schema owner":    login(t, "dt_test_prov_schema_owner", "NOINHERIT IN ROLE twin_owner"),
		"a provisioner that is the owner": login(t, "dt_test_prov_owner", "NOINHERIT IN ROLE twin_provisioner, twin_owner"),
		"a provisioner that is a worker":  login(t, "dt_test_prov_worker", "NOINHERIT IN ROLE twin_provisioner, twin_worker"),
		"a provisioner that is the API":   login(t, "dt_test_prov_api", "NOINHERIT IN ROLE twin_provisioner, twin_api"),
		"a provisioner with BYPASSRLS":    login(t, "dt_test_prov_bypassrls", "NOINHERIT BYPASSRLS IN ROLE twin_provisioner"),
		"a provisioner with CREATEROLE":   login(t, "dt_test_prov_createrole", "NOINHERIT CREATEROLE IN ROLE twin_provisioner"),
		"a provisioner with CREATEDB":     login(t, "dt_test_prov_createdb", "NOINHERIT CREATEDB IN ROLE twin_provisioner"),
		"a provisioner with REPLICATION":  login(t, "dt_test_prov_replication", "NOINHERIT REPLICATION IN ROLE twin_provisioner"),
	}
	// A member that inherits the role's rights but may not SET ROLE to it:
	// provisioning switches to the role, so it needs SET (PostgreSQL 16).
	inheritOnly := login(t, "dt_test_prov_no_set", "NOINHERIT")
	if _, err := dbtest.Admin(t).Exec(ctx, `GRANT twin_provisioner TO dt_test_prov_no_set WITH INHERIT TRUE, SET FALSE`); err != nil {
		t.Fatal(err)
	}
	refused["a member that cannot SET ROLE"] = inheritOnly
	for name, db := range refused {
		if err := provisioning.CheckOperator(ctx, db); !errors.Is(err, provisioning.ErrNotOperator) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Provisioning acts as twin_provisioner, which cannot change what it
// created or touch anything else, even through the operator login (D-042;
// the full privilege matrix is in db/tests/roles.sql).
func TestProvisionerCannotChangeAnything(t *testing.T) {
	ctx := context.Background()
	db := operator(t)
	org, owner, err := provisioning.Organization(ctx, db, "Acme", issuer, "sub-"+ids.New("x"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE twin.memberships SET role='viewer' WHERE organization_id=current_setting('app.organization_id')`,
		`DELETE FROM twin.memberships WHERE organization_id=current_setting('app.organization_id')`,
		`UPDATE twin.organizations SET name='x' WHERE id=current_setting('app.organization_id')`,
		`SELECT count(*) FROM twin.memberships`,
		`SELECT count(*) FROM twin.audit_events`,
		`ALTER TABLE twin.memberships DISABLE ROW LEVEL SECURITY`,
	} {
		err := db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SET LOCAL ROLE twin_provisioner`); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id',$1,true)`, org); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, stmt)
			return err
		})
		if !errors.Is(err, postgres.ErrPermission) {
			t.Errorf("%s: %v", stmt, err)
		}
	}
	// The operator login itself (NOINHERIT) has no rights without SET ROLE.
	err = db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO twin.memberships(organization_id,user_id,role) VALUES ($1,$2,'viewer')`, org, owner)
		return err
	})
	if !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("the operator login without SET ROLE: %v", err)
	}
}

// The API login cannot create users or organizations, even with the scope
// settings their policies read: provisioning is not an API capability
// (D-008, TEST-011).
func TestAPICannotProvision(t *testing.T) {
	ctx := context.Background()
	api := postgres.OpenPool(dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 1, false))
	for _, stmt := range []string{
		`INSERT INTO twin.users(id,oidc_issuer,oidc_subject) VALUES ('usr_01M4YDQK00TEMP1ATE00000000','https://test.invalid','s')`,
		`INSERT INTO twin.organizations(id,organization_id,name) VALUES ('org_01M4YDQK00TEMP1ATE00000000','org_01M4YDQK00TEMP1ATE00000000','x')`,
	} {
		err := api.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer','https://test.invalid',true), set_config('app.oidc_subject','s',true),
				set_config('app.organization_id','org_01M4YDQK00TEMP1ATE00000000',true)`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, stmt)
			return err
		})
		if !errors.Is(err, postgres.ErrPermission) {
			t.Errorf("%s: %v", stmt, err)
		}
	}
}

// A user is created once per issuer and subject; an organization gets its
// first owner and an audit event, and that owner can work in it at once.
func TestProvision(t *testing.T) {
	ctx := context.Background()
	db := operator(t)
	subject := "sub-" + ids.New("x")
	id, created, err := provisioning.User(ctx, db, issuer, subject)
	if err != nil || !created || !strings.HasPrefix(id, "usr_") {
		t.Fatalf("user: %q %v %v", id, created, err)
	}
	again, created, err := provisioning.User(ctx, db, issuer, subject)
	if err != nil || created || again != id {
		t.Errorf("the same user again: %q %v %v", again, created, err)
	}
	org, owner, err := provisioning.Organization(ctx, db, "Acme 公司", issuer, subject)
	if err != nil || owner != id || !strings.HasPrefix(org, "org_") {
		t.Fatalf("organization: %q %q %v", org, owner, err)
	}

	apiPool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 2, true)
	tdb := tenant.New(apiPool)
	resolved, err := tdb.ResolveUser(ctx, issuer, subject)
	if err != nil || resolved != id {
		t.Fatalf("resolving the owner: %q %v", resolved, err)
	}
	err = tdb.Run(ctx, tenant.Scope{OrganizationID: org, UserID: id}, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if m.Role != tenant.Owner {
			t.Errorf("role %q", m.Role)
		}
		var action, actor string
		if err := tx.QueryRow(ctx, `SELECT action, actor_type||':'||actor_id FROM twin.audit_events WHERE organization_id=$1`, org).Scan(&action, &actor); err != nil {
			return err
		}
		if action != "organization.provision" || actor != "system:"+provisioning.Actor {
			t.Errorf("audit: %s by %s", action, actor)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []struct{ name, issuer, subject string }{
		{"", issuer, subject},
		{strings.Repeat("n", 201), issuer, subject},
		{"tab\tname", issuer, subject},
		{" ", issuer, subject},
		{" Acme", issuer, subject},
		{"Acme ", issuer, subject},
		{"Ac\u0085me", issuer, subject}, // C1 control
		{"Acme\u202e", issuer, subject}, // bidi override (format)
		{"Ac\u200bme", issuer, subject}, // zero width space (format)
		{"Ac\u00a0me", issuer, subject}, // a separator other than the ASCII space
		{"Ac\ue000me", issuer, subject}, // private use
		{"Ac\xffme", issuer, subject},   // not UTF-8
		{"Acme", "", subject},
		{"Acme", "http://test.invalid", subject},
		{"Acme", "test.invalid", subject},
		{"Acme", issuer, "a\x00b"},
		{"Acme", issuer, "a\nb"},
		{"Acme", issuer, strings.Repeat("s", 256)},
	} {
		if _, _, err := provisioning.Organization(ctx, db, bad.name, bad.issuer, bad.subject); !errors.Is(err, provisioning.ErrInvalidInput) {
			t.Errorf("%q %q %q: %v", bad.name, bad.issuer, bad.subject, err)
		}
		if bad.name == "Acme" {
			if _, _, err := provisioning.User(ctx, db, bad.issuer, bad.subject); !errors.Is(err, provisioning.ErrInvalidInput) {
				t.Errorf("user %q %q: %v", bad.issuer, bad.subject, err)
			}
		}
	}
	for _, good := range []string{"A", strings.Repeat("名", 200), "Acme 公司 (Taiwan) – R&D"} {
		if _, _, err := provisioning.Organization(ctx, db, good, issuer, subject); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

// Parallel provisioning of one identity creates one user: every call gets
// its ID and exactly one reports creating it (T-010 review L3).
func TestUserOnceUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	operator(t)
	db := postgres.OpenPool(dbtest.Pool(t, dbtest.LoginURL(t, Operator), 8, false))
	for round := range 10 {
		subject := "sub-" + ids.New("x")
		var wg sync.WaitGroup
		got, made, errs := make([]string, 8), make([]bool, 8), make([]error, 8)
		for i := range 8 {
			wg.Go(func() { got[i], made[i], errs[i] = provisioning.User(ctx, db, issuer, subject) })
		}
		wg.Wait()
		created := 0
		for i := range 8 {
			if errs[i] != nil || got[i] != got[0] {
				t.Fatalf("round %d, call %d: %q %v (first %q)", round, i, got[i], errs[i], got[0])
			}
			if made[i] {
				created++
			}
		}
		if created != 1 {
			t.Errorf("round %d: %d calls report creating the user", round, created)
		}
	}
}
