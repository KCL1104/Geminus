package auth_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kcl1104/geminus/internal/auth"
)

func TestBearerToken(t *testing.T) {
	for _, c := range []struct {
		name, url string
		headers   []string
		token     string
		reason    string
	}{
		{"bearer", "/", []string{"Bearer abc.def.ghi"}, "abc.def.ghi", ""},
		{"scheme is case-insensitive", "/", []string{"bearer abc.def.ghi"}, "abc.def.ghi", ""},
		{"missing", "/", nil, "", auth.ReasonMissing},
		{"basic", "/", []string{"Basic dXNlcjpwYXNz"}, "", auth.ReasonScheme},
		{"no token", "/", []string{"Bearer"}, "", auth.ReasonScheme},
		{"empty token", "/", []string{"Bearer "}, "", auth.ReasonScheme},
		{"two spaces", "/", []string{"Bearer  abc"}, "", auth.ReasonScheme},
		{"two headers", "/", []string{"Bearer a.b.c", "Bearer d.e.f"}, "", auth.ReasonScheme},
		{"not token68", "/", []string{"Bearer a.b.c,other"}, "", auth.ReasonScheme},
		{"token in the query", "/models?access_token=a.b.c", nil, "", auth.ReasonTokenInQuery},
		{"token in the query and the header", "/models?access_token=x", []string{"Bearer a.b.c"}, "", auth.ReasonTokenInQuery},
		{"id_token in the query", "/models?id_token=x", []string{"Bearer a.b.c"}, "", auth.ReasonTokenInQuery},
	} {
		r := httptest.NewRequest(http.MethodGet, c.url, nil)
		for _, h := range c.headers {
			r.Header.Add("Authorization", h)
		}
		tok, err := auth.BearerToken(r)
		if tok != c.token || auth.Reason(err) != c.reason || (err != nil && !errors.Is(err, auth.ErrInvalidToken)) {
			t.Errorf("%s: %q %v", c.name, tok, err)
		}
	}
}

func TestChallenge(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	_, missing := auth.BearerToken(r)
	if got := auth.Challenge(missing); got != `Bearer realm="geminus"` {
		t.Errorf("missing: %s", got)
	}
	r.Header.Set("Authorization", "Basic x")
	_, bad := auth.BearerToken(r)
	if got := auth.Challenge(bad); got != `Bearer realm="geminus", error="invalid_token"` {
		t.Errorf("invalid: %s", got)
	}
}

type fakeVerifier func(string) (auth.Identity, error)

func (f fakeVerifier) Verify(_ context.Context, tok string) (auth.Identity, error) { return f(tok) }

// The middleware passes a verified identity on, and otherwise calls the
// error writer without reaching the handler; an unexpected verifier error
// still fails closed as an invalid token.
func TestMiddleware(t *testing.T) {
	p, v, _ := setup(t)
	var failed error
	fail := func(w http.ResponseWriter, _ *http.Request, err error) {
		failed = err
		w.WriteHeader(http.StatusTeapot)
	}
	var seen auth.Identity
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		id, ok := auth.IdentityFrom(r.Context())
		if !ok {
			t.Error("no identity in the context")
		}
		seen = id
	})
	serve := func(v auth.Verifier, header string) int {
		failed, seen, calls = nil, auth.Identity{}, 0
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		w := httptest.NewRecorder()
		auth.Middleware(v, fail)(next).ServeHTTP(w, r)
		if (failed == nil) != (calls == 1) || calls > 1 {
			t.Errorf("handler called %d times after %v", calls, failed)
		}
		return w.Code
	}
	if code := serve(v, "Bearer "+p.Token(t, p.RSA, nil, p.Claims("user-1", t0))); code != http.StatusOK || seen.Subject != "user-1" || failed != nil {
		t.Errorf("valid token: %d %+v %v", code, seen, failed)
	}
	if code := serve(v, ""); code != http.StatusTeapot || !errors.Is(failed, auth.ErrInvalidToken) || seen.Subject != "" {
		t.Errorf("no token: %d %v", code, failed)
	}
	if code := serve(v, "Bearer "+p.Token(t, p.RSA, nil, with(p.Claims("user-1", t0), "aud", "other"))); code != http.StatusTeapot || auth.Reason(failed) != auth.ReasonAudience {
		t.Errorf("wrong audience: %d %v", code, failed)
	}
	down := fakeVerifier(func(string) (auth.Identity, error) {
		return auth.Identity{}, fmt.Errorf("provider down: %w", auth.ErrKeysUnavailable)
	})
	if code := serve(down, "Bearer a.b.c"); code != http.StatusTeapot || !errors.Is(failed, auth.ErrKeysUnavailable) || errors.Is(failed, auth.ErrInvalidToken) {
		t.Errorf("keys unavailable: %d %v", code, failed)
	}
	if got := (&auth.TokenError{Reason: auth.ReasonKeys}).Error(); got == "" {
		t.Error("a zero TokenError has no message")
	}
	odd := fakeVerifier(func(string) (auth.Identity, error) { return auth.Identity{}, errors.New("verifier bug") })
	if code := serve(odd, "Bearer a.b.c"); code != http.StatusTeapot || !errors.Is(failed, auth.ErrInvalidToken) {
		t.Errorf("unexpected verifier error: %d %v", code, failed)
	}
	if _, err := auth.IdentityFrom(context.Background()); err {
		t.Error("identity in an empty context")
	}
	for name, f := range map[string]func(){
		"nil verifier": func() { auth.Middleware(nil, fail) },
		"nil writer":   func() { auth.Middleware(v, nil) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}
