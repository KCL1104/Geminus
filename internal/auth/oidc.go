package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	jose "github.com/go-jose/go-jose/v4"
)

// Limits on what a token and a key set may be.
const (
	MaxTokenBytes  = 8 << 10   // a compact JWS longer than this is refused before parsing
	maxJWKSBytes   = 256 << 10 // a larger JWKS response is an error
	maxSubject     = 255       // users.oidc_subject is compared byte for byte
	maxURL         = 2048      // users.oidc_issuer, as tenant.ResolveUser bounds it
	minRSABits     = 2048
	maxClockSkew   = 5 * time.Minute
	defaultSkew    = 30 * time.Second
	defaultFetch   = 10 * time.Second
	maxFetch       = time.Minute
	defaultRefresh = time.Minute
	minRefresh     = time.Second
	maxRefresh     = 24 * time.Hour
	coldRetry      = 5 * time.Second // retry interval while no usable keys are cached
	failedWait     = time.Second     // how long a request waits for a retry after a failed fetch
	defaultMaxAge  = time.Hour
	defaultStale   = 24 * time.Hour
	maxStale       = 7 * 24 * time.Hour
)

// asymmetric is every algorithm a deployment may allow: no "none", no HMAC
// (a public key must never become a shared secret).
var asymmetric = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}

// Header parameters a token may not carry: key locations and embedded keys
// (keys come only from the configured JWKS), critical extensions, unencoded
// payloads, and encryption parameters.
var forbiddenHeader = []string{"jku", "x5u", "jwk", "x5c", "x5t", "x5t#S256", "crit", "b64", "zip", "enc"}

// Access-token types a typ header may name (RFC 7519, RFC 9068).
var tokenTypes = []string{"jwt", "at+jwt", "application/at+jwt"}

// Config is a deployment's OIDC provider: endpoints come from the deployment
// allowlist, never from a token or a discovery document (SECURITY §3).
type Config struct {
	Issuer   string // exact iss value; an https URL
	Audience string // the API's audience; aud must contain it
	JWKSURL  string // https URL of the provider's JWKS
	// Algorithms allowed; empty means RS256 and ES256.
	Algorithms []string
	// ClockSkew tolerated on exp, nbf and iat; zero means 30 s, at most 5 min.
	ClockSkew time.Duration
	// MinRefreshInterval spaces JWKS fetches (an unknown kid triggers one);
	// KeyMaxAge is how long a fetched JWKS is used before it is refreshed in
	// the background; KeyMaxStale is how long it may still be used while
	// refreshes fail. Zero means 1 min, 1 h and 24 h; they must be ordered,
	// MinRefreshInterval between 1 s and 24 h, KeyMaxStale at most 7 days.
	MinRefreshInterval, KeyMaxAge, KeyMaxStale time.Duration
	// FetchTimeout bounds one JWKS fetch; zero means 10 s, at most 1 min.
	FetchTimeout time.Duration
	// RequireAccessTokenType refuses tokens whose typ is not at+jwt
	// (RFC 9068), for providers that mark access tokens, so an ID token
	// that happens to carry the API's audience is never accepted.
	RequireAccessTokenType bool
	// HTTPClient fetches the JWKS; redirects are never followed. Nil uses a
	// default client.
	HTTPClient *http.Client
	// Now is the clock; nil means time.Now. It must not run backwards
	// (time.Now's monotonic reading): a clock set back delays refreshes
	// until it catches up.
	Now func() time.Time
}

