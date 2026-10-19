package me_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/tenant/me"
)

const user = "usr_01M4YDQK0016KTK4JM6KAHZ6BR"

type verifier struct{ err error }

func (v verifier) Verify(context.Context, string) (auth.Identity, error) {
	return auth.Identity{Issuer: "https://id.test", Subject: "sub-1"}, v.err
}

type resolver struct {
	err    error
	called bool
}

func (r *resolver) ResolveUser(_ context.Context, iss, sub string) (string, error) {
	r.called = true
	if iss != "https://id.test" || sub != "sub-1" {
		return "", errors.New("resolved another identity")
	}
	return user, r.err
}

type reader struct {
	out me.Me
	err error
	got []string
}

func (r *reader) Get(_ context.Context, u string) (me.Me, error) {
	r.got = append(r.got, u)
	return r.out, r.err
}

var three = me.Me{UserID: user, Memberships: []me.Membership{
	{OrganizationID: "org_01M4YDQK004J6EG1BW3SRYPDGD", OrganizationName: "Acme Delivery", Role: tenant.Viewer},
	{OrganizationID: "org_01M4YDQK00Y5H2929M9WWZWTVH", OrganizationName: "Acme Delivery", Role: tenant.Owner},
	{OrganizationID: "org_01M4YDQK00TA00000000000003", OrganizationName: "研發部", Role: tenant.Editor},
}}

