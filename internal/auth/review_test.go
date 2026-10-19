package auth_test

// Regression tests from the T-008 independent review (probes in the
// review's scratchpad, review-t008/export/internal/auth/zz_probe*_test.go).

import (
	"context"
	"crypto/elliptic"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/auth/authtest"
)

// Review #1: a refresh never blocks a request that has a usable key. With
// the keys past KeyMaxAge and the provider hanging, requests with a known
// kid still return at once (stale-while-revalidate); a request that must
// wait for the refresh (unknown kid) gives up when its context ends.
func TestRefreshNeverBlocksUsableKeys(t *testing.T) {
	p, v, c := setup(t)
	ctx := context.Background()
	if _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("u", t0))); err != nil {
		t.Fatal(err)
	}
	release := p.Hold()
	t.Cleanup(release)
	c.Add(2 * time.Hour) // past KeyMaxAge, within KeyMaxStale
	tok := p.Token(t, p.RSA, nil, p.Claims("u", c.Now()))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var worst time.Duration
	for range 8 {
		wg.Go(func() {
			start := time.Now()
			if _, err := v.Verify(ctx, tok); err != nil {
				t.Errorf("known kid during a hung refresh: %v", err)
			}
			mu.Lock()
			worst = max(worst, time.Since(start))
			mu.Unlock()
		})
	}
	wg.Wait()
	if worst > time.Second {
		t.Errorf("a request with a usable key waited %v for a hung refresh", worst)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := v.Verify(short, p.Token(t, authtest.Key{ID: "rotated", Alg: "RS256", Signer: p.RSA.Signer}, nil, p.Claims("u", c.Now())))
	if !errors.Is(err, auth.ErrKeysUnavailable) || time.Since(start) > 2*time.Second {
		t.Errorf("unknown kid during a hung refresh: %v after %v (must end with its context)", err, time.Since(start))
	}
}

// Review #3: refreshes stay spaced while the provider fails, both for
// stale known keys and for unknown kids.
func TestOutageRefreshThrottled(t *testing.T) {
	p, v, c := setup(t)
	ctx := context.Background()
	if _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("u", t0))); err != nil {
		t.Fatal(err)
	}
	p.SetJWKS([]byte("down"), http.StatusServiceUnavailable)
	c.Add(2 * time.Hour)
	before := p.Hits()
	for i := range 200 {
		kid := p.RSA
		if i%2 == 1 {
			kid = authtest.Key{ID: "unknown", Alg: "RS256", Signer: p.RSA.Signer}
		}
		_, _ = v.Verify(ctx, p.Token(t, kid, nil, p.Claims("u", c.Now())))
	}
	time.Sleep(200 * time.Millisecond) // let any background refresh reach the provider
	if n := p.Hits() - before; n > 1 {
		t.Errorf("%d JWKS fetches for 200 requests within one interval during an outage, want at most 1", n)
	}
}

