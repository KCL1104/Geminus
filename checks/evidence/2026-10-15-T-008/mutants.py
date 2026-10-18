#!/usr/bin/env python3
"""T-008 mutation run: each mutant removes or weakens one check of the token
verifier, the bearer extraction, the configuration or the user lookup, and
must make a test fail. Applied with `go test -overlay` (the working tree is
never modified); KILLED means a test failed.

Usage: GEMINUS_TEST_DATABASE_URL=... python3 checks/evidence/2026-10-15-T-008/mutants.py [repo-root]
The tenant mutants need the disposable database of `make db-test`; without
it the run stops (GEMINUS_REQUIRE_DB_TESTS=1 turns a skip into a failure,
which would count as a kill, so the URL is required up front).
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[3])
O = "internal/auth/oidc.go"
H = "internal/auth/http.go"
A = "internal/auth/auth.go"
T = "internal/tenant/transaction.go"
C = "internal/config/auth.go"
PKGS = ["./internal/auth/...", "./internal/config/...", "./cmd/api/...", "./internal/tenant/..."]

MUT = [
    ("token-size-unchecked", O, "\tif len(token) > MaxTokenBytes {\n\t\treturn Identity{}, invalid(ReasonTooLarge)\n\t}\n", ""),
    ("alg-allowlist-ignored", O, "\tif json.Unmarshal(h[\"alg\"], &alg) != nil || !slices.Contains(v.cfg.Algorithms, alg) {", "\tif json.Unmarshal(h[\"alg\"], &alg) != nil {"),
    ("kid-optional", O, "\tif json.Unmarshal(h[\"kid\"], &kid) != nil || kid == \"\" || len(kid) > 256 {", "\tif json.Unmarshal(h[\"kid\"], &kid) != nil || len(kid) > 256 {"),
    ("forbidden-header-ignored", O, "\t\tif _, ok := h[p]; ok {\n\t\t\treturn \"\", \"\", invalid(ReasonHeader)", "\t\tif _, ok := h[p]; ok && false {\n\t\t\treturn \"\", \"\", invalid(ReasonHeader)"),
    ("crit-allowed", O, "\"x5t#S256\", \"crit\", \"b64\"", "\"x5t#S256\", \"b64\""),
    ("jku-allowed", O, "var forbiddenHeader = []string{\"jku\", ", "var forbiddenHeader = []string{"),
    ("typ-ignored", O, "\tif t, ok := h[\"typ\"]; ok {", "\tif t, ok := h[\"typ\"]; ok && false {"),
    ("key-alg-ignored", O, "\tif (key.Algorithm != \"\" && key.Algorithm != alg) || !fits(alg, key.Key) {", "\tif !fits(alg, key.Key) {"),
    ("key-type-ignored", O, "\tif (key.Algorithm != \"\" && key.Algorithm != alg) || !fits(alg, key.Key) {", "\tif key.Algorithm != \"\" && key.Algorithm != alg {"),
    ("rsa-size-ignored", O, "&& k.N.BitLen() >= minRSABits", ""),
    ("curve-ignored", O, "\t\treturn map[string]elliptic.Curve{\"ES256\": elliptic.P256(), \"ES384\": elliptic.P384(), \"ES512\": elliptic.P521()}[alg] == k.Curve", "\t\t_ = elliptic.P256\n\t\treturn strings.HasPrefix(alg, \"ES\")"),
    ("signature-ignored", O, "\tpayload, err := jws.Verify(key.Key)\n\tif err != nil {", "\tpayload, err := jws.UnsafePayloadWithoutVerification(), error(nil)\n\tif err != nil {"),
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
    ("subject-control-allowed", O, "\tif c.Sub == nil || *c.Sub == \"\" || len(*c.Sub) > maxSubject || !printable(*c.Sub) {", "\tif c.Sub == nil || *c.Sub == \"\" || len(*c.Sub) > maxSubject {"),
    ("skew-not-defaulted", O, "\tif cfg.ClockSkew == 0 {\n\t\tcfg.ClockSkew = defaultSkew\n\t}\n", ""),
    ("skew-unbounded", O, "\tif c.ClockSkew < 0 || c.ClockSkew > maxClockSkew {", "\tif c.ClockSkew < 0 {"),
    ("hmac-configurable", O, "\t\tif !slices.Contains(asymmetric, a) {", "\t\tif !slices.Contains(asymmetric, a) && a != \"HS256\" {"),
    ("http-jwks-allowed", O, "\tcase u.Scheme != \"https\" || u.Host == \"\" || u.Opaque != \"\":", "\tcase u.Host == \"\" || u.Opaque != \"\":"),
    ("refresh-unthrottled", O, "\tif v.last.IsZero() || now.Sub(v.last) >= v.cfg.MinRefreshInterval {", "\tif true {"),
    ("max-age-ignored", O, "\tif ks != nil && now.Sub(ks.fetched) < v.cfg.KeyMaxAge {\n\t\tif k, ok := ks.byID[kid]; ok {\n\t\t\treturn k, nil // another", "\tif ks != nil {\n\t\tif k, ok := ks.byID[kid]; ok {\n\t\t\treturn k, nil // another"),
    ("max-age-fast-path-ignored", O, "\tif ks := v.keys.Load(); ks != nil && now.Sub(ks.fetched) < v.cfg.KeyMaxAge {", "\tif ks := v.keys.Load(); ks != nil {"),
    ("stale-limit-ignored", O, "\tif ks == nil || now.Sub(ks.fetched) >= v.cfg.KeyMaxStale {", "\tif ks == nil {"),
    ("outage-unknown-kid-is-invalid", O, "\tif v.err != nil {\n\t\t// The key may", "\tif false {\n\t\t// The key may"),
    # Equivalent: a private JWK decodes to a private key type, which fits()
    # never accepts, so the IsPublic check is defence in depth only.
    ("private-keys-used", O, "|| !k.IsPublic() || !k.Valid()", "|| !k.Valid()", "equivalent"),
    ("enc-keys-used", O, "(k.Use != \"\" && k.Use != \"sig\") || ", ""),
    ("jwk-alg-filter-ignored", O, "\t\t\t(k.Algorithm != \"\" && !slices.Contains(v.cfg.Algorithms, k.Algorithm)) || ", "\t\t\t"),
    ("duplicate-kid-kept", O, "\t\tif _, seen := byID[k.KeyID]; seen || dup[k.KeyID] {", "\t\tif dup[k.KeyID] {"),
    ("redirects-followed", O, "\tclient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }\n", ""),
    ("jwks-size-unlimited", O, "\tif len(body) > maxJWKSBytes {", "\tif false {"),
    ("jwks-status-ignored", O, "\tif resp.StatusCode != http.StatusOK {", "\tif false {"),
    ("fetch-cancelled-with-request", O, "context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)", "context.WithTimeout(ctx, fetchTimeout)"),
    ("query-token-allowed", H, "\tif q.Has(\"access_token\") || q.Has(\"id_token\") {", "\tif false && q.Has(\"access_token\") {"),
    ("id-token-query-allowed", H, "\tif q.Has(\"access_token\") || q.Has(\"id_token\") {", "\tif q.Has(\"access_token\") {"),
    ("two-headers-allowed", H, "\tif len(h) > 1 {", "\tif false {"),
    ("scheme-unchecked", H, "!strings.EqualFold(scheme, \"Bearer\") || ", "scheme == \"\" || "),
    ("token68-unchecked", H, " || !token68(token) {", " {"),
    ("middleware-continues-after-failure", H, "\t\t\t\tfail(w, r, err)\n\t\t\t\treturn\n", "\t\t\t\tfail(w, r, err)\n"),
    ("middleware-passes-unknown-errors", H, "\t\t\t\tif !errors.Is(err, ErrInvalidToken) && !errors.Is(err, ErrKeysUnavailable) {", "\t\t\t\tif false {"),
    ("challenge-always-error", H, "\tif Reason(err) == ReasonMissing {", "\tif false {"),
    ("zero-token-error-nil", A, "\tif e.err == nil {\n\t\treturn ErrInvalidToken\n\t}\n", ""),
    ("config-required-unchecked", C, "\t\tif v.value == \"\" {", "\t\tif false {"),
    ("config-validate-skipped", C, "\tif err := cfg.Validate(); err != nil {", "\tif err := error(nil); err != nil {"),
    ("resolve-without-identity", T, "\t\tif _, err := tx.Exec(ctx, `SELECT set_config('app.oidc_issuer', $1, true), set_config('app.oidc_subject', $2, true)`, issuer, subject); err != nil {\n\t\t\treturn err\n\t\t}\n\t\terr := tx.QueryRow", "\t\terr := tx.QueryRow"),
    # Equivalent under RLS: identity_self already confines the query to the
    # issuer and subject set in the transaction; the WHERE clause is the
    # application filter AGENTS.md requires next to RLS.
    ("resolve-subject-only", T, "`SELECT id FROM twin.users WHERE oidc_issuer=$1 AND oidc_subject=$2`, issuer, subject)", "`SELECT id FROM twin.users WHERE $1<>'' AND oidc_subject=$2`, issuer, subject)", "equivalent"),
    ("resolve-length-unchecked", T, "\tif issuer == \"\" || subject == \"\" || len(issuer) > 2048 || len(subject) > 255 {", "\tif issuer == \"\" || subject == \"\" {"),
    ("resolve-unknown-not-typed", T, "\t\tif errors.Is(err, pgx.ErrNoRows) {\n\t\t\treturn ErrUnknownUser\n\t\t}\n\t\treturn err\n\t})", "\t\treturn err\n\t})"),
]


def main() -> int:
    if not os.environ.get("GEMINUS_TEST_DATABASE_URL"):
        print("set GEMINUS_TEST_DATABASE_URL to the disposable database of `make db-test`", file=sys.stderr)
        return 2
    env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1")
    results = []
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new, *flags) in enumerate(MUT):
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
