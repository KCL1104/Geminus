package api_test

// Integration tests of the project routes through the whole stack: the
// handler cmd/api serves, a TLS test OIDC provider, and the disposable
// database as the plain API login (TEST-009 for projects, TEST-010).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/auth/authtest"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Responses must be UTC whatever the server's zone; a zone with an offset
// shows a response that forgets to convert.
func init() { time.Local = time.FixedZone("UTC+8", 8*3600) }

type env struct {
	t      *testing.T
	p      *authtest.Provider
	srv    *httptest.Server
	admin  *pgxpool.Pool
	a, b   dbtest.Org
	logs   *bytes.Buffer
	logsMu *sync.Mutex
}

func setup(t *testing.T) *env {
	t.Helper()
	admin := dbtest.Admin(t)
	p := authtest.New(t)
	cfg := p.Config()
	v, err := auth.NewOIDCVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.Pool(t, dbtest.LoginURL(t, dbtest.APILogin), 8, true)
	if err := tenant.CheckRuntimeRole(context.Background(), pool, tenant.APIRole); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, p: p, admin: admin, logs: &bytes.Buffer{}, logsMu: &sync.Mutex{}}
	log := slog.New(slog.NewJSONHandler(lockedWriter{e.logs, e.logsMu}, nil))
	h, err := api.Handler(log, v, tenant.New(pool))
	if err != nil {
		t.Fatal(err)
	}
	e.srv = httptest.NewServer(h)
	t.Cleanup(e.srv.Close)
	e.a = dbtest.NewOrg(t, admin, p.Issuer, tenant.Owner, tenant.Editor, tenant.Viewer)
	e.b = dbtest.NewOrg(t, admin, p.Issuer, tenant.Owner)
	return e
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (e *env) token(subject string) string {
	return e.p.Token(e.t, e.p.RSA, nil, e.p.Claims(subject, time.Now()))
}

type resp struct {
	status int
	header http.Header
	body   []byte
}

