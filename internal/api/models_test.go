package api_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/kcl1104/geminus/internal/storage/dbtest"
	"github.com/kcl1104/geminus/internal/tenant"
)

const createModelBody = `{"name":"Pipeline","domain":"software_delivery","template":"default_delivery_pipeline"}`

type modelSummary struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	LatestVersion struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	} `json:"latest_version"`
	Draft struct {
		Revision int64 `json:"revision"`
	} `json:"draft"`
}

type draftBody struct {
	Revision int64          `json:"revision"`
	IR       map[string]any `json:"ir"`
	Hash     string         `json:"hash"`
}

type versionBody struct {
	ID      string  `json:"id"`
	Version int64   `json:"version"`
	Comment *string `json:"comment"`
}

// newModel creates a project and a model in organization a as its owner.
func (e *env) newModel(t *testing.T) modelSummary { return e.newModelIn(t, "") }

// newModelIn is newModel with keys tagged, for a second model in a test.
func (e *env) newModelIn(t *testing.T, tag string) modelSummary {
	t.Helper()
	owner := e.a.Members[tenant.Owner]
	r := e.create(owner.Subject, e.a.ID, "key-"+t.Name()+tag+"-prj", `{"name":"Models"}`)
	if r.status != 201 {
		t.Fatalf("project: %d %s", r.status, r.body)
	}
	prj := decode[project](t, r)
	r = e.do("POST", "/v1/projects/"+prj.ID+"/models", owner.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-" + t.Name() + tag + "-mdl"}, createModelBody)
	if r.status != 201 {
		t.Fatalf("model: %d %s", r.status, r.body)
	}
	return decode[modelSummary](t, r)
}

func (e *env) patch(subject, modelID, ifMatch, body string) resp {
	h := map[string]string{"Content-Type": "application/json-patch+json"}
	if ifMatch != "" {
		h["If-Match"] = ifMatch
	}
	return e.do("PATCH", "/v1/models/"+modelID+"/draft", subject, e.a.ID, h, body)
}

func (e *env) publish(subject, modelID, key, ifMatch, body string) resp {
	h := map[string]string{"Idempotency-Key": key}
	if ifMatch != "" {
		h["If-Match"] = ifMatch
	}
	return e.do("POST", "/v1/models/"+modelID+"/versions", subject, e.a.ID, h, body)
}

func etag(rev int64) string { return fmt.Sprintf(`"draft-revision-%d"`, rev) }

// The model workflow over HTTP (T-013): create, read, patch, publish with a
// comment, list and read versions, reset; each response matches its
// schema, the draft responses carry the revision as a strong ETag, and the
// keyed writes replay.
func TestModelRoutes(t *testing.T) {
	e := setup(t)
	owner, editor, viewer := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	prj := decode[project](t, e.create(owner.Subject, e.a.ID, "key-routes-prj", `{"name":"Delivery"}`))
	base := "/v1/projects/" + prj.ID + "/models"

	create := e.do("POST", base, editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-routes-mdl"}, createModelBody)
	if create.status != 201 {
		t.Fatalf("create: %d %s", create.status, create.body)
	}
	valid(t, create, "http.schema.json#/$defs/Model")
	m := decode[modelSummary](t, create)
	if again := e.do("POST", base, editor.Subject, e.a.ID, map[string]string{"Idempotency-Key": "key-routes-mdl"}, createModelBody); again.status != 201 || string(again.body) != string(create.body) {
		t.Errorf("create replay: %d %s", again.status, again.body)
	}
	v1 := m.LatestVersion.ID

	if r := e.do("GET", "/v1/models/"+m.ID, viewer.Subject, e.a.ID, nil, ""); r.status != 200 {
		t.Errorf("get model: %d %s", r.status, r.body)
	} else {
		valid(t, r, "http.schema.json#/$defs/Model")
	}
	r := e.do("GET", "/v1/models/"+m.ID+"/draft", viewer.Subject, e.a.ID, nil, "")
	if r.status != 200 || r.header.Get("ETag") != etag(1) {
		t.Fatalf("get draft: %d %q %s", r.status, r.header.Get("ETag"), r.body)
	}
	valid(t, r, "http.schema.json#/$defs/Draft")

	r = e.patch(editor.Subject, m.ID, etag(1), `[{"op":"replace","path":"/name","value":"Renamed"}]`)
	if r.status != 200 || r.header.Get("ETag") != etag(2) {
		t.Fatalf("patch: %d %q %s", r.status, r.header.Get("ETag"), r.body)
	}
	valid(t, r, "http.schema.json#/$defs/Draft")
	if d := decode[draftBody](t, r); d.Revision != 2 || d.IR["name"] != "Renamed" {
		t.Errorf("patched draft %d %v", d.Revision, d.IR["name"])
	}

	pub := fmt.Sprintf(`{"expected_base_version_id":%q,"comment":"Renamed for the review"}`, v1)
	r = e.publish(editor.Subject, m.ID, "key-routes-pub", etag(2), pub)
	if r.status != 201 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/ModelVersion")
	v2 := decode[versionBody](t, r)
	if v2.Version != 2 || v2.Comment == nil || *v2.Comment != "Renamed for the review" {
		t.Errorf("published %+v", v2)
	}
	if again := e.publish(editor.Subject, m.ID, "key-routes-pub", etag(2), pub); again.status != 201 || string(again.body) != string(r.body) {
		t.Errorf("publish replay: %d %s", again.status, again.body)
	}
	if n := e.count(`SELECT count(*) FROM twin.audit_events WHERE action='model.publish' AND target_id=$1`, v2.ID); n != 1 {
		t.Errorf("%d publish audit events", n)
	}

	r = e.do("GET", "/v1/models/"+m.ID+"/versions", viewer.Subject, e.a.ID, nil, "")
	if r.status != 200 {
		t.Fatalf("list versions: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/Versions")
	if r = e.do("GET", "/v1/model-versions/"+v2.ID, viewer.Subject, e.a.ID, nil, ""); r.status != 200 {
		t.Fatalf("get version: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/ModelVersion")
	if got := decode[versionBody](t, r); got.Comment == nil || *got.Comment != "Renamed for the review" {
		t.Errorf("read back %+v", got)
	}
	got := decode[modelSummary](t, e.do("GET", "/v1/models/"+m.ID, viewer.Subject, e.a.ID, nil, ""))
	if got.LatestVersion.ID != v2.ID || got.LatestVersion.Version != 2 || got.Draft.Revision != 3 || got.Name != "Renamed" {
		t.Errorf("model after publish %+v", got)
	}

	if r = e.patch(owner.Subject, m.ID, etag(3), `[{"op":"replace","path":"/name","value":"Scratch"}]`); r.status != 200 {
		t.Fatalf("second patch: %d %s", r.status, r.body)
	}
	reset := fmt.Sprintf(`{"expected_latest_version_id":%q,"confirm_discard":true}`, v2.ID)
	r = e.do("POST", "/v1/models/"+m.ID+"/draft/reset", owner.Subject, e.a.ID, map[string]string{"If-Match": etag(4)}, reset)
	if r.status != 200 || r.header.Get("ETag") != etag(5) {
		t.Fatalf("reset: %d %q %s", r.status, r.header.Get("ETag"), r.body)
	}
	valid(t, r, "http.schema.json#/$defs/Draft")
	if d := decode[draftBody](t, r); d.IR["name"] != "Renamed" {
		t.Errorf("reset draft name %v", d.IR["name"])
	}

	r = e.do("GET", base, viewer.Subject, e.a.ID, nil, "")
	if r.status != 200 {
		t.Fatalf("list models: %d %s", r.status, r.body)
	}
	valid(t, r, "http.schema.json#/$defs/Models")
	if !strings.Contains(string(r.body), m.ID) {
		t.Errorf("list models %s", r.body)
	}
}

// TEST-009 (models): viewers read and cannot write; another
// organization's member, and one naming its own organization, see 404;
// an unknown identity is 403; no token is 401. IDs of the wrong kind or
// shape are 400; a well-formed unknown one is 404.
func TestModelRoleMatrix(t *testing.T) {
	e := setup(t)
	m := e.newModel(t)
	owner, viewer, outsider := e.a.Members[tenant.Owner], e.a.Members[tenant.Viewer], e.b.Members[tenant.Owner]
	stranger := dbtest.NewUser(t, e.admin, e.p.Issuer)
	draft := decode[draftBody](t, e.do("GET", "/v1/models/"+m.ID+"/draft", owner.Subject, e.a.ID, nil, ""))
	model, version := "/v1/models/"+m.ID, "/v1/model-versions/"+m.LatestVersion.ID
	reads := []string{model, model + "/draft", model + "/versions", version}
	for _, path := range reads {
		for name, c := range map[string]struct {
			subject, org string
			status       int
			code         string
		}{
			"viewer":                           {viewer.Subject, e.a.ID, 200, ""},
			"another organization's member":    {outsider.Subject, e.a.ID, 404, "NOT_FOUND"},
			"a member of the other one, in it": {outsider.Subject, e.b.ID, 404, "NOT_FOUND"},
			"unknown identity":                 {stranger.Subject + "-unknown", e.a.ID, 403, "FORBIDDEN"},
			"no token":                         {"", e.a.ID, 401, "UNAUTHENTICATED"},
		} {
			r := e.do("GET", path, c.subject, c.org, nil, "")
			if r.status != c.status || c.code != "" && code(t, r) != c.code {
				t.Errorf("GET %s, %s: %d %s", path, name, r.status, r.body)
			}
		}
	}
	h := map[string]string{"Idempotency-Key": "key-matrix-x", "If-Match": etag(draft.Revision)}
	for name, req := range map[string]struct{ method, path, body string }{
		"create":  {"POST", "/v1/projects/" + decode[project](t, e.create(owner.Subject, e.a.ID, "key-matrix-prj", `{"name":"P"}`)).ID + "/models", createModelBody},
		"patch":   {"PATCH", model + "/draft", `[{"op":"replace","path":"/name","value":"No"}]`},
		"reset":   {"POST", model + "/draft/reset", fmt.Sprintf(`{"expected_latest_version_id":%q,"confirm_discard":true}`, m.LatestVersion.ID)},
		"publish": {"POST", model + "/versions", fmt.Sprintf(`{"expected_base_version_id":%q}`, m.LatestVersion.ID)},
	} {
		hh := map[string]string{"Idempotency-Key": h["Idempotency-Key"] + name, "If-Match": h["If-Match"]}
		if req.method == "PATCH" {
			hh["Content-Type"] = "application/json-patch+json"
		}
		if r := e.do(req.method, req.path, viewer.Subject, e.a.ID, hh, req.body); r.status != 403 || code(t, r) != "FORBIDDEN" {
			t.Errorf("%s as a viewer: %d %s", name, r.status, r.body)
		}
		if r := e.do(req.method, req.path, outsider.Subject, e.b.ID, hh, req.body); r.status != 404 || code(t, r) != "NOT_FOUND" {
			t.Errorf("%s from another organization: %d %s", name, r.status, r.body)
		}
	}
	if after := decode[draftBody](t, e.do("GET", model+"/draft", owner.Subject, e.a.ID, nil, "")); after.Revision != draft.Revision || after.Hash != draft.Hash {
		t.Errorf("a refused write changed the draft: %d", after.Revision)
	}
	for path, status := range map[string]int{
		"/v1/models/not-an-id":                             400,
		"/v1/models/" + m.LatestVersion.ID:                 400, // a version ID where a model ID goes
		"/v1/model-versions/" + m.ID:                       400,
		"/v1/projects/" + m.ID + "/models":                 400,
		"/v1/models/mdl_01M4YDQK00TA00000000000099":        404,
		"/v1/model-versions/mv_01M4YDQK00TA00000000000099": 404,
	} {
		if r := e.do("GET", path, owner.Subject, e.a.ID, nil, ""); r.status != status {
			t.Errorf("GET %s: %d %s", path, r.status, r.body)
		} else {
			code(t, r)
		}
	}
}

// D-003 over HTTP: a draft write names the revision it read. Without
// If-Match it is 428, a malformed one is 400, a stale one 409, and none of
// them changes the draft. Of two writes from the same revision exactly one
// applies (TEST-005).
func TestDraftPreconditions(t *testing.T) {
	e := setup(t)
	m := e.newModel(t)
	owner, editor := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor]
	rename := `[{"op":"replace","path":"/name","value":"Changed"}]`
	reset := fmt.Sprintf(`{"expected_latest_version_id":%q,"confirm_discard":true}`, m.LatestVersion.ID)
	pub := fmt.Sprintf(`{"expected_base_version_id":%q}`, m.LatestVersion.ID)
	writes := map[string]func(ifMatch string) resp{
		"patch": func(v string) resp { return e.patch(editor.Subject, m.ID, v, rename) },
		"reset": func(v string) resp {
			h := map[string]string{}
			if v != "" {
				h["If-Match"] = v
			}
			return e.do("POST", "/v1/models/"+m.ID+"/draft/reset", editor.Subject, e.a.ID, h, reset)
		},
		"publish": func(v string) resp { return e.publish(editor.Subject, m.ID, "key-pre-"+fmt.Sprint(len(v)), v, pub) },
	}
	for name, write := range writes {
		for v, want := range map[string]string{
			"":                     "PRECONDITION_REQUIRED",
			`draft-revision-1`:     "INVALID_REQUEST",
			`W/"draft-revision-1"`: "INVALID_REQUEST",
			`*`:                    "INVALID_REQUEST",
			`"draft-revision-7"`:   "MODEL_VERSION_CONFLICT",
		} {
			r := write(v)
			if got := code(t, r); got != want {
				t.Errorf("%s with If-Match %q: %d %s", name, v, r.status, r.body)
			}
			if want == "PRECONDITION_REQUIRED" && r.status != 428 {
				t.Errorf("%s without If-Match: status %d", name, r.status)
			}
		}
	}
	if d := decode[draftBody](t, e.do("GET", "/v1/models/"+m.ID+"/draft", owner.Subject, e.a.ID, nil, "")); d.Revision != 1 {
		t.Fatalf("refused writes changed the draft to revision %d", d.Revision)
	}

	var wg sync.WaitGroup
	results := make([]resp, 2)
	for i := range results {
		wg.Go(func() {
			results[i] = e.patch(editor.Subject, m.ID, etag(1), fmt.Sprintf(`[{"op":"replace","path":"/name","value":"Writer %d"}]`, i))
		})
	}
	wg.Wait()
	statuses := []int{results[0].status, results[1].status}
	if !(statuses[0] == 200 && statuses[1] == 409 || statuses[0] == 409 && statuses[1] == 200) {
		t.Errorf("two patches from revision 1: %v %s %s", statuses, results[0].body, results[1].body)
	}
	if d := decode[draftBody](t, e.do("GET", "/v1/models/"+m.ID+"/draft", owner.Subject, e.a.ID, nil, "")); d.Revision != 2 {
		t.Errorf("after the race the draft is at revision %d", d.Revision)
	}
}

// The patch body: application/json-patch+json, at most the IR's 2 MiB,
// the patch schema with at least one operation (else 422
// REQUEST_VALIDATION_FAILED); whether it applies to a valid model is
// MODEL_VALIDATION_FAILED, with the issues as details.
func TestPatchBody(t *testing.T) {
	e := setup(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	for name, c := range map[string]struct {
		contentType, body string
		status            int
		code, path        string
	}{
		"application/json":      {"application/json", `[{"op":"replace","path":"/name","value":"x"}]`, 415, "UNSUPPORTED_MEDIA_TYPE", ""},
		"not JSON":              {"", `[{"op":`, 400, "INVALID_REQUEST", ""},
		"no operation":          {"", `[]`, 422, "REQUEST_VALIDATION_FAILED", ""},
		"move":                  {"", `[{"op":"move","from":"/name","path":"/description"}]`, 422, "REQUEST_VALIDATION_FAILED", "/0/op"},
		"U+0000":                {"", `[{"op":"replace","path":"/name","value":"a\u0000b"}]`, 422, "REQUEST_VALIDATION_FAILED", "/0/value"},
		"too large":             {"", `[{"op":"replace","path":"/name","value":"` + strings.Repeat("x", 2<<20) + `"}]`, 413, "PAYLOAD_TOO_LARGE", ""},
		"1.5 MiB is read":       {"", `[{"op":"replace","path":"/name","value":"` + strings.Repeat("x", 3<<19) + `"}]`, 422, "MODEL_VALIDATION_FAILED", "/name"},
		"forbidden path":        {"", `[{"op":"replace","path":"/model_id","value":"mdl_01M4YDQK00TA00000000000077"}]`, 422, "MODEL_VALIDATION_FAILED", "/0/path"},
		"schema-invalid result": {"", `[{"op":"replace","path":"/name","value":""}]`, 422, "MODEL_VALIDATION_FAILED", "/name"},
	} {
		ct := "application/json-patch+json"
		if c.contentType != "" {
			ct = c.contentType
		}
		r := e.do("PATCH", "/v1/models/"+m.ID+"/draft", editor.Subject, e.a.ID, map[string]string{"Content-Type": ct, "If-Match": etag(1)}, c.body)
		if r.status != c.status || code(t, r) != c.code {
			t.Errorf("%s: %d %s", name, r.status, r.body)
			continue
		}
		if c.path != "" && !strings.Contains(string(r.body), `"path":"`+c.path+`"`) {
			t.Errorf("%s: no detail at %s: %s", name, c.path, r.body)
		}
	}
	if d := decode[draftBody](t, e.do("GET", "/v1/models/"+m.ID+"/draft", editor.Subject, e.a.ID, nil, "")); d.Revision != 1 {
		t.Errorf("refused patches changed the draft to revision %d", d.Revision)
	}
}

// Publishing: a key is required and binds the request; a clean draft is
// LIFECYCLE_CONFLICT; another base is MODEL_VERSION_CONFLICT; the comment
// follows the Publish schema; a draft with errors is not published, and
// its issues come back as details.
func TestPublishRefusals(t *testing.T) {
	e := setup(t)
	m := e.newModel(t)
	editor := e.a.Members[tenant.Editor]
	pub := func(comment string) string {
		return fmt.Sprintf(`{"expected_base_version_id":%q,"comment":%s}`, m.LatestVersion.ID, comment)
	}
	if r := e.do("POST", "/v1/models/"+m.ID+"/versions", editor.Subject, e.a.ID, map[string]string{"If-Match": etag(1)}, pub(`"x"`)); r.status != 400 || code(t, r) != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Errorf("no key: %d %s", r.status, r.body)
	}
	if r := e.publish(editor.Subject, m.ID, "key-ref-clean", etag(1), pub(`"x"`)); r.status != 409 || code(t, r) != "LIFECYCLE_CONFLICT" {
		t.Errorf("clean draft: %d %s", r.status, r.body)
	}
	if r := e.patch(editor.Subject, m.ID, etag(1), `[{"op":"replace","path":"/name","value":"Next"}]`); r.status != 200 {
		t.Fatalf("patch: %d %s", r.status, r.body)
	}
	other := `{"expected_base_version_id":"mv_01M4YDQK00TA00000000000099"}`
	if r := e.publish(editor.Subject, m.ID, "key-ref-base", etag(2), other); r.status != 409 || code(t, r) != "MODEL_VERSION_CONFLICT" {
		t.Errorf("another base: %d %s", r.status, r.body)
	}
	for name, comment := range map[string]string{
		"2001 characters": `"` + strings.Repeat("審", 2001) + `"`,
		"U+0000":          `"a\u0000b"`,
		"not a string":    `42`,
	} {
		if r := e.publish(editor.Subject, m.ID, "key-ref-c-"+fmt.Sprint(len(comment)), etag(2), pub(comment)); r.status != 422 || code(t, r) != "REQUEST_VALIDATION_FAILED" ||
			!strings.Contains(string(r.body), `"path":"/comment"`) {
			t.Errorf("comment %s: %d %s", name, r.status, r.body)
		}
	}
	if r := e.publish(editor.Subject, m.ID, "key-ref-bind", etag(2), pub(`"one"`)); r.status != 201 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	if r := e.publish(editor.Subject, m.ID, "key-ref-bind", etag(2), pub(`"two"`)); r.status != 409 || code(t, r) != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Errorf("the key with another comment: %d %s", r.status, r.body)
	}
	// Removing the sink leaves flows that point at nothing: the draft is
	// saved with the error, and publishing it is refused.
	version := decode[modelSummary](t, e.do("GET", "/v1/models/"+m.ID, editor.Subject, e.a.ID, nil, "")).LatestVersion.ID
	if r := e.patch(editor.Subject, m.ID, etag(3), `[{"op":"test","path":"/nodes/8/id","value":"deployed"},{"op":"remove","path":"/nodes/8"}]`); r.status != 200 {
		t.Fatalf("breaking patch: %d %s", r.status, r.body)
	}
	r := e.publish(editor.Subject, m.ID, "key-ref-invalid", etag(4), fmt.Sprintf(`{"expected_base_version_id":%q}`, version))
	if r.status != 422 || code(t, r) != "MODEL_VALIDATION_FAILED" || !strings.Contains(string(r.body), `"code":"UNKNOWN_REFERENCE"`) {
		t.Errorf("invalid draft: %d %s", r.status, r.body)
	}
}

type listPage struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// The version list pages with the contract's cursor and limit, newest
// first; a cursor is bound to its list, and a bad limit or cursor is 400.
func TestModelLists(t *testing.T) {
	e := setup(t)
	m := e.newModel(t)
	owner := e.a.Members[tenant.Owner]
	for rev := int64(1); rev <= 3; rev += 2 {
		if r := e.patch(owner.Subject, m.ID, etag(rev), fmt.Sprintf(`[{"op":"replace","path":"/name","value":"Name %d"}]`, rev)); r.status != 200 {
			t.Fatalf("patch: %d %s", r.status, r.body)
		}
		latest := decode[modelSummary](t, e.do("GET", "/v1/models/"+m.ID, owner.Subject, e.a.ID, nil, "")).LatestVersion.ID
		if r := e.publish(owner.Subject, m.ID, fmt.Sprintf("key-lists-%d", rev), etag(rev+1), fmt.Sprintf(`{"expected_base_version_id":%q}`, latest)); r.status != 201 {
			t.Fatalf("publish: %d %s", r.status, r.body)
		}
	}
	versions := "/v1/models/" + m.ID + "/versions"
	var seen []string
	for next := versions + "?limit=2"; next != ""; {
		r := e.do("GET", next, owner.Subject, e.a.ID, nil, "")
		if r.status != 200 {
			t.Fatalf("GET %s: %d %s", next, r.status, r.body)
		}
		valid(t, r, "http.schema.json#/$defs/Versions")
		p := decode[listPage](t, r)
		for _, it := range p.Items {
			seen = append(seen, it.ID)
		}
		next = ""
		if p.NextCursor != nil {
			next = versions + "?limit=2&cursor=" + *p.NextCursor
		}
	}
	if len(seen) != 3 || seen[2] != m.LatestVersion.ID {
		t.Errorf("versions, newest first, end with version 1: %v", seen)
	}
	first := decode[listPage](t, e.do("GET", versions+"?limit=1", owner.Subject, e.a.ID, nil, ""))
	if first.NextCursor == nil {
		t.Fatal("no cursor after the first of three versions")
	}
	other := e.newModelIn(t, "-other")
	for path, want := range map[string]string{
		versions + "?limit=0":   "INVALID_REQUEST",
		versions + "?limit=101": "INVALID_REQUEST",
		versions + "?cursor=x":  "INVALID_REQUEST",
		"/v1/models/" + other.ID + "/versions?cursor=" + *first.NextCursor: "INVALID_REQUEST",
	} {
		if r := e.do("GET", path, owner.Subject, e.a.ID, nil, ""); r.status != 400 || code(t, r) != want {
			t.Errorf("GET %s: %d %s", path, r.status, r.body)
		}
	}
}
