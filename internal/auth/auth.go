// Package auth authenticates API requests (T-008, docs/SECURITY.md §3): it
// verifies an OIDC access token and yields the caller's verified issuer and
// subject. It authenticates only. The user, membership and role come from
// the database (internal/tenant, D-008), never from token claims, request
// headers or the client.
//
// Keys come only from the JWKS URL the deployment configures; a token can
// never name where its keys are (no jku, x5u, jwk or x5c). Only asymmetric
// signature algorithms on an allowlist are accepted, never "none" or HMAC.
// Errors carry a reason code for logs and metrics, never the token or its
// claims.
package auth

import (
	"context"
	"errors"
	"time"
)

// Identity is a verified caller: the token's issuer and subject.
type Identity struct {
	Issuer  string
	Subject string
	Expiry  time.Time
}

// Verifier verifies a bearer token. Implementations are safe for
// concurrent use.
type Verifier interface {
	Verify(ctx context.Context, token string) (Identity, error)
}

var (
	// ErrInvalidToken: no token, or a token that is not acceptable. The API
	// answers 401 UNAUTHENTICATED.
	ErrInvalidToken = errors.New("auth: invalid token")
	// ErrKeysUnavailable: the token may be valid but its signing key cannot
	// be fetched now. The API answers 503 SERVICE_UNAVAILABLE.
	ErrKeysUnavailable = errors.New("auth: signing keys unavailable")
)

// Reason codes of a TokenError.
const (
	ReasonMissing        = "missing"          // no Authorization header
	ReasonScheme         = "scheme"           // not a single "Bearer <token>"
	ReasonTokenInQuery   = "token_in_query"   // access_token in the URL (SECURITY §3)
	ReasonTooLarge       = "too_large"        // longer than MaxTokenBytes
	ReasonMalformed      = "malformed"        // not a compact JWS
	ReasonAlgorithm      = "algorithm"        // alg not allowed, or not the key's
	ReasonHeader         = "header"           // kid missing, or a forbidden header parameter
	ReasonType           = "type"             // typ is not a JWT access token type
	ReasonUnknownKey     = "unknown_key"      // kid not in the JWKS, even after a refresh
	ReasonSignature      = "signature"        // signature does not verify
	ReasonClaims         = "claims"           // payload is not a valid claims object
	ReasonIssuer         = "issuer"           // iss is not the configured issuer
	ReasonAudience       = "audience"         // aud does not contain the configured audience
	ReasonExpired        = "expired"          // exp missing or passed
	ReasonNotYetValid    = "not_yet_valid"    // nbf in the future
	ReasonIssuedInFuture = "issued_in_future" // iat in the future
	ReasonSubject        = "subject"          // sub missing, too long or not printable
	ReasonKeys           = "keys_unavailable" // JWKS fetch failed
)

// TokenError is a rejected token with its reason. It unwraps to
// ErrInvalidToken, or to ErrKeysUnavailable for ReasonKeys.
type TokenError struct {
	Reason string
	err    error
}

func (e *TokenError) Error() string { return e.Unwrap().Error() + ": " + e.Reason }

// Unwrap returns the error class; a TokenError made outside this package
// is an invalid token.
func (e *TokenError) Unwrap() error {
	if e.err == nil {
		return ErrInvalidToken
	}
	return e.err
}

func invalid(reason string) error { return &TokenError{Reason: reason, err: ErrInvalidToken} }

// Reason returns the reason code of a TokenError, or "" for other errors.
func Reason(err error) string {
	var te *TokenError
	if errors.As(err, &te) {
		return te.Reason
	}
	return ""
}

type identityKey struct{}

// WithIdentity returns ctx carrying id.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom returns the identity Middleware verified for this request.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}
