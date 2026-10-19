package workerscope_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
)

// The allowlist holds organization IDs, each once.
func TestNewRefusesABadAllowlist(t *testing.T) {
	a, b := ids.New("org"), ids.New("org")
	for name, assigned := range map[string][]string{
		"not an organization ID": {a, "org_bad"},
		"a user ID":              {ids.New("usr")},
		"listed twice":           {a, b, a},
	} {
		if _, err := workerscope.New(nil, assigned); !errors.Is(err, tenant.ErrInvalidScope) {
			t.Errorf("%s: %v", name, err)
		}
	}
	w, err := workerscope.New(nil, []string{b, a})
	if err != nil {
		t.Fatal(err)
	}
	got := w.Assigned()
	got[0] = "changed"
	if w.Assigned()[0] != b || w.Assigned()[1] != a {
		t.Errorf("Assigned is not the deployment order, or shares its slice: %v", w.Assigned())
	}
}

const issuer = "https://test.invalid"

// D-008: the worker serves its assigned organizations only, one per
// transaction, as the worker role (no membership reads), and leaves no
// scope on the pooled connection.
func TestAssignment(t *testing.T) {
	ctx := context.Background()
	admin := dbtest.Admin(t)
	a, b := dbtest.NewOrg(t, admin, issuer, tenant.Owner), dbtest.NewOrg(t, admin, issuer, tenant.Owner)
	projects := map[string]string{a.ID: ids.New("prj"), b.ID: ids.New("prj")}
	for _, o := range []dbtest.Org{a, b} {
		if _, err := admin.Exec(ctx, `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`,
			projects[o.ID], o.ID, o.Members[tenant.Owner].UserID); err != nil {
			t.Fatal(err)
		}
	}
	pool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 1, true)
	if err := tenant.CheckRuntimeRole(ctx, pool, tenant.WorkerRole); err != nil {
		t.Fatal(err)
	}
	w, err := workerscope.New(pool, []string{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := w.Run(ctx, b.ID, pgx.TxOptions{}, func(context.Context, pgx.Tx) error { called = true; return nil }); !errors.Is(err, workerscope.ErrNotAssigned) || called {
		t.Errorf("an unassigned organization: %v (fn called: %v)", err, called)
	}
	var seen []string
	if err := w.Run(ctx, a.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM twin.projects WHERE id = ANY($1)`, []string{projects[a.ID], projects[b.ID]})
		if err != nil {
			return err
		}
		seen, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	}); err != nil || len(seen) != 1 || seen[0] != projects[a.ID] {
		t.Errorf("the assigned scope sees %v (%v)", seen, err)
	}
	if err := w.Run(ctx, a.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT count(*) FROM twin.memberships`)
		return err
	}); !errors.Is(err, postgres.ErrPermission) {
		t.Errorf("the worker reads memberships: %v", err)
	}
	// One connection: what the next checkout sees is what Run left.
	var org string
	if err := pool.QueryRow(ctx, `SELECT coalesce(current_setting('app.organization_id', true), '')`).Scan(&org); err != nil || org != "" {
		t.Errorf("the scope outlived the transaction: %q (%v)", org, err)
	}
}
