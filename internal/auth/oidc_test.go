package auth_test

import (
	"context"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/auth/authtest"
)

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// clock is a settable time source.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func setup(t *testing.T, edit ...func(*auth.Config)) (*authtest.Provider, *auth.OIDCVerifier, *clock) {
	t.Helper()
	p := authtest.New(t)
	c := &clock{now: t0}
	cfg := p.Config()
	cfg.Now = c.Now
	for _, e := range edit {
		e(&cfg)
	}
	v, err := auth.NewOIDCVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p, v, c
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func with(m map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == nil {
			delete(out, kv[i].(string))
		} else {
			out[kv[i].(string)] = kv[i+1]
		}
	}
	return out
}

func TestValidTokens(t *testing.T) {
	p, v, _ := setup(t)
	claims := p.Claims("user-1", t0)
	for name, tok := range map[string]string{
		"RS256":          p.Token(t, p.RSA, nil, claims),
		"ES256":          p.Token(t, p.EC, nil, claims),
		"audience array": p.Token(t, p.RSA, nil, with(claims, "aud", []string{"other", p.Audience})),
		"typ at+jwt":     p.Token(t, p.RSA, map[string]any{"typ": "at+jwt"}, claims),
		"no typ":         p.Token(t, p.RSA, map[string]any{"typ": nil}, claims),
		"no nbf or iat":  p.Token(t, p.RSA, nil, with(claims, "nbf", nil, "iat", nil)),
		"255-byte sub":   p.Token(t, p.RSA, nil, with(claims, "sub", strings.Repeat("s", 255))),
	} {
		id, err := v.Verify(context.Background(), tok)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if id.Issuer != p.Issuer || id.Subject == "" || !id.Expiry.Equal(t0.Add(5*time.Minute)) {
			t.Errorf("%s: identity %+v", name, id)
		}
	}
}

