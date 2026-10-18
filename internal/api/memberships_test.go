package api_test

import (
	"fmt"
	"testing"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/tenant"
)

// TEST-009 (memberships) through the whole stack: only an owner manages
// members; the path's organization must be the selected one; conflicts,
// unknown users and malformed requests get their codes; a removed member is
// refused at once.
func TestMembershipRoutes(t *testing.T) {
	e := setup(t)
	owner, editor := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor]
	outsider := e.b.Members[tenant.Owner]
	stranger := dbtest.NewUser(t, e.admin, e.p.Issuer)
	base := "/v1/organizations/" + e.a.ID + "/memberships"

	if r := e.do("GET", base, owner.Subject, e.a.ID, nil, ""); r.status != 200 {
		t.Errorf("list as the owner: %d %s", r.status, r.body)
	} else {
		valid(t, r, "http.schema.json#/$defs/Memberships")
	}
	for name, c := range map[string]struct {
		subject, path, org string
		status             int
		code               string
	}{
		"editor":                          {editor.Subject, base, e.a.ID, 403, "FORBIDDEN"},
		"another organization's owner":    {outsider.Subject, base, e.a.ID, 404, "NOT_FOUND"},
		"path names another organization": {owner.Subject, "/v1/organizations/" + e.b.ID + "/memberships", e.a.ID, 404, "NOT_FOUND"},
		"header names another one":        {outsider.Subject, base, e.b.ID, 404, "NOT_FOUND"},
		"no token":                        {"", base, e.a.ID, 401, "UNAUTHENTICATED"},
		"bad limit":                       {owner.Subject, base + "?limit=0", e.a.ID, 400, "INVALID_REQUEST"},
		"bad cursor":                      {owner.Subject, base + "?cursor=x", e.a.ID, 400, "INVALID_REQUEST"},
	} {
		if r := e.do("GET", c.path, c.subject, c.org, nil, ""); r.status != c.status || code(t, r) != c.code {
			t.Errorf("list, %s: %d %s", name, r.status, r.body)
		}
	}

	add := func(subject, key, body string) resp {
		return e.do("POST", base, subject, e.a.ID, map[string]string{"Idempotency-Key": key}, body)
	}
	body := fmt.Sprintf(`{"user_id":%q,"role":"viewer"}`, stranger.UserID)
	if r := e.do("POST", base, owner.Subject, e.a.ID, nil, body); r.status != 400 || code(t, r) != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Errorf("add without a key: %d %s", r.status, r.body)
	}
	if r := add(editor.Subject, "key-mem-0", body); r.status != 403 {
		t.Errorf("add as an editor: %d %s", r.status, r.body)
	}
	r := add(owner.Subject, "key-mem-1", body)
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/Membership")
	if again := add(owner.Subject, "key-mem-1", body); again.status != 201 || string(again.body) != string(r.body) {
		t.Errorf("replay: %d %s", again.status, again.body)
	}
	for name, c := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"a member again": {body, 409, "LIFECYCLE_CONFLICT"},
		"unknown user":   {fmt.Sprintf(`{"user_id":%q,"role":"viewer"}`, ids.New("usr")), 422, "REQUEST_VALIDATION_FAILED"},
		"unknown role":   {fmt.Sprintf(`{"user_id":%q,"role":"admin"}`, stranger.UserID), 422, "REQUEST_VALIDATION_FAILED"},
		"extra member":   {fmt.Sprintf(`{"user_id":%q,"role":"viewer","organization_id":%q}`, stranger.UserID, e.b.ID), 422, "REQUEST_VALIDATION_FAILED"},
	} {
		if r := add(owner.Subject, "key-mem-"+ids.New("k"), c.body); r.status != c.status || code(t, r) != c.code {
			t.Errorf("add, %s: %d %s", name, r.status, r.body)
		}
	}

	member := base + "/" + stranger.UserID
	patch := func(subject, path, body string) resp { return e.do("PATCH", path, subject, e.a.ID, nil, body) }
	if r := patch(owner.Subject, member, `{"role":"editor"}`); r.status != 200 {
		t.Errorf("promote: %d %s", r.status, r.body)
	} else {
		valid(t, r, "http.schema.json#/$defs/Membership")
	}
	if r := patch(owner.Subject, base+"/"+owner.UserID, `{"role":"viewer"}`); r.status != 409 || code(t, r) != "LIFECYCLE_CONFLICT" {
		t.Errorf("demoting the only owner: %d %s", r.status, r.body)
	}
	if r := patch(owner.Subject, base+"/usr_bad", `{"role":"viewer"}`); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
		t.Errorf("a malformed user ID: %d %s", r.status, r.body)
	}
	if r := patch(owner.Subject, base+"/"+ids.New("usr"), `{"role":"viewer"}`); r.status != 404 || code(t, r) != "NOT_FOUND" {
		t.Errorf("not a member: %d %s", r.status, r.body)
	}
	if r := patch(owner.Subject, member, `{"role":"admin"}`); r.status != 422 || code(t, r) != "REQUEST_VALIDATION_FAILED" {
		t.Errorf("an unknown role: %d %s", r.status, r.body)
	}

	if r := e.do("DELETE", base+"/"+owner.UserID, owner.Subject, e.a.ID, nil, ""); r.status != 409 || code(t, r) != "LIFECYCLE_CONFLICT" {
		t.Errorf("removing the only owner: %d %s", r.status, r.body)
	}
	if r := e.do("DELETE", member, owner.Subject, e.a.ID, nil, ""); r.status != 204 || len(r.body) != 0 || r.header.Get("X-Request-ID") == "" {
		t.Errorf("remove: %d %q", r.status, r.body)
	}
	if r := e.do("DELETE", member, owner.Subject, e.a.ID, nil, ""); r.status != 404 {
		t.Errorf("remove again: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/projects", stranger.Subject, e.a.ID, nil, ""); r.status != 404 || code(t, r) != "NOT_FOUND" {
		t.Errorf("the removed member's next request: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action LIKE 'membership.%'`, e.a.ID); n != 3 {
		t.Errorf("%d membership audit events, want 3 (add, update, delete)", n)
	}
}