// do sends a request as subject ("" for none) in org ("" for no header).
func (e *env) do(method, path, subject, org string, headers map[string]string, body string) resp {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if subject != "" {
		req.Header.Set("Authorization", "Bearer "+e.token(subject))
	}
	if org != "" {
		req.Header.Set("X-Organization-ID", org)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return e.send(req)
}

func (e *env) send(req *http.Request) resp {
	e.t.Helper()
	res, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp{res.StatusCode, res.Header, data}
}

func (e *env) create(subject, org, key, body string) resp {
	return e.do("POST", "/v1/projects", subject, org, map[string]string{"Idempotency-Key": key}, body)
}

// valid checks a response body against its schema and the request ID.
func valid(t *testing.T, r resp, ref string) {
	t.Helper()
	issues, err := validate.Document(r.body, ref, "RESPONSE")
	if err != nil || len(issues) > 0 {
		t.Errorf("%s: %s does not match %s: %v %v", r.header.Get("X-Request-ID"), r.body, ref, issues, err)
	}
	if id := r.header.Get("X-Request-ID"); !strings.HasPrefix(id, "req_") || len(id) != 30 {
		t.Errorf("X-Request-ID %q", id)
	}
}

type errorBody struct {
	Error struct {
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func code(t *testing.T, r resp) string {
	t.Helper()
	valid(t, r, "common.schema.json#/$defs/Error")
	var e errorBody
	if err := json.Unmarshal(r.body, &e); err != nil {
		t.Fatalf("%s: %v", r.body, err)
	}
	if e.Error.RequestID != r.header.Get("X-Request-ID") {
		t.Errorf("request_id %s, header %s", e.Error.RequestID, r.header.Get("X-Request-ID"))
	}
	return e.Error.Code
}

type project struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	CreatedAt      string `json:"created_at"`
	CreatedBy      string `json:"created_by"`
}

func decode[T any](t *testing.T, r resp) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("%s: %v", r.body, err)
	}
	return v
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// TEST-009 (projects): who may do what. Owners and editors create; viewers
// read; a member of another organization and an authenticated stranger see
// 404; an unknown identity is 403; a missing or bad token is 401.
func TestRoleMatrix(t *testing.T) {
	e := setup(t)
	owner, editor, viewer := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	outsider := e.b.Members[tenant.Owner]
	stranger := dbtest.NewUser(t, e.admin, e.p.Issuer)
	for i, c := range []struct {
		name, subject, org string
		status             int
		code               string
	}{
		{"owner", owner.Subject, e.a.ID, 201, ""},
		{"editor", editor.Subject, e.a.ID, 201, ""},
		{"viewer", viewer.Subject, e.a.ID, 403, "FORBIDDEN"},
		{"member of another organization", outsider.Subject, e.a.ID, 404, "NOT_FOUND"},
		{"user without memberships", stranger.Subject, e.a.ID, 404, "NOT_FOUND"},
		{"identity without a user", "sub-nobody", e.a.ID, 403, "FORBIDDEN"},
		{"no token", "", e.a.ID, 401, "UNAUTHENTICATED"},
		{"no organization", owner.Subject, "", 400, "INVALID_REQUEST"},
		{"malformed organization", owner.Subject, "org_bad", 400, "INVALID_REQUEST"},
	} {
		r := e.create(c.subject, c.org, "key-role-"+string(rune('a'+i)), `{"name":"P"}`)
		if r.status != c.status {
			t.Errorf("create, %s: %d %s", c.name, r.status, r.body)
			continue
		}
		if c.code != "" && code(t, r) != c.code {
			t.Errorf("create, %s: %s", c.name, r.body)
		}
		if c.status == 201 {
			valid(t, r, "http.schema.json#/$defs/Project")
		}
	}
	if r := e.create("", e.a.ID, "key-role-x", `{"name":"P"}`); r.header.Get("WWW-Authenticate") != `Bearer realm="geminus"` {
		t.Errorf("401 without credentials: challenge %q", r.header.Get("WWW-Authenticate"))
	}
	r := e.do("POST", "/v1/projects", "", e.a.ID, map[string]string{"Idempotency-Key": "key-role-y", "Authorization": "Bearer a.b.c"}, `{"name":"P"}`)
	if r.status != 401 || r.header.Get("WWW-Authenticate") != `Bearer realm="geminus", error="invalid_token"` {
		t.Errorf("bad token: %d %q", r.status, r.header.Get("WWW-Authenticate"))
	}
	if n := e.count(`SELECT count(*) FROM twin.projects WHERE organization_id=$1`, e.a.ID); n != 2 {
		t.Errorf("%d projects in a, want 2 (owner and editor)", n)
	}
	if n := e.count(`SELECT count(*) FROM twin.projects WHERE organization_id=$1`, e.b.ID); n != 0 {
		t.Errorf("%d projects in b", n)
	}
	// Reads: every member of a; nobody else.
	created := e.create(owner.Subject, e.a.ID, "key-role-z", `{"name":"Readable"}`)
	p := decode[project](t, created)
	for _, s := range []string{owner.Subject, editor.Subject, viewer.Subject} {
		if r := e.do("GET", "/v1/projects/"+p.ID, s, e.a.ID, nil, ""); r.status != 200 || decode[project](t, r) != p {
			t.Errorf("get as a member: %d %s, want %s", r.status, r.body, created.body)
		} else {
			valid(t, r, "http.schema.json#/$defs/Project")
		}
		if r := e.do("GET", "/v1/projects", s, e.a.ID, nil, ""); r.status != 200 {
			t.Errorf("list as a member: %d %s", r.status, r.body)
		}
	}
	for name, c := range map[string][2]string{
		"other org's member in its own org": {outsider.Subject, e.b.ID},
		"other org's member naming a":       {outsider.Subject, e.a.ID},
		"stranger":                          {stranger.Subject, e.a.ID},
	} {
		if r := e.do("GET", "/v1/projects/"+p.ID, c[0], c[1], nil, ""); r.status != 404 || code(t, r) != "NOT_FOUND" {
			t.Errorf("get, %s: %d %s", name, r.status, r.body)
		}
	}
	if r := e.do("GET", "/v1/projects", outsider.Subject, e.a.ID, nil, ""); r.status != 404 {
		t.Errorf("list in an organization one is not a member of: %d", r.status)
	}
}

// Create: the response is the stored project, its audit event names the
// user and the request, and malformed requests are refused before any write.
func TestCreate(t *testing.T) {
	e := setup(t)
	owner := e.a.Members[tenant.Owner]
	r := e.create(owner.Subject, e.a.ID, "key-create-1", `{"name":"Delivery pipeline"}`)
	if r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/Project")
	if r.header.Get("Content-Type") != "application/json" || r.header.Get("X-Content-Type-Options") != "nosniff" || r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("headers %v", r.header)
	}
	p := decode[project](t, r)
	if p.OrganizationID != e.a.ID || p.Name != "Delivery pipeline" || p.CreatedBy != owner.UserID {
		t.Errorf("project %+v", p)
	}
	if at, err := time.Parse(time.RFC3339Nano, p.CreatedAt); err != nil || !strings.HasSuffix(p.CreatedAt, "Z") || time.Since(at) > time.Minute || time.Since(at) < -time.Minute {
		t.Errorf("created_at %q", p.CreatedAt)
	}
	reqID := r.header.Get("X-Request-ID")
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND actor_type='user' AND actor_id=$2
		AND action='project.create' AND target_type='project' AND target_id=$3 AND request_id=$4 AND metadata='{}'::jsonb`, e.a.ID, owner.UserID, p.ID, reqID); n != 1 {
		t.Errorf("%d audit events for the create", n)
	}
	// Counted per organization: other packages' tests write to the same database.
	projectsIn := `SELECT count(*) FROM twin.projects WHERE organization_id=$1`
	before := e.count(projectsIn, e.a.ID)
	for name, c := range map[string]struct {
		headers map[string]string
		body    string
		status  int
		code    string
	}{
		"no Idempotency-Key": {map[string]string{}, `{"name":"x"}`, 400, "IDEMPOTENCY_KEY_REQUIRED"},
		"short key":          {map[string]string{"Idempotency-Key": "short"}, `{"name":"x"}`, 400, "INVALID_REQUEST"},
		"non-ASCII key":      {map[string]string{"Idempotency-Key": "key-é-long-enough"}, `{"name":"x"}`, 400, "INVALID_REQUEST"},
		"key with a space":   {map[string]string{"Idempotency-Key": "key with a space"}, `{"name":"x"}`, 400, "INVALID_REQUEST"},
		"key of 129":         {map[string]string{"Idempotency-Key": strings.Repeat("k", 129)}, `{"name":"x"}`, 400, "INVALID_REQUEST"},
		"key of 7":           {map[string]string{"Idempotency-Key": "key-sev"}, `{"name":"x"}`, 400, "INVALID_REQUEST"},
		"malformed JSON":     {map[string]string{"Idempotency-Key": "key-bad-1"}, `{"name":`, 400, "INVALID_REQUEST"},
		"duplicate member":   {map[string]string{"Idempotency-Key": "key-bad-2"}, `{"name":"a","name":"b"}`, 400, "INVALID_REQUEST"},
		"unknown member":     {map[string]string{"Idempotency-Key": "key-bad-3"}, `{"name":"a","organization_id":"` + e.b.ID + `"}`, 422, "REQUEST_VALIDATION_FAILED"},
		"empty name":         {map[string]string{"Idempotency-Key": "key-bad-4"}, `{"name":""}`, 422, "REQUEST_VALIDATION_FAILED"},
		"name too long":      {map[string]string{"Idempotency-Key": "key-bad-5"}, `{"name":"` + strings.Repeat("n", 201) + `"}`, 422, "REQUEST_VALIDATION_FAILED"},
		"wrong content type": {map[string]string{"Idempotency-Key": "key-bad-6", "Content-Type": "text/plain"}, `{"name":"x"}`, 415, "UNSUPPORTED_MEDIA_TYPE"},
		"body over 1 MiB":    {map[string]string{"Idempotency-Key": "key-bad-7"}, `{"name":"x","pad":"` + strings.Repeat(" ", 1<<20) + `"}`, 413, "PAYLOAD_TOO_LARGE"},
		"created_by in body": {map[string]string{"Idempotency-Key": "key-bad-8"}, `{"name":"x","created_by":"` + e.b.Members[tenant.Owner].UserID + `"}`, 422, "REQUEST_VALIDATION_FAILED"},
	} {
		r := e.do("POST", "/v1/projects", owner.Subject, e.a.ID, c.headers, c.body)
		if r.status != c.status || code(t, r) != c.code {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	req, err := http.NewRequest("POST", e.srv.URL+"/v1/projects", strings.NewReader(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token(owner.Subject))
	req.Header.Set("X-Organization-ID", e.a.ID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add("Idempotency-Key", "key-two-first")
	req.Header.Add("Idempotency-Key", "key-two-second")
	if r := e.send(req); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
		t.Errorf("two Idempotency-Key headers: %d %s", r.status, r.body)
	}
	if after := e.count(projectsIn, e.a.ID); after != before || e.count(projectsIn, e.b.ID) != 0 {
		t.Errorf("refused requests created %d projects", after-before)
	}
	// A refused request leaves no claimed key either: the same key works.
	if r := e.create(owner.Subject, e.a.ID, "key-bad-4", `{"name":"now valid"}`); r.status != 201 {
		t.Errorf("a key whose first request was refused: %d %s", r.status, r.body)
	}
	for _, k := range []string{"key-of-8", strings.Repeat("K", 128), "!~#$%&'*+-.^_`|"} {
		if r := e.create(owner.Subject, e.a.ID, k, `{"name":"bounds"}`); r.status != 201 {
			t.Errorf("key %q: %d %s", k, r.status, r.body)
		}
	}
	if r := e.do("GET", "/v1/nowhere", owner.Subject, e.a.ID, nil, ""); r.status != 404 || code(t, r) != "NOT_FOUND" {
		t.Errorf("unknown route: %d %s", r.status, r.body)
	}
	if r := e.do("DELETE", "/v1/projects", owner.Subject, e.a.ID, nil, ""); r.status != 404 || code(t, r) != "NOT_FOUND" {
		t.Errorf("unknown method: %d %s", r.status, r.body)
	}
}