// Every rejected token has its reason, maps to ErrInvalidToken, and the
// error never carries the token or its subject.
func TestRejectedTokens(t *testing.T) {
	p, v, _ := setup(t)
	claims := p.Claims("secret-subject", t0)
	header := func(extra map[string]any) []byte {
		h := map[string]any{"alg": "RS256", "kid": p.RSA.ID, "typ": "JWT"}
		for k, val := range extra {
			if val == nil {
				delete(h, k)
			} else {
				h[k] = val
			}
		}
		return mustJSON(t, h)
	}
	pub, err := x509.MarshalPKIXPublicKey(p.RSA.Public())
	if err != nil {
		t.Fatal(err)
	}
	jwk, err := json.Marshal(jose.JSONWebKey{Key: p.RSA.Public(), KeyID: p.RSA.ID})
	if err != nil {
		t.Fatal(err)
	}
	attacker := authtest.NewRSA(t, p.RSA.ID, "RS256", 2048) // same kid, unpublished key
	valid := p.Token(t, p.RSA, nil, claims)
	parts := strings.Split(valid, ".")
	cases := []struct{ name, token, reason string }{
		{"empty", "", auth.ReasonMalformed},
		{"two parts", "a.b", auth.ReasonMalformed},
		{"four parts", valid + ".x", auth.ReasonMalformed},
		{"too large", strings.Repeat("a", auth.MaxTokenBytes+1), auth.ReasonTooLarge},
		{"header not base64", "!!." + parts[1] + "." + parts[2], auth.ReasonMalformed},
		{"header not JSON", authtest.Sign(t, p.RSA, []byte("not json"), mustJSON(t, claims)), auth.ReasonMalformed},
		{"duplicate header member", authtest.Sign(t, p.RSA, []byte(`{"alg":"RS256","kid":"rsa-1","kid":"rsa-1"}`), mustJSON(t, claims)), auth.ReasonMalformed},
		{"JSON serialization", `{"payload":"` + parts[1] + `","protected":"` + parts[0] + `","signature":"` + parts[2] + `"}`, auth.ReasonMalformed},
		{"alg none", authtest.Unsigned(header(map[string]any{"alg": "none"}), mustJSON(t, claims)), auth.ReasonAlgorithm},
		{"HS256 with the public key as secret", authtest.HMAC(header(map[string]any{"alg": "HS256"}), mustJSON(t, claims), pub), auth.ReasonAlgorithm},
		{"HS256 with the JWK as secret", authtest.HMAC(header(map[string]any{"alg": "HS256"}), mustJSON(t, claims), jwk), auth.ReasonAlgorithm},
		{"alg not allowed", p.Token(t, authtest.Key{ID: p.RSA.ID, Alg: "RS512", Signer: p.RSA.Signer}, nil, claims), auth.ReasonAlgorithm},
		{"alg missing", authtest.Sign(t, p.RSA, header(map[string]any{"alg": nil}), mustJSON(t, claims)), auth.ReasonAlgorithm},
		{"alg not a string", authtest.Sign(t, p.RSA, header(map[string]any{"alg": 256}), mustJSON(t, claims)), auth.ReasonAlgorithm},
		{"ES256 header on the RSA key", p.Token(t, authtest.Key{ID: p.RSA.ID, Alg: "ES256", Signer: p.EC.Signer}, nil, claims), auth.ReasonAlgorithm},
		{"RS256 header on the EC key", p.Token(t, authtest.Key{ID: p.EC.ID, Alg: "RS256", Signer: p.RSA.Signer}, nil, claims), auth.ReasonAlgorithm},
		{"no kid", p.Token(t, p.RSA, map[string]any{"kid": nil}, claims), auth.ReasonHeader},
		{"empty kid", p.Token(t, p.RSA, map[string]any{"kid": ""}, claims), auth.ReasonHeader},
		{"jku", p.Token(t, p.RSA, map[string]any{"jku": "https://attacker.invalid/jwks"}, claims), auth.ReasonHeader},
		{"x5u", p.Token(t, p.RSA, map[string]any{"x5u": "https://attacker.invalid/cert"}, claims), auth.ReasonHeader},
		{"embedded jwk", p.Token(t, attacker, map[string]any{"jwk": jose.JSONWebKey{Key: attacker.Public()}}, claims), auth.ReasonHeader},
		{"x5c", p.Token(t, p.RSA, map[string]any{"x5c": []string{"MIIB"}}, claims), auth.ReasonHeader},
		{"crit", p.Token(t, p.RSA, map[string]any{"crit": []string{"exp"}}, claims), auth.ReasonHeader},
		{"b64 false", p.Token(t, p.RSA, map[string]any{"b64": false}, claims), auth.ReasonHeader},
		{"typ JOSE", p.Token(t, p.RSA, map[string]any{"typ": "JOSE"}, claims), auth.ReasonType},
		{"typ not a string", p.Token(t, p.RSA, map[string]any{"typ": 1}, claims), auth.ReasonType},
		{"unknown kid", p.Token(t, authtest.Key{ID: "rsa-9", Alg: "RS256", Signer: p.RSA.Signer}, nil, claims), auth.ReasonUnknownKey},
		{"signed by another key with the same kid", p.Token(t, attacker, nil, claims), auth.ReasonSignature},
		{"payload changed after signing", parts[0] + "." + b64(with(claims, "sub", "admin"), t) + "." + parts[2], auth.ReasonSignature},
		{"payload not JSON", authtest.Sign(t, p.RSA, header(nil), []byte("sub=admin")), auth.ReasonClaims},
		{"payload an array", authtest.Sign(t, p.RSA, header(nil), []byte(`["sub"]`)), auth.ReasonClaims},
		{"duplicate claim", authtest.Sign(t, p.RSA, header(nil), []byte(fmt.Sprintf(`{"iss":%q,"aud":%q,"sub":"a","sub":"b","exp":%d}`, p.Issuer, p.Audience, t0.Add(time.Minute).Unix()))), auth.ReasonClaims},
		{"exp a string", p.Token(t, p.RSA, nil, with(claims, "exp", "2099-01-01")), auth.ReasonClaims},
		{"aud a number", p.Token(t, p.RSA, nil, with(claims, "aud", 7)), auth.ReasonClaims},
		{"iss missing", p.Token(t, p.RSA, nil, with(claims, "iss", nil)), auth.ReasonIssuer},
		{"iss other", p.Token(t, p.RSA, nil, with(claims, "iss", "https://attacker.invalid")), auth.ReasonIssuer},
		{"iss with a trailing slash", p.Token(t, p.RSA, nil, with(claims, "iss", p.Issuer+"/")), auth.ReasonIssuer},
		{"aud missing", p.Token(t, p.RSA, nil, with(claims, "aud", nil)), auth.ReasonAudience},
		{"aud other", p.Token(t, p.RSA, nil, with(claims, "aud", "https://other.api")), auth.ReasonAudience},
		{"aud array without ours", p.Token(t, p.RSA, nil, with(claims, "aud", []string{"a", "b"})), auth.ReasonAudience},
		{"exp missing", p.Token(t, p.RSA, nil, with(claims, "exp", nil)), auth.ReasonExpired},
		{"expired beyond the skew", p.Token(t, p.RSA, nil, with(claims, "exp", t0.Add(-30*time.Second).Unix())), auth.ReasonExpired},
		{"exp out of range", p.Token(t, p.RSA, nil, with(claims, "exp", 1e300)), auth.ReasonExpired},
		{"nbf beyond the skew", p.Token(t, p.RSA, nil, with(claims, "nbf", t0.Add(31*time.Second).Unix())), auth.ReasonNotYetValid},
		{"nbf out of range", p.Token(t, p.RSA, nil, with(claims, "nbf", -5)), auth.ReasonClaims},
		{"iat beyond the skew", p.Token(t, p.RSA, nil, with(claims, "iat", t0.Add(31*time.Second).Unix())), auth.ReasonIssuedInFuture},
		{"iat out of range", p.Token(t, p.RSA, nil, with(claims, "iat", 1e300)), auth.ReasonClaims},
		{"sub missing", p.Token(t, p.RSA, nil, with(claims, "sub", nil)), auth.ReasonSubject},
		{"sub empty", p.Token(t, p.RSA, nil, with(claims, "sub", "")), auth.ReasonSubject},
		{"sub 256 bytes", p.Token(t, p.RSA, nil, with(claims, "sub", strings.Repeat("s", 256))), auth.ReasonSubject},
		{"sub with a control character", p.Token(t, p.RSA, nil, with(claims, "sub", "a\nb")), auth.ReasonSubject},
		{"sub not a string", p.Token(t, p.RSA, nil, with(claims, "sub", 7)), auth.ReasonClaims},
	}
	for _, c := range cases {
		_, err := v.Verify(context.Background(), c.token)
		if got := auth.Reason(err); got != c.reason || !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("%s: reason %q (%v), want %q", c.name, got, err, c.reason)
		}
		if err != nil && (strings.Contains(err.Error(), "secret-subject") || (len(c.token) > 20 && strings.Contains(err.Error(), c.token[:20]))) {
			t.Errorf("%s: the error carries token content: %v", c.name, err)
		}
	}
}

