package models_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

// irFor is the golden default-delivery IR as model id's, with its hash.
func irFor(t *testing.T, id, name string) ([]byte, string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/golden-models/default-delivery.json")), &doc); err != nil {
		t.Fatal(err)
	}
	doc["model_id"], doc["name"] = id, name
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := hashing.Canonical(data)
	if err != nil {
		t.Fatal(err)
	}
	return canonical, hashing.Sum(canonical)
}

// fixture is a project with a model, its v1 and its draft, committed.
type fixture struct {
	org, user, project, model, v1 string
	ir                            []byte
	hash                          string
}

func newFixture(t *testing.T, tx pgx.Tx, org dbtest.Org) fixture {
	t.Helper()
	ctx := context.Background()
	f := fixture{org: org.ID, user: org.Members[tenant.Owner].UserID, project: ids.New("prj"), model: ids.New("mdl"), v1: ids.New("mv")}
	f.ir, f.hash = irFor(t, f.model, "Pipeline")
	if _, err := tx.Exec(ctx, `INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, f.project, f.org, f.user); err != nil {
		t.Fatal(err)
	}
	if _, err := models.InsertModel(ctx, tx, models.Model{ID: f.model, OrganizationID: f.org, ProjectID: f.project, Name: "Pipeline", Domain: "software_delivery", LatestVersionID: f.v1, CreatedBy: f.user}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.InsertVersion(ctx, tx, models.Version{ID: f.v1, OrganizationID: f.org, ModelID: f.model, Version: 1, SchemaVersion: "0.1", IR: f.ir, Hash: f.hash, CreatedBy: f.user}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.InsertDraft(ctx, tx, models.Draft{OrganizationID: f.org, ModelID: f.model, BaseVersionID: f.v1, Revision: 1, IR: f.ir, Hash: f.hash, UpdatedBy: f.user}); err != nil {
		t.Fatal(err)
	}
	return f
}

func admin(t *testing.T) (*postgres.DB, dbtest.Org, dbtest.Org) {
	pool := dbtest.Admin(t)
	return postgres.OpenPool(pool), dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner), dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
}

// The organization filter holds by itself: as the provisioning superuser,
// whom RLS does not restrict, each organization reads only its own models,
// versions and drafts (AGENTS.md: application filter next to RLS).
func TestOrganizationFilter(t *testing.T) {
	db, a, b := admin(t)
	ctx := context.Background()
	var fa, fb fixture
	if err := db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		fa, fb = newFixture(t, tx, a), newFixture(t, tx, b)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err := db.Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		if m, err := models.GetModel(ctx, tx, fa.org, fa.model); err != nil || m.ProjectID != fa.project || m.LatestVersionID != fa.v1 || m.Name != "Pipeline" {
			t.Errorf("a's model: %+v %v", m, err)
		}
		if _, err := models.GetModel(ctx, tx, fa.org, fb.model); !errors.Is(err, models.ErrNotFound) {
			t.Errorf("b's model in a: %v", err)
		}
		if _, err := models.GetVersion(ctx, tx, fa.org, fb.v1); !errors.Is(err, models.ErrNotFound) {
			t.Errorf("b's version in a: %v", err)
		}
		if _, err := models.GetDraft(ctx, tx, fa.org, fb.model); !errors.Is(err, models.ErrNotFound) {
			t.Errorf("b's draft in a: %v", err)
		}
		if list, err := models.ListModels(ctx, tx, fa.org, fb.project, nil, 10); err != nil || len(list) != 0 {
			t.Errorf("b's project listed in a: %v %v", list, err)
		}
		if list, err := models.ListVersions(ctx, tx, fa.org, fb.model, nil, 10); err != nil || len(list) != 0 {
			t.Errorf("b's versions listed in a: %v %v", list, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Writes are filtered too: a's organization cannot move b's model.
	err = db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := models.LockModel(ctx, tx, fa.org, fb.model); !errors.Is(err, models.ErrNotFound) {
			t.Errorf("locking b's model in a: %v", err)
		}
		if err := models.SetLatest(ctx, tx, fa.org, fb.model, fb.v1, "x"); !errors.Is(err, models.ErrNotFound) {
			t.Errorf("SetLatest of b's model in a: %v", err)
		}
		d, _ := models.GetDraft(ctx, tx, fb.org, fb.model)
		d.OrganizationID, d.Revision = fa.org, 2
		if _, err := models.UpdateDraft(ctx, tx, d, 1); !errors.Is(err, models.ErrStale) {
			t.Errorf("UpdateDraft of b's draft in a: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A draft write only applies at the revision the caller read: the second
// of two writers from the same revision gets ErrStale and changes nothing
// (TEST-005 at the SQL level).
func TestUpdateDraftCAS(t *testing.T) {
	db, a, _ := admin(t)
	ctx := context.Background()
	var f fixture
	if err := db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error { f = newFixture(t, tx, a); return nil }); err != nil {
		t.Fatal(err)
	}
	edited, editedHash := irFor(t, f.model, "Edited")
	issues := []model.ValidationIssue{model.NewIssue("UNREACHABLE_NODE", "/nodes/1", "dormant", model.SeverityWarning)}
	err := db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		d := models.Draft{OrganizationID: f.org, ModelID: f.model, BaseVersionID: f.v1, Revision: 2, IR: edited, Hash: editedHash, Issues: issues, UpdatedBy: f.user}
		if _, err := models.UpdateDraft(ctx, tx, d, 1); err != nil {
			return err
		}
		other := d
		other.Revision, other.IR, other.Hash = 2, f.ir, f.hash
		if _, err := models.UpdateDraft(ctx, tx, other, 1); !errors.Is(err, models.ErrStale) {
			t.Errorf("second writer from revision 1: %v", err)
		}
		got, err := models.GetDraft(ctx, tx, f.org, f.model)
		if err != nil {
			return err
		}
		if got.Revision != 2 || got.Hash != editedHash || !slices.Equal(got.Issues, issues) || got.BaseVersionID != f.v1 {
			t.Errorf("draft after the race: revision %d hash %s issues %v", got.Revision, got.Hash, got.Issues)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A version's manifest is stored twice, as the frozen JSON and as binding
// rows; the database checks at commit that they agree, so a version whose
// rows differ from its manifest never commits (API_DATABASE §8). A stored
// manifest reads back as written.
func TestVersionManifest(t *testing.T) {
	db, a, _ := admin(t)
	ctx := context.Background()
	var f fixture
	var dsv, dsv2 string
	h := "sha256:" + strings.Repeat("a", 64)
	if err := db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		f = newFixture(t, tx, a)
		ds := ids.New("ds")
		dsv, dsv2 = ids.New("dsv"), ids.New("dsv")
		if _, err := tx.Exec(ctx, `INSERT INTO twin.datasets(id,organization_id,project_id,kind,name,created_by) VALUES ($1,$2,$3,'empirical_samples','Review',$4)`, ds, f.org, f.project, f.user); err != nil {
			return err
		}
		for i, id := range []string{dsv, dsv2} {
			if _, err := tx.Exec(ctx, `INSERT INTO twin.dataset_versions(id,organization_id,dataset_id,version,object_key,content_hash,content_type,row_count,unit,created_by)
				VALUES ($1,$2,$3,$4,$5,$6,'application/json',3,'minute',$7)`, id, f.org, ds, i+1, "tenant/"+f.org+"/datasets/"+id+".json", h, f.user); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manifest := []model.DatasetBinding{
		{LogicalRef: "dataset:a-b", DatasetVersionID: dsv, ContentHash: h, Unit: model.Minute, SampleCount: 3},
		{LogicalRef: "dataset:a_b", DatasetVersionID: dsv2, ContentHash: h, Unit: model.Minute, SampleCount: 3},
	}
	v2 := ids.New("mv")
	if err := db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := models.InsertVersion(ctx, tx, models.Version{ID: v2, OrganizationID: f.org, ModelID: f.model, Version: 2, SchemaVersion: "0.1", ParentVersionID: &f.v1,
			IR: f.ir, Hash: f.hash, DatasetBindings: manifest, CreatedBy: f.user})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err := db.Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		v, err := models.GetVersion(ctx, tx, f.org, v2)
		if err != nil {
			return err
		}
		if !slices.Equal(v.DatasetBindings, manifest) || v.ParentVersionID == nil || *v.ParentVersionID != f.v1 || v.Version != 2 {
			t.Errorf("stored version: %+v", v)
		}
		list, err := models.ListVersions(ctx, tx, f.org, f.model, nil, 10)
		if err != nil || len(list) != 2 || list[0].ID != v2 || list[1].ID != f.v1 {
			t.Errorf("newest first: %v %v", list, err)
		}
		before := int64(2)
		if list, err := models.ListVersions(ctx, tx, f.org, f.model, &before, 10); err != nil || len(list) != 1 || list[0].ID != f.v1 {
			t.Errorf("before version 2: %v %v", list, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A binding row the manifest does not list makes the commit fail.
	err = db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		v3 := ids.New("mv")
		if _, err := models.InsertVersion(ctx, tx, models.Version{ID: v3, OrganizationID: f.org, ModelID: f.model, Version: 3, SchemaVersion: "0.1", ParentVersionID: &v2,
			IR: f.ir, Hash: f.hash, CreatedBy: f.user}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO twin.model_version_datasets(organization_id,model_id,model_version_id,logical_ref,dataset_version_id,content_hash,unit,sample_count)
			VALUES ($1,$2,$3,'dataset:extra',$4,$5,'minute',3)`, f.org, f.model, v3, dsv, h)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "manifest mismatch") {
		t.Errorf("a version whose rows differ from its manifest committed: %v", err)
	}
}

