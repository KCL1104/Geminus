package auth

import (
	"errors"
	"net/http"
	"strings"
)

// BearerToken returns the token of the request's single "Authorization:
// Bearer <token>" header. A token in the URL is refused even when a header
// is present: URLs reach logs, history and referrers (SECURITY §3). Cookies
// are not read; a cookie mode needs the CSRF design of the auth ADR first.
func BearerToken(r *http.Request) (string, error) {
	q := r.URL.Query()
	if q.Has("access_token") || q.Has("id_token") {
		return "", invalid(ReasonTokenInQuery)
	}
	h := r.Header.Values("Authorization")
	if len(h) == 0 {
		return "", invalid(ReasonMissing)
	}
	if len(h) > 1 {
		return "", invalid(ReasonScheme)
	}
	scheme, token, ok := strings.Cut(h[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || !token68(token) {
		return "", invalid(ReasonScheme)
	}
	return token, nil
}

// token68 is RFC 7235's token68 (a compact JWS is a subset).
func token68(s string) bool {
	s = strings.TrimRight(s, "=")
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', strings.IndexByte("-._~+/", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// Challenge is the WWW-Authenticate value for a 401 caused by err (RFC 6750
// §3: a request without credentials gets no error code).
func Challenge(err error) string {
	if Reason(err) == ReasonMissing {
		return `Bearer realm="geminus"`
	}
	return `Bearer realm="geminus", error="invalid_token"`
}

// Middleware verifies the bearer token of every request and passes the
// identity on in the request context. On failure it calls fail, which
// writes the API's error envelope (UNAUTHENTICATED with Challenge for
// ErrInvalidToken, SERVICE_UNAVAILABLE for ErrKeysUnavailable), so this
// package defines no second error format. It never logs the token.
func Middleware(v Verifier, fail func(w http.ResponseWriter, r *http.Request, err error)) func(http.Handler) http.Handler {
	if v == nil || fail == nil {
		panic("auth: Middleware needs a verifier and an error writer")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := BearerToken(r)
			var id Identity
			if err == nil {
				id, err = v.Verify(r.Context(), token)
			}
			if err != nil {
				if !errors.Is(err, ErrInvalidToken) && !errors.Is(err, ErrKeysUnavailable) {
					err = &TokenError{Reason: ReasonMalformed, err: errors.Join(ErrInvalidToken, err)}
				}
				fail(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
		})
	}
}