func b64(v any, t *testing.T) string { return base64.RawURLEncoding.EncodeToString(mustJSON(t, v)) }

// Clock skew is 30 s by default: the boundaries are exact.
func TestClockSkewBoundaries(t *testing.T) {
	p, v, _ := setup(t)
	claims := p.Claims("user-1", t0)
	for _, c := range []struct {
		name   string
		claims map[string]any
		reason string
	}{
		{"exp 29 s ago", with(claims, "exp", t0.Add(-29*time.Second).Unix()), ""},
		{"exp 30 s ago", with(claims, "exp", t0.Add(-30*time.Second).Unix()), auth.ReasonExpired},
		{"nbf in 30 s", with(claims, "nbf", t0.Add(30*time.Second).Unix()), ""},
		{"nbf in 31 s", with(claims, "nbf", t0.Add(31*time.Second).Unix()), auth.ReasonNotYetValid},
		{"iat in 30 s", with(claims, "iat", t0.Add(30*time.Second).Unix()), ""},
		{"iat in 31 s", with(claims, "iat", t0.Add(31*time.Second).Unix()), auth.ReasonIssuedInFuture},
	} {
		_, err := v.Verify(context.Background(), p.Token(t, p.RSA, nil, c.claims))
		if auth.Reason(err) != c.reason {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	p2, strict, _ := setup(t, func(c *auth.Config) { c.ClockSkew = time.Second })
	if _, err := strict.Verify(context.Background(), p2.Token(t, p2.RSA, nil, with(p2.Claims("user-1", t0), "exp", t0.Add(-2*time.Second).Unix()))); auth.Reason(err) != auth.ReasonExpired {
		t.Errorf("1 s skew: %v", err)
	}
	if _, err := strict.Verify(context.Background(), p2.Token(t, p2.RSA, nil, with(p2.Claims("user-1", t0), "exp", t0.Add(-500*time.Millisecond).Unix()+1))); err != nil {
		t.Errorf("1 s skew, within: %v", err)
	}
}

// A rotated-in key is fetched on its first use; fetches are spaced by
// MinRefreshInterval, so random kids cannot make the API hammer the
// provider; a key removed from the JWKS stops working after KeyMaxAge.
func TestKeyRotation(t *testing.T) {
	p, v, c := setup(t)
	claims := p.Claims("user-1", t0)
	ctx := context.Background()
	if _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, claims)); err != nil || p.Hits() != 1 {
		t.Fatalf("first use: %v, %d fetches", err, p.Hits())
	}
	for i := range 100 {
		_, err := v.Verify(ctx, p.Token(t, authtest.Key{ID: fmt.Sprintf("random-%d", i), Alg: "RS256", Signer: p.RSA.Signer}, nil, claims))
		if auth.Reason(err) != auth.ReasonUnknownKey {
			t.Fatalf("random kid: %v", err)
		}
	}
	if p.Hits() != 1 {
		t.Errorf("%d fetches for 100 unknown kids within a minute of the first, want 1", p.Hits())
	}
	next := authtest.NewEC(t, "ec-2", "ES256", elliptic.P256())
	p.Publish(t, p.RSA, p.EC, next)
	if _, err := v.Verify(ctx, p.Token(t, next, nil, claims)); auth.Reason(err) != auth.ReasonUnknownKey {
		t.Errorf("before the refresh interval: %v", err)
	}
	c.Add(time.Minute)
	if _, err := v.Verify(ctx, p.Token(t, next, nil, p.Claims("user-1", c.Now()))); err != nil {
		t.Errorf("rotated-in key after the interval: %v", err)
	}
	p.Publish(t, next) // rsa-1 and ec-1 retired
	c.Add(30 * time.Minute)
	if _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("user-1", c.Now()))); err != nil {
		t.Errorf("a cached key within KeyMaxAge: %v", err)
	}
	c.Add(31 * time.Minute)
	if _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("user-1", c.Now()))); auth.Reason(err) != auth.ReasonUnknownKey {
		t.Errorf("a retired key after KeyMaxAge: %v", err)
	}
}

