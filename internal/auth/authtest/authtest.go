// Package authtest is a test-only OIDC provider: it holds signing keys,
// serves their JWKS over TLS and mints tokens, including malformed and
// malicious ones, with full control of header and payload bytes.
//
// Test-only: imported from _test.go files, never from production code
// (scripts/check-architecture), so no binary can accept its tokens.
package authtest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json/v2"
	"hash"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/kcl1104/geminus/internal/auth"
)

// Key is a signing key the provider may publish.
type Key struct {
	ID     string
	Alg    string // JWS alg the key signs with
	Signer crypto.Signer
	// JWKAlg is the "alg" member published in the JWKS ("" to omit).
	JWKAlg string
}

// Public is the key's public half.
func (k Key) Public() crypto.PublicKey { return k.Signer.Public() }

// NewRSA, NewEC and NewEd make keys; tests fail on generation errors.
func NewRSA(t testing.TB, id, alg string, bits int) Key {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	return Key{ID: id, Alg: alg, Signer: k, JWKAlg: alg}
}

func NewEC(t testing.TB, id, alg string, curve elliptic.Curve) Key {
	t.Helper()
	k, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Key{ID: id, Alg: alg, Signer: k, JWKAlg: alg}
}

func NewEd(t testing.TB, id string) Key {
	t.Helper()
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Key{ID: id, Alg: "EdDSA", Signer: k, JWKAlg: "EdDSA"}
}

// Provider is an issuer with a JWKS endpoint.
type Provider struct {
	Issuer   string
	Audience string
	Server   *httptest.Server
	RSA, EC  Key // published by default

	mu      sync.Mutex
	jwks    []byte // served body, unless overridden
	status  int
	hits    atomic.Int64
	release chan struct{} // when set, the JWKS handler waits for it
}

// New starts a provider whose JWKS publishes an RS256 and an ES256 key.
func New(t testing.TB) *Provider {
	t.Helper()
	p := &Provider{Audience: "https://api.geminus.test", status: http.StatusOK,
		RSA: NewRSA(t, "rsa-1", "RS256", 2048), EC: NewEC(t, "ec-1", "ES256", elliptic.P256())}
	p.Server = httptest.NewTLSServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.Server.Close)
	p.Issuer = p.Server.URL + "/issuer"
	p.Publish(t, p.RSA, p.EC)
	return p
}

func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	p.hits.Add(1)
	p.mu.Lock()
	body, status, release := p.jwks, p.status, p.release
	p.mu.Unlock()
	if release != nil {
		<-release
	}
	if r.URL.Path != "/jwks" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/jwk-set+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// Config is an auth.Config for this provider; the HTTP client trusts the
// server's test certificate.
func (p *Provider) Config() auth.Config {
	return auth.Config{Issuer: p.Issuer, Audience: p.Audience, JWKSURL: p.Server.URL + "/jwks", HTTPClient: p.Server.Client()}
}

// Publish replaces the served JWKS with keys' public halves.
func (p *Provider) Publish(t testing.TB, keys ...Key) {
	t.Helper()
	var set jose.JSONWebKeySet
	for _, k := range keys {
		set.Keys = append(set.Keys, jose.JSONWebKey{Key: k.Public(), KeyID: k.ID, Algorithm: k.JWKAlg, Use: "sig"})
	}
	body, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	p.SetJWKS(body, http.StatusOK)
}

// SetJWKS serves body with status.
func (p *Provider) SetJWKS(body []byte, status int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.jwks, p.status = body, status
}

// Hold makes JWKS requests wait until the returned function is called.
func (p *Provider) Hold() (release func()) {
	ch := make(chan struct{})
	p.mu.Lock()
	p.release = ch
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		p.release = nil
		p.mu.Unlock()
		close(ch)
	}
}

// Hits counts JWKS requests.
func (p *Provider) Hits() int64 { return p.hits.Load() }

// Claims are valid claims for subject at now.
func (p *Provider) Claims(subject string, now time.Time) map[string]any {
	return map[string]any{"iss": p.Issuer, "aud": p.Audience, "sub": subject,
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()}
}

// Token signs claims with k under the header {"alg": k.Alg, "kid": k.ID,
// "typ": "JWT"} merged with extra (a nil value deletes a member).
func (p *Provider) Token(t testing.TB, k Key, extra map[string]any, claims map[string]any) string {
	t.Helper()
	h := map[string]any{"alg": k.Alg, "kid": k.ID, "typ": "JWT"}
	for name, v := range extra {
		if v == nil {
			delete(h, name)
		} else {
			h[name] = v
		}
	}
	return Sign(t, k, mustJSON(t, h), mustJSON(t, claims))
}

// Sign signs header and payload bytes as given with k's algorithm.
func Sign(t testing.TB, k Key, header, payload []byte) string {
	t.Helper()
	input := b64(header) + "." + b64(payload)
	return input + "." + b64(signature(t, k.Alg, k.Signer, []byte(input)))
}

// HMAC signs header and payload with HS256 and secret: the "public key as
// HMAC secret" attack.
func HMAC(header, payload, secret []byte) string {
	input := b64(header) + "." + b64(payload)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(input))
	return input + "." + b64(m.Sum(nil))
}

// Unsigned is header.payload. with an empty signature (alg "none").
func Unsigned(header, payload []byte) string { return b64(header) + "." + b64(payload) + "." }

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func mustJSON(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func signature(t testing.TB, alg string, s crypto.Signer, input []byte) []byte {
	t.Helper()
	var h hash.Hash
	var ch crypto.Hash
	switch alg[len(alg)-3:] {
	case "256":
		h, ch = sha256.New(), crypto.SHA256
	case "384":
		h, ch = sha512.New384(), crypto.SHA384
	case "512":
		h, ch = sha512.New(), crypto.SHA512
	}
	if alg == "EdDSA" {
		return ed25519.Sign(s.(ed25519.PrivateKey), input)
	}
	h.Write(input)
	digest := h.Sum(nil)
	switch k := s.(type) {
	case *rsa.PrivateKey:
		var sig []byte
		var err error
		if alg[:2] == "PS" {
			sig, err = rsa.SignPSS(rand.Reader, k, ch, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		} else {
			sig, err = rsa.SignPKCS1v15(rand.Reader, k, ch, digest)
		}
		if err != nil {
			t.Fatal(err)
		}
		return sig
	case *ecdsa.PrivateKey:
		r, sv, err := ecdsa.Sign(rand.Reader, k, digest)
		if err != nil {
			t.Fatal(err)
		}
		size := (k.Curve.Params().BitSize + 7) / 8
		return append(pad(r, size), pad(sv, size)...)
	}
	t.Fatalf("no signer for %s", alg)
	return nil
}

func pad(n *big.Int, size int) []byte {
	b := n.Bytes()
	return append(make([]byte, size-len(b)), b...)
}