// waitFor polls cond briefly (background refreshes finish asynchronously).
func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// Review #5: a JWKS failure keeps its cause (status, parse error), never
// the URL, which may carry a query credential.
func TestKeysErrorKeepsTheCause(t *testing.T) {
	p := authtest.New(t)
	cfg := p.Config()
	cfg.JWKSURL += "?key=SECRETVALUE"
	cfg.Now = func() time.Time { return t0 }
	v, err := auth.NewOIDCVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p.SetJWKS([]byte("missing"), http.StatusNotFound)
	_, err = v.Verify(context.Background(), p.Token(t, p.RSA, nil, p.Claims("u", t0)))
	if !errors.Is(err, auth.ErrKeysUnavailable) || !strings.Contains(err.Error(), "status 404") {
		t.Errorf("404: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "SECRETVALUE") {
		t.Errorf("the error carries the JWKS URL: %v", err)
	}
	down := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	down.Close() // connection refused: the transport error names the URL
	cfg = p.Config()
	cfg.JWKSURL, cfg.HTTPClient = down.URL+"/jwks?key=SECRETVALUE", down.Client()
	cfg.Now = func() time.Time { return t0 }
	v, _ = auth.NewOIDCVerifier(cfg)
	_, err = v.Verify(context.Background(), p.Token(t, p.RSA, nil, p.Claims("u", t0)))
	if !errors.Is(err, auth.ErrKeysUnavailable) || strings.Contains(err.Error(), "SECRETVALUE") || !strings.Contains(err.Error(), "fetching JWKS") {
		t.Errorf("unreachable provider: %v", err)
	}
}

// Review #6: each segment has exactly one encoding; a token re-encoded
// with other trailing bits is malformed even though it decodes the same.
func TestStrictEncoding(t *testing.T) {
	p, v, _ := setup(t)
	tok := p.Token(t, p.RSA, nil, p.Claims("u", t0))
	parts := strings.Split(tok, ".")
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	tried := 0
	for i := range parts {
		seg := parts[i]
		if len(seg)%4 == 0 {
			continue
		}
		idx := strings.IndexByte(alpha, seg[len(seg)-1])
		alt := seg[:len(seg)-1] + string(alpha[idx^1])
		a, _ := base64.RawURLEncoding.DecodeString(seg)
		b, err := base64.RawURLEncoding.DecodeString(alt)
		if err != nil || string(a) != string(b) {
			continue
		}
		tried++
		q := append([]string(nil), parts...)
		q[i] = alt
		if _, err := v.Verify(context.Background(), strings.Join(q, ".")); auth.Reason(err) != auth.ReasonMalformed {
			t.Errorf("segment %d re-encoded: %v", i, err)
		}
	}
	if tried == 0 {
		t.Fatal("no segment has spare bits: the test would be vacuous")
	}
}

// Review #12: every allowed algorithm verifies with its own key type and
// size; the curve must match the alg even without a JWK alg.
func TestAllAlgorithms(t *testing.T) {
	p := authtest.New(t)
	keys := []authtest.Key{
		authtest.NewRSA(t, "rs384", "RS384", 2048), authtest.NewRSA(t, "rs512", "RS512", 3072),
		authtest.NewRSA(t, "ps256", "PS256", 2048), authtest.NewRSA(t, "ps384", "PS384", 2048), authtest.NewRSA(t, "ps512", "PS512", 4096),
		authtest.NewEC(t, "es384", "ES384", elliptic.P384()), authtest.NewEC(t, "es512", "ES512", elliptic.P521()), authtest.NewEd(t, "ed"),
	}
	p521 := authtest.NewEC(t, "p521-noalg", "ES512", elliptic.P521())
	p521.JWKAlg = ""
	p.Publish(t, append(keys, p521)...)
	cfg := p.Config()
	cfg.Now = func() time.Time { return t0 }
	cfg.Algorithms = []string{"RS384", "RS512", "PS256", "PS384", "PS512", "ES384", "ES512", "EdDSA"}
	v, err := auth.NewOIDCVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range append(keys, p521) {
		if _, err := v.Verify(context.Background(), p.Token(t, k, nil, p.Claims("u", t0))); err != nil {
			t.Errorf("%s (%s): %v", k.Alg, k.ID, err)
		}
	}
	if _, err := v.Verify(context.Background(), p.Token(t, authtest.Key{ID: p521.ID, Alg: "ES384", Signer: p521.Signer}, nil, p.Claims("u", t0))); auth.Reason(err) != auth.ReasonAlgorithm {
		t.Errorf("ES384 header on a P-521 key: %v", err)
	}
	// The remaining forbidden header parameters.
	v2, _ := auth.NewOIDCVerifier(func() auth.Config { c := p.Config(); c.Now = func() time.Time { return t0 }; return c }())
	p.Publish(t, p.RSA)
	for _, h := range []string{"x5t", "x5t#S256", "zip", "enc"} {
		if _, err := v2.Verify(context.Background(), p.Token(t, p.RSA, map[string]any{h: "x"}, p.Claims("u", t0))); auth.Reason(err) != auth.ReasonHeader {
			t.Errorf("header %s: %v", h, err)
		}
	}
}

// Review #12: the JWKS body is read through a size limit, so an endless
// body fails on its size, not on the timeout.
func TestEndlessJWKSBody(t *testing.T) {
	p := authtest.New(t)
	endless := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := []byte(strings.Repeat(" ", 32<<10))
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(endless.Close)
	cfg := p.Config()
	cfg.JWKSURL, cfg.HTTPClient, cfg.Now = endless.URL+"/jwks", endless.Client(), func() time.Time { return t0 }
	v, err := auth.NewOIDCVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = v.Verify(context.Background(), p.Token(t, p.RSA, nil, p.Claims("u", t0)))
	if !errors.Is(err, auth.ErrKeysUnavailable) || !strings.Contains(err.Error(), "256 KiB") || time.Since(start) > 5*time.Second {
		t.Errorf("endless body: %v after %v", err, time.Since(start))
	}
}

// Review #13: while no keys are cached, a failed fetch is retried after a
// few seconds, not a full refresh interval, so a cold start recovers.
func TestColdStartRecovers(t *testing.T) {
	p, v, c := setup(t)
	p.SetJWKS(nil, http.StatusBadGateway)
	tok := func() string { return p.Token(t, p.RSA, nil, p.Claims("u", c.Now())) }
	if _, err := v.Verify(context.Background(), tok()); !errors.Is(err, auth.ErrKeysUnavailable) {
		t.Fatalf("provider down: %v", err)
	}
	p.Publish(t, p.RSA, p.EC)
	c.Add(6 * time.Second)
	if _, err := v.Verify(context.Background(), tok()); err != nil {
		t.Errorf("6 s after a failed cold start, with the provider back: %v", err)
	}
	// Recovered: an unknown kid is unknown again, not an outage.
	c.Add(time.Minute)
	if _, err := v.Verify(context.Background(), p.Token(t, authtest.Key{ID: "random", Alg: "RS256", Signer: p.RSA.Signer}, nil, p.Claims("u", c.Now()))); auth.Reason(err) != auth.ReasonUnknownKey {
		t.Errorf("unknown kid after recovery: %v", err)
	}
	// Keys past KeyMaxStale are no keys: the short retry applies again.
	p.SetJWKS(nil, http.StatusBadGateway)
	c.Add(25 * time.Hour)
	if _, err := v.Verify(context.Background(), tok()); !errors.Is(err, auth.ErrKeysUnavailable) {
		t.Fatalf("keys past KeyMaxStale, provider down: %v", err)
	}
	p.Publish(t, p.RSA)
	c.Add(6 * time.Second)
	if _, err := v.Verify(context.Background(), tok()); err != nil {
		t.Errorf("6 s after a failed fetch with only expired keys: %v", err)
	}
}

// Review PLAUSIBLE: a subject with a Unicode format character (e.g. U+202E,
// right-to-left override) would spoof logs; it is refused like a control
// character.
func TestSubjectFormatCharacters(t *testing.T) {
	p, v, _ := setup(t)
	for _, sub := range []string{"user\u202eadmin", "a\u200bb", "x\ufeff", "a\u2028b", "a\u00a0b", "x\ue000"} {
		if _, err := v.Verify(context.Background(), p.Token(t, p.RSA, nil, with(p.Claims("u", t0), "sub", sub))); auth.Reason(err) != auth.ReasonSubject {
			t.Errorf("sub %q: %v", sub, err)
		}
	}
}

// Review #8, #9 and PLAUSIBLE: configuration errors do not echo values;
// the issuer fits users.oidc_issuer as tenant.ResolveUser bounds it; key
// intervals are ordered and bounded.
func TestConfigBounds(t *testing.T) {
	good := auth.Config{Issuer: "https://id.example/realm", Audience: "https://api.example", JWKSURL: "https://id.example/jwks"}
	for name, edit := range map[string]func(*auth.Config){
		"echoed algorithm":           func(c *auth.Config) { c.Algorithms = []string{"SECRETVALUE"} },
		"issuer of 2049 bytes":       func(c *auth.Config) { c.Issuer = "https://id.example/" + strings.Repeat("i", 2049-19) },
		"JWKS URL of 2049 bytes":     func(c *auth.Config) { c.JWKSURL = "https://id.example/" + strings.Repeat("j", 2049-19) },
		"stale limit below max age":  func(c *auth.Config) { c.KeyMaxAge, c.KeyMaxStale = 2*time.Hour, time.Hour },
		"refresh interval above age": func(c *auth.Config) { c.MinRefreshInterval, c.KeyMaxAge = 2*time.Hour, time.Hour },
		"refresh interval over a day": func(c *auth.Config) {
			c.MinRefreshInterval, c.KeyMaxAge, c.KeyMaxStale = 25*time.Hour, 48*time.Hour, 72*time.Hour
		},
		"refresh interval under 1 s":  func(c *auth.Config) { c.MinRefreshInterval = 999 * time.Millisecond },
		"stale limit over 7 days":     func(c *auth.Config) { c.KeyMaxStale = 7*24*time.Hour + time.Second },
		"default age above refresh":   func(c *auth.Config) { c.MinRefreshInterval = 2 * time.Hour },
		"default stale below age":     func(c *auth.Config) { c.KeyMaxAge = 25 * time.Hour },
		"negative fetch timeout":      func(c *auth.Config) { c.FetchTimeout = -time.Second },
		"fetch timeout over a minute": func(c *auth.Config) { c.FetchTimeout = time.Minute + time.Second },
		"audience format character":   func(c *auth.Config) { c.Audience = "api\u202e" },
	} {
		c := good
		edit(&c)
		_, err := auth.NewOIDCVerifier(c)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "SECRETVALUE") {
			t.Errorf("%s: the error echoes the value: %v", name, err)
		}
	}
	// The limits themselves are allowed.
	for name, edit := range map[string]func(*auth.Config){
		"issuer of 2048 bytes":   func(c *auth.Config) { c.Issuer = "https://id.example/" + strings.Repeat("i", 2048-19) },
		"JWKS URL of 2048 bytes": func(c *auth.Config) { c.JWKSURL = "https://id.example/" + strings.Repeat("j", 2048-19) },
		"equal intervals": func(c *auth.Config) {
			c.MinRefreshInterval, c.KeyMaxAge, c.KeyMaxStale = time.Hour, time.Hour, time.Hour
		},
		"shortest refresh":       func(c *auth.Config) { c.MinRefreshInterval = time.Second },
		"longest refresh":        func(c *auth.Config) { c.MinRefreshInterval, c.KeyMaxAge = 24*time.Hour, 24*time.Hour },
		"longest stale limit":    func(c *auth.Config) { c.KeyMaxStale = 7 * 24 * time.Hour },
		"longest fetch timeout":  func(c *auth.Config) { c.FetchTimeout = time.Minute },
		"non-ASCII audience":     func(c *auth.Config) { c.Audience = "api-日本 x" },
		"required access tokens": func(c *auth.Config) { c.RequireAccessTokenType = true },
	} {
		c := good
		edit(&c)
		if _, err := auth.NewOIDCVerifier(c); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Review #4: one JWKS fetch is bounded by FetchTimeout; the request that
// waits for it gets ErrKeysUnavailable with the deadline as its cause.
func TestFetchTimeout(t *testing.T) {
	p, v, _ := setup(t, func(c *auth.Config) { c.FetchTimeout = 100 * time.Millisecond })
	release := p.Hold()
	t.Cleanup(release)
	// The request gives up only after 3 s, and with Canceled, so the
	// deadline can come only from the fetch.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer time.AfterFunc(3*time.Second, cancel).Stop()
	start := time.Now()
	_, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("u", t0)))
	if !errors.Is(err, auth.ErrKeysUnavailable) || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Errorf("hung provider: %v after %v", err, time.Since(start))
	}
}