// When the JWKS cannot be read: no keys yet is ErrKeysUnavailable; cached
// keys stay usable until KeyMaxStale; an unknown kid cannot be confirmed
// either way, so it is ErrKeysUnavailable too.
func TestJWKSUnavailable(t *testing.T) {
	p, v, c := setup(t)
	ctx := context.Background()
	p.SetJWKS([]byte("oops"), http.StatusInternalServerError)
	if _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("u", t0))); !errors.Is(err, auth.ErrKeysUnavailable) || errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("no keys: %v", err)
	}
	p.Publish(t, p.RSA)
	c.Add(time.Minute)
	if _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("u", c.Now()))); err != nil {
		t.Fatalf("after recovery: %v", err)
	}
	p.SetJWKS(nil, http.StatusServiceUnavailable)
	c.Add(2 * time.Hour) // stale, refresh fails
	if _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("u", c.Now()))); err != nil {
		t.Errorf("stale keys while the provider is down: %v", err)
	}
	if _, err := v.Verify(ctx, p.Token(t, p.EC, nil, p.Claims("u", c.Now()))); !errors.Is(err, auth.ErrKeysUnavailable) {
		t.Errorf("unknown kid while the provider is down: %v", err)
	}
	c.Add(23 * time.Hour)
	if _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("u", c.Now()))); !errors.Is(err, auth.ErrKeysUnavailable) {
		t.Errorf("keys older than KeyMaxStale: %v", err)
	}
}