// Validate reports every problem with c, without echoing values.
func (c Config) Validate() error {
	var errs []error
	if err := httpsURL(c.Issuer, false); err != nil {
		errs = append(errs, fmt.Errorf("issuer: %w", err))
	}
	if c.Audience == "" || len(c.Audience) > 512 || !printable(c.Audience) {
		errs = append(errs, errors.New("audience: required, printable, at most 512 bytes"))
	}
	if err := httpsURL(c.JWKSURL, true); err != nil {
		errs = append(errs, fmt.Errorf("JWKS URL: %w", err))
	}
	for i, a := range c.Algorithms {
		if !slices.Contains(asymmetric, a) {
			errs = append(errs, fmt.Errorf("algorithm %d is not an allowed asymmetric signature algorithm", i+1))
		}
	}
	if c.ClockSkew < 0 || c.ClockSkew > maxClockSkew {
		errs = append(errs, errors.New("clock skew: between 0 and 5 minutes"))
	}
	if c.FetchTimeout < 0 || c.FetchTimeout > maxFetch {
		errs = append(errs, errors.New("fetch timeout: between 0 and 1 minute"))
	}
	refresh, age, stale := or(c.MinRefreshInterval, defaultRefresh), or(c.KeyMaxAge, defaultMaxAge), or(c.KeyMaxStale, defaultStale)
	switch { // a negative interval fails the first or the second case
	case refresh < minRefresh || refresh > maxRefresh || stale > maxStale:
		errs = append(errs, errors.New("key refresh intervals: refresh between 1 s and 24 h, stale limit at most 7 days"))
	case refresh > age || age > stale:
		errs = append(errs, errors.New("key refresh intervals: refresh interval <= max age <= stale limit"))
	}
	return errors.Join(errs...)
}

// or is d, or def when d is zero.
func or(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

func httpsURL(raw string, queryOK bool) error {
	u, err := url.Parse(raw)
	switch {
	case raw == "" || err != nil:
		return errors.New("required absolute https URL")
	case len(raw) > maxURL:
		return errors.New("at most 2048 bytes")
	case u.Scheme != "https" || u.Host == "" || u.Opaque != "":
		return errors.New("must be an absolute https URL")
	case u.User != nil:
		return errors.New("must not contain credentials")
	case u.Fragment != "" || strings.Contains(raw, "#"):
		return errors.New("must not contain a fragment")
	case !queryOK && u.RawQuery != "":
		return errors.New("must not contain a query")
	}
	return nil
}

// ValidIdentity reports whether a token this package accepts can carry
// issuer and subject: the issuer is what Config.Validate accepts (the
// token's iss must equal it) and printable, and the subject is what a
// token's sub must be. Provisioning checks it, so every user it creates can
// sign in (ADR-023).
func ValidIdentity(issuer, subject string) bool {
	return httpsURL(issuer, false) == nil && printable(issuer) && validSubject(subject)
}

func validSubject(s string) bool { return s != "" && len(s) <= maxSubject && printable(s) }

// printable: valid UTF-8 of graphic characters and ASCII spaces only; no
// control, format (e.g. U+202E), separator or private-use characters,
// which could disguise an identifier in logs.
func printable(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) })
}

// OIDCVerifier verifies access tokens of one provider against its JWKS.
type OIDCVerifier struct {
	cfg    Config
	algs   []jose.SignatureAlgorithm
	client *http.Client

	keys       atomic.Pointer[keySet] // stored under mu
	mu         sync.Mutex             // guards the fields below
	refreshing chan struct{}          // closed when the fetch in flight ends
	last       time.Time              // start of the last fetch
	err        error                  // its error, if it failed
}

type keySet struct {
	byID    map[string]jose.JSONWebKey
	fetched time.Time
}

