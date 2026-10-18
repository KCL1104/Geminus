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
const Operator = "dt_test_provisioner"

func operator(t *testing.T) *postgres.DB {
	t.Helper()
	admin := dbtest.Admin(t)
	if _, err := admin.Exec(context.Background(), `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+Operator+`') THEN
			CREATE ROLE `+Operator+` LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB IN ROLE twin_owner;
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

// Only an operator login provisions: the API, the worker, a superuser (a
// member of every role), a login that is also a runtime login, one with
// more attributes than it needs and one without the owner role are refused
// (D-008, TEST-011; T-010 review L2, I3).
func TestCheckOperator(t *testing.T) {
	ctx := context.Background()
	if err := provisioning.CheckOperator(ctx, operator(t)); err != nil {
		t.Errorf("the operator login: %v", err)
	}
	if err := provisioning.CheckOperator(ctx, login(t, "dt_test_inherit_provisioner", "INHERIT IN ROLE twin_owner")); err != nil {
		t.Errorf("an INHERIT member of twin_owner: %v", err)
	}
	refused := map[string]*postgres.DB{
		"a superuser":                    postgres.OpenPool(dbtest.Admin(t)),
		"the API login":                  postgres.OpenPool(dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 1, false)),
		"the worker login":               login(t, "dt_test_worker_only", "NOINHERIT IN ROLE twin_worker"),
		"a login without the owner role": login(t, "dt_test_plain_login", "NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB"),
		"an owner that is a worker":      login(t, "dt_test_owner_worker", "NOINHERIT IN ROLE twin_owner, twin_worker"),
		"an owner that is the API":       login(t, "dt_test_owner_api", "NOINHERIT IN ROLE twin_owner, twin_api"),
		"an owner with BYPASSRLS":        login(t, "dt_test_owner_bypassrls", "NOINHERIT BYPASSRLS IN ROLE twin_owner"),
		"an owner with CREATEROLE":       login(t, "dt_test_owner_createrole", "NOINHERIT CREATEROLE IN ROLE twin_owner"),
		"an owner with CREATEDB":         login(t, "dt_test_owner_createdb", "NOINHERIT CREATEDB IN ROLE twin_owner"),
		"an owner with REPLICATION":      login(t, "dt_test_owner_replication", "NOINHERIT REPLICATION IN ROLE twin_owner"),
	}
	for name, db := range refused {
		if err := provisioning.CheckOperator(ctx, db); !errors.Is(err, provisioning.ErrNotOperator) {
			t.Errorf("%s: %v", name, err)
		}
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
