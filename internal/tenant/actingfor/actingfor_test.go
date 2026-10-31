package actingfor_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/actingfor"
	"github.com/kcl1104/geminus/internal/tenant/workerscope"
)

// H-061 §6.1 (D-008 as amended (c)): the package's SQL is exactly two
// fixed statements, pinned here word for word: the read-only discovery and
// the role_lost end. Nothing else in it sends SQL (its scope is set by the
// tenant-internal scope package, shared with the other scopes).
func TestExactlyTwoFixedStatements(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "actingfor.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	consts := map[string]string{}
	var sends []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ValueSpec:
			for i, name := range x.Names {
				if i < len(x.Values) {
					if lit, ok := x.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						v, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Fatal(err)
						}
						consts[name.Name] = v
					}
				}
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && slices.Contains([]string{"Exec", "Query", "QueryRow", "SendBatch", "CopyFrom", "Prepare"}, sel.Sel.Name) {
				arg := "?"
				if len(x.Args) > 1 {
					if id, ok := x.Args[1].(*ast.Ident); ok {
						arg = id.Name
					}
				}
				sends = append(sends, sel.Sel.Name+"("+arg+")")
			}
		}
		return true
	})
	if want := []string{"Query(pendingSQL)", "Exec(endLostSQL)"}; !slices.Equal(sends, want) {
		t.Fatalf("the package sends %v, want exactly %v", sends, want)
	}
	if consts["pendingSQL"] != wantPending || consts["endLostSQL"] != wantEndLost {
		t.Errorf("the statements changed:\n%s\n%s", consts["pendingSQL"], consts["endLostSQL"])
	}
	for name, sql := range map[string]string{"pending": wantPending, "end_lost": wantEndLost} {
		if strings.Contains(sql, "$3") || !strings.Contains(sql, "organization_id = $1") {
			t.Errorf("%s is not organization-only with fixed parameters", name)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(wantPending), "SELECT") {
		t.Error("the discovery is not a SELECT")
	}
}

const wantPending = `SELECT 'call', c.investigation_id, c.seq, i.created_by, current_setting('transaction_read_only') = 'on'
  FROM twin.investigation_tool_calls c
  JOIN twin.investigations i ON i.organization_id = c.organization_id AND i.id = c.investigation_id
  WHERE c.organization_id = $1 AND c.status = 'pending' AND i.status = 'running'
UNION ALL
SELECT 'lost', i.id, 0, i.created_by, current_setting('transaction_read_only') = 'on'
  FROM twin.investigations i
  WHERE i.organization_id = $1 AND i.status = 'running' AND NOT EXISTS (SELECT 1 FROM twin.memberships m
    WHERE m.organization_id = i.organization_id AND m.user_id = i.created_by AND m.role IN ('owner', 'editor'))
ORDER BY 1, 2, 3
LIMIT $2`

const wantEndLost = `UPDATE twin.investigations SET status = 'stopped', reason = 'role_lost'
  WHERE organization_id = $1 AND id = $2 AND status = 'running' AND NOT EXISTS (SELECT 1 FROM twin.memberships m
    WHERE m.organization_id = $1 AND m.user_id = investigations.created_by AND m.role IN ('owner', 'editor'))`

// Only assigned organizations, one at a time, and only IDs of their kind:
// refused before the database.
func TestAssignedOrganizationsOnly(t *testing.T) {
	a := "org_01M4YDQK00TA00000000000000"
	for _, assigned := range [][]string{{"org_x"}, {a, a}, {"usr_01M4YDQK00TA00000000000001"}} {
		if _, err := actingfor.New(nil, assigned); !errors.Is(err, tenant.ErrInvalidScope) {
			t.Errorf("%v: %v", assigned, err)
		}
	}
	s, err := actingfor.New(nil, []string{a})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Assigned(); !slices.Equal(got, []string{a}) {
		t.Errorf("assigned %v", got)
	}
	other := "org_01M4YDQK00TB00000000000000"
	if _, err := s.Pending(context.Background(), other); !errors.Is(err, actingfor.ErrNotAssigned) {
		t.Errorf("pending in an unassigned organization: %v", err)
	}
	if _, err := s.EndLost(context.Background(), other, "inv_01M4YDQK00TB00000000000001"); !errors.Is(err, actingfor.ErrNotAssigned) {
		t.Errorf("end in an unassigned organization: %v", err)
	}
	for _, inv := range []string{"", "inv_x", "aip_01M4YDQK00TA00000000000001", "inv_01M4YDQK00TA00000000000001'; --"} {
		if _, err := s.EndLost(context.Background(), a, inv); !errors.Is(err, tenant.ErrInvalidScope) {
			t.Errorf("end of %q: %v", inv, err)
		}
	}
}

const issuer = "https://test.invalid"

var policy = jobs.Policy{Lease: time.Minute, MaxAttempts: 3, BackoffBase: time.Hour, BackoffMax: 4 * time.Hour}

func ptr[T any](v T) *T { return &v }

