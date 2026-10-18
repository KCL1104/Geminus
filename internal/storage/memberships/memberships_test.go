package memberships_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

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
