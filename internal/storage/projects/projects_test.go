package projects_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/storage/projects"
	"github.com/kcl1104/geminus/internal/tenant"
)

// The organization filter holds by itself: as the provisioning superuser,
// whom RLS does not restrict, each organization still gets only its own
// projects (AGENTS.md: application filter next to RLS).
func TestOrganizationFilter(t *testing.T) {
	admin := dbtest.Admin(t)
	a := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner)
	b := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner)
	ctx := context.Background()
	var inA, inB projects.Project
	err := postgres.OpenPool(admin).Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if inA, err = projects.Insert(ctx, tx, projects.Project{ID: ids.New("prj"), OrganizationID: a.ID, Name: "A", CreatedBy: a.Members[tenant.Owner].UserID}); err != nil {
			return err
		}
		inB, err = projects.Insert(ctx, tx, projects.Project{ID: ids.New("prj"), OrganizationID: b.ID, Name: "B", CreatedBy: b.Members[tenant.Owner].UserID})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = postgres.OpenPool(admin).Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		for _, after := range []*projects.Position{nil, {ID: "prj_00000000000000000000000000"}} {
			list, err := projects.List(ctx, tx, a.ID, after, 10)
			if err != nil {
				return err
			}
			if len(list) != 1 || list[0].ID != inA.ID {
				t.Errorf("a's list after %v: %+v", after, list)
			}
		}
		if _, err := projects.Get(ctx, tx, a.ID, inB.ID); !errors.Is(err, projects.ErrNotFound) {
			t.Errorf("b's project in a: %v", err)
		}
		if p, err := projects.Get(ctx, tx, b.ID, inB.ID); err != nil || p.Name != "B" || p.CreatedBy != b.Members[tenant.Owner].UserID {
			t.Errorf("b's project in b: %+v %v", p, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Keyset paging is by (created_at, id): projects created in one
// transaction share created_at, and a page boundary between them neither
// repeats nor skips one.
func TestKeysetTies(t *testing.T) {
	admin := dbtest.Admin(t)
	a := dbtest.NewOrg(t, admin, "https://test.invalid", tenant.Owner)
	ctx := context.Background()
	var want []string
	err := postgres.OpenPool(admin).Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for range 4 {
			p, err := projects.Insert(ctx, tx, projects.Project{ID: ids.New("prj"), OrganizationID: a.ID, Name: "Tied", CreatedBy: a.Members[tenant.Owner].UserID})
			if err != nil {
				return err
			}
			want = append(want, p.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	var got []string
	err = postgres.OpenPool(admin).Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var after *projects.Position
		for range 6 {
			page, err := projects.List(ctx, tx, a.ID, after, 1)
			if err != nil || len(page) == 0 {
				return err
			}
			if len(page) > 1 {
				t.Errorf("limit 1 returned %d rows", len(page))
			}
			got = append(got, page[0].ID)
			after = &projects.Position{CreatedAt: page[0].CreatedAt, ID: page[0].ID}
		}
		return nil
	})
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("paged %v, want %v (%v)", got, want, err)
	}
}