type panicking struct{}

func (panicking) RoundTrip(*http.Request) (*http.Response, error) { panic("transport bug") }

// The background fetch contains a panic, as net/http would in a handler:
// the request gets ErrKeysUnavailable and the process lives on.
func TestFetchPanicContained(t *testing.T) {
	p, v, _ := setup(t, func(c *auth.Config) { c.HTTPClient = &http.Client{Transport: panicking{}} })
	if _, err := v.Verify(context.Background(), p.Token(t, p.RSA, nil, p.Claims("u", t0))); !errors.Is(err, auth.ErrKeysUnavailable) || !strings.Contains(err.Error(), "internal error") {
		t.Errorf("panicking transport: %v", err)
	}
}

// Review #14: with RequireAccessTokenType only tokens typed at+jwt pass, so
// an ID token whose aud happens to be the API's is refused; without it any
// JWT type is accepted, as before.
func TestRequireAccessTokenType(t *testing.T) {
	p, strict, _ := setup(t, func(c *auth.Config) { c.RequireAccessTokenType = true })
	cfg := p.Config()
	cfg.Now = func() time.Time { return t0 }
	lax, err := auth.NewOIDCVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	claims := p.Claims("u", t0)
	for _, c := range []struct {
		typ         any
		strict, lax string
	}{
		{"at+jwt", "", ""},
		{"AT+JWT", "", ""},
		{"application/at+jwt", "", ""},
		{"JWT", auth.ReasonType, ""},
		{nil, auth.ReasonType, ""},
		{"id+jwt", auth.ReasonType, auth.ReasonType},
	} {
		tok := p.Token(t, p.RSA, map[string]any{"typ": c.typ}, claims)
		if _, err := strict.Verify(context.Background(), tok); auth.Reason(err) != c.strict {
			t.Errorf("required, typ %v: %v", c.typ, err)
		}
		if _, err := lax.Verify(context.Background(), tok); auth.Reason(err) != c.lax {
			t.Errorf("not required, typ %v: %v", c.typ, err)
		}
	}
}

