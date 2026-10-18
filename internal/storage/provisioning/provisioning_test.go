package provisioning_test

import (
	"context"
	"errors"
	"strings"
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

// Only an operator login provisions: the API, the worker and a superuser
// (a member of every role) are refused (D-008, TEST-011).
func TestCheckOperator(t *testing.T) {
	ctx := context.Background()
	if err := provisioning.CheckOperator(ctx, operator(t)); err != nil {
		t.Errorf("the operator login: %v", err)
	}
	if err := provisioning.CheckOperator(ctx, postgres.OpenPool(dbtest.Admin(t))); !errors.Is(err, provisioning.ErrNotOperator) {
		t.Errorf("a superuser: %v", err)
	}
	if err := provisioning.CheckOperator(ctx, postgres.OpenPool(dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 1, false))); !errors.Is(err, provisioning.ErrNotOperator) {
		t.Errorf("the API login: %v", err)
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
		{"Acme", "", subject},
		{"Acme", issuer, "a\x00b"},
		{"Acme", issuer, strings.Repeat("s", 256)},
	} {
		if _, _, err := provisioning.Organization(ctx, db, bad.name, bad.issuer, bad.subject); !errors.Is(err, provisioning.ErrInvalidInput) {
			t.Errorf("%q %q %q: %v", bad.name, bad.issuer, bad.subject, err)
		}
	}
}