// D-011: same key and request replays; another request with the key is
// 409; keys are per organization and per user; racing retries create one
// project; an expired key is claimed afresh.
func TestIdempotency(t *testing.T) {
	e := setup(t)
	owner, editor := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor]
	first := e.create(owner.Subject, e.a.ID, "key-idem-1", `{"name":"Once"}`)
	if first.status != 201 {
		t.Fatalf("%d %s", first.status, first.body)
	}
	p := decode[project](t, first)
	for name, body := range map[string]string{"same body": `{"name":"Once"}`, "same JSON, other spelling": "{ \"name\" : \"Once\" }\n"} {
		r := e.create(owner.Subject, e.a.ID, "key-idem-1", body)
		if r.status != 201 || !bytes.Equal(r.body, first.body) {
			t.Errorf("%s: %d %s, want the first response %s", name, r.status, r.body, first.body)
		}
		if r.header.Get("X-Request-ID") == first.header.Get("X-Request-ID") {
			t.Errorf("%s: the replay reused the first request ID", name)
		}
	}
	if r := e.create(owner.Subject, e.a.ID, "key-idem-1", `{"name":"Twice"}`); r.status != 409 || code(t, r) != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Errorf("same key, other body: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.projects WHERE organization_id=$1`, e.a.ID); n != 1 {
		t.Errorf("%d projects after retries, want 1", n)
	}
	ttl := func(when string) {
		t.Helper()
		if n := e.count(`SELECT count(*) FROM twin.idempotency_keys WHERE organization_id=$1 AND actor_id=$2 AND key='key-idem-1'
			AND expires_at-created_at = interval '24 hours' AND created_at > now()-interval '1 minute' AND response_status=201`, e.a.ID, owner.UserID); n != 1 {
			t.Errorf("%s: the key does not replay for 24 hours from its request", when)
		}
	}
	ttl("first request")
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE organization_id=$1 AND action='project.create'`, e.a.ID); n != 1 {
		t.Errorf("%d audit events after retries, want 1", n)
	}
	// The key is the user's and the organization's own.
	if r := e.create(editor.Subject, e.a.ID, "key-idem-1", `{"name":"Once"}`); r.status != 201 || decode[project](t, r).ID == p.ID {
		t.Errorf("another user's same key: %d %s", r.status, r.body)
	}
	if r := e.create(e.b.Members[tenant.Owner].Subject, e.b.ID, "key-idem-1", `{"name":"Once"}`); r.status != 201 || decode[project](t, r).ID == p.ID {
		t.Errorf("another organization's same key: %d %s", r.status, r.body)
	}
	// Racing retries of one request: one project, one answer.
	const n = 16
	bodies := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			r := e.create(owner.Subject, e.a.ID, "key-idem-race", `{"name":"Raced"}`)
			bodies[i] = string(r.body)
			if r.status != 201 {
				t.Errorf("racing retry: %d %s", r.status, r.body)
			}
		})
	}
	wg.Wait()
	if uniq := slices.Compact(slices.Sorted(slices.Values(bodies))); len(uniq) != 1 {
		t.Errorf("racing retries got different answers: %v", bodies)
	}
	if c := e.count(`SELECT count(*) FROM twin.projects WHERE organization_id=$1 AND name='Raced'`, e.a.ID); c != 1 {
		t.Errorf("%d projects from %d racing retries, want 1", c, n)
	}
	// A stored response is no pass once the role is gone (API_DATABASE §7):
	// the role is checked before the key, so a demoted editor's retry is 403
	// and a removed member's 404, not the stored 201.
	if r := e.create(editor.Subject, e.a.ID, "key-idem-revoked", `{"name":"Revoked"}`); r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	for _, c := range []struct{ sql, code string }{
		{`UPDATE twin.memberships SET role='viewer' WHERE organization_id=$1 AND user_id=$2`, "FORBIDDEN"},
		{`DELETE FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, "NOT_FOUND"},
	} {
		if _, err := e.admin.Exec(context.Background(), c.sql, e.a.ID, editor.UserID); err != nil {
			t.Fatal(err)
		}
		if r := e.create(editor.Subject, e.a.ID, "key-idem-revoked", `{"name":"Revoked"}`); code(t, r) != c.code {
			t.Errorf("retry after %q: %d %s", c.sql, r.status, r.body)
		}
	}
	// After the key expires, it starts a new request.
	if _, err := e.admin.Exec(context.Background(), `UPDATE twin.idempotency_keys SET created_at=now()-interval '2 days', expires_at=now()-interval '1 day'
		WHERE organization_id=$1 AND key='key-idem-1' AND actor_id=$2`, e.a.ID, owner.UserID); err != nil {
		t.Fatal(err)
	}
	again := e.create(owner.Subject, e.a.ID, "key-idem-1", `{"name":"Twice"}`)
	if again.status != 201 || decode[project](t, again).Name != "Twice" {
		t.Fatalf("expired key: %d %s", again.status, again.body)
	}
	ttl("claimed afresh")
	if r := e.create(owner.Subject, e.a.ID, "key-idem-1", `{"name":"Twice"}`); r.status != 201 || !bytes.Equal(r.body, again.body) {
		t.Errorf("retry of the fresh request: %d %s, want %s", r.status, r.body, again.body)
	}
	if r := e.create(owner.Subject, e.a.ID, "key-idem-1", `{"name":"Once"}`); r.status != 409 {
		t.Errorf("the expired request's body after the fresh claim: %d %s", r.status, r.body)
	}
}