// NewOIDCVerifier checks cfg and returns a verifier. It does not contact
// the provider; the JWKS is fetched on first use.
func NewOIDCVerifier(cfg Config) (*OIDCVerifier, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if len(cfg.Algorithms) == 0 {
		cfg.Algorithms = []string{"RS256", "ES256"}
	}
	cfg.ClockSkew = or(cfg.ClockSkew, defaultSkew)
	cfg.FetchTimeout = or(cfg.FetchTimeout, defaultFetch)
	cfg.MinRefreshInterval = or(cfg.MinRefreshInterval, defaultRefresh)
	cfg.KeyMaxAge = or(cfg.KeyMaxAge, defaultMaxAge)
	cfg.KeyMaxStale = or(cfg.KeyMaxStale, defaultStale)
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	client := &http.Client{}
	if cfg.HTTPClient != nil {
		c := *cfg.HTTPClient
		client = &c
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	v := &OIDCVerifier{cfg: cfg, client: client}
	for _, a := range cfg.Algorithms {
		v.algs = append(v.algs, jose.SignatureAlgorithm(a))
	}
	return v, nil
}

var _ Verifier = (*OIDCVerifier)(nil)

// Verify checks the token's form, header, signature and claims, in that
// order, and returns the caller's identity.
func (v *OIDCVerifier) Verify(ctx context.Context, token string) (Identity, error) {
	if len(token) > MaxTokenBytes {
		return Identity{}, invalid(ReasonTooLarge)
	}
	// Only base64url characters and dots: the decoder below would skip CR
	// and LF, giving one token several spellings.
	if strings.ContainsFunc(token, func(r rune) bool {
		return !('A' <= r && r <= 'Z' || 'a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '-' || r == '_' || r == '.')
	}) {
		return Identity{}, invalid(ReasonMalformed)
	}
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		return Identity{}, invalid(ReasonMalformed)
	}
	var header []byte
	for i, seg := range segments {
		// Strict: each segment has exactly one encoding.
		b, err := base64.RawURLEncoding.Strict().DecodeString(seg)
		if err != nil {
			return Identity{}, invalid(ReasonMalformed)
		}
		if i == 0 {
			header = b
		}
	}
	alg, kid, err := v.header(header)
	if err != nil {
		return Identity{}, err
	}
	jws, err := jose.ParseSignedCompact(token, v.algs)
	if err != nil || len(jws.Signatures) != 1 {
		return Identity{}, invalid(ReasonMalformed)
	}
	key, err := v.key(ctx, kid)
	if err != nil {
		return Identity{}, err
	}
	if (key.Algorithm != "" && key.Algorithm != alg) || !fits(alg, key.Key) {
		return Identity{}, invalid(ReasonAlgorithm)
	}
	payload, err := jws.Verify(key.Key)
	if err != nil {
		return Identity{}, invalid(ReasonSignature)
	}
	return v.claims(payload)
}

// header checks the protected header before any key is looked up.
func (v *OIDCVerifier) header(raw []byte) (alg, kid string, err error) {
	var h map[string]jsontext.Value
	if err := json.Unmarshal(raw, &h); err != nil {
		return "", "", invalid(ReasonMalformed) // includes duplicate member names
	}
	if json.Unmarshal(h["alg"], &alg) != nil || !slices.Contains(v.cfg.Algorithms, alg) {
		return "", "", invalid(ReasonAlgorithm)
	}
	if json.Unmarshal(h["kid"], &kid) != nil || kid == "" || len(kid) > 256 {
		return "", "", invalid(ReasonHeader)
	}
	for _, p := range forbiddenHeader {
		if _, ok := h[p]; ok {
			return "", "", invalid(ReasonHeader)
		}
	}
	if t, ok := h["typ"]; ok || v.cfg.RequireAccessTokenType {
		var typ string
		if json.Unmarshal(t, &typ) != nil || !slices.Contains(tokenTypes, strings.ToLower(typ)) ||
			(v.cfg.RequireAccessTokenType && !strings.HasSuffix(strings.ToLower(typ), "at+jwt")) {
			return "", "", invalid(ReasonType)
		}
	}
	return alg, kid, nil
}

// fits reports whether key is the right kind of key for alg.
func fits(alg string, key any) bool {
	switch k := key.(type) {
	case *rsa.PublicKey:
		return (strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "PS")) && k.N.BitLen() >= minRSABits
	case *ecdsa.PublicKey:
		return map[string]elliptic.Curve{"ES256": elliptic.P256(), "ES384": elliptic.P384(), "ES512": elliptic.P521()}[alg] == k.Curve
	case ed25519.PublicKey:
		return alg == "EdDSA"
	}
	return false
}

type tokenClaims struct {
	Iss *string        `json:"iss"`
	Sub *string        `json:"sub"`
	Aud jsontext.Value `json:"aud"`
	Exp *float64       `json:"exp"`
	Nbf *float64       `json:"nbf"`
	Iat *float64       `json:"iat"`
}