// Only usable signing keys are taken from the JWKS.
func TestJWKSContent(t *testing.T) {
	p := authtest.New(t)
	claims := p.Claims("u", t0)
	weak := authtest.NewRSA(t, "weak", "RS256", 1024)
	ed := authtest.NewEd(t, "ed-1")
	key := func(k authtest.Key, use string, private bool) map[string]any {
		var pub any = k.Public()
		if private {
			pub = k.Signer
		}
		b, err := json.Marshal(jose.JSONWebKey{Key: pub, KeyID: k.ID, Algorithm: k.JWKAlg, Use: use})
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	set := func(keys ...map[string]any) []byte { return mustJSON(t, map[string]any{"keys": keys}) }
	cases := []struct {
		name   string
		jwks   []byte
		token  string
		reason string
	}{
		{"private key published", set(key(p.RSA, "sig", true), key(p.EC, "sig", false)), p.Token(t, p.RSA, nil, claims), auth.ReasonUnknownKey},
		{"only a private key", set(key(p.RSA, "sig", true)), p.Token(t, p.RSA, nil, claims), auth.ReasonKeys},
		{"duplicate kid", set(key(p.RSA, "sig", false), key(authtest.NewRSA(t, p.RSA.ID, "RS256", 2048), "sig", false), key(p.EC, "", false)), p.Token(t, p.RSA, nil, claims), auth.ReasonUnknownKey},
		{"encryption key", set(key(p.RSA, "enc", false), key(p.EC, "sig", false)), p.Token(t, p.RSA, nil, claims), auth.ReasonUnknownKey},
		{"1024-bit RSA", set(key(weak, "sig", false), key(p.EC, "sig", false)), p.Token(t, weak, nil, claims), auth.ReasonUnknownKey},
		{"Ed25519 not allowed by default", set(key(ed, "sig", false), key(p.EC, "sig", false)), p.Token(t, ed, nil, claims), auth.ReasonAlgorithm},
		{"key alg not allowed", set(key(authtest.Key{ID: "rs512", Signer: p.RSA.Signer, JWKAlg: "RS512"}, "sig", false), key(p.EC, "sig", false)), p.Token(t, authtest.Key{ID: "rs512", Alg: "RS256", Signer: p.RSA.Signer}, nil, claims), auth.ReasonUnknownKey},
		{"key without alg, token alg of another family", set(key(authtest.Key{ID: "noalg", Signer: p.RSA.Signer}, "sig", false), key(p.EC, "sig", false)), p.Token(t, authtest.Key{ID: "noalg", Alg: "ES256", Signer: p.EC.Signer}, nil, claims), auth.ReasonAlgorithm},
		{"unknown key types skipped", set(map[string]any{"kty": "oct", "kid": "hmac", "k": "c2VjcmV0"}, map[string]any{"kty": "XYZ", "kid": "new"}, key(p.RSA, "sig", false)), p.Token(t, p.RSA, nil, claims), ""},
		{"error status with a valid key set", nil, p.Token(t, p.RSA, nil, claims), auth.ReasonKeys},
		{"valid key set padded past 256 KiB", append(set(key(p.RSA, "sig", false)), []byte(strings.Repeat(" ", 256<<10))...), p.Token(t, p.RSA, nil, claims), auth.ReasonKeys},
		{"no keys member", []byte(`{}`), p.Token(t, p.RSA, nil, claims), auth.ReasonKeys},
		{"not JSON", []byte(`<html>`), p.Token(t, p.RSA, nil, claims), auth.ReasonKeys},
		{"larger than 256 KiB", append(set(key(p.RSA, "sig", false))[:1], append([]byte(`"pad":"`+strings.Repeat("x", 256<<10)+`",`), set(key(p.RSA, "sig", false))[1:]...)...), p.Token(t, p.RSA, nil, claims), auth.ReasonKeys},
	}
	for _, c := range cases {
		if c.jwks == nil {
			p.SetJWKS(set(key(p.RSA, "sig", false)), http.StatusInternalServerError)
		} else {
			p.SetJWKS(c.jwks, http.StatusOK)
		}
		cfg := p.Config()
		cfg.Now = func() time.Time { return t0 }
		v, err := auth.NewOIDCVerifier(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Verify(context.Background(), c.token); auth.Reason(err) != c.reason {
			t.Errorf("%s: %v, want %q", c.name, err, c.reason)
		}
	}
	// Ed25519 when the deployment allows it.
	p.SetJWKS(set(key(ed, "sig", false)), http.StatusOK)
	cfg := p.Config()
	cfg.Now, cfg.Algorithms = func() time.Time { return t0 }, []string{"EdDSA"}
	v, err := auth.NewOIDCVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), p.Token(t, ed, nil, claims)); err != nil {
		t.Errorf("EdDSA allowed: %v", err)
	}
}