// An expired key under racing retries (review M1): every round expires
// the key, then 16 retries of one request race on it. The row lock makes
// them claim it once; without it each would read "expired" and create a
// project.
func TestExpiredKeyRace(t *testing.T) {
	e := setup(t)
	owner := e.a.Members[tenant.Owner]
	const rounds, n = 30, 16
	if r := e.create(owner.Subject, e.a.ID, "key-expired-race", `{"name":"Expired race"}`); r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	for round := range rounds {
		if _, err := e.admin.Exec(context.Background(), `UPDATE twin.idempotency_keys SET created_at=now()-interval '2 days', expires_at=now()-interval '1 day'
			WHERE organization_id=$1 AND key='key-expired-race'`, e.a.ID); err != nil {
			t.Fatal(err)
		}
		bodies := make([]string, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() {
				r := e.create(owner.Subject, e.a.ID, "key-expired-race", `{"name":"Expired race"}`)
				if r.status != 201 {
					t.Errorf("round %d: %d %s", round, r.status, r.body)
				}
				bodies[i] = string(r.body)
			})
		}
		wg.Wait()
		if uniq := slices.Compact(slices.Sorted(slices.Values(bodies))); len(uniq) != 1 {
			t.Fatalf("round %d: %d different answers to one request", round, len(uniq))
		}
		if c := e.count(`SELECT count(*) FROM twin.projects WHERE organization_id=$1 AND name='Expired race'`, e.a.ID); c != round+2 {
			t.Fatalf("round %d: %d projects, want %d (one per claim)", round, c, round+2)
		}
	}
}

