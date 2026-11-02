package api_test

// H-079 through the whole stack: listProposals' newest-first order, its
// small pages and the cursor bound to that order, and the request's words
// on every listed and read proposal, under the API login with RLS. Without
// GEMINUS_TEST_DATABASE_URL these skip (NOT RUN).

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	proposalstore "github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
)

type listedProposal struct {
	ProposalID string `json:"proposal_id"`
	ModelID    string `json:"model_id"`
	Intent     string `json:"intent"`
}

type proposalPage struct {
	Items      []listedProposal `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

// modelOf makes a project and a model in org with its owner, through the
// routes, and returns the model and its first version.
func (e *env) modelOf(t *testing.T, org dbtest.Org, tag string) (string, string) {
	t.Helper()
	owner := org.Members[tenant.Owner]
	r := e.create(owner.Subject, org.ID, "key-"+t.Name()+tag+"-prj", `{"name":"Models"}`)
	if r.status != 201 {
		t.Fatalf("project: %d %s", r.status, r.body)
	}
	prj := decode[project](t, r)
	r = e.do("POST", "/v1/projects/"+prj.ID+"/models", owner.Subject, org.ID, map[string]string{"Idempotency-Key": "key-" + t.Name() + tag + "-mdl"}, createModelBody)
	if r.status != 201 {
		t.Fatalf("model: %d %s", r.status, r.body)
	}
	m := decode[modelSummary](t, r)
	return m.ID, m.LatestVersion.ID
}

func TestProposalListNewestFirst(t *testing.T) {
	e := setupProposals(t)
	editor, viewer := e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	m := e.newModel(t)
	other := e.newModelIn(t, "-other")
	bModel, bBase := e.modelOf(t, e.b, "-b")

	// Five requests on the model, one on another model of a, one in b: each in its own transaction, so in this order.
	var asked []string
	for i := 1; i <= 5; i++ {
		words := fmt.Sprintf("Request %d: raise the CI runners to %d.\nKeep the rest.", i, i+1)
		r := e.propose(editor.Subject, e.a.ID, m.ID, fmt.Sprintf("prop-newest-%04d", i), proposalBody(m.LatestVersion.ID, "edit", words))
		if r.status != 202 {
			t.Fatalf("propose %d: %d %s", i, r.status, r.body)
		}
		asked = append(asked, decode[struct {
			ID string `json:"proposal_id"`
		}](t, r).ID)
	}
	if r := e.propose(editor.Subject, e.a.ID, other.ID, "prop-newest-other", proposalBody(other.LatestVersion.ID, "scenario", "Another model's request")); r.status != 202 {
		t.Fatalf("another model: %d %s", r.status, r.body)
	}
	bOwner := e.b.Members[tenant.Owner]
	if r := e.propose(bOwner.Subject, e.b.ID, bModel, "prop-newest-b", proposalBody(bBase, "edit", "b's own request")); r.status != 202 {
		t.Fatalf("b: %d %s", r.status, r.body)
	}
	want := slices.Clone(asked)
	slices.Reverse(want)

	list := func(query url.Values) (resp, proposalPage) {
		t.Helper()
		r := e.do("GET", "/v1/models/"+m.ID+"/ai-proposals?"+query.Encode(), viewer.Subject, e.a.ID, nil, "")
		if r.status != 200 {
			return r, proposalPage{}
		}
		valid(t, r, "http.schema.json#/$defs/Proposals")
		return r, decode[proposalPage](t, r)
	}
	// Newest first, two at a time: three pages, each with the person's words, nothing of another model or organization.
	var seen []string
	var cursors []string
	cursor := ""
	for page := 0; ; page++ {
		q := url.Values{"order": {"newest"}, "limit": {"2"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		r, p := list(q)
		if r.status != 200 {
			t.Fatalf("page %d: %d %s", page, r.status, r.body)
		}
		for _, it := range p.Items {
			if it.ModelID != m.ID {
				t.Errorf("another model's proposal %s listed", it.ProposalID)
			}
			i := slices.Index(asked, it.ProposalID)
			if i < 0 {
				t.Fatalf("proposal %s was not asked for on this model", it.ProposalID)
			}
			if w := fmt.Sprintf("Request %d: raise the CI runners to %d.\nKeep the rest.", i+1, i+2); it.Intent != w {
				t.Errorf("%s: intent %q, want %q", it.ProposalID, it.Intent, w)
			}
			seen = append(seen, it.ProposalID)
		}
		if p.NextCursor == nil {
			break
		}
		if page > 3 {
			t.Fatal("the pages do not end")
		}
		cursor = *p.NextCursor
		cursors = append(cursors, cursor)
	}
	if !slices.Equal(seen, want) || len(cursors) != 2 {
		t.Errorf("newest first: %v (%d cursors), want %v", seen, len(cursors), want)
	}
	// The default is oldest first, as before, with the words too.
	if r, p := list(url.Values{"limit": {"10"}}); r.status != 200 || len(p.Items) != 5 || p.Items[0].ProposalID != asked[0] || p.Items[4].ProposalID != asked[4] ||
		p.Items[0].Intent == "" {
		t.Errorf("oldest first: %d %+v", r.status, p)
	}
	if r, p := list(url.Values{"order": {"oldest"}, "limit": {"1"}}); r.status != 200 || len(p.Items) != 1 || p.Items[0].ProposalID != asked[0] || p.NextCursor == nil {
		t.Errorf("order=oldest: %d %+v", r.status, p)
	} else if r, _ := list(url.Values{"order": {"newest"}, "cursor": {*p.NextCursor}}); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
		// A cursor is bound to its order: oldest first's is refused newest first, and the reverse.
		t.Errorf("oldest first's cursor, newest first: %d %s", r.status, r.body)
	}
	if r, _ := list(url.Values{"cursor": {cursors[0]}}); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
		t.Errorf("newest first's cursor, oldest first: %d %s", r.status, r.body)
	}
	for _, q := range []string{"order=sideways", "order=NEWEST", "order=", "order=newest&order=oldest"} {
		if r := e.do("GET", "/v1/models/"+m.ID+"/ai-proposals?"+q, viewer.Subject, e.a.ID, nil, ""); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
			t.Errorf("%s: %d %s", q, r.status, r.body)
		}
	}
	// getProposal carries the same words.
	if r := e.do("GET", "/v1/ai-proposals/"+asked[2], viewer.Subject, e.a.ID, nil, ""); r.status != 200 || decode[listedProposal](t, r).Intent != "Request 3: raise the CI runners to 4.\nKeep the rest." {
		t.Errorf("get: %d %s", r.status, r.body)
	}
	// A member of b lists a's model newest first as any other way: 404, and b's cursor of its own model is not a's.
	if r := e.do("GET", "/v1/models/"+m.ID+"/ai-proposals?order=newest", bOwner.Subject, e.b.ID, nil, ""); r.status != 404 {
		t.Errorf("a's model from b: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/models/"+m.ID+"/ai-proposals?order=newest&cursor="+url.QueryEscape(cursors[0]), bOwner.Subject, e.b.ID, nil, ""); r.status != 400 && r.status != 404 {
		t.Errorf("a's cursor from b: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/models/"+bModel+"/ai-proposals?order=newest&cursor="+url.QueryEscape(cursors[0]), bOwner.Subject, e.b.ID, nil, ""); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
		t.Errorf("a's cursor on b's model: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/models/"+bModel+"/ai-proposals?order=newest", bOwner.Subject, e.b.ID, nil, ""); r.status != 200 {
		t.Errorf("b's model in b: %d %s", r.status, r.body)
	} else if p := decode[proposalPage](t, r); len(p.Items) != 1 || p.Items[0].Intent != "b's own request" {
		t.Errorf("b's model in b: %+v", p)
	}

	// The organization filter by itself (as the provisioning superuser, past RLS) and RLS by itself (b's scope, a's ID).
	ctx := context.Background()
	if err := postgres.OpenPool(e.admin).Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		got, err := proposalstore.ListNewest(ctx, tx, e.a.ID, m.ID, nil, 10)
		if err != nil {
			return err
		}
		var order []string
		for _, p := range got {
			order = append(order, p.ID)
		}
		if !slices.Equal(order, want) {
			t.Errorf("a's model in a: %v, want %v", order, want)
		}
		if got, err := proposalstore.ListNewest(ctx, tx, e.b.ID, m.ID, nil, 10); err != nil || len(got) != 0 {
			t.Errorf("a's model in b: %d %v", len(got), err)
		}
		// After the third newest: the two oldest, newest first.
		if got, err := proposalstore.ListNewest(ctx, tx, e.a.ID, m.ID, &proposalstore.Position{CreatedAt: got[2].CreatedAt, ID: got[2].ID}, 10); err != nil ||
			len(got) != 2 || got[0].ID != asked[1] || got[1].ID != asked[0] {
			t.Errorf("after the third: %+v %v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	apiDB := tenant.New(dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 2, true))
	if err := apiDB.Run(ctx, tenant.Scope{OrganizationID: e.b.ID, UserID: bOwner.UserID}, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
		got, err := proposalstore.ListNewest(ctx, tx, e.a.ID, m.ID, nil, 10)
		if err != nil || len(got) != 0 {
			t.Errorf("under RLS, b's owner lists %d of a's proposals (%v)", len(got), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