func (v *OIDCVerifier) claims(payload []byte) (Identity, error) {
	var c tokenClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Identity{}, invalid(ReasonClaims)
	}
	if c.Iss == nil || *c.Iss != v.cfg.Issuer {
		return Identity{}, invalid(ReasonIssuer)
	}
	auds, ok := audiences(c.Aud)
	if !ok {
		return Identity{}, invalid(ReasonClaims)
	}
	if !slices.Contains(auds, v.cfg.Audience) {
		return Identity{}, invalid(ReasonAudience)
	}
	now, skew := v.cfg.Now(), v.cfg.ClockSkew
	exp, ok := numericDate(c.Exp)
	if !ok || c.Exp == nil || !now.Before(exp.Add(skew)) {
		return Identity{}, invalid(ReasonExpired)
	}
	if nbf, ok := numericDate(c.Nbf); !ok {
		return Identity{}, invalid(ReasonClaims)
	} else if c.Nbf != nil && now.Add(skew).Before(nbf) {
		return Identity{}, invalid(ReasonNotYetValid)
	}
	if iat, ok := numericDate(c.Iat); !ok {
		return Identity{}, invalid(ReasonClaims)
	} else if c.Iat != nil && iat.After(now.Add(skew)) {
		return Identity{}, invalid(ReasonIssuedInFuture)
	}
	if c.Sub == nil || !validSubject(*c.Sub) {
		return Identity{}, invalid(ReasonSubject)
	}
	return Identity{Issuer: *c.Iss, Subject: *c.Sub, Expiry: exp}, nil
}

// audiences reads aud, a string or an array of strings (RFC 7519 §4.1.3).
func audiences(raw jsontext.Value) ([]string, bool) {
	if len(raw) == 0 {
		return nil, true // absent: matches nothing
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, true
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many, true
	}
	return nil, false
}

// numericDate converts a NumericDate; absent is fine (ok, zero time), a
// value outside year 1970–9999 is not.
func numericDate(v *float64) (time.Time, bool) {
	if v == nil {
		return time.Time{}, true
	}
	if math.IsNaN(*v) || *v < 0 || *v >= 253402300800 {
		return time.Time{}, false
	}
	sec, frac := math.Modf(*v)
	return time.Unix(int64(sec), int64(frac*1e9)), true
}

// key returns the verification key for kid. A cached key is returned at
// once, also while the JWKS is refreshed in the background after KeyMaxAge,
// and while refreshes fail, until KeyMaxStale (stale-while-revalidate). An
// unknown kid waits for a refresh, so rotated keys are picked up, but only
// until ctx ends, and for at most a second when the previous fetch failed
// (the provider is likely still down). One fetch at a time, spaced by
// MinRefreshInterval (5 s while no usable keys are cached), so tokens with
// random kids cannot make the API hammer the provider.
func (v *OIDCVerifier) key(ctx context.Context, kid string) (jose.JSONWebKey, error) {
	now := v.cfg.Now()
	if ks := v.keys.Load(); ks != nil && now.Sub(ks.fetched) < v.cfg.KeyMaxStale {
		if k, ok := ks.byID[kid]; ok {
			if now.Sub(ks.fetched) >= v.cfg.KeyMaxAge {
				v.refresh(now)
			}
			return k, nil
		}
	}
	if done, failed := v.refresh(now); done != nil {
		var limit <-chan time.Time
		if failed != nil {
			t := time.NewTimer(failedWait)
			defer t.Stop()
			limit = t.C
		}
		select {
		case <-done:
		case <-ctx.Done():
			return jose.JSONWebKey{}, keysUnavailable(ctx.Err())
		case <-limit:
			return jose.JSONWebKey{}, keysUnavailable(failed)
		}
	}
	v.mu.Lock()
	ks, fetchErr := v.keys.Load(), v.err
	v.mu.Unlock()
	if ks == nil || now.Sub(ks.fetched) >= v.cfg.KeyMaxStale {
		return jose.JSONWebKey{}, keysUnavailable(fetchErr)
	}
	if k, ok := ks.byID[kid]; ok {
		return k, nil
	}
	if fetchErr != nil {
		// The key may have been rotated in while the JWKS cannot be read.
		return jose.JSONWebKey{}, keysUnavailable(fetchErr)
	}
	return jose.JSONWebKey{}, invalid(ReasonUnknownKey)
}