func serve(t *testing.T, v auth.Verifier, users *resolver, r *reader, method string, headers map[string][]string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var logs bytes.Buffer
	mux := http.NewServeMux()
	me.Register(mux, slog.New(slog.NewJSONHandler(&logs, nil)), v, users, r)
	req := httptest.NewRequest(method, "/v1/me", nil)
	for k, vs := range headers {
		for _, x := range vs {
			req.Header.Add(k, x)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w, logs.String()
}

type envelope struct {
	Error struct {
		Code    string `json:"code"`
		Details []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"details"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

// The handler: the resolved user's list as the schema's Me, never cached
// and never logged; X-Organization-ID, a missing token and an unknown
// identity are refused before the list is read.
func TestHandler(t *testing.T) {
	bearer := []string{"Bearer a.b.c"}
	for _, c := range []struct {
		name    string
		v       verifier
		users   *resolver
		r       *reader
		headers map[string][]string
		status  int
		code    string
	}{
		{"three memberships", verifier{}, &resolver{}, &reader{out: three}, map[string][]string{"Authorization": bearer}, 200, ""},
		{"none", verifier{}, &resolver{}, &reader{out: me.Me{UserID: user, Memberships: []me.Membership{}}}, map[string][]string{"Authorization": bearer}, 200, ""},
		{"truncated", verifier{}, &resolver{}, &reader{out: me.Me{UserID: user, Memberships: three.Memberships, Truncated: true}}, map[string][]string{"Authorization": bearer}, 200, ""},
		{"an organization selected", verifier{}, &resolver{}, &reader{out: three}, map[string][]string{"Authorization": bearer, "X-Organization-ID": {"org_01M4YDQK00Y5H2929M9WWZWTVH"}}, 400, "INVALID_REQUEST"},
		{"an empty selector", verifier{}, &resolver{}, &reader{out: three}, map[string][]string{"Authorization": bearer, "X-Organization-ID": {""}}, 400, "INVALID_REQUEST"},
		{"no token", verifier{}, &resolver{}, &reader{out: three}, nil, 401, "UNAUTHENTICATED"},
		{"a rejected token", verifier{err: &auth.TokenError{Reason: auth.ReasonExpired}}, &resolver{}, &reader{out: three}, map[string][]string{"Authorization": bearer}, 401, "UNAUTHENTICATED"},
		{"an unknown identity", verifier{}, &resolver{err: tenant.ErrUnknownUser}, &reader{out: three}, map[string][]string{"Authorization": bearer}, 403, "FORBIDDEN"},
		{"the database must be retried", verifier{}, &resolver{}, &reader{err: tenant.ErrRetryable}, map[string][]string{"Authorization": bearer}, 503, "SERVICE_UNAVAILABLE"},
		{"a broken scope", verifier{}, &resolver{}, &reader{err: tenant.ErrInvalidScope}, map[string][]string{"Authorization": bearer}, 500, "INTERNAL_ERROR"},
	} {
		w, logs := serve(t, c.v, c.users, c.r, "GET", c.headers)
		if w.Code != c.status {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
			continue
		}
		h := w.Header()
		if h.Get("Content-Type") != "application/json" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" ||
			!strings.HasPrefix(h.Get("X-Request-ID"), "req_") {
			t.Errorf("%s: headers %v", c.name, h)
		}
		for _, m := range three.Memberships {
			if strings.Contains(logs, m.OrganizationName) {
				t.Errorf("%s: the log names an organization: %s", c.name, logs)
			}
		}
		if c.status == 200 {
			if issues, err := validate.Document(w.Body.Bytes(), "http.schema.json#/$defs/Me", "RESPONSE"); err != nil || len(issues) > 0 {
				t.Errorf("%s: %s does not match Me: %v %v", c.name, w.Body, issues, err)
			}
			var got me.Me
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.UserID != c.r.out.UserID || got.Truncated != c.r.out.Truncated ||
				!slices.Equal(got.Memberships, c.r.out.Memberships) {
				t.Errorf("%s: body %s, want %+v (%v)", c.name, w.Body, c.r.out, err)
			}
			if !slices.Equal(c.r.got, []string{user}) || logs != "" {
				t.Errorf("%s: read for %v; logs %q", c.name, c.r.got, logs)
			}
			continue
		}
		var e envelope
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error.Code != c.code || e.Error.RequestID != h.Get("X-Request-ID") {
			t.Errorf("%s: %s (%v)", c.name, w.Body, err)
		}
		if c.status == 400 && (len(e.Error.Details) != 1 || e.Error.Details[0].Reason != "X-Organization-ID") {
			t.Errorf("%s: details %+v, want one naming the header", c.name, e.Error.Details)
		}
		if refusedFirst := c.status < 500; refusedFirst && len(c.r.got) != 0 {
			t.Errorf("%s: the list was read for a refused request", c.name)
		}
		if (c.status == 400 || c.status == 401) && c.users.called {
			t.Errorf("%s: a user was resolved before the request was refused", c.name)
		}
		if c.status == 401 && h.Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate", c.name)
		}
	}
	// The empty list is [], not null.
	w, _ := serve(t, verifier{}, &resolver{}, &reader{out: me.Me{UserID: user, Memberships: []me.Membership{}}}, "GET", map[string][]string{"Authorization": bearer})
	if !strings.Contains(w.Body.String(), `"memberships":[]`) {
		t.Errorf("no memberships: %s", w.Body)
	}
}

// The route is the contract's: GET /v1/me, scope "user", no role
// (planning/api-operations.json, from openapi.yaml).
func TestRoutesMatchContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(contracttest.Root(t), "planning", "api-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	type operation struct {
		Method      string   `json:"method"`
		Path        string   `json:"path"`
		OperationID string   `json:"operation_id"`
		Roles       []string `json:"roles"`
		Scope       string   `json:"scope"`
	}
	var doc struct {
		Operations []operation `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(me.Routes) != 1 {
		t.Fatalf("routes %+v", me.Routes)
	}
	for _, r := range me.Routes {
		i := slices.IndexFunc(doc.Operations, func(o operation) bool { return o.OperationID == r.OperationID })
		if i < 0 {
			t.Fatalf("%s is not a contract operation", r.OperationID)
		}
		o := doc.Operations[i]
		if o.Method != r.Method || o.Path != r.Path || o.Scope != r.Scope || r.Scope != "user" || len(o.Roles) != 0 || len(r.Roles) != 0 {
			t.Errorf("%s: route %+v, contract %+v", r.OperationID, r, o)
		}
	}
	// Every other operation is in an organization and names its roles.
	for _, o := range doc.Operations {
		if o.OperationID != "getMe" && (o.Scope != "" || len(o.Roles) == 0) {
			t.Errorf("%s: scope %q, roles %v", o.OperationID, o.Scope, o.Roles)
		}
	}
}

// Me and Membership are the schema's: the fixtures decode strictly and
// round-trip unchanged.
func TestFixturesRoundTrip(t *testing.T) {
	root := contracttest.Root(t)
	n := 0
	for _, c := range contracttest.Cases(t) {
		if c.Schema != "schemas/http.schema.json#/$defs/Me" || !c.Valid {
			continue
		}
		contracttest.RoundTrip[me.Me](t, c.ID, contracttest.ReadFile(t, filepath.Join(root, c.Instance)))
		n++
	}
	if n != 2 {
		t.Errorf("%d Me fixtures, want valid-me and valid-me-no-memberships", n)
	}
}
