package proposals_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
)

// A proposal is generated ready, needs_input or invalid; Generate refuses
// any other state before it reaches the database.
func TestGenerateOnlyGenerates(t *testing.T) {
	for _, s := range []proposals.Status{proposals.Generating, proposals.Failed, proposals.Approved, proposals.Rejected, proposals.Conflicted, "x"} {
		if err := proposals.Generate(context.Background(), nil, "org_01M4YDQK00Y5H2929M9WWZWTVH", "aip_01M4YDQK00ADN261CME3WV5HZ6",
			proposals.Generated{Status: s}); err == nil {
			t.Errorf("%s was written", s)
		}
	}
}

// version commits, as the provisioning superuser, a project, a model and
// its first version (the minimal contract IR) in org, and returns the
// model, the version and an ID pool for it.
func version(t *testing.T, admin *postgres.DB, org dbtest.Org) (string, string, []byte) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata", "contracts", "valid-minimal-ir.json")), &doc); err != nil {
		t.Fatal(err)
	}
	prj, mdl, mv := ids.New("prj"), ids.New("mdl"), ids.New("mv")
	doc["model_id"] = mdl
	raw, _ := json.Marshal(doc)
	ir, err := hashing.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	var decoded model.ModelIR
	if err := model.Decode(ir, &decoded); err != nil {
		t.Fatal(err)
	}
	pool, err := idpool.New(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	poolJSON, _ := json.Marshal(pool)
	user := org.Members[tenant.Owner].UserID
	if err := admin.Tx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO twin.projects(id,organization_id,name,created_by) VALUES ($1,$2,'P',$3)`, []any{prj, org.ID, user}},
			{`INSERT INTO twin.models(id,organization_id,project_id,name,domain,created_by) VALUES ($1,$2,$3,'M','software_delivery',$4)`, []any{mdl, org.ID, prj, user}},
			{`INSERT INTO twin.model_versions(id,organization_id,model_id,version,schema_version,ir,hash,created_by) VALUES ($1,$2,$3,1,'0.1',$4,$5,$6)`,
				[]any{mv, org.ID, mdl, string(ir), hashing.Sum(ir), user}},
			{`UPDATE twin.models SET latest_version_id=$1 WHERE id=$2`, []any{mv, mdl}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return mdl, mv, poolJSON
}

// The organization filter holds by itself: as the provisioning superuser,
// whom RLS does not restrict, each organization still gets only its own
// proposals (AGENTS.md: application filter next to RLS); the request
// round-trips.
func TestOrganizationFilter(t *testing.T) {
	pool := dbtest.Admin(t)
	admin := postgres.OpenPool(pool)
	a := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	b := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	ctx := context.Background()
	var inA, inB proposals.Proposal
	for _, c := range []struct {
		org dbtest.Org
		out *proposals.Proposal
	}{{a, &inA}, {b, &inB}} {
		mdl, mv, idPool := version(t, admin, c.org)
		if err := admin.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			*c.out, err = proposals.Insert(ctx, tx, proposals.Proposal{ID: ids.New("aip"), OrganizationID: c.org.ID, ModelID: mdl, BaseVersionID: mv,
				Mode: "scenario", Intent: "what if", OutputLocale: "zh-Hant", SelectedEntityIDs: []string{"work"}, IDPool: idPool,
				CreatedBy: c.org.Members[tenant.Owner].UserID})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := admin.Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		list, err := proposals.List(ctx, tx, a.ID, inA.ModelID, nil, 10)
		if err != nil {
			return err
		}
		if len(list) != 1 || list[0].ID != inA.ID {
			t.Errorf("a's list: %+v", list)
		}
		if list, err := proposals.List(ctx, tx, a.ID, inB.ModelID, nil, 10); err != nil || len(list) != 0 {
			t.Errorf("b's model in a: %+v %v", list, err)
		}
		if _, err := proposals.Get(ctx, tx, a.ID, inB.ID); !errors.Is(err, proposals.ErrNotFound) {
			t.Errorf("b's proposal in a: %v", err)
		}
		p, err := proposals.Get(ctx, tx, b.ID, inB.ID)
		if err != nil || p.Status != proposals.Generating || p.OutputLocale != "zh-Hant" || !slices.Equal(p.SelectedEntityIDs, []string{"work"}) ||
			p.Mode != "scenario" || p.Summary != nil || p.Patch != nil || p.Validation != nil || string(p.ProviderMetadata) != "{}" {
			t.Errorf("b's proposal in b: %+v %v", p, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// H-003 review M2: what PostgreSQL refuses to store in a generated row
// (text with U+0000, jsonb with \u0000, 0012's content checks) is
// ErrUnstorable, which the generator ends the attempt with for good.
func TestGenerateRefusesUnstorable(t *testing.T) {
	pool := dbtest.Admin(t)
	admin := postgres.OpenPool(pool)
	org := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	mdl, mv, idPool := version(t, admin, org)
	ctx := context.Background()
	rollback := errors.New("roll back")
	which := "Which step?"
	nul := "a\x00b"
	for name, g := range map[string]proposals.Generated{
		"text with U+0000":   {Status: proposals.NeedsInput, Summary: &nul, Questions: []string{"Which step?"}},
		"jsonb with \\u0000": {Status: proposals.NeedsInput, Summary: &which, Questions: []string{nul}},
		"a content check":    {Status: proposals.NeedsInput, Questions: []string{"Which step?"}},
	} {
		err := admin.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			p, err := proposals.Insert(ctx, tx, proposals.Proposal{ID: ids.New("aip"), OrganizationID: org.ID, ModelID: mdl, BaseVersionID: mv,
				Mode: "edit", Intent: "which?", OutputLocale: "en", IDPool: idPool, CreatedBy: org.Members[tenant.Owner].UserID})
			if err != nil {
				return err
			}
			if err := proposals.Generate(ctx, tx, org.ID, p.ID, g); !errors.Is(err, proposals.ErrUnstorable) {
				t.Errorf("%s: %v", name, err)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
