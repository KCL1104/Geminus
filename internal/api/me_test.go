package api_test

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/tenant"
)

type meBody struct {
	UserID      string `json:"user_id"`
	Memberships []struct {
		OrganizationID   string `json:"organization_id"`
		OrganizationName string `json:"organization_name"`
		Role             string `json:"role"`
	} `json:"memberships"`
	Truncated bool `json:"truncated"`
}

type detailedError struct {
	Error struct {
		Details []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

type ownMembership struct{ org, name, role string }

func (b meBody) list() []ownMembership {
	var out []ownMembership
	for _, m := range b.Memberships {
		out = append(out, ownMembership{m.OrganizationID, m.OrganizationName, m.Role})
	}
	return out
}

func sortedOwn(l []ownMembership) []ownMembership {
	l = slices.Clone(l)
	slices.SortFunc(l, func(a, b ownMembership) int { return cmp.Or(cmp.Compare(a.name, b.name), cmp.Compare(a.org, b.org)) })
	return l
}

// me is GET /v1/me as subject, with no X-Organization-ID.
func (e *env) me(subject string) resp {
	e.t.Helper()
	r := e.do("GET", "/v1/me", subject, "", nil, "")
	if r.status == 200 {
		valid(e.t, r, "http.schema.json#/$defs/Me")
	}
	return r
}

// D-045 through the whole stack as the plain API login: the caller's own
// memberships across organizations, ordered by organization name, then ID,
// in byte order (equal names included); another user's memberships never;
// an unknown identity 403 (no user created); X-Organization-ID 400; no
// token 401; never cached, logged or audited; a change shows at once.
func TestMe(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	run := ids.New("x") // names unique to this run, to look for in the logs
	u := dbtest.NewUser(t, e.admin, e.p.Issuer)
	var want []ownMembership
	var orgs []dbtest.Org
	for i, name := range []string{"Zeta", "acme", "Acme", "Acme", "Ärzte", "_x", "研發部", "Acme ", "Z"} {
		name += " " + run
		o := dbtest.NewOrgNamed(t, e.admin, e.p.Issuer, name, tenant.Owner, tenant.Viewer)
		role := []tenant.Role{tenant.Owner, tenant.Editor, tenant.Viewer}[i%3]
		dbtest.Join(t, e.admin, o.ID, u.UserID, role)
		want = append(want, ownMembership{o.ID, name, string(role)})
		orgs = append(orgs, o)
	}
	other := dbtest.NewOrgNamed(t, e.admin, e.p.Issuer, "Aardvark "+run, tenant.Owner) // not u's
	want = sortedOwn(want)

	r := e.me(u.Subject)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	b := decode[meBody](t, r)
	if b.UserID != u.UserID || b.Truncated || !slices.Equal(b.list(), want) {
		t.Errorf("me: %s\nwant %v", r.body, want)
	}
	h := r.header
	if h.Get("Content-Type") != "application/json" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("headers %v", h)
	}
	// The other members of those organizations see only their own.
	owner := orgs[0].Members[tenant.Owner]
	if b := decode[meBody](t, e.me(owner.Subject)); b.UserID != owner.UserID || !slices.Equal(b.list(), []ownMembership{{orgs[0].ID, "Zeta " + run, "owner"}}) {
		t.Errorf("another member: %+v", b)
	}
	if b := decode[meBody](t, e.me(other.Members[tenant.Owner].Subject)); len(b.Memberships) != 1 || b.Memberships[0].OrganizationID != other.ID {
		t.Errorf("the owner of an organization u is not in: %+v", b)
	}
	// setup's organizations: a's owner is in a alone.
	a := e.a.Members[tenant.Owner]
	if b := decode[meBody](t, e.me(a.Subject)); !slices.Equal(b.list(), []ownMembership{{e.a.ID, "Test org", "owner"}}) {
		t.Errorf("a's owner: %+v", b)
	}

	// A provisioned user without memberships: an empty list.
	stranger := dbtest.NewUser(t, e.admin, e.p.Issuer)
	r = e.me(stranger.Subject)
	if b := decode[meBody](t, r); r.status != 200 || b.UserID != stranger.UserID || b.Truncated || len(b.Memberships) != 0 || !strings.Contains(string(r.body), `"memberships":[]`) {
		t.Errorf("no memberships: %d %s", r.status, r.body)
	}

	// Refusals.
	users := e.count(`SELECT count(*) FROM twin.users`)
	if r := e.me("sub-nobody-" + run); r.status != 403 || code(t, r) != "FORBIDDEN" {
		t.Errorf("unknown identity: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.users`); n != users {
		t.Errorf("an unknown identity created %d users", n-users)
	}
	if r := e.me(""); r.status != 401 || code(t, r) != "UNAUTHENTICATED" || r.header.Get("WWW-Authenticate") != `Bearer realm="geminus"` {
		t.Errorf("no token: %d %v %s", r.status, r.header, r.body)
	}
	for name, org := range map[string]string{"one of u's organizations": orgs[0].ID, "another organization": other.ID, "malformed": "org_x", "empty": ""} {
		req, err := http.NewRequest("GET", e.srv.URL+"/v1/me", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+e.token(u.Subject))
		req.Header["X-Organization-Id"] = []string{org}
		r := e.send(req)
		var body detailedError
		if r.status == 400 {
			body = decode[detailedError](t, r)
		}
		if r.status != 400 || code(t, r) != "INVALID_REQUEST" || len(body.Error.Details) != 1 || body.Error.Details[0].Reason != "X-Organization-ID" {
			t.Errorf("X-Organization-ID %s: %d %s", name, r.status, r.body)
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD"} {
		if r := e.do(method, "/v1/me", u.Subject, "", nil, ""); r.status != 404 {
			t.Errorf("%s /v1/me: %d %s", method, r.status, r.body)
		}
	}

	// A change shows on the next request: a revoked membership disappears,
	// a role change is the new role, and only for that organization.
	if _, err := e.admin.Exec(ctx, `DELETE FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, want[0].org, u.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.Exec(ctx, `UPDATE twin.memberships SET role='owner' WHERE organization_id=$1 AND user_id=$2`, want[1].org, u.UserID); err != nil {
		t.Fatal(err)
	}
	next := slices.Clone(want[1:])
	next[0].role = "owner"
	if b := decode[meBody](t, e.me(u.Subject)); !slices.Equal(b.list(), next) {
		t.Errorf("after a revocation and a role change: %+v\nwant %v", b.list(), next)
	}
	// The organization-scoped API still authorizes with the membership:
	// the revoked organization is 404, whatever /me said earlier.
	if r := e.do("GET", "/v1/projects", u.Subject, want[0].org, nil, ""); r.status != 404 {
		t.Errorf("projects in the revoked organization: %d %s", r.status, r.body)
	}

	// Reads are not audited, and nothing logs an organization's name.
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id = ANY($1)`, []string{orgs[0].ID, orgs[1].ID, other.ID}); n != 0 {
		t.Errorf("%d audit events for reads", n)
	}
	e.logsMu.Lock()
	logs := e.logs.String()
	e.logsMu.Unlock()
	if strings.Contains(logs, run) {
		t.Errorf("the log names an organization: %s", logs)
	}
}

// D-045: at most 100 memberships, the first 100 in order, and truncated
// only when there are more (100 exactly is the whole list).
func TestMeTruncates(t *testing.T) {
	e := setup(t)
	run := ids.New("x")
	u, v := dbtest.NewUser(t, e.admin, e.p.Issuer), dbtest.NewUser(t, e.admin, e.p.Issuer)
	var want []ownMembership
	for i := range 101 {
		// Created out of order: the list's order is the name's.
		name := fmt.Sprintf("Org %03d %s", (i*37)%101, run)
		o := dbtest.NewOrgNamed(t, e.admin, e.p.Issuer, name)
		dbtest.Join(t, e.admin, o.ID, u.UserID, tenant.Viewer)
		if len(want) < 100 {
			dbtest.Join(t, e.admin, o.ID, v.UserID, tenant.Editor)
		}
		want = append(want, ownMembership{o.ID, name, "viewer"})
	}
	all := sortedOwn(want)
	r := e.me(u.Subject)
	b := decode[meBody](t, r)
	if r.status != 200 || !b.Truncated || len(b.Memberships) != 100 || !slices.Equal(b.list(), all[:100]) {
		t.Errorf("101 memberships: %d, %d listed, truncated %v; first %v", r.status, len(b.Memberships), b.Truncated, b.list()[:min(3, len(b.Memberships))])
	}
	var vWant []ownMembership
	for _, m := range want[:100] {
		vWant = append(vWant, ownMembership{m.org, m.name, "editor"})
	}
	if b := decode[meBody](t, e.me(v.Subject)); b.Truncated || !slices.Equal(b.list(), sortedOwn(vWant)) {
		t.Errorf("exactly 100 memberships: %d listed, truncated %v", len(b.Memberships), b.Truncated)
	}
}