// Re-review #1: after a failed fetch the provider is likely still down, so
// a request waits for the retry for at most a second instead of holding its
// connection for the whole fetch timeout.
func TestRetryAfterFailureWaitsBriefly(t *testing.T) {
	p, v, c := setup(t, func(c *auth.Config) { c.FetchTimeout = 2 * time.Second })
	release := p.Hold()
	t.Cleanup(release)
	tok := p.Token(t, p.RSA, nil, p.Claims("u", t0))
	if _, err := v.Verify(context.Background(), tok); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first fetch, provider hanging: %v", err)
	}
	c.Add(6 * time.Second) // past the cold retry interval: a retry starts
	start := time.Now()
	_, err := v.Verify(context.Background(), tok)
	if !errors.Is(err, auth.ErrKeysUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("during the retry: %v, want the previous fetch's error", err)
	}
	if d := time.Since(start); d < 900*time.Millisecond || d > 1700*time.Millisecond {
		t.Errorf("waited %v for the retry, want about 1 s", d)
	}
}

type goexitOnce struct {
	done atomic.Bool
	next http.RoundTripper
}

func (g *goexitOnce) RoundTrip(r *http.Request) (*http.Response, error) {
	if g.done.CompareAndSwap(false, true) {
		runtime.Goexit()
	}
	return g.next.RoundTrip(r)
}