// The JWKS URL is the only place keys come from: a redirect is not followed.
func TestJWKSRedirectNotFollowed(t *testing.T) {
	p := authtest.New(t)
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, p.Server.URL+"/jwks", http.StatusFound)
	}))
	defer other.Close()
	cfg := p.Config()
	cfg.JWKSURL, cfg.HTTPClient, cfg.Now = other.URL+"/jwks", other.Client(), func() time.Time { return t0 }
	v, err := auth.NewOIDCVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), p.Token(t, p.RSA, nil, p.Claims("u", t0))); !errors.Is(err, auth.ErrKeysUnavailable) || p.Hits() != 0 {
		t.Errorf("redirected JWKS: %v (%d fetches at the target)", err, p.Hits())
	}
}

// A request whose context is cancelled does not abort the refresh others
// wait for; concurrent verification is race-free (run with -race).
func TestConcurrentVerify(t *testing.T) {
	p, v, _ := setup(t)
	release := p.Hold()
	ctx, cancel := context.WithCancel(context.Background())
	tok := p.Token(t, p.RSA, nil, p.Claims("u", t0))
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := range 32 {
		wg.Go(func() {
			c := context.Background()
			if i == 0 {
				c = ctx
			}
			_, err := v.Verify(c, tok)
			errs <- err
		})
	}
	cancel()
	time.Sleep(20 * time.Millisecond)
	release()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("verify: %v", err)
		}
	}
	if p.Hits() != 1 {
		t.Errorf("%d fetches for 32 concurrent first uses, want 1", p.Hits())
	}
}

