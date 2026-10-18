#!/usr/bin/env python3
"""T-008 mutation run after the independent review: each mutant removes or
weakens one check of the token verifier, the key refresh, the bearer
extraction, the configuration or the user lookup, and must make a test
fail. Applied with `go test -overlay` (the working tree is never modified);
KILLED means a test failed. Supersedes 2026-10-15-T-008/mutants.py, whose
patterns name code the review fixes replaced.

Usage: GEMINUS_TEST_DATABASE_URL=... python3 checks/evidence/2026-10-18-T-008-review/mutants.py [--check] [--only=NAME ...] [repo-root]
--check only confirms that every pattern occurs exactly once; --only runs
the named mutants (mutation-addendum.log). The tenant
mutants need the disposable database of `make db-test`; without it the run
stops (GEMINUS_REQUIRE_DB_TESTS=1 turns a skip into a failure, which would
count as a kill, so the URL is required up front).
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

ONLY = [a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")]
ARGS = [a for a in sys.argv[1:] if a != "--check" and not a.startswith("--only=")]
CHECK = "--check" in sys.argv[1:]
REPO = pathlib.Path(ARGS[0] if ARGS else pathlib.Path(__file__).resolve().parents[3])
O = "internal/auth/oidc.go"
H = "internal/auth/http.go"
A = "internal/auth/auth.go"
T = "internal/tenant/transaction.go"
C = "internal/config/auth.go"
PKGS = ["./internal/auth/...", "./internal/config/...", "./cmd/api/...", "./internal/tenant/..."]

MUT = [
    # --- Token form and header.
    ("token-size-unchecked", O, "\tif len(token) > MaxTokenBytes {\n\t\treturn Identity{}, invalid(ReasonTooLarge)\n\t}\n", ""),
    ("segment-count-unchecked", O, "\tif len(segments) != 3 {", "\tif len(segments) < 1 {"),
    ("decoding-not-strict", O, "base64.RawURLEncoding.Strict().DecodeString(seg)", "base64.RawURLEncoding.DecodeString(seg)"),
    ("strict-header-only", O, "\tfor i, seg := range segments {", "\tfor i, seg := range segments[:1] {"),
    ("alg-allowlist-ignored", O, "\tif json.Unmarshal(h[\"alg\"], &alg) != nil || !slices.Contains(v.cfg.Algorithms, alg) {", "\tif json.Unmarshal(h[\"alg\"], &alg) != nil {"),
    ("kid-optional", O, "\tif json.Unmarshal(h[\"kid\"], &kid) != nil || kid == \"\" || len(kid) > 256 {", "\tif json.Unmarshal(h[\"kid\"], &kid) != nil || len(kid) > 256 {"),
    ("forbidden-header-ignored", O, "\t\tif _, ok := h[p]; ok {\n\t\t\treturn \"\", \"\", invalid(ReasonHeader)", "\t\tif _, ok := h[p]; ok && false {\n\t\t\treturn \"\", \"\", invalid(ReasonHeader)"),
    ("crit-allowed", O, "\"x5t#S256\", \"crit\", \"b64\"", "\"x5t#S256\", \"b64\""),
    ("jku-allowed", O, "var forbiddenHeader = []string{\"jku\", ", "var forbiddenHeader = []string{"),
    ("x5t-allowed", O, "\"x5c\", \"x5t\", \"x5t#S256\"", "\"x5c\", \"x5t#S256\""),
    ("x5t-s256-allowed", O, "\"x5t\", \"x5t#S256\", \"crit\"", "\"x5t\", \"crit\""),
    ("zip-allowed", O, "\"b64\", \"zip\", \"enc\"}", "\"b64\", \"enc\"}"),
    ("enc-allowed", O, "\"zip\", \"enc\"}", "\"zip\"}"),
    ("typ-ignored", O, "\tif t, ok := h[\"typ\"]; ok || v.cfg.RequireAccessTokenType {", "\tif t, ok := h[\"typ\"]; false && (ok || v.cfg.RequireAccessTokenType) {"),
    ("require-at-absent-typ-allowed", O, "\tif t, ok := h[\"typ\"]; ok || v.cfg.RequireAccessTokenType {", "\tif t, ok := h[\"typ\"]; ok {"),
    ("require-at-ignored", O, "\t\t\t(v.cfg.RequireAccessTokenType && !strings.HasSuffix(strings.ToLower(typ), \"at+jwt\")) {", "\t\t\tfalse {"),
    # --- Key and signature.
    ("key-alg-ignored", O, "\tif (key.Algorithm != \"\" && key.Algorithm != alg) || !fits(alg, key.Key) {", "\tif !fits(alg, key.Key) {"),
    ("key-type-ignored", O, "\tif (key.Algorithm != \"\" && key.Algorithm != alg) || !fits(alg, key.Key) {", "\tif key.Algorithm != \"\" && key.Algorithm != alg {"),
    ("rsa-size-ignored", O, "&& k.N.BitLen() >= minRSABits", ""),
    ("ps-not-fitting", O, "(strings.HasPrefix(alg, \"RS\") || strings.HasPrefix(alg, \"PS\"))", "strings.HasPrefix(alg, \"RS\")"),
    ("curve-ignored", O, "\t\treturn map[string]elliptic.Curve{\"ES256\": elliptic.P256(), \"ES384\": elliptic.P384(), \"ES512\": elliptic.P521()}[alg] == k.Curve", "\t\t_ = elliptic.P256\n\t\treturn strings.HasPrefix(alg, \"ES\")"),
    ("es512-wrong-curve", O, "\"ES512\": elliptic.P521()}", "\"ES512\": elliptic.P384()}"),
    ("eddsa-not-fitting", O, "\t\treturn alg == \"EdDSA\"", "\t\treturn false"),
    ("signature-ignored", O, "\tpayload, err := jws.Verify(key.Key)\n\tif err != nil {", "\tpayload, err := jws.UnsafePayloadWithoutVerification(), error(nil)\n\tif err != nil {"),
    # --- Claims.
    ("issuer-ignored", O, "\tif c.Iss == nil || *c.Iss != v.cfg.Issuer {", "\tif c.Iss == nil {"),
    ("issuer-prefix", O, "\tif c.Iss == nil || *c.Iss != v.cfg.Issuer {", "\tif c.Iss == nil || !strings.HasPrefix(*c.Iss, v.cfg.Issuer) {"),
    ("audience-ignored", O, "\tif !slices.Contains(auds, v.cfg.Audience) {", "\tif !slices.Contains(auds, v.cfg.Audience) && false {"),
    ("audience-type-ignored", O, "\tauds, ok := audiences(c.Aud)\n\tif !ok {", "\tauds, ok := audiences(c.Aud)\n\tif !ok && false {"),
    ("exp-optional", O, "\tif !ok || c.Exp == nil || !now.Before(exp.Add(skew)) {", "\tif !ok || (c.Exp != nil && !now.Before(exp.Add(skew))) {"),
    ("exp-without-skew", O, "\tif !ok || c.Exp == nil || !now.Before(exp.Add(skew)) {", "\tif !ok || c.Exp == nil || !now.Before(exp) {"),
    ("exp-inclusive", O, "\tif !ok || c.Exp == nil || !now.Before(exp.Add(skew)) {", "\tif !ok || c.Exp == nil || now.After(exp.Add(skew)) {"),
    ("nbf-ignored", O, "\t} else if c.Nbf != nil && now.Add(skew).Before(nbf) {", "\t} else if c.Nbf != nil && false && now.Add(skew).Before(nbf) {"),
    ("iat-ignored", O, "\t} else if c.Iat != nil && iat.After(now.Add(skew)) {", "\t} else if c.Iat != nil && false && iat.After(now.Add(skew)) {"),
    ("date-range-unchecked", O, "\tif math.IsNaN(*v) || *v < 0 || *v >= 253402300800 {", "\tif math.IsNaN(*v) {"),
    ("subject-length", O, "len(*c.Sub) > maxSubject", "len(*c.Sub) > maxSubject+1"),
    ("subject-unprintable-allowed", O, "\tif c.Sub == nil || *c.Sub == \"\" || len(*c.Sub) > maxSubject || !printable(*c.Sub) {", "\tif c.Sub == nil || *c.Sub == \"\" || len(*c.Sub) > maxSubject {"),
    ("printable-control-only", O, "func(r rune) bool { return !unicode.IsPrint(r) }", "unicode.IsControl"),
    # --- Configuration.
    ("skew-not-defaulted", O, "\tcfg.ClockSkew = or(cfg.ClockSkew, defaultSkew)\n", ""),
    ("fetch-timeout-not-defaulted", O, "\tcfg.FetchTimeout = or(cfg.FetchTimeout, defaultFetch)\n", ""),
    ("skew-unbounded", O, "\tif c.ClockSkew < 0 || c.ClockSkew > maxClockSkew {", "\tif c.ClockSkew < 0 {"),
    ("hmac-configurable", O, "\t\tif !slices.Contains(asymmetric, a) {", "\t\tif !slices.Contains(asymmetric, a) && a != \"HS256\" {"),
    ("algorithm-echoed", O, "fmt.Errorf(\"algorithm %d is not an allowed asymmetric signature algorithm\", i+1)", "fmt.Errorf(\"algorithm %d %q is not an allowed asymmetric signature algorithm\", i+1, a)"),
    ("http-jwks-allowed", O, "\tcase u.Scheme != \"https\" || u.Host == \"\" || u.Opaque != \"\":", "\tcase u.Host == \"\" || u.Opaque != \"\":"),
    ("url-length-unbounded", O, "\tcase len(raw) > maxURL:", "\tcase false:"),
    ("url-length-off-by-one", O, "\tcase len(raw) > maxURL:", "\tcase len(raw) >= maxURL:"),
    ("fetch-timeout-unbounded", O, "\tif c.FetchTimeout < 0 || c.FetchTimeout > maxFetch {", "\tif c.FetchTimeout < 0 {"),
    ("fetch-timeout-negative", O, "\tif c.FetchTimeout < 0 || c.FetchTimeout > maxFetch {", "\tif c.FetchTimeout > maxFetch {"),
    ("intervals-not-defaulted-in-validate", O, "\trefresh, age, stale := or(c.MinRefreshInterval, defaultRefresh), or(c.KeyMaxAge, defaultMaxAge), or(c.KeyMaxStale, defaultStale)", "\trefresh, age, stale := c.MinRefreshInterval, c.KeyMaxAge, c.KeyMaxStale"),
    ("refresh-floor-removed", O, "\tcase refresh < minRefresh || refresh > maxRefresh || stale > maxStale:", "\tcase refresh > maxRefresh || stale > maxStale:"),
    ("refresh-ceiling-removed", O, "\tcase refresh < minRefresh || refresh > maxRefresh || stale > maxStale:", "\tcase refresh < minRefresh || stale > maxStale:"),
    ("stale-ceiling-removed", O, "\tcase refresh < minRefresh || refresh > maxRefresh || stale > maxStale:", "\tcase refresh < minRefresh || refresh > maxRefresh:"),
    ("interval-order-refresh-age", O, "\tcase refresh > age || age > stale:", "\tcase age > stale:"),
    ("interval-order-age-stale", O, "\tcase refresh > age || age > stale:", "\tcase refresh > age:"),
    # --- Key refresh (review #1, #3, #4, #5, #13).
    ("stale-fast-path-unbounded", O, "\tif ks := v.keys.Load(); ks != nil && now.Sub(ks.fetched) < v.cfg.KeyMaxStale {\n\t\tif k, ok := ks.byID[kid]; ok {", "\tif ks := v.keys.Load(); ks != nil {\n\t\tif k, ok := ks.byID[kid]; ok {"),
    ("max-age-never-refreshes", O, "\t\t\tif now.Sub(ks.fetched) >= v.cfg.KeyMaxAge {\n\t\t\t\tv.refresh(now)\n\t\t\t}\n", ""),
    ("known-key-always-refreshes", O, "\t\t\tif now.Sub(ks.fetched) >= v.cfg.KeyMaxAge {\n\t\t\t\tv.refresh(now)", "\t\t\tif true {\n\t\t\t\tv.refresh(now)"),
    ("known-key-waits-for-refresh", O, "\t\t\t\tv.refresh(now)\n\t\t\t}\n\t\t\treturn k, nil", "\t\t\t\tif d, _ := v.refresh(now); d != nil {\n\t\t\t\t\t<-d\n\t\t\t\t}\n\t\t\t}\n\t\t\treturn k, nil"),
    ("wait-ignores-context", O, "\t\tcase <-ctx.Done():\n\t\t\treturn jose.JSONWebKey{}, keysUnavailable(ctx.Err())", "\t\tcase <-make(chan struct{}):\n\t\t\treturn jose.JSONWebKey{}, keysUnavailable(ctx.Err())"),
    ("unknown-kid-does-not-wait", O, "\tif done, failed := v.refresh(now); done != nil {", "\tif done, failed := v.refresh(now); done != nil && false {"),
    ("stale-limit-ignored", O, "\tif ks == nil || now.Sub(ks.fetched) >= v.cfg.KeyMaxStale {\n\t\treturn jose.JSONWebKey{}, keysUnavailable(fetchErr)", "\tif ks == nil {\n\t\treturn jose.JSONWebKey{}, keysUnavailable(fetchErr)"),
    ("outage-unknown-kid-is-invalid", O, "\tif fetchErr != nil {\n\t\t// The key may", "\tif false {\n\t\t// The key may"),
    ("no-single-flight", O, "\tif v.refreshing != nil {\n\t\treturn v.refreshing, v.err\n\t}\n", ""),
    ("refresh-unthrottled", O, "\tif !v.last.IsZero() && now.Sub(v.last) < interval {", "\tif false {"),
    ("throttle-only-after-success", O, "\tv.last = now\n\tdone := make(chan struct{})\n\tv.refreshing = done\n\tgo func() {\n\t\t// No request's context: the fetch serves every waiting request. The\n\t\t// deferred update also runs if the fetch never returns normally\n\t\t// (runtime.Goexit in a transport), so a refresh is never stuck.\n\t\tvar ks *keySet\n\t\terr := errors.New(\"fetching JWKS: aborted\")\n\t\tdefer func() {\n\t\t\tv.mu.Lock()\n\t\t\tif err == nil {\n\t\t\t\tv.keys.Store(ks)", "\tdone := make(chan struct{})\n\tv.refreshing = done\n\tgo func() {\n\t\tvar ks *keySet\n\t\terr := errors.New(\"fetching JWKS: aborted\")\n\t\tdefer func() {\n\t\t\tv.mu.Lock()\n\t\t\tif err == nil {\n\t\t\t\tv.last = now\n\t\t\t\tv.keys.Store(ks)"),
    ("last-not-recorded", O, "\tv.last = now\n\tdone := make(chan struct{})", "\tdone := make(chan struct{})"),
    ("cold-retry-removed", O, "\t\tinterval = min(interval, coldRetry)", "\t\t_ = coldRetry"),
    ("cold-retry-only-before-first-keys", O, "\tif ks := v.keys.Load(); ks == nil || now.Sub(ks.fetched) >= v.cfg.KeyMaxStale {\n\t\tinterval", "\tif ks := v.keys.Load(); ks == nil {\n\t\tinterval"),
    ("keys-wiped-by-failed-refresh", O, "\t\t\tif err == nil {\n\t\t\t\tv.keys.Store(ks)\n\t\t\t}", "\t\t\tv.keys.Store(ks)"),
    ("error-kept-after-success", O, "\t\t\tv.err, v.refreshing = err, nil", "\t\t\tif err != nil {\n\t\t\t\tv.err = err\n\t\t\t}\n\t\t\tv.refreshing = nil"),
    ("refreshing-never-cleared", O, "\t\t\tv.err, v.refreshing = err, nil", "\t\t\tv.err = err"),
    # Re-review follow-ups (and the re-review's surviving mutants).
    ("charset-unchecked", O, "\tif strings.ContainsFunc(token, func(r rune) bool {", "\tif false && strings.ContainsFunc(token, func(r rune) bool {"),
    ("failed-wait-removed", O, "\t\tif failed != nil {\n\t\t\tt := time.NewTimer(failedWait)", "\t\tif failed != nil && false {\n\t\t\tt := time.NewTimer(failedWait)"),
    ("failed-wait-drops-cause", O, "\t\t\treturn jose.JSONWebKey{}, keysUnavailable(failed)", "\t\t\treturn jose.JSONWebKey{}, keysUnavailable(nil)"),
    ("goexit-cleanup-not-deferred", O, "\t\tdefer func() {\n\t\t\tv.mu.Lock()\n\t\t\tif err == nil {\n\t\t\t\tv.keys.Store(ks)\n\t\t\t}\n\t\t\tv.err, v.refreshing = err, nil\n\t\t\tv.mu.Unlock()\n\t\t\tclose(done)\n\t\t}()\n\t\tks, err = v.safeFetch()\n", "\t\tks, err = v.safeFetch()\n\t\tfunc() {\n\t\t\tv.mu.Lock()\n\t\t\tif err == nil {\n\t\t\t\tv.keys.Store(ks)\n\t\t\t}\n\t\t\tv.err, v.refreshing = err, nil\n\t\t\tv.mu.Unlock()\n\t\t\tclose(done)\n\t\t}()\n"),
    ("aborted-error-nil", O, "\t\terr := errors.New(\"fetching JWKS: aborted\")", "\t\tvar err error"),
    ("cold-retry-always", O, "\tif ks := v.keys.Load(); ks == nil || now.Sub(ks.fetched) >= v.cfg.KeyMaxStale {\n\t\tinterval = min(interval, coldRetry)", "\tif true {\n\t\tinterval = min(interval, coldRetry)"),
    ("url-limit-off-by-one-up", O, "\tcase len(raw) > maxURL:", "\tcase len(raw) > maxURL+1:"),
    ("stale-boundary-inclusive", O, "\tif ks := v.keys.Load(); ks != nil && now.Sub(ks.fetched) < v.cfg.KeyMaxStale {", "\tif ks := v.keys.Load(); ks != nil && now.Sub(ks.fetched) <= v.cfg.KeyMaxStale {"),
    ("max-age-boundary", O, "\t\t\tif now.Sub(ks.fetched) >= v.cfg.KeyMaxAge {", "\t\t\tif now.Sub(ks.fetched) > v.cfg.KeyMaxAge {"),
    ("in-flight-not-joined", O, "\tif v.refreshing != nil {\n\t\treturn v.refreshing, v.err\n\t}\n", "\tif v.refreshing != nil {\n\t\treturn nil, v.err\n\t}\n"),
    ("panic-not-contained", O, "\t\tif recover() != nil {\n\t\t\tks, err = nil, errors.New(\"fetching JWKS: internal error\")\n\t\t}", "\t\t_ = errors.New"),
    ("cause-dropped", O, "\treturn &TokenError{Reason: ReasonKeys, err: fmt.Errorf(\"%w: %w\", ErrKeysUnavailable, cause)}", "\treturn &TokenError{Reason: ReasonKeys, err: ErrKeysUnavailable}"),
    ("url-kept-in-error", O, "\t\tif errors.As(err, &ue) {\n\t\t\terr = ue.Err", "\t\tif errors.As(err, &ue) && false {\n\t\t\terr = ue.Err"),
    ("fetch-without-timeout", O, "context.WithTimeout(context.Background(), v.cfg.FetchTimeout)", "context.WithCancel(context.Background())"),
    # --- Key set content.
    # Equivalent: a private JWK decodes to a private key type, which fits()
    # never accepts, so the IsPublic check is defence in depth only.
    ("private-keys-used", O, "|| !k.IsPublic() || !k.Valid()", "|| !k.Valid()", "equivalent"),
    ("enc-keys-used", O, "(k.Use != \"\" && k.Use != \"sig\") || ", ""),
    ("jwk-alg-filter-ignored", O, "\t\t\t(k.Algorithm != \"\" && !slices.Contains(v.cfg.Algorithms, k.Algorithm)) || ", "\t\t\t"),
    ("duplicate-kid-kept", O, "\t\tif _, seen := byID[k.KeyID]; seen || dup[k.KeyID] {", "\t\tif dup[k.KeyID] {"),
    ("redirects-followed", O, "\tclient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }\n", ""),
    ("jwks-size-unlimited", O, "\tif len(body) > maxJWKSBytes {", "\tif false {"),
    ("jwks-read-unbounded", O, "io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes+1))", "io.ReadAll(resp.Body)"),
    ("jwks-status-ignored", O, "\tif resp.StatusCode != http.StatusOK {", "\tif false {"),
    # --- Bearer extraction and challenges (review #7, #11).
    ("query-token-allowed", H, "\tif tokenInQuery(r.URL.RawQuery) {", "\tif false {"),
    ("query-case-sensitive", H, "strings.EqualFold(name, \"access_token\")", "name == \"access_token\""),
    ("id-token-query-allowed", H, " || strings.EqualFold(name, \"id_token\") {", " {"),
    ("query-semicolon-ignored", H, "return r == '&' || r == ';'", "return r == '&'"),
    ("query-not-unescaped", H, "\t\tname, err := url.QueryUnescape(name)", "\t\tname, err := name, error(nil)\n\t\t_ = url.QueryUnescape"),
    ("query-unescape-error-ignored", H, "\t\tif err != nil || strings.EqualFold(name, \"access_token\")", "\t\tif (err != nil && false) || strings.EqualFold(name, \"access_token\")"),
    ("two-headers-allowed", H, "\tif len(h) > 1 {", "\tif false {"),
    ("scheme-unchecked", H, "\tcase !strings.EqualFold(scheme, \"Bearer\"):", "\tcase scheme == \"\":"),
    ("not-bearer-as-scheme", H, "\t\treturn \"\", invalid(ReasonNotBearer)", "\t\treturn \"\", invalid(ReasonScheme)"),
    ("leading-space-allowed", H, "\tcase token == \"\" || token[0] == ' ':", "\tcase token == \"\":"),
    ("token68-unchecked", H, "\tcase !token68(token):", "\tcase false:"),
    ("middleware-continues-after-failure", H, "\t\t\t\tfail(w, r, err)\n\t\t\t\treturn\n", "\t\t\t\tfail(w, r, err)\n"),
    ("middleware-passes-unknown-errors", H, "\t\t\t\tif !errors.Is(err, ErrInvalidToken) && !errors.Is(err, ErrKeysUnavailable) {", "\t\t\t\tif false {"),
    ("challenge-not-bearer-error", H, "\tcase ReasonMissing, ReasonNotBearer:", "\tcase ReasonMissing:"),
    ("challenge-always-error", H, "\tcase ReasonMissing, ReasonNotBearer:", "\tcase \"never\":"),
    ("challenge-query-invalid-token", H, "\tcase ReasonScheme, ReasonTokenInQuery:", "\tcase ReasonScheme:"),
    ("challenge-no-invalid-request", H, "\tcase ReasonScheme, ReasonTokenInQuery:", "\tcase \"never\":"),
    ("challenge-on-503", H, "\tcase ReasonKeys:\n\t\treturn \"\"\n", ""),
    ("zero-token-error-nil", A, "\tif e.err == nil {\n\t\treturn ErrInvalidToken\n\t}\n", ""),
    # --- Deployment configuration.
    ("config-required-unchecked", C, "\t\tif v.value == \"\" {", "\t\tif false {"),
    ("config-validate-skipped", C, "\tif err := cfg.Validate(); err != nil {", "\tif err := error(nil); err != nil {"),
    ("config-require-at-ignored", C, "\t\tcfg.RequireAccessTokenType = true\n", ""),
    ("config-require-at-garbage", C, "\tdefault:\n\t\terrs = append(errs, &Error{\"AUTH_OIDC_REQUIRE_AT_JWT\"", "\tcase \"never\":\n\t\terrs = append(errs, &Error{\"AUTH_OIDC_REQUIRE_AT_JWT\""),
    # --- User lookup (review #2, #10).
    ("resolve-without-identity", T, "\t\tif _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer', $1, true), set_config('app.oidc_subject', $2, true)`, issuer, subject); err != nil {\n\t\t\treturn err\n\t\t}\n\t\terr := tx.QueryRow", "\t\terr := tx.QueryRow"),
    ("resolve-session-settings", T, "`SELECT set_config('app.oidc_issuer', $1, true), set_config('app.oidc_subject', $2, true)`, issuer, subject); err != nil {\n\t\t\treturn err\n\t\t}\n\t\terr := tx.QueryRow", "`SELECT set_config('app.oidc_issuer', $1, false), set_config('app.oidc_subject', $2, false)`, issuer, subject); err != nil {\n\t\t\treturn err\n\t\t}\n\t\terr := tx.QueryRow"),
    ("resolve-subject-only", T, "\t\t\tWHERE oidc_issuer=$1 AND oidc_subject=$2`, issuer, subject)", "\t\t\tWHERE $1<>'' AND oidc_subject=$2`, issuer, subject)"),
    ("resolve-issuer-only", T, "\t\t\tWHERE oidc_issuer=$1 AND oidc_subject=$2`, issuer, subject)", "\t\t\tWHERE oidc_issuer=$1 AND $2<>''`, issuer, subject)"),
    ("resolve-not-read-only", T, "\terr := d.db.Tx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {\n\t\tif _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer'", "\terr := d.db.Tx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {\n\t\tif _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer'"),
    # Equivalent: the assertion fires only if the lookup is not read-only,
    # which the code never does; resolve-not-read-only shows that it fires.
    ("resolve-read-only-unasserted", T, "\tif !readOnly {", "\tif false {", "equivalent"),
    ("resolve-length-unchecked", T, "\tif len(issuer) > 2048 || len(subject) > 255 {", "\tif false {"),
    ("resolve-length-off-by-one", T, "\tif len(issuer) > 2048 || len(subject) > 255 {", "\tif len(issuer) > 2048 || len(subject) > 254 {"),
    ("resolve-empty-allowed", T, "\t\tif s == \"\" || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {", "\t\tif !utf8.ValidString(s) || strings.ContainsRune(s, 0) {"),
    ("resolve-utf8-unchecked", T, "\t\tif s == \"\" || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {", "\t\tif s == \"\" || (!utf8.ValidString(s) && false) || strings.ContainsRune(s, 0) {"),
    ("resolve-nul-allowed", T, "\t\tif s == \"\" || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {", "\t\tif s == \"\" || !utf8.ValidString(s) || strings.ContainsRune(s, -1) {"),
    ("resolve-unknown-not-typed", T, "\t\tif errors.Is(err, pgx.ErrNoRows) {\n\t\t\treturn ErrUnknownUser\n\t\t}\n\t\treturn err\n\t})", "\t\treturn err\n\t})"),
]


def main() -> int:
    if CHECK:
        bad = [(n, (REPO / f).read_text().count(o)) for n, f, o, *_ in MUT if (REPO / f).read_text().count(o) != 1]
        for n, c in bad:
            print(f"{n}: pattern found {c} times")
        print(f"{len(MUT) - len(bad)}/{len(MUT)} patterns occur exactly once")
        return 1 if bad else 0
    if not os.environ.get("GEMINUS_TEST_DATABASE_URL"):
        print("set GEMINUS_TEST_DATABASE_URL to the disposable database of `make db-test`", file=sys.stderr)
        return 2
    env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1")
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new, *flags) in enumerate(MUT):
            if ONLY and name not in ONLY:
                continue
            src = (REPO / f).read_text()
            if src.count(old) != 1:
                results.append((name, f"BAD-MUTANT (pattern found {src.count(old)} times)"))
                print("\t".join(results[-1]), flush=True)
                continue
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(src.replace(old, new))
            ov = pathlib.Path(tmp) / f"m{i}.json"
            ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
            p = subprocess.run(["go", "test", "-count=1", "-timeout", "300s", "-overlay", str(ov), *PKGS],
                               cwd=REPO, capture_output=True, text=True, env=env)
            o = p.stdout + p.stderr
            if p.returncode == 0:
                status = "SURVIVED"
            elif "build failed" in o or "setup failed" in o:
                status = "BUILD-FAILED"
            else:
                status = "KILLED"
            first = next((l.strip() for l in o.splitlines() if l.strip().startswith(("--- FAIL", "panic:"))), "")
            if "equivalent" in flags:
                status = "EQUIVALENT" if status == "SURVIVED" else f"UNEXPECTED-{status}"
            results.append((name, f"{status} {first[:100]}"))
            print("\t".join(results[-1]), flush=True)
    killed = sum(1 for r in results if r[1].startswith("KILLED"))
    equivalent = sum(1 for r in results if r[1].startswith("EQUIVALENT"))
    print(f"{killed}/{len(results) - equivalent} killed, {equivalent} equivalent (documented above)")
    return 0 if killed + equivalent == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
