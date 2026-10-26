package proposals_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
)

// T-056 (migration 0017): a photo proposal's image is recorded with it, in
// the transaction that creates it, and read with it in its organization
// only (the application filter holds by itself: as the provisioning
// superuser, whom RLS does not restrict); a proposal asked for in words
// has none.
func TestAnImageIsReadWithItsProposal(t *testing.T) {
	pool := dbtest.Admin(t)
	admin := postgres.OpenPool(pool)
	org := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	other := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	mdl, mv, idPool := version(t, admin, org)
	user := org.Members[tenant.Owner].UserID
	ctx := context.Background()
	photo, words := ids.New("aip"), ids.New("aip")
	key, err := objects.NewKey(org.ID, objects.Assets, "proposal-images", photo, "image")
	if err != nil {
		t.Fatal(err)
	}
	im := proposals.Image{ObjectKey: key, SHA256: "sha256:" + strings.Repeat("a", 64), Size: 21843, MediaType: "image/png", Width: 1200, Height: 520}
	insert := func(ctx context.Context, tx pgx.Tx, id, intent string) error {
		_, err := proposals.Insert(ctx, tx, proposals.Proposal{ID: id, OrganizationID: org.ID, ModelID: mdl, BaseVersionID: mv,
			Mode: "edit", Intent: intent, OutputLocale: "en", IDPool: idPool, CreatedBy: user})
		return err
	}
	if err := admin.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := insert(ctx, tx, photo, ""); err != nil {
			return err
		}
		if err := proposals.InsertImage(ctx, tx, org.ID, photo, im); err != nil {
			return err
		}
		return insert(ctx, tx, words, "raise")
	}); err != nil {
		t.Fatal(err)
	}
	// Another organization cannot attach an image to this organization's
	// proposal.
	probe := ids.New("aip")
	err = admin.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := insert(ctx, tx, probe, ""); err != nil {
			return err
		}
		otherKey, _ := objects.NewKey(other.ID, objects.Assets, "proposal-images", probe, "image")
		return proposals.InsertImage(ctx, tx, other.ID, probe, proposals.Image{ObjectKey: otherKey, SHA256: im.SHA256, Size: 1,
			MediaType: "image/png", Width: 64, Height: 64})
	})
	if err == nil {
		t.Error("an image was recorded for another organization's proposal")
	}
	if err := admin.Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		p, err := proposals.Get(ctx, tx, org.ID, photo)
		if err != nil {
			return err
		}
		if p.Image == nil || *p.Image != im || p.Intent != "" {
			t.Errorf("the photo proposal: %+v", p.Image)
		}
		if p, err := proposals.Get(ctx, tx, org.ID, words); err != nil || p.Image != nil {
			t.Errorf("the proposal in words: %+v %v", p.Image, err)
		}
		if _, err := proposals.Get(ctx, tx, org.ID, probe); !errors.Is(err, proposals.ErrNotFound) {
			t.Errorf("the probe was kept: %v", err)
		}
		list, err := proposals.List(ctx, tx, org.ID, mdl, nil, 10)
		if err != nil || len(list) != 2 {
			t.Fatalf("%d %v", len(list), err)
		}
		withImage := 0
		for _, p := range list {
			if p.Image != nil {
				withImage++
			}
		}
		if withImage != 1 {
			t.Errorf("%d listed proposals have an image", withImage)
		}
		if _, err := proposals.Get(ctx, tx, other.ID, photo); !errors.Is(err, proposals.ErrNotFound) {
			t.Errorf("the photo proposal in another organization: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