func TestConfigValidation(t *testing.T) {
	good := auth.Config{Issuer: "https://id.example/realm", Audience: "https://api.example", JWKSURL: "https://id.example/jwks?v=1"}
	if _, err := auth.NewOIDCVerifier(good); err != nil {
		t.Fatalf("good config: %v", err)
	}
	for name, edit := range map[string]func(*auth.Config){
		"no issuer":            func(c *auth.Config) { c.Issuer = "" },
		"http issuer":          func(c *auth.Config) { c.Issuer = "http://id.example" },
		"issuer query":         func(c *auth.Config) { c.Issuer = "https://id.example/?x=1" },
		"issuer fragment":      func(c *auth.Config) { c.Issuer = "https://id.example/#x" },
		"issuer credentials":   func(c *auth.Config) { c.Issuer = "https://user:SECRETVALUE@id.example" },
		"relative issuer":      func(c *auth.Config) { c.Issuer = "id.example" },
		"no audience":          func(c *auth.Config) { c.Audience = "" },
		"audience control":     func(c *auth.Config) { c.Audience = "a\nb" },
		"http JWKS":            func(c *auth.Config) { c.JWKSURL = "http://id.example/jwks" },
		"JWKS credentials":     func(c *auth.Config) { c.JWKSURL = "https://u:SECRETVALUE@id.example/jwks" },
		"HS256":                func(c *auth.Config) { c.Algorithms = []string{"HS256"} },
		"none":                 func(c *auth.Config) { c.Algorithms = []string{"none"} },
		"skew too large":       func(c *auth.Config) { c.ClockSkew = 10 * time.Minute },
		"negative skew":        func(c *auth.Config) { c.ClockSkew = -time.Second },
		"negative refresh":     func(c *auth.Config) { c.MinRefreshInterval = -time.Second },
		"negative max age":     func(c *auth.Config) { c.KeyMaxAge = -time.Second },
		"negative stale limit": func(c *auth.Config) { c.KeyMaxStale = -time.Second },
	} {
		c := good
		edit(&c)
		_, err := auth.NewOIDCVerifier(c)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "SECRETVALUE") {
			t.Errorf("%s: the error echoes the configured value: %v", name, err)
		}
	}
}

// A key is used only with its own algorithm: the JWK's alg pins it within a
// family (an RS256 key never verifies PS256), and without an alg the key
// type and curve must fit the token's alg.
func TestKeyAlgorithmPinned(t *testing.T) {
	p := authtest.New(t)
	ec384 := authtest.NewEC(t, "ec384", "ES384", elliptic.P384())
	ec384.JWKAlg = ""
	p.Publish(t, p.RSA, ec384)
	cfg := p.Config()
	cfg.Now, cfg.Algorithms = func() time.Time { return t0 }, []string{"RS256", "PS256", "ES256", "ES384"}
	v, err := auth.NewOIDCVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	claims := p.Claims("u", t0)
	for _, c := range []struct {
		name, token, reason string
	}{
		{"PS256 on the RS256 key", p.Token(t, authtest.Key{ID: p.RSA.ID, Alg: "PS256", Signer: p.RSA.Signer}, nil, claims), auth.ReasonAlgorithm},
		{"ES256 on a P-384 key without alg", p.Token(t, authtest.Key{ID: ec384.ID, Alg: "ES256", Signer: ec384.Signer}, nil, claims), auth.ReasonAlgorithm},
		{"ES384 on the P-384 key", p.Token(t, ec384, nil, claims), ""},
		{"RS256 on the RS256 key", p.Token(t, p.RSA, nil, claims), ""},
	} {
		if _, err := v.Verify(context.Background(), c.token); auth.Reason(err) != c.reason {
			t.Errorf("%s: %v, want %q", c.name, err, c.reason)
		}
	}
}

// A request that is already cancelled still completes the JWKS fetch it
// starts: the fetch has its own context and timeout.
func TestCancelledRequestStillFetches(t *testing.T) {
	p, v, _ := setup(t)
	release := p.Hold()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		release()
	}()
	if _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("u", t0))); err != nil {
		t.Errorf("cancelled request: %v", err)
	}
	if p.Hits() != 1 {
		t.Errorf("%d fetches", p.Hits())
	}
}