// refresh starts a JWKS fetch unless one is in flight or the last one
// started too recently. It returns a channel closed when the fetch in
// flight ends, or nil if there is none, and the previous fetch's error.
func (v *OIDCVerifier) refresh(now time.Time) (<-chan struct{}, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.refreshing != nil {
		return v.refreshing, v.err
	}
	interval := v.cfg.MinRefreshInterval
	if ks := v.keys.Load(); ks == nil || now.Sub(ks.fetched) >= v.cfg.KeyMaxStale {
		interval = min(interval, coldRetry)
	}
	if !v.last.IsZero() && now.Sub(v.last) < interval {
		return nil, v.err
	}
	v.last = now
	done := make(chan struct{})
	v.refreshing = done
	go func() {
		// No request's context: the fetch serves every waiting request. The
		// deferred update also runs if the fetch never returns normally
		// (runtime.Goexit in a transport), so a refresh is never stuck.
		var ks *keySet
		err := errors.New("fetching JWKS: aborted")
		defer func() {
			v.mu.Lock()
			if err == nil {
				v.keys.Store(ks)
			}
			v.err, v.refreshing = err, nil
			v.mu.Unlock()
			close(done)
		}()
		ks, err = v.safeFetch()
	}()
	return done, v.err
}

// safeFetch is fetch with a panic turned into an error: the fetch runs in
// its own goroutine, where net/http would not contain it.
func (v *OIDCVerifier) safeFetch() (ks *keySet, err error) {
	defer func() {
		if recover() != nil {
			ks, err = nil, errors.New("fetching JWKS: internal error")
		}
	}()
	return v.fetch()
}

// keysUnavailable is ReasonKeys with its cause, which never holds the URL.
func keysUnavailable(cause error) error {
	if cause == nil {
		return &TokenError{Reason: ReasonKeys, err: ErrKeysUnavailable}
	}
	return &TokenError{Reason: ReasonKeys, err: fmt.Errorf("%w: %w", ErrKeysUnavailable, cause)}
}

// fetch reads the JWKS within FetchTimeout. Its errors leave out the URL,
// which may carry a credential in its query.
func (v *OIDCVerifier) fetch() (*keySet, error) {
	ctx, cancel := context.WithTimeout(context.Background(), v.cfg.FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return nil, errors.New("fetching JWKS: invalid request")
	}
	req.Header.Set("Accept", "application/jwk-set+json, application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("fetching JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching JWKS: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading JWKS: %w", err)
	}
	if len(body) > maxJWKSBytes {
		return nil, errors.New("JWKS larger than 256 KiB")
	}
	return v.parseKeys(body, v.cfg.Now())
}

// parseKeys keeps the usable signing keys: public, with a kid, for
// signatures, of an allowed algorithm and a sufficient size. A kid that
// appears twice is dropped, since it would be ambiguous. Keys of unknown
// types are skipped, so a provider adding one does not break the rest.
func (v *OIDCVerifier) parseKeys(body []byte, now time.Time) (*keySet, error) {
	var doc struct {
		Keys []jsontext.Value `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("JWKS is not a JSON key set")
	}
	byID := map[string]jose.JSONWebKey{}
	dup := map[string]bool{}
	for _, raw := range doc.Keys {
		var k jose.JSONWebKey
		if err := k.UnmarshalJSON(bytes.Clone(raw)); err != nil {
			continue
		}
		if k.KeyID == "" || (k.Use != "" && k.Use != "sig") || !k.IsPublic() || !k.Valid() ||
			(k.Algorithm != "" && !slices.Contains(v.cfg.Algorithms, k.Algorithm)) || !slices.ContainsFunc(v.cfg.Algorithms, func(a string) bool { return fits(a, k.Key) }) {
			continue
		}
		if _, seen := byID[k.KeyID]; seen || dup[k.KeyID] {
			delete(byID, k.KeyID)
			dup[k.KeyID] = true
			continue
		}
		byID[k.KeyID] = k
	}
	if len(byID) == 0 {
		return nil, errors.New("JWKS has no usable signing key")
	}
	return &keySet{byID: byID, fetched: now}, nil
}
