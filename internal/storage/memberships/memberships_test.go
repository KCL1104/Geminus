package memberships_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/memberships"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

// The organization filter alone keeps every statement in its organization:
// run as the provisioning superuser, whom RLS does not restrict, a's
// organization reads, changes and removes none of b's memberships
// (AGENTS.md: application filter next to RLS).
func TestOrganizationFilter(t *testing.T) {
	admin := dbtest.Admin(t)
	a := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner)
	b := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner, tenant.Viewer)
	bViewer := b.Members[tenant.Viewer].UserID
	db := postgres.OpenPool(admin)
	ctx := context.Background()
	err := db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, lock := range []bool{false, true} {
			if _, err := memberships.Get(ctx, tx, a.ID, bViewer, lock); !errors.Is(err, memberships.ErrNotFound) {
				t.Errorf("b's member in a (lock %v): %v", lock, err)
			}
		}
		if m, err := memberships.Get(ctx, tx, b.ID, bViewer, false); err != nil || m.Role != "viewer" {
			t.Errorf("b's member in b: %+v %v", m, err)
		}
		list, err := memberships.List(ctx, tx, a.ID, nil, 10)
		if err != nil || len(list) != 1 || list[0].UserID != a.Members[tenant.Owner].UserID {
			t.Errorf("a's members: %+v %v", list, err)
		}
		after := "usr_00000000000000000000000000"
		if list, err := memberships.List(ctx, tx, a.ID, &after, 10); err != nil || len(list) != 1 {
			t.Errorf("a's members after a cursor: %+v %v", list, err)
		}
		if _, err := memberships.SetRole(ctx, tx, a.ID, bViewer, "owner"); !errors.Is(err, memberships.ErrNotFound) {
			t.Errorf("changing b's member in a: %v", err)
		}
		if err := memberships.Delete(ctx, tx, a.ID, bViewer); !errors.Is(err, memberships.ErrNotFound) {
			t.Errorf("removing b's member in a: %v", err)
		}
		if m, err := memberships.Get(ctx, tx, b.ID, bViewer, false); err != nil || m.Role != "viewer" {
			t.Errorf("b's member after a's attempts: %+v %v", m, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// OfUser (D-045): the user filter alone keeps the list to the user's own
// memberships (run as the provisioning superuser, whom RLS does not
// restrict), ordered by organization name, then ID, in byte order whatever
// the database's collation (CI's is en_US: there "acme" sorts before
// "Zeta"), and limit bounds it.
func TestOfUser(t *testing.T) {
	admin := dbtest.Admin(t)
	user := dbtest.NewUser(t, admin, "https://test.invalid")
	type org struct{ id, name, role string }
	var want []org
	for i, name := range []string{"Zeta", "acme", "Acme", "Acme", "Ärzte", "_Underscore", "研發部", "Acme ", "a", "Z"} {
		o := dbtest.NewOrgNamed(t, admin, "https://test.invalid", name, tenant.Owner) // another user owns it
		role := []tenant.Role{tenant.Owner, tenant.Editor, tenant.Viewer}[i%3]
		dbtest.Join(t, admin, o.ID, user.UserID, role)
		want = append(want, org{o.ID, name, string(role)})
	}
	dbtest.NewOrgNamed(t, admin, "https://test.invalid", "Aardvark", tenant.Owner) // not the user's
	// Equal names inserted in descending ID order: only the ID tie-break
	// orders them (a scan returns rows as inserted).
	same := []string{ids.New("org"), ids.New("org"), ids.New("org"), ids.New("org")}
	slices.Sort(same)
	slices.Reverse(same)
	for _, id := range same {
		if _, err := admin.Exec(context.Background(), `INSERT INTO twin.organizations(id,organization_id,name) VALUES ($1,$1,'Same')`, id); err != nil {
			t.Fatal(err)
		}
		dbtest.Join(t, admin, id, user.UserID, tenant.Viewer)
		want = append(want, org{id, "Same", "viewer"})
	}
	slices.SortFunc(want, func(a, b org) int { return cmp.Or(cmp.Compare(a.name, b.name), cmp.Compare(a.id, b.id)) })
	err := postgres.OpenPool(admin).Tx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		for _, limit := range []int{len(want) + 5, len(want), 3, 1} {
			got, err := memberships.OfUser(ctx, tx, user.UserID, limit)
			if err != nil {
				return err
			}
			n := min(limit, len(want))
			if len(got) != n {
				t.Errorf("limit %d: %d rows, want %d", limit, len(got), n)
				continue
			}
			for i, g := range got {
				if w := want[i]; g.OrganizationID != w.id || g.OrganizationName != w.name || g.Role != w.role {
					t.Errorf("limit %d, row %d: %+v, want %+v", limit, i, g, w)
				}
			}
		}
		if got, err := memberships.OfUser(ctx, tx, dbtest.NewUser(t, admin, "https://test.invalid").UserID, 10); err != nil || len(got) != 0 {
			t.Errorf("a user without memberships: %+v %v", got, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Keyset pages start after the given user ID, not at it.
func TestListAfter(t *testing.T) {
	admin := dbtest.Admin(t)
	o := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner, tenant.Editor, tenant.Viewer)
	err := postgres.OpenPool(admin).Tx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		all, err := memberships.List(ctx, tx, o.ID, nil, 10)
		if err != nil || len(all) != 3 {
			t.Fatalf("all: %+v %v", all, err)
		}
		rest, err := memberships.List(ctx, tx, o.ID, &all[0].UserID, 10)
		if err != nil || len(rest) != 2 || rest[0].UserID != all[1].UserID {
			t.Errorf("after the first: %+v %v", rest, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