// Discovery, as the API login in one assigned organization at a time with
// no user: the pending calls of running investigations, with their
// requesters, and the running investigations whose requester holds no
// owner or editor membership, read-only; nothing of another organization.
// EndLost ends one such investigation (role_lost) and nothing else: the
// database refuses it while the requester is still an editor.
func TestPendingAndEndLost(t *testing.T) {
	admin := dbtest.Admin(t)
	ctx := context.Background()
	a := dbtest.NewOrg(t, admin, issuer, tenant.Owner, tenant.Editor)
	b := dbtest.NewOrg(t, admin, issuer, tenant.Owner)
	_, mdl, mv := dbtest.NewModel(t, admin, a)
	_, mdlB, mvB := dbtest.NewModel(t, admin, b)
	api := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 4, true)
	if err := tenant.CheckRuntimeRole(ctx, api, tenant.APIRole); err != nil {
		t.Fatal(err)
	}
	db := tenant.New(api)
	wp := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.WorkerLogin), 4, true)
	worker, err := workerscope.New(wp, []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	start := func(o dbtest.Org, model, base, user string) (string, jobs.Lease) {
		t.Helper()
		inv := ids.New("inv")
		if err := db.Run(ctx, tenant.Scope{OrganizationID: o.ID, UserID: user}, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			if _, err := investigations.Insert(ctx, tx, investigations.Investigation{ID: inv, OrganizationID: o.ID, ModelID: model, BaseVersionID: base,
				Mode: investigations.Edit, Source: investigations.FromRequest, Intent: ptr("Cut the lead time"), OutputLocale: "en",
				ProposalModel: "muse-spark-1.3", PlannerModel: "muse-spark-1.3", PromptTemplateVersion: "investigator-0.1.0",
				Caps: investigations.CapsFor(investigations.Edit, false), CreatedBy: user}); err != nil {
				return err
			}
			_, err := jobs.Enqueue(ctx, tx, jobs.New{ID: ids.New("job"), OrganizationID: o.ID, Type: jobs.Investigation,
				Resource: jobs.Resource{InvestigationID: inv}, Payload: map[string]any{"investigation_id": inv}, MaxAttempts: 5}, policy)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		var l jobs.Lease
		if err := worker.Run(ctx, o.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			var ok bool
			var err error
			_, l, ok, err = jobs.Claim(ctx, tx, o.ID, "worker-a3:agent1", []jobs.Type{jobs.Investigation}, policy)
			if err == nil && !ok {
				err = errors.New("no investigation job to claim")
			}
			if err != nil {
				return err
			}
			_, err = investigations.InsertCall(ctx, tx, investigations.NewCall{OrganizationID: o.ID, InvestigationID: inv, Seq: 1,
				Tool: "propose_model_edit", Arguments: []byte(`{"idea":"Add a second tester at ICT"}`),
				ArgumentsHash: ptr("sha256:2222222222222222222222222222222222222222222222222222222222222222"), JobID: l.JobID, LeaseToken: l.Token,
				Status: investigations.Pending})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return inv, l
	}
	editor, owner := a.Members[tenant.Editor].UserID, a.Members[tenant.Owner].UserID
	invA, _ := start(a, mdl, mv, editor)
	invB, _ := start(b, mdlB, mvB, b.Members[tenant.Owner].UserID)
	s, err := actingfor.New(api, []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Pending(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Calls) != 1 || p.Calls[0] != (actingfor.Call{InvestigationID: invA, Seq: 1, RequestedBy: editor}) || len(p.Lost) != 0 {
		t.Errorf("a's discovery: %+v", p)
	}
	if p, err := s.Pending(ctx, b.ID); err != nil || len(p.Calls) != 1 || p.Calls[0].InvestigationID != invB {
		t.Errorf("b's discovery: %+v %v", p, err)
	}
	// While the requester is an editor, EndLost changes nothing.
	if ended, err := s.EndLost(ctx, a.ID, invA); err != nil || ended {
		t.Errorf("EndLost while the requester is an editor: %v %v", ended, err)
	}
	// Demoted to viewer: discovered as lost, and ended with role_lost once.
	if _, err := admin.Exec(ctx, `UPDATE twin.memberships SET role='viewer' WHERE organization_id=$1 AND user_id=$2`, a.ID, editor); err != nil {
		t.Fatal(err)
	}
	p, err = s.Pending(ctx, a.ID)
	if err != nil || len(p.Lost) != 1 || p.Lost[0] != (actingfor.Lost{InvestigationID: invA, RequestedBy: editor}) {
		t.Errorf("a's discovery after the demotion: %+v %v", p, err)
	}
	if ended, err := s.EndLost(ctx, a.ID, invA); err != nil || !ended {
		t.Fatalf("EndLost after the demotion: %v %v", ended, err)
	}
	if ended, err := s.EndLost(ctx, a.ID, invA); err != nil || ended {
		t.Errorf("EndLost twice: %v %v", ended, err)
	}
	var status, reason string
	if err := db.Run(ctx, tenant.Scope{OrganizationID: a.ID, UserID: owner}, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		return tx.QueryRow(ctx, `SELECT status, reason FROM twin.investigations WHERE organization_id=$1 AND id=$2`, a.ID, invA).Scan(&status, &reason)
	}); err != nil || status != "stopped" || reason != "role_lost" {
		t.Errorf("after EndLost: %s %s %v", status, reason, err)
	}
	// Its pending call is no longer discovered; another organization's
	// investigation is never ended from this one.
	if p, err := s.Pending(ctx, a.ID); err != nil || len(p.Calls) != 0 || len(p.Lost) != 0 {
		t.Errorf("a's discovery after the end: %+v %v", p, err)
	}
	if ended, err := s.EndLost(ctx, a.ID, invB); err != nil || ended {
		t.Errorf("b's investigation ended from a: %v %v", ended, err)
	}
}