// Re-review #2: a fetch that never returns normally (runtime.Goexit in a
// transport) still ends the refresh, so the next one can start.
func TestFetchGoexitRecovers(t *testing.T) {
	p := authtest.New(t)
	c := &clock{now: t0}
	cfg := p.Config()
	cfg.Now, cfg.HTTPClient = c.Now, &http.Client{Transport: &goexitOnce{next: p.Server.Client().Transport}}
	v, err := auth.NewOIDCVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), p.Token(t, p.RSA, nil, p.Claims("u", t0))); !errors.Is(err, auth.ErrKeysUnavailable) || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("aborted fetch: %v", err)
	}
	c.Add(6 * time.Second)
	if _, err := v.Verify(context.Background(), p.Token(t, p.RSA, nil, p.Claims("u", c.Now()))); err != nil {
		t.Errorf("the next fetch: %v", err)
	}
}

// Re-review #3: Go's base64 decoder skips CR and LF even in strict mode; a
// token is base64url characters and dots only.
func TestNewlinesInToken(t *testing.T) {
	p, v, _ := setup(t)
	parts := strings.Split(p.Token(t, p.RSA, nil, p.Claims("u", t0)), ".")
	for i := range parts {
		for _, nl := range []string{"\r\n", "\n", "\r"} {
			q := append([]string(nil), parts...)
			q[i] = q[i][:4] + nl + q[i][4:]
			if _, err := v.Verify(context.Background(), strings.Join(q, ".")); auth.Reason(err) != auth.ReasonMalformed {
				t.Errorf("segment %d with %q: %v", i, nl, err)
			}
		}
	}
	for _, bad := range []string{"+", "/", "=", " ", "\x00", "é"} {
		q := append([]string(nil), parts...)
		q[1] += bad
		if _, err := v.Verify(context.Background(), strings.Join(q, ".")); auth.Reason(err) != auth.ReasonMalformed {
			t.Errorf("payload with %q: %v", bad, err)
		}
	}
}