// Requests outside the contract still get the error envelope (review L2):
// paths ServeMux would redirect, and methods no operation declares.
func TestOutsideTheContract(t *testing.T) {
	e := setup(t)
	owner := e.a.Members[tenant.Owner]
	for _, path := range []string{"/v1//projects", "/v1/x/../projects", "/v1/./projects", "/v1/projects/", "//v1/projects"} {
		if r := e.do("GET", path, owner.Subject, e.a.ID, nil, ""); r.status != 404 || code(t, r) != "NOT_FOUND" {
			t.Errorf("GET %s: %d %s", path, r.status, r.body)
		}
	}
	for _, method := range []string{"HEAD", "PUT", "PATCH", "OPTIONS"} {
		r := e.do(method, "/v1/projects", owner.Subject, e.a.ID, nil, "")
		if r.status != 404 || !strings.HasPrefix(r.header.Get("X-Request-ID"), "req_") || r.header.Get("Content-Type") != "application/json" {
			t.Errorf("%s /v1/projects: %d %v", method, r.status, r.header)
		}
	}
	// Malformed query strings are refused, not partly read (review L6).
	for _, q := range []string{"limit=1;cursor=x", "limit=%zz", "cursor=%"} {
		if r := e.do("GET", "/v1/projects?"+q, owner.Subject, e.a.ID, nil, ""); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
			t.Errorf("?%s: %d %s", q, r.status, r.body)
		}
	}
	// U+0000 cannot be stored as text: refused as a validation failure,
	// not a database error (review L1). An escaped backslash is just text.
	if r := e.create(owner.Subject, e.a.ID, "key-nul-1", `{"name":"a\u0000b"}`); r.status != 422 || code(t, r) != "REQUEST_VALIDATION_FAILED" {
		t.Errorf("NUL in a name: %d %s", r.status, r.body)
	}
	if r := e.create(owner.Subject, e.a.ID, "key-nul-2", `{"name":"a\\u0000b"}`); r.status != 201 || decode[project](t, r).Name != `a\u0000b` {
		t.Errorf("an escaped backslash before u0000: %d %s", r.status, r.body)
	}
}

