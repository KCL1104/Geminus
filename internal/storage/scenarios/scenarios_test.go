package scenarios_test

// DB-backed tests of the scenario SQL (T-028; TEST-009, TEST-010). Without
// GEMINUS_TEST_DATABASE_URL they skip (NOT RUN).

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/storage/scenarios"
	"github.com/kcl1104/geminus/internal/tenant"
)

const emptyPatchHash = "sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"

// versions commits, as the provisioning superuser, a project, a model and
// two versions of it (the minimal contract IR) in org, and returns the
// model and the versions.
func versions(t *testing.T, admin *postgres.DB, org dbtest.Org) (string, string, string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", "valid-minimal-ir.json")), &doc); err != nil {
		t.Fatal(err)
	}
	prj, mdl, v1, v2 := ids.New("prj"), ids.New("mdl"), ids.New("mv"), ids.New("mv")
	doc["model_id"] = mdl
	raw, _ := json.Marshal(doc)
	ir, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	user := org.Members[tenant.Owner].UserID
	if err := admin.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, []any{prj, org.ID, user}},
			{`INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by) VALUES ($1,$2,$3,'M','software_delivery',$4)`, []any{mdl, org.ID, prj, user}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,created_by) VALUES ($1,$2,$3,1,'0.1',$4,$5,$6)`,
				[]any{v1, org.ID, mdl, string(ir), hashing.Sum(ir), user}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,parent_version_id,ir,hash,created_by) VALUES ($1,$2,$3,2,'0.1',$4,$5,$6,$7)`,
				[]any{v2, org.ID, mdl, v1, string(ir), hashing.Sum(ir), user}},
			{`UPDATE twin.models SET latest_version_id=$1 WHERE id=$2`, []any{v2, mdl}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return mdl, v1, v2
}

// The organization and the version filter hold by themselves: as the
// provisioning superuser, whom RLS does not restrict, each list holds
// only its organization's scenarios on that version, in (created_at, id)
// order, a page continues after its position, and Get finds no other
// organization's scenario (AGENTS.md: application filter next to RLS).
func TestListAndGet(t *testing.T) {
	pool := dbtest.Admin(t)
	admin := postgres.OpenPool(pool)
	a := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	b := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	ctx := context.Background()
	mdl, v1, v2 := versions(t, admin, a)
	mdlB, _, vB := versions(t, admin, b)
	var onV2 []scenarios.Scenario
	var onV1, inB scenarios.Scenario
	if err := admin.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		insert := func(org dbtest.Org, model, base, name string) (scenarios.Scenario, error) {
			return scenarios.Insert(ctx, tx, scenarios.Scenario{ID: ids.New("scn"), OrganizationID: org.ID, ModelID: model, BaseVersionID: base,
				Name: name, Patch: []byte(`[]`), PatchHash: emptyPatchHash, CreatedBy: org.Members[tenant.Owner].UserID})
		}
		for _, name := range []string{"first", "second", "third"} {
			s, err := insert(a, mdl, v2, name)
			if err != nil {
				return err
			}
			onV2 = append(onV2, s)
		}
		var err error
		if onV1, err = insert(a, mdl, v1, "older"); err != nil {
			return err
		}
		inB, err = insert(b, mdlB, vB, "b's")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := admin.Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		list, err := scenarios.List(ctx, tx, a.ID, v2, nil, 10)
		if err != nil {
			return err
		}
		// One transaction: the same created_at, so the ID orders them.
		want := []string{onV2[0].ID, onV2[1].ID, onV2[2].ID}
		if len(list) != 3 {
			t.Fatalf("a's list on version 2: %+v", list)
		}
		slices.Sort(want) // ULIDs of one prefix: ASCII, the same order in every collation
		for i, s := range list {
			if s.ID != want[i] || s.OrganizationID != a.ID || s.BaseVersionID != v2 || string(s.Patch) != "[]" || s.SourceProposalID != nil {
				t.Errorf("item %d: %+v, want %s", i, s, want[i])
			}
		}
		page, err := scenarios.List(ctx, tx, a.ID, v2, &scenarios.Position{CreatedAt: list[0].CreatedAt, ID: list[0].ID}, 1)
		if err != nil || len(page) != 1 || page[0].ID != list[1].ID {
			t.Errorf("the page after the first: %+v %v", page, err)
		}
		if l, err := scenarios.List(ctx, tx, a.ID, v1, nil, 10); err != nil || len(l) != 1 || l[0].ID != onV1.ID || l[0].Name != "older" {
			t.Errorf("a's list on version 1: %+v %v", l, err)
		}
		if l, err := scenarios.List(ctx, tx, a.ID, vB, nil, 10); err != nil || len(l) != 0 {
			t.Errorf("b's version in a: %+v %v", l, err)
		}
		if l, err := scenarios.List(ctx, tx, b.ID, v2, nil, 10); err != nil || len(l) != 0 {
			t.Errorf("a's version in b: %+v %v", l, err)
		}
		if _, err := scenarios.Get(ctx, tx, a.ID, inB.ID); !errors.Is(err, scenarios.ErrNotFound) {
			t.Errorf("b's scenario in a: %v", err)
		}
		got, err := scenarios.Get(ctx, tx, b.ID, inB.ID)
		if err != nil || got.Name != "b's" || got.ModelID != mdlB || !got.CreatedAt.Equal(inB.CreatedAt) {
			t.Errorf("b's scenario in b: %+v %v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