// Re-review #4: the exact ages at which a cached key is refreshed and at
// which it stops being usable, and the full refresh interval while keys are
// cached (the 5 s retry is only for no usable keys).
func TestKeyAgeBoundaries(t *testing.T) {
	p, v, c := setup(t)
	ctx := context.Background()
	verify := func() error { _, err := v.Verify(ctx, p.Token(t, p.RSA, nil, p.Claims("u", c.Now()))); return err }
	if err := verify(); err != nil || p.Hits() != 1 {
		t.Fatalf("first use: %v, %d fetches", err, p.Hits())
	}
	c.Add(6 * time.Second)
	if _, err := v.Verify(ctx, p.Token(t, authtest.Key{ID: "random", Alg: "RS256", Signer: p.RSA.Signer}, nil, p.Claims("u", c.Now()))); auth.Reason(err) != auth.ReasonUnknownKey || p.Hits() != 1 {
		t.Errorf("unknown kid 6 s after a successful fetch: %v, %d fetches (want no fetch before MinRefreshInterval)", err, p.Hits())
	}
	c.Add(time.Hour - 6*time.Second - time.Nanosecond)
	if err := verify(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if p.Hits() != 1 {
		t.Errorf("%d fetches 1 ns before KeyMaxAge, want 1", p.Hits())
	}
	c.Add(time.Nanosecond)
	if err := verify(); err != nil {
		t.Fatal(err)
	}
	// Until the refresh has stored its keys: they are stamped with the
	// clock's time then, and the clock moves on next.
	if !waitFor(t, func() bool { return p.Hits() == 2 && !v.Refreshing() }) {
		t.Errorf("%d fetches at KeyMaxAge, want a background refresh", p.Hits())
	}
	// Refreshed at 1 h. With the provider down, the keys stay usable until
	// exactly 25 h (KeyMaxStale after the last success).
	p.SetJWKS(nil, http.StatusServiceUnavailable)
	c.Add(24*time.Hour - time.Nanosecond)
	if err := verify(); err != nil {
		t.Errorf("1 ns before KeyMaxStale: %v", err)
	}
	if !waitFor(t, func() bool { return p.Hits() == 3 && !v.Refreshing() }) {
		t.Errorf("%d fetches 1 ns before KeyMaxStale, want a background refresh", p.Hits())
	}
	c.Add(time.Nanosecond)
	if err := verify(); !errors.Is(err, auth.ErrKeysUnavailable) {
		t.Errorf("at KeyMaxStale: %v", err)
	}
}