type page struct {
	Items      []project `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

// Lists are the organization's own projects in a stable order, paged by a
// cursor that only works for the list and organization it came from.
func TestList(t *testing.T) {
	e := setup(t)
	owner, viewer := e.a.Members[tenant.Owner], e.a.Members[tenant.Viewer]
	var want []string
	for i := range 7 {
		r := e.create(owner.Subject, e.a.ID, "key-list-"+string(rune('a'+i)), `{"name":"P`+string(rune('a'+i))+`"}`)
		want = append(want, decode[project](t, r).ID)
	}
	e.create(e.b.Members[tenant.Owner].Subject, e.b.ID, "key-list-b", `{"name":"B's"}`)
	var got []string
	cursor := ""
	pages := 0
	for {
		path := "/v1/projects?limit=3"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := e.do("GET", path, viewer.Subject, e.a.ID, nil, "")
		if r.status != 200 {
			t.Fatalf("%d %s", r.status, r.body)
		}
		valid(t, r, "http.schema.json#/$defs/Projects")
		pg := decode[page](t, r)
		pages++
		for _, p := range pg.Items {
			got = append(got, p.ID)
		}
		if pg.NextCursor == nil {
			break
		}
		cursor = *pg.NextCursor
		if pages > 5 {
			t.Fatal("pagination does not end")
		}
	}
	if !slices.Equal(got, want) || pages != 3 {
		t.Errorf("paged %v in %d pages, want %v in 3", got, pages, want)
	}
	if r := e.do("GET", "/v1/projects", viewer.Subject, e.a.ID, nil, ""); len(decode[page](t, r).Items) != 7 || decode[page](t, r).NextCursor != nil {
		t.Errorf("fewer projects than the default limit: %s", r.body)
	}
	// The default limit is 50.
	c51 := dbtest.NewOrg(t, e.admin, e.p.Issuer, tenant.Owner)
	for i := range 51 {
		if r := e.create(c51.Members[tenant.Owner].Subject, c51.ID, "key-many-"+strings.Repeat("x", 3)+string(rune('A'+i/26))+string(rune('a'+i%26)), `{"name":"P"}`); r.status != 201 {
			t.Fatalf("%d %s", r.status, r.body)
		}
	}
	if r := e.do("GET", "/v1/projects", c51.Members[tenant.Owner].Subject, c51.ID, nil, ""); len(decode[page](t, r).Items) != 50 || decode[page](t, r).NextCursor == nil {
		t.Errorf("default limit with 51 projects: %d items, next %v", len(decode[page](t, r).Items), decode[page](t, r).NextCursor)
	}
	// A cursor of a's list is refused in b, and edited cursors are refused.
	r := e.do("GET", "/v1/projects?limit=3", viewer.Subject, e.a.ID, nil, "")
	c := *decode[page](t, r).NextCursor
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		t.Fatal(err)
	}
	edit := func(from, to string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), from, to, 1)))
	}
	// The same bytes in a non-canonical encoding (a last digit with
	// padding bits set) are refused; their canonical encoding is accepted.
	spaced := slices.Clone(raw)
	for len(spaced)%3 != 1 {
		spaced = append(spaced, ' ')
	}
	canonical := base64.RawURLEncoding.EncodeToString(spaced)
	const digits = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	loose := canonical[:len(canonical)-1] + string(digits[strings.IndexByte(digits, canonical[len(canonical)-1])+1])
	if r := e.do("GET", "/v1/projects?cursor="+canonical, viewer.Subject, e.a.ID, nil, ""); r.status != 200 {
		t.Errorf("canonical encoding of a valid cursor: %d %s", r.status, r.body)
	}
	// A valid cursor padded with JSON whitespace: 2048 characters (1536
	// bytes) is the contract's maximum; 2050 is refused although it decodes
	// (review M1).
	pad := func(n int) string {
		b := append([]byte("{"+strings.Repeat(" ", n-len(raw))), raw[1:]...)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	if c := pad(1536); len(c) != 2048 {
		t.Fatalf("padded cursor of %d characters", len(c))
	} else if r := e.do("GET", "/v1/projects?cursor="+c, viewer.Subject, e.a.ID, nil, ""); r.status != 200 {
		t.Errorf("a valid cursor of 2048 characters: %d %s", r.status, r.body)
	}
	if c := pad(1537); len(c) != 2050 {
		t.Fatalf("padded cursor of %d characters", len(c))
	} else if r := e.do("GET", "/v1/projects?cursor="+c, viewer.Subject, e.a.ID, nil, ""); r.status != 400 {
		t.Errorf("a cursor of 2050 characters: %d %s", r.status, r.body)
	}
	for name, bad := range map[string]string{
		"other organization's cursor": edit(e.a.ID, e.b.ID),
		"other list":                  edit(`"l":"projects"`, `"l":"models"`),
		"other version":               edit(`"v":1`, `"v":2`),
		"unknown member":              edit(`"v":1`, `"v":1,"x":1`),
		"not a project ID":            edit(`"i":"prj_`, `"i":"mdl_`),
		"not a time":                  edit(`"t":"`, `"t":"x`),
		"non-zero padding bits":       loose,
		"not base64":                  "!!!",
		"padded base64":               c + "==",
		"empty":                       "",
	} {
		r := e.do("GET", "/v1/projects?cursor="+bad, viewer.Subject, e.a.ID, nil, "")
		if bad == "" {
			r = e.do("GET", "/v1/projects?cursor=", viewer.Subject, e.a.ID, nil, "")
		}
		if r.status != 400 || code(t, r) != "INVALID_REQUEST" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := e.do("GET", "/v1/projects?cursor="+c, e.b.Members[tenant.Owner].Subject, e.b.ID, nil, ""); r.status != 400 {
		t.Errorf("a's cursor used by b's owner in b: %d %s", r.status, r.body)
	}
	for _, l := range []string{"0", "101", "-1", "abc", "05", "+5", "1.5"} {
		if r := e.do("GET", "/v1/projects?limit="+l, viewer.Subject, e.a.ID, nil, ""); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
			t.Errorf("limit=%s: %d %s", l, r.status, r.body)
		}
	}
	for _, l := range []string{"1", "100"} {
		if r := e.do("GET", "/v1/projects?limit="+l, viewer.Subject, e.a.ID, nil, ""); r.status != 200 {
			t.Errorf("limit=%s: %d %s", l, r.status, r.body)
		}
	}
	if r := e.do("GET", "/v1/projects?limit=1&limit=2", viewer.Subject, e.a.ID, nil, ""); r.status != 400 {
		t.Errorf("two limits: %d", r.status)
	}
	if r := e.do("GET", "/v1/projects?cursor="+c+"&cursor="+c, viewer.Subject, e.a.ID, nil, ""); r.status != 400 {
		t.Errorf("two cursors: %d", r.status)
	}
	// TEST-010: b's project never appears in a's list or by ID from a.
	bID := ""
	for _, p := range decode[page](t, e.do("GET", "/v1/projects", e.b.Members[tenant.Owner].Subject, e.b.ID, nil, "")).Items {
		bID = p.ID
	}
	if slices.Contains(got, bID) || bID == "" {
		t.Errorf("b's project %q in a's list %v", bID, got)
	}
	if r := e.do("GET", "/v1/projects/"+bID, owner.Subject, e.a.ID, nil, ""); r.status != 404 {
		t.Errorf("b's project from a: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/projects/prj_bad", owner.Subject, e.a.ID, nil, ""); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
		t.Errorf("malformed ID: %d %s", r.status, r.body)
	}
}