// Models of a project page by (created_at, id) without repeating or
// skipping one, including models created in one transaction (same
// created_at).
func TestListModelsKeyset(t *testing.T) {
	db, a, _ := admin(t)
	ctx := context.Background()
	var f fixture
	var want []string
	if err := db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		f = newFixture(t, tx, a)
		want = append(want, f.model)
		for range 4 {
			id, v := ids.New("mdl"), ids.New("mv")
			ir, hash := irFor(t, id, "M")
			if _, err := models.InsertModel(ctx, tx, models.Model{ID: id, OrganizationID: f.org, ProjectID: f.project, Name: "M", Domain: "software_delivery", LatestVersionID: v, CreatedBy: f.user}); err != nil {
				return err
			}
			if _, err := models.InsertVersion(ctx, tx, models.Version{ID: v, OrganizationID: f.org, ModelID: id, Version: 1, SchemaVersion: "0.1", IR: ir, Hash: hash, CreatedBy: f.user}); err != nil {
				return err
			}
			want = append(want, id)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(want) // one transaction: one created_at, so id decides
	var got []string
	err := db.Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var after *models.Position
		for {
			page, err := models.ListModels(ctx, tx, f.org, f.project, after, 2)
			if err != nil {
				return err
			}
			for _, m := range page {
				got = append(got, m.ID)
			}
			if len(page) < 2 {
				return nil
			}
			last := page[len(page)-1]
			after = &models.Position{CreatedAt: last.CreatedAt, ID: last.ID}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("pages %v, want %v", got, want)
	}
}
