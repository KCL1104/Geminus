package httpapi

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/schemas"
)

// The code catalogue and its statuses are the schema's (D-025): every code
// of the enum, each with its x-http-status, and nothing else.
func TestStatusMatchesSchema(t *testing.T) {
	data, err := schemas.FS.ReadFile("common.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs struct {
			ErrorCode struct {
				Enum   []string       `json:"enum"`
				Status map[string]int `json:"x-http-status"`
			} `json:"ErrorCode"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for c, s := range Status {
		got[string(c)] = s
	}
	if !maps.Equal(got, doc.Defs.ErrorCode.Status) {
		t.Errorf("Status %v, schema %v", got, doc.Defs.ErrorCode.Status)
	}
	if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, slices.Sorted(slices.Values(doc.Defs.ErrorCode.Enum))) {
		t.Errorf("codes %v, schema enum %v", keys, doc.Defs.ErrorCode.Enum)
	}
}

func serve(log *slog.Logger, f HandlerFunc, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	Handler(log, f).ServeHTTP(w, r)
	return w
}

type errorEnvelope struct {
	Error struct {
		Code      Code     `json:"code"`
		Message   string   `json:"message"`
		Details   []Detail `json:"details"`
		RequestID string   `json:"request_id"`
	} `json:"error"`
}

// checkEnvelope asserts a schema-valid error body whose request_id is the
// response's X-Request-ID, and returns it.
func checkEnvelope(t *testing.T, w *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	if issues, err := validate.Document(w.Body.Bytes(), "common.schema.json#/$defs/Error", "ERR"); err != nil || len(issues) > 0 {
		t.Errorf("error body %s: %v %v", w.Body, issues, err)
	}
	var e errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.Error.RequestID == "" || e.Error.RequestID != w.Header().Get("X-Request-ID") {
		t.Errorf("request_id %q, header %q", e.Error.RequestID, w.Header().Get("X-Request-ID"))
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("headers %v: an API response is not sniffed or cached", w.Header())
	}
	return e
}

// Every error a handler can return becomes one envelope with the code's
// status; internal errors say nothing about their cause and are logged.
func TestWriteError(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		status    int
		code      Code
		challenge string
	}{
		{"typed error", &Error{Code: LifecycleConflict, Message: "m", Details: []Detail{{Path: "/x", Reason: "r"}}}, 409, LifecycleConflict, ""},
		{"wrapped typed error", fmt.Errorf("context: %w", Errorf(NotFound, "m")), 404, NotFound, ""},
		{"missing token", &auth.TokenError{Reason: auth.ReasonMissing}, 401, Unauthenticated, `Bearer realm="geminus"`},
		{"bad token", &auth.TokenError{Reason: auth.ReasonExpired}, 401, Unauthenticated, `Bearer realm="geminus", error="invalid_token"`},
		{"keys unavailable", fmt.Errorf("x: %w", auth.ErrKeysUnavailable), 503, ServiceUnavailable, ""},
		{"unknown user", tenant.ErrUnknownUser, 403, Forbidden, ""},
		{"no membership", tenant.ErrNoMembership, 404, NotFound, ""},
		{"role", tenant.ErrForbidden, 403, Forbidden, ""},
		{"retryable", fmt.Errorf("tx: %w", tenant.ErrRetryable), 503, ServiceUnavailable, ""},
		{"outcome unknown", tenant.ErrOutcomeUnknown, 503, ServiceUnavailable, ""},
		{"cancelled", context.Canceled, 503, ServiceUnavailable, ""},
		{"deadline", fmt.Errorf("query: %w", context.DeadlineExceeded), 503, ServiceUnavailable, ""},
		{"internal", errors.New("pq: relation twin.secret_table does not exist at 10.0.0.5"), 500, InternalError, ""},
		{"code outside the catalogue", &Error{Code: "TEAPOT", Message: "m"}, 500, InternalError, ""},
	} {
		var logs bytes.Buffer
		w := serve(slog.New(slog.NewTextHandler(&logs, nil)), func(http.ResponseWriter, *http.Request) error { return c.err },
			httptest.NewRequest("GET", "/", nil))
		e := checkEnvelope(t, w)
		if w.Code != c.status || e.Error.Code != c.code {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
		if got := w.Header().Get("WWW-Authenticate"); got != c.challenge {
			t.Errorf("%s: WWW-Authenticate %q, want %q", c.name, got, c.challenge)
		}
		if c.code == InternalError {
			if strings.Contains(w.Body.String(), "secret_table") || strings.Contains(w.Body.String(), "10.0.0.5") || e.Error.Message != "internal error" {
				t.Errorf("%s: the body reveals the cause: %s", c.name, w.Body)
			}
			if !strings.Contains(logs.String(), e.Error.RequestID) || (c.name == "internal" && !strings.Contains(logs.String(), "secret_table")) {
				t.Errorf("%s: not logged with its request ID and cause: %s", c.name, logs.String())
			}
			if c.name == "code outside the catalogue" && (!strings.Contains(logs.String(), "unknown error code") || strings.Contains(logs.String(), "panicked")) {
				t.Errorf("%s: not logged as an unknown code: %s", c.name, logs.String())
			}
		}
	}
}

// The selector's ID format is the schema's (D-001), not a second copy.
// T-054 review L3: the one 503 of a snapshot that could not be read, the
// same on every route: SERVICE_UNAVAILABLE, the route's message and
// details[0].code DATASET_STORAGE_FAILED; written like any typed error,
// without an ERROR log.
func TestDatasetStorageFailed(t *testing.T) {
	e := DatasetStorageFailed("nothing was changed (retry later)")
	if e.Code != ServiceUnavailable || Status[e.Code] != 503 || e.Message != "nothing was changed (retry later)" ||
		!slices.Equal(e.Details, []Detail{{Path: "", Reason: "the dataset storage failed", Code: "DATASET_STORAGE_FAILED"}}) {
		t.Errorf("%+v", e)
	}
	var logs bytes.Buffer
	w := serve(slog.New(slog.NewTextHandler(&logs, nil)), func(http.ResponseWriter, *http.Request) error { return fmt.Errorf("x: %w", e) },
		httptest.NewRequest("POST", "/", nil))
	if env := checkEnvelope(t, w); w.Code != 503 || env.Error.Code != ServiceUnavailable || logs.Len() != 0 {
		t.Errorf("%d %s, log %q", w.Code, w.Body, logs.String())
	}
	if DatasetStorageFailed("a") == DatasetStorageFailed("a") {
		t.Error("one shared *Error: a route could change another's")
	}
}

func TestOrganizationIDMatchesSchema(t *testing.T) {
	data, err := schemas.FS.ReadFile("common.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if got, want := orgID.String(), doc.Defs["OrganizationID"].Pattern; got != want || want == "" {
		t.Errorf("X-Organization-ID pattern %q, schema OrganizationID %q", got, want)
	}
}

// Request IDs are the server's, never the client's, and unique.
func TestRequestID(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Request-ID", "req_01AAAAAAAAAAAAAAAAAAAAAAAA")
		var inside string
		w := serve(slog.Default(), func(w http.ResponseWriter, r *http.Request) error { inside = RequestID(r.Context()); return nil }, r)
		id := w.Header().Get("X-Request-ID")
		if id != inside || seen[id] || id == "req_01AAAAAAAAAAAAAAAAAAAAAAAA" || len(id) != 30 || !strings.HasPrefix(id, "req_") {
			t.Fatalf("request ID %q (inside %q)", id, inside)
		}
		seen[id] = true
	}
	if RequestID(context.Background()) != "" {
		t.Error("a request ID outside a request")
	}
}

// A panicking handler is a logged INTERNAL_ERROR; http.ErrAbortHandler
// keeps its meaning (abort the response).
func TestRecover(t *testing.T) {
	var logs bytes.Buffer
	w := serve(slog.New(slog.NewTextHandler(&logs, nil)), func(http.ResponseWriter, *http.Request) error { panic("boom") }, httptest.NewRequest("GET", "/", nil))
	if e := checkEnvelope(t, w); w.Code != 500 || e.Error.Code != InternalError || !strings.Contains(logs.String(), "boom") {
		t.Errorf("panic: %d %s %s", w.Code, w.Body, logs.String())
	}
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("ErrAbortHandler was swallowed")
		}
	}()
	serve(slog.Default(), func(http.ResponseWriter, *http.Request) error { panic(http.ErrAbortHandler) }, httptest.NewRequest("GET", "/", nil))
}

type createProject struct {
	Name string `json:"name"`
}

// Bodies: media type, size, well-formedness and schema, each with its code
// and a JSON Pointer for schema failures.
func TestDecodeJSON(t *testing.T) {
	const ref = "http.schema.json#/$defs/CreateProject"
	for _, c := range []struct {
		name, contentType, body string
		status                  int
		code                    Code
		path                    string
	}{
		{"valid", "application/json", `{"name":"P"}`, 204, "", ""},
		{"charset utf-8", "application/json; charset=utf-8", `{"name":"P"}`, 204, "", ""},
		{"charset in other case", "Application/JSON; Charset=Utf-8", `{"name":"P"}`, 204, "", ""},
		{"exactly the limit", "application/json", `{"name":"P"}` + strings.Repeat(" ", 1024-12), 204, "", ""},
		{"one byte over the limit", "application/json", `{"name":"P"}` + strings.Repeat(" ", 1024-11), 413, PayloadTooLarge, ""},
		{"parameter without a value", "application/json; charset", `{"name":"P"}`, 415, UnsupportedMediaType, ""},
		{"no content type", "", `{"name":"P"}`, 415, UnsupportedMediaType, ""},
		{"text/plain", "text/plain", `{"name":"P"}`, 415, UnsupportedMediaType, ""},
		{"latin-1", "application/json; charset=latin1", `{"name":"P"}`, 415, UnsupportedMediaType, ""},
		{"extra parameter", "application/json; profile=x", `{"name":"P"}`, 415, UnsupportedMediaType, ""},
		{"other parameter valued utf-8", "application/json; profile=utf-8", `{"name":"P"}`, 415, UnsupportedMediaType, ""},
		{"json-patch", "application/json-patch+json", `{"name":"P"}`, 415, UnsupportedMediaType, ""},
		{"too large", "application/json", `{"name":"` + strings.Repeat("a", 2000) + `"}`, 413, PayloadTooLarge, ""},
		{"malformed", "application/json", `{"name":`, 400, InvalidRequest, ""},
		{"duplicate member", "application/json", `{"name":"a","name":"b"}`, 400, InvalidRequest, ""},
		{"invalid UTF-8", "application/json", "{\"name\":\"\xff\"}", 400, InvalidRequest, ""},
		{"trailing data", "application/json", `{"name":"a"} {}`, 400, InvalidRequest, ""},
		{"unknown member", "application/json", `{"name":"a","x":1}`, 422, RequestValidationFailed, ""},
		{"wrong type", "application/json", `{"name":1}`, 422, RequestValidationFailed, "/name"},
		{"missing name", "application/json", `{}`, 422, RequestValidationFailed, ""},
		{"empty name", "application/json", `{"name":""}`, 422, RequestValidationFailed, "/name"},
		{"two failures", "application/json", `{"name":"","x":1}`, 422, RequestValidationFailed, "/name"},
		{"U+0000 in a value", "application/json", `{"name":"P\u0000"}`, 422, RequestValidationFailed, "/name"},
		{"array", "application/json", `[]`, 422, RequestValidationFailed, ""},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(c.body))
		if c.contentType != "" {
			r.Header.Set("Content-Type", c.contentType)
		}
		var got createProject
		var raw []byte
		w := serve(slog.Default(), func(w http.ResponseWriter, r *http.Request) error {
			var err error
			raw, err = DecodeJSON(r, 1024, ref, &got)
			if err == nil {
				w.WriteHeader(204)
			}
			return err
		}, r)
		if w.Code != c.status {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
			continue
		}
		if c.status == 204 {
			if got.Name != "P" || string(raw) != c.body {
				t.Errorf("%s: decoded %+v, raw %q", c.name, got, raw)
			}
			continue
		}
		e := checkEnvelope(t, w)
		if e.Error.Code != c.code {
			t.Errorf("%s: code %s", c.name, e.Error.Code)
		}
		if c.path != "" && !slices.ContainsFunc(e.Error.Details, func(d Detail) bool { return d.Path == c.path }) {
			t.Errorf("%s: no detail at %s: %v", c.name, c.path, e.Error.Details)
		}
		if (c.code == RequestValidationFailed || c.code == InvalidRequest) && len(e.Error.Details) == 0 {
			t.Errorf("%s: no details", c.name)
		}
		if c.code == RequestValidationFailed && slices.ContainsFunc(e.Error.Details, func(d Detail) bool { return d.Severity != "error" }) {
			t.Errorf("%s: a schema failure without its severity: %v", c.name, e.Error.Details)
		}
		if c.name == "two failures" && len(e.Error.Details) < 2 {
			t.Errorf("%s: one detail per failure expected: %v", c.name, e.Error.Details)
		}
	}
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// truncated is a valid document followed by a read error: what was read
// is not the whole body, so it is not used.
func truncated() io.Reader { return io.MultiReader(strings.NewReader(`{"name":"P"}`), failingBody{}) }

// A body that cannot be read is the client's 400; a schema reference that
// does not resolve, or a schema-valid body that does not fit the Go type,
// is the server's fault: 500, and never a success.
func TestDecodeJSONFailures(t *testing.T) {
	for _, c := range []struct {
		name   string
		body   io.Reader
		ref    string
		dst    any
		status int
	}{
		{"unreadable body", failingBody{}, "http.schema.json#/$defs/CreateProject", &createProject{}, 400},
		{"body cut off after a valid document", truncated(), "http.schema.json#/$defs/CreateProject", &createProject{}, 400},
		{"unknown schema", strings.NewReader(`{"name":"P"}`), "http.schema.json#/$defs/NoSuchThing", &createProject{}, 500},
		{"type drifted from the schema", strings.NewReader(`{"name":"P"}`), "http.schema.json#/$defs/CreateProject", new(int), 500},
	} {
		r := httptest.NewRequest("POST", "/", c.body)
		r.Header.Set("Content-Type", "application/json")
		w := serve(slog.New(slog.DiscardHandler), func(w http.ResponseWriter, r *http.Request) error {
			_, err := DecodeJSON(r, 1024, c.ref, c.dst)
			if err == nil {
				w.WriteHeader(204)
			}
			return err
		}, r)
		if w.Code != c.status {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
			continue
		}
		checkEnvelope(t, w)
	}
}

// A value that cannot be encoded is still an error envelope, with the
// request's ID.
func TestWriteJSONFallback(t *testing.T) {
	w := serve(slog.Default(), func(w http.ResponseWriter, r *http.Request) error {
		WriteJSON(w, 200, make(chan int))
		return nil
	}, httptest.NewRequest("GET", "/", nil))
	if e := checkEnvelope(t, w); w.Code != 500 || e.Error.Code != InternalError {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

type fakeVerifier struct {
	id  auth.Identity
	err error
}

func (f fakeVerifier) Verify(context.Context, string) (auth.Identity, error) { return f.id, f.err }

type fakeResolver struct {
	user string
	err  error
	got  [2]string
}

func (f *fakeResolver) ResolveUser(_ context.Context, iss, sub string) (string, error) {
	f.got = [2]string{iss, sub}
	return f.user, f.err
}

// Scoped: the token is verified before anything else, the selector must be
// one organization ID, and the scope carries the resolved user.
func TestScoped(t *testing.T) {
	const org = "org_01M4YDQK00TA00000000000000"
	const user = "usr_01M4YDQK00TA00000000000001"
	good := fakeVerifier{id: auth.Identity{Issuer: "https://id", Subject: "s"}}
	for _, c := range []struct {
		name     string
		v        auth.Verifier
		resolver *fakeResolver
		headers  map[string][]string
		status   int
		code     Code
	}{
		{"ok", good, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {org}}, 204, ""},
		{"no token", good, &fakeResolver{user: user}, map[string][]string{"X-Organization-ID": {org}}, 401, Unauthenticated},
		{"token in the query only", good, &fakeResolver{user: user}, map[string][]string{"X-Organization-ID": {org}}, 401, Unauthenticated},
		{"rejected token", fakeVerifier{err: &auth.TokenError{Reason: auth.ReasonSignature}}, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {org}}, 401, Unauthenticated},
		{"keys unavailable", fakeVerifier{err: fmt.Errorf("jwks: %w", auth.ErrKeysUnavailable)}, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {org}}, 503, ServiceUnavailable},
		{"no selector", good, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}}, 400, InvalidRequest},
		{"two selectors", good, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {org, org}}, 400, InvalidRequest},
		{"malformed selector", good, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {"org_x"}}, 400, InvalidRequest},
		{"lowercase selector", good, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {strings.ToLower(org)}}, 400, InvalidRequest},
		{"selector with a suffix", good, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {org + "0"}}, 400, InvalidRequest},
		{"selector with a prefix", good, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {"x" + org}}, 400, InvalidRequest},
		{"selector beyond 128 bits", good, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {"org_8" + org[5:]}}, 400, InvalidRequest},
		{"selector of another kind", good, &fakeResolver{user: user}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {"prj_" + org[4:]}}, 400, InvalidRequest},
		{"unknown user", good, &fakeResolver{err: tenant.ErrUnknownUser}, map[string][]string{"Authorization": {"Bearer a.b.c"}, "X-Organization-ID": {org}}, 403, Forbidden},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if c.name == "token in the query only" {
			r = httptest.NewRequest("GET", "/?access_token=a.b.c", nil)
		}
		for k, vs := range c.headers {
			for _, v := range vs {
				r.Header.Add(k, v)
			}
		}
		var scope tenant.Scope
		var verified bool
		w := serve(slog.Default(), Scoped(c.v, c.resolver, func(w http.ResponseWriter, r *http.Request, s tenant.Scope) error {
			scope = s
			_, verified = auth.IdentityFrom(r.Context())
			w.WriteHeader(204)
			return nil
		}), r)
		if w.Code != c.status {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
			continue
		}
		if c.status == 204 {
			if scope != (tenant.Scope{OrganizationID: org, UserID: user}) || !verified || c.resolver.got != [2]string{"https://id", "s"} {
				t.Errorf("%s: scope %+v, identity in context %v, resolved %v", c.name, scope, verified, c.resolver.got)
			}
			continue
		}
		if e := checkEnvelope(t, w); e.Error.Code != c.code {
			t.Errorf("%s: %s", c.name, w.Body)
		}
		if c.status != 403 && c.resolver.got != [2]string{} {
			t.Errorf("%s: resolved a user before refusing the request", c.name)
		}
	}
}

// Self (D-045): the token is verified first, any X-Organization-ID is
// refused before a user is resolved, an unknown identity is 403, and next
// gets the resolved user with the identity in its context.
func TestSelf(t *testing.T) {
	const org = "org_01M4YDQK00TA00000000000000"
	const user = "usr_01M4YDQK00TA00000000000001"
	good := fakeVerifier{id: auth.Identity{Issuer: "https://id", Subject: "s"}}
	bearer := []string{"Bearer a.b.c"}
	for _, c := range []struct {
		name     string
		v        auth.Verifier
		resolver *fakeResolver
		headers  map[string][]string
		status   int
		code     Code
	}{
		{"ok", good, &fakeResolver{user: user}, map[string][]string{"Authorization": bearer}, 204, ""},
		{"no token", good, &fakeResolver{user: user}, nil, 401, Unauthenticated},
		{"token in the query only", good, &fakeResolver{user: user}, nil, 401, Unauthenticated},
		{"rejected token", fakeVerifier{err: &auth.TokenError{Reason: auth.ReasonSignature}}, &fakeResolver{user: user}, map[string][]string{"Authorization": bearer}, 401, Unauthenticated},
		{"rejected token and a selector", fakeVerifier{err: &auth.TokenError{Reason: auth.ReasonSignature}}, &fakeResolver{user: user}, map[string][]string{"Authorization": bearer, "X-Organization-ID": {org}}, 401, Unauthenticated},
		{"keys unavailable", fakeVerifier{err: fmt.Errorf("jwks: %w", auth.ErrKeysUnavailable)}, &fakeResolver{user: user}, map[string][]string{"Authorization": bearer}, 503, ServiceUnavailable},
		{"a selector", good, &fakeResolver{user: user}, map[string][]string{"Authorization": bearer, "X-Organization-ID": {org}}, 400, InvalidRequest},
		{"an empty selector", good, &fakeResolver{user: user}, map[string][]string{"Authorization": bearer, "X-Organization-ID": {""}}, 400, InvalidRequest},
		{"two selectors", good, &fakeResolver{user: user}, map[string][]string{"Authorization": bearer, "X-Organization-ID": {org, org}}, 400, InvalidRequest},
		{"a malformed selector", good, &fakeResolver{user: user}, map[string][]string{"Authorization": bearer, "X-Organization-ID": {"org_x"}}, 400, InvalidRequest},
		{"a selector in lower case", good, &fakeResolver{user: user}, map[string][]string{"Authorization": bearer, "x-organization-id": {org}}, 400, InvalidRequest},
		{"unknown user", good, &fakeResolver{err: tenant.ErrUnknownUser}, map[string][]string{"Authorization": bearer}, 403, Forbidden},
		{"resolver unavailable", good, &fakeResolver{err: tenant.ErrRetryable}, map[string][]string{"Authorization": bearer}, 503, ServiceUnavailable},
	} {
		r := httptest.NewRequest("GET", "/v1/me", nil)
		if c.name == "token in the query only" {
			r = httptest.NewRequest("GET", "/v1/me?access_token=a.b.c", nil)
		}
		for k, vs := range c.headers {
			for _, v := range vs {
				r.Header.Add(k, v)
			}
		}
		var got string
		var verified, called bool
		w := serve(slog.Default(), Self(c.v, c.resolver, func(w http.ResponseWriter, r *http.Request, u string) error {
			got, called = u, true
			_, verified = auth.IdentityFrom(r.Context())
			w.WriteHeader(204)
			return nil
		}), r)
		if w.Code != c.status {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
			continue
		}
		if c.status == 204 {
			if got != user || !verified || c.resolver.got != [2]string{"https://id", "s"} {
				t.Errorf("%s: user %q, identity in context %v, resolved %v", c.name, got, verified, c.resolver.got)
			}
			continue
		}
		e := checkEnvelope(t, w)
		if e.Error.Code != c.code || called {
			t.Errorf("%s: %s (next called: %v)", c.name, w.Body, called)
		}
		if c.code == InvalidRequest && (len(e.Error.Details) != 1 || e.Error.Details[0].Reason != "X-Organization-ID" || e.Error.Details[0].Path != "") {
			t.Errorf("%s: details %+v, want one naming the header", c.name, e.Error.Details)
		}
		if c.resolver.err == nil && c.resolver.got != [2]string{} {
			t.Errorf("%s: resolved a user before refusing the request", c.name)
		}
	}
}

// U+0000 is found in values and member names at any depth, with the
// pointer of the value or of the object holding the name (review L1).
func TestNULStrings(t *testing.T) {
	for doc, want := range map[string]string{
		`{"a":"x\u0000"}`:                    "/a",
		`{"a":{"b":["ok","\u0000"]}}`:        "/a/b/1",
		`{"x":{"k\u0000":1}}`:                "/x",
		`{"k\u0000":1}`:                      "",
		`["\u0000"]`:                         "/0",
		`{"a":[[[[[[[[[["\u0000"]]]]]]]]]]}`: "/a/0/0/0/0/0/0/0/0/0/0",
	} {
		if got, ok := nulString([]byte(doc)); !ok || got != want {
			t.Errorf("%s: %q %v, want %q", doc, got, ok, want)
		}
	}
	for _, doc := range []string{`{"a":"x"}`, `{"a":"\\u0000"}`, `{"a":"\u0001"}`, `{}`} {
		if got, ok := nulString([]byte(doc)); ok {
			t.Errorf("%s: U+0000 reported at %q", doc, got)
		}
	}
}

// The U+0000 detail fits common.schema.json's path limit however deep or
// long the member names are: like a validator issue, the pointer is cut to
// the deepest ancestor that fits (T-009 re-review R2). The IR's metadata
// takes any member names, so a schema-valid body can carry such a path.
func TestNULPathBounded(t *testing.T) {
	var ir map[string]any
	if err := json.Unmarshal(contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/golden-models/default-delivery.json")), &ir); err != nil {
		t.Fatal(err)
	}
	a, b, c := strings.Repeat("a", 1000), strings.Repeat("b", 1000), strings.Repeat("c", 1000)
	ir["metadata"] = map[string]any{a: map[string]any{b: map[string]any{c: "x\x00"}}}
	body, err := json.Marshal(ir)
	if err != nil {
		t.Fatal(err)
	}
	full := "/metadata/" + a + "/" + b + "/" + c
	r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	var dst map[string]any
	_, err = DecodeJSON(r, MaxBodyBytes, "model-ir.schema.json", &dst)
	var e *Error
	if !errors.As(err, &e) || e.Code != RequestValidationFailed || len(e.Details) != 1 {
		t.Fatalf("%v", err)
	}
	p := e.Details[0].Path
	if len([]rune(p)) > 2048 || p != "/metadata/"+a+"/"+b || !strings.HasPrefix(full, p) {
		t.Errorf("path of %d characters: %.40q…", len([]rune(p)), p)
	}
}
