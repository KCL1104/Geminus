package proposals_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"slices"
	"strings"
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

// T-026: a decision writes only the state it may leave, in the
// organization it names: approving or conflicting needs ready, rejecting
// ready or needs_input; anything else writes nothing (ErrNotDecidable), as
// the provisioning superuser, whom RLS does not restrict, sees too.
func TestDecisionsNeedTheirState(t *testing.T) {
	pool := dbtest.Admin(t)
	admin := postgres.OpenPool(pool)
	org := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	other := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	mdl, mv, idPool := version(t, admin, org)
	user := org.Members[tenant.Owner].UserID
	ctx := context.Background()
	if err := admin.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		p, err := proposals.Insert(ctx, tx, proposals.Proposal{ID: ids.New("aip"), OrganizationID: org.ID, ModelID: mdl, BaseVersionID: mv,
			Mode: "edit", Intent: "raise", OutputLocale: "en", IDPool: idPool, CreatedBy: user})
		if err != nil {
			return err
		}
		version := ids.New("mv")
		comment := "no"
		for name, decide := range map[string]func() error{
			"approve":  func() error { return proposals.Approve(ctx, tx, org.ID, p.ID, user, &version, nil, nil) },
			"conflict": func() error { return proposals.Conflict(ctx, tx, org.ID, p.ID, user) },
			"reject":   func() error { return proposals.Reject(ctx, tx, org.ID, p.ID, user, &comment) },
			"reject in another organization": func() error {
				return proposals.Reject(ctx, tx, other.ID, p.ID, other.Members[tenant.Owner].UserID, nil)
			},
		} {
			if err := decide(); !errors.Is(err, proposals.ErrNotDecidable) {
				t.Errorf("%s a generating proposal: %v", name, err)
			}
		}
		got, err := proposals.Get(ctx, tx, org.ID, p.ID)
		if err != nil {
			return err
		}
		if got.Status != proposals.Generating || got.DecidedBy != nil || got.DecidedAt != nil || got.DecisionComment != nil || got.NewVersionID != nil {
			t.Errorf("a refused decision wrote: %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Review L4: each decision's organization filter holds by itself. As the
// provisioning superuser, whom RLS does not restrict, a decision that
// names another organization writes nothing on a ready and on a
// needs_input proposal, states its status filter alone would let through
// (ErrNotDecidable). Everything runs in one transaction that is rolled
// back: a proposal generated without its job never commits (0012).
func TestDecisionsStayInTheirOrganization(t *testing.T) {
	pool := dbtest.Admin(t)
	admin := postgres.OpenPool(pool)
	org := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	other := dbtest.NewOrg(t, pool, "https://test.invalid", tenant.Owner)
	mdl, mv, idPool := version(t, admin, org)
	user, stranger := org.Members[tenant.Owner].UserID, other.Members[tenant.Owner].UserID
	ctx := context.Background()
	rollback := errors.New("roll back")
	err := admin.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		generated := func(g proposals.Generated) proposals.Proposal {
			t.Helper()
			p, err := proposals.Insert(ctx, tx, proposals.Proposal{ID: ids.New("aip"), OrganizationID: org.ID, ModelID: mdl, BaseVersionID: mv,
				Mode: "edit", Intent: "raise", OutputLocale: "en", IDPool: idPool, CreatedBy: user})
			if err != nil {
				t.Fatal(err)
			}
			if err := proposals.Generate(ctx, tx, org.ID, p.ID, g); err != nil {
				t.Fatal(err)
			}
			return p
		}
		summary, which, low, hash, confidence := "Rename it.", "Which step?", "low", "sha256:"+strings.Repeat("a", 64), 0.9
		ready := generated(proposals.Generated{Status: proposals.Ready, Summary: &summary, ExpectedEffects: []string{"A simulation will tell."},
			Patch: []byte(`[{"op":"replace","path":"/name","value":"N"}]`), PatchHash: &hash, ServerRisk: &low, ProviderRisk: &low, Risk: &low,
			Confidence: &confidence, Validation: []byte(`{"valid":true,"issues":[]}`)})
		asked := generated(proposals.Generated{Status: proposals.NeedsInput, Summary: &which, Questions: []string{which}})
		version := ids.New("mv")
		for _, c := range []struct {
			name   string
			decide func() error
		}{
			{"approve the ready proposal", func() error { return proposals.Approve(ctx, tx, other.ID, ready.ID, stranger, &version, nil, nil) }},
			{"conflict the ready proposal", func() error { return proposals.Conflict(ctx, tx, other.ID, ready.ID, stranger) }},
			{"reject the ready proposal", func() error { return proposals.Reject(ctx, tx, other.ID, ready.ID, stranger, nil) }},
			{"reject the needs_input proposal", func() error { return proposals.Reject(ctx, tx, other.ID, asked.ID, stranger, nil) }},
		} {
			if err := c.decide(); !errors.Is(err, proposals.ErrNotDecidable) {
				t.Errorf("%s in another organization: %v", c.name, err)
			}
		}
		for p, want := range map[string]proposals.Status{ready.ID: proposals.Ready, asked.ID: proposals.NeedsInput} {
			got, err := proposals.Get(ctx, tx, org.ID, p)
			if err != nil {
				return err
			}
			if got.Status != want || got.DecidedBy != nil || got.DecidedAt != nil || got.NewVersionID != nil {
				t.Errorf("a decision in another organization wrote: %+v", got)
			}
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
}
