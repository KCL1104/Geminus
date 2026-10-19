#!/usr/bin/env python3
"""T-043 mutation check: each mutant weakens one rule of the browser session
(apps/web/lib/session: sealing, cookies, CSRF, the proxy, refresh, the OIDC
client, configuration) and must make the session or BFF tests fail.

Mutants are applied to a copy of apps/web (its node_modules linked, not
copied); the repository is never modified. A mutant that does not compile
or run is a harness error, never a kill. A mutant may replace several
spans of one file (old and new are then lists of the same length).

Usage: python3 checks/evidence/2026-10-19-T-043/ts-mutants.py [--only=NAME ...] [repo-root]
"""
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

ARGS = [a for a in sys.argv[1:] if not a.startswith("--only=")]
ONLY = {a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")}
REPO = pathlib.Path(ARGS[0] if ARGS else pathlib.Path(__file__).resolve().parents[3]).resolve()
WEB = REPO / "apps" / "web"
SEAL, SESSION, BFF, OIDC, CONFIG, SERVER = (f"lib/session/{n}.ts" for n in ("seal", "session", "bff", "oidc", "config", "server"))
TESTS = ["tests/session.test.ts", "tests/bff.test.ts", "tests/server.test.ts"]

MUTANTS = [
    # Sealing
    ("fixed-iv", SEAL, "const iv = crypto.getRandomValues(new Uint8Array(IV_BYTES));", "const iv = new Uint8Array(IV_BYTES);"),
    ("seal-with-previous-key", SEAL, ["keys.current.key, plain));", "${FORMAT}.${keys.current.id}."], ["(keys.previous ?? keys.current).key, plain));", "${FORMAT}.${(keys.previous ?? keys.current).id}."]),
    ("aad-ignores-the-cookie", SEAL, "new TextEncoder().encode(`${cookie}\\n${FORMAT}`)", "new TextEncoder().encode(FORMAT)"),
    ("previous-key-ignored", SEAL, "[keys.current, keys.previous].find(", "[keys.current].find("),
    ("size-unchecked", SEAL, "if (cookie.length + 1 + out.length > MAX_COOKIE_BYTES) throw", "if (false) throw"),
    ("non-canonical-base64", SEAL, "  if (bin.toString(\"base64url\") !== s) return null;\n", ""),
    ("three-keys", SEAL, "entries.length > 2 ||", "entries.length > 3 ||"),
    ("short-key", SEAL, "bytes.length !== 32", "bytes.length < 16"),
    ("duplicate-key-ids", SEAL, "    if (keys.some((k) => k.id === id)) throw new SessionConfigError(\"SESSION_KEYS: two keys have the same ID\");\n", ""),
    # Cookies and session lifetime
    ("idle-unchecked", SESSION, "now - s.lastSeenAt >= IDLE_MS || ", ""),
    ("absolute-unchecked", SESSION, "now - s.createdAt >= ABSOLUTE_MS || ", ""),
    ("login-never-expires", SESSION, "now - l.createdAt >= LOGIN_MS", "false"),
    ("return-to-other-host", SESSION, " || path.startsWith(\"//\")) return \"/\";", ") return \"/\";"),
    ("return-to-backslash", SESSION, "/[^\\x21-\\x7e]|\\\\/", "/[^\\x21-\\x7e]/"),
    ("return-to-non-ascii", SESSION, "/[^\\x21-\\x7e]|\\\\/", "/[\\x00-\\x1f\\x7f]|\\\\/"),
    ("login-from-the-future", SESSION, " || l.createdAt > now + SKEW_MS) return null;", ") return null;"),
    ("session-from-the-future", SESSION, " || s.lastSeenAt > now + SKEW_MS) return null;", ") return null;"),
    ("no-skew-tolerance", SESSION, " || s.lastSeenAt > now + SKEW_MS) return null;", " || s.lastSeenAt > now) return null;"),
    ("login-extra-fields", SESSION, "    Object.keys(l).length === 5 &&\n", ""),
    ("not-httponly", SESSION, "\"Path=/; HttpOnly; Secure; SameSite=Lax\"", "\"Path=/; Secure; SameSite=Lax\""),
    ("extra-session-fields", SESSION, "    Object.keys(s).length === 5 &&\n", ""),
    # CSRF
    ("csrf-without-header", BFF, " && request.headers.get(\"x-geminus-request\") === \"1\";", ";"),
    ("csrf-any-origin", BFF, "return request.headers.get(\"origin\") === appOrigin && ", "return "),
    ("csrf-cross-site-fetch", BFF, "(site === null || site === \"same-origin\")", "(site !== \"\")"),
    ("csrf-only-delete", BFF, "if (request.method !== \"GET\" && request.method !== \"HEAD\" && !sameOrigin(", "if (request.method === \"DELETE\" && !sameOrigin("),
    ("logout-without-csrf", BFF, "      if (!sameOrigin(request, cfg.appOrigin)) return apiError(\"FORBIDDEN\", \"cross-site request refused\", now);\n      const session = await readSession(", "      const session = await readSession("),
    # The proxy
    ("proxy-forwards-cookies", BFF, "const requestHeaders = [\"accept\",", "const requestHeaders = [\"cookie\", \"accept\","),
    ("proxy-forwards-authorization", BFF, "const requestHeaders = [\"accept\",", "const requestHeaders = [\"authorization\", \"accept\","),
    ("proxy-returns-set-cookie", BFF, "const responseHeaders = [\"content-type\",", "const responseHeaders = [\"set-cookie\", \"content-type\","),
    ("proxy-returns-location", BFF, "const responseHeaders = [\"content-type\",", "const responseHeaders = [\"location\", \"content-type\","),
    ("proxy-any-path", BFF, "path.some((s) => !SEGMENT.test(s))", "path.some((s) => s === \"\")"),
    ("unauthenticated-keeps-the-cookie", BFF, "return apiError(\"UNAUTHENTICATED\", \"not signed in\", now, [[\"Set-Cookie\", clearCookie(SESSION_COOKIE)]]);", "return apiError(\"UNAUTHENTICATED\", \"not signed in\", now);"),
    ("touch-every-request", BFF, "let write = now - session.lastSeenAt >= TOUCH_MS;", "let write = true;"),
    ("any-method", BFF, "  if (!methods.has(request.method)) return apiError(\"INVALID_REQUEST\", \"method not allowed\", now);\n", ""),
    ("upload-buffered", BFF, "body: hasBody ? request.body : undefined,", "body: hasBody ? await request.arrayBuffer() : undefined,"),
    ("download-buffered", BFF, "return new Response(upstream.body, { status: upstream.status, headers: out });", "return new Response(await upstream.arrayBuffer(), { status: upstream.status, headers: out });"),
    ("no-cancellation", BFF, "      signal: request.signal,\n", ""),
    ("no-content-type-back", BFF, "const responseHeaders = [\"content-type\", \"etag\",", "const responseHeaders = [\"etag\","),
    ("no-last-event-id", BFF, "\"x-organization-id\", \"last-event-id\"]", "\"x-organization-id\"]"),
    ("follow-api-redirects", BFF, "      redirect: \"manual\",\n", "      redirect: \"follow\",\n"),
    ("no-nosniff", BFF, "  out.set(\"x-content-type-options\", \"nosniff\");\n", ""),
    ("cookie-response-cacheable", BFF, "    out.set(\"cache-control\", \"no-store\");\n", ""),
    ("api-down-is-500", BFF, "return apiError(\"SERVICE_UNAVAILABLE\", \"the API is unavailable\", now,", "throw new Error(\"the API is unavailable\"); (now,"),
    # Refresh
    ("never-refresh", BFF, "if (session.accessExpiresAt - now < REFRESH_MARGIN_MS) {", "if (false) {"),
    ("refresh-not-shared", BFF, "      this.entries.set(key, entry);\n", ""),
    ("refresh-shared-forever", BFF, "    for (const [k, v] of this.entries) if (now - v.at >= REFRESH_REUSE_MS) this.entries.delete(k);\n", ""),
    ("refresh-failure-remembered", BFF, "        () => this.entries.delete(key),\n", "        () => undefined,\n"),
    ("refresh-key-constant", BFF, "    const key = await digest(refreshToken);\n    const known", "    const key = \"same-key\";\n    const known"),
    ("refresh-cache-unbounded", BFF, "if (this.entries.size < this.max) {", "if (true) {"),
    ("logout-keeps-the-shared-refresh", BFF, "        await refreshes.forget(session.refreshToken);\n", ""),
    ("forget-keeps-what-it-issued", BFF, "    for (const [k, v] of this.entries) if (v.issued === refreshToken) this.entries.delete(k);\n", ""),
    ("forget-keeps-what-it-spent", BFF, "    this.entries.delete(await digest(refreshToken));\n", ""),
    # A provider outage is not the end of the session (review M-2)
    ("outage-ends-the-session", BFF, "if (e instanceof OidcFailure && !e.refused) {", "if (false) {"),
    ("outage-clears-the-cookie", BFF, "\"the identity provider is unavailable\", now, [[\"Retry-After\", \"5\"]]);", "\"the identity provider is unavailable\", now, [[\"Retry-After\", \"5\"], [\"Set-Cookie\", clearCookie(SESSION_COOKIE)]]);"),
    # Which refresh failures end the session (review round 2 L-1, L-2)
    ("network-refuses", OIDC, "  if (e instanceof TypeError) return { refused: false };\n", ""),
    ("timeout-refuses", OIDC, "    if (e.code === \"OAUTH_TIMEOUT\" || e.code === \"OAUTH_ABORT\") return { refused: false };\n", ""),
    ("invalid-client-refuses", OIDC, "\"too_many_requests\", \"invalid_client\"]", "\"too_many_requests\"]"),
    ("only-429-is-temporary", OIDC, "refused: !(UNAVAILABLE.has(e.error) || e.status === 429 || e.status >= 500)", "refused: !(e.status === 429)"),
    ("challenge-always-temporary", OIDC, "refused: error !== undefined && !UNAVAILABLE.has(error) && e.status !== 429 && e.status < 500", "refused: false"),
    ("challenge-without-code-refuses", OIDC, "refused: error !== undefined && !UNAVAILABLE.has(error)", "refused: !UNAVAILABLE.has(error as string)"),
    ("unexpected-2xx-temporary", OIDC, "return { refused: e.cause.status >= 200 && e.cause.status < 300, status: e.cause.status };", "return { refused: false, status: e.cause.status };"),
    ("unusable-answer-temporary", OIDC, "  return { refused: true };\n}", "  return { refused: false };\n}"),
    ("refresh-unfit-tokens-temporary", OIDC, "    throw new OidcFailure(\"refresh\", e instanceof Error ? e.message : \"unknown error\");", "    throw new OidcFailure(\"refresh\", e instanceof Error ? e.message : \"unknown error\", false);"),
    ("refresh-drops-unrotated-token", OIDC, "const refreshToken = tokens.refresh_token ?? keepRefresh;", "const refreshToken = tokens.refresh_token ?? null;"),
    ("unkept-login-token-left-live", OIDC, "    if (typeof tokens.refresh_token === \"string\" && tokens.refresh_token !== \"\") await discard(o, tokens.refresh_token);\n", ""),
    ("unkept-callback-token-left-live", BFF, "        if (session.refreshToken !== null) await discard(oidc, session.refreshToken);\n", ""),
    ("unkept-refresh-token-left-live", BFF, "      if (session.refreshToken !== null) await discard(deps.oidc, session.refreshToken);\n", ""),
    ("no-retry-after", BFF, "\"the identity provider is unavailable\", now, [[\"Retry-After\", \"5\"]]);", "\"the identity provider is unavailable\", now);"),
    ("codes-not-logged", BFF, "      if (e instanceof OidcFailure && e.answer.error !== undefined) fields.error = e.answer.error;\n", ""),
    # The API failing after a refresh (review round 2 M-1)
    ("api-down-drops-the-refreshed-cookie", BFF, "setCookie === null ? [] : [[\"Set-Cookie\", setCookie]]", "[]"),
    ("cookie-only-on-2xx", BFF, "  if (setCookie !== null) {\n    out.append", "  if (setCookie !== null && upstream.ok) {\n    out.append"),
    ("apierror-without-nosniff", BFF, "[\"X-Content-Type-Options\", \"nosniff\"], [\"X-Request-ID\", requestId]", "[\"X-Request-ID\", requestId]"),
    ("logout-without-nosniff", BFF, "[\"X-Content-Type-Options\", \"nosniff\"], [\"Set-Cookie\", clearCookie(SESSION_COOKIE)]", "[\"Set-Cookie\", clearCookie(SESSION_COOKIE)]"),
    ("skew-a-day", SESSION, "export const SKEW_MS = 60_000;", "export const SKEW_MS = 86_400_000;"),
    ("reuse-window-an-hour", BFF, "export const REFRESH_REUSE_MS = 30_000;", "export const REFRESH_REUSE_MS = 3_600_000;"),
    ("every-refresh-failure-temporary", OIDC, "    const { refused, ...answer } = refusal(e);", "    const { refused: _, ...answer } = refusal(e);\n    const refused = false;"),
    ("every-provider-error-refuses", OIDC, "    return { refused: !(UNAVAILABLE.has(e.error) || e.status === 429 || e.status >= 500), error: e.error, status: e.status };", "    return { refused: true, error: e.error, status: e.status };"),
    # Tokens the session cannot hold (review L-2)
    ("tokens-unchecked", OIDC, "  if (!sessionToken(tokens.access_token) || (refreshToken !== null && !sessionToken(refreshToken))) {", "  if (false) {"),
    ("too-large-at-the-callback-is-500", BFF, "        if (!(e instanceof SealedTooLarge)) throw e;\n        deps.log(\"login.failed\"", "        throw e;\n        deps.log(\"login.failed\""),
    ("too-large-at-refresh-is-500", BFF, "      if (!(e instanceof SealedTooLarge)) throw e;\n      deps.log(\"session.refresh_failed\"", "      throw e;\n      deps.log(\"session.refresh_failed\""),
    ("logout-without-revocation", BFF, "          await revoke(oidc, session.refreshToken);\n", ""),
    ("callback-uses-the-request-host", BFF, "finishLogin(oidc, new URL(`${oidc.redirectUri}${query}`), login, now)", "finishLogin(oidc, new URL(request.url), login, now)"),
    ("provider-error-ignored", BFF, "      if (new URLSearchParams(query).has(\"error\")) {\n        deps.log(\"login.refused_by_provider\");\n        return failed(\"refused\");\n      }\n", ""),
    # The OIDC client
    ("id-token-signature-unchecked", OIDC, "  client.enableNonRepudiationChecks(config);\n", ""),
    ("no-audience", OIDC, "    scope: o.scopes,\n    audience: o.audience,\n", "    scope: o.scopes,\n"),
    ("no-subject-check", OIDC, "if (claims === undefined || typeof claims.sub !== \"string\" || claims.sub === \"\") throw", "if (claims === undefined) throw"),
    ("any-expiry", OIDC, "if (s === undefined || !Number.isSafeInteger(s) || s <= 0) throw", "if (false) throw"),
    # Configuration
    ("unconfigured-is-500", SERVER, "if (h === null) return apiError(\"SERVICE_UNAVAILABLE\", \"sign-in is not configured\", Date.now());", "if (h === null) throw new Error(\"not configured\");"),
    ("built-per-request", SERVER, "  built ??= build();\n", "  built = build();\n"),
    ("http-anywhere", CONFIG, "!(url.protocol === \"http:\" && loopback.has(url.hostname))", "!(url.protocol === \"http:\")"),
    ("mixed-http", CONFIG, "if (!loopbackHttp && all.some((u) => u.protocol === \"http:\")) {", "if (false) {"),
    ("scopes-without-openid", CONFIG, "!scopes.includes(\"openid\") || ", ""),
    ("origin-with-a-path", CONFIG, "  if (kind === \"origin\" && url.pathname !== \"/\") return { ok: false, why: \"must be an origin, without a path\" };\n", ""),
]
EQUIVALENT = {
    "proxy-forwards-authorization": "the proxy sets its own Authorization after copying the listed headers, so a browser's never reaches the API (the test sends one and checks the session's token arrives)",
}


def run(copy: pathlib.Path) -> str:
    r = subprocess.run(["npx", "vitest", "run", *TESTS], cwd=copy, capture_output=True, text=True, timeout=600)
    out = r.stdout + r.stderr
    if r.returncode == 0:
        return "SURVIVED"
    failed = [l.strip() for l in out.splitlines() if l.strip().startswith("×")]
    if failed:
        return "KILLED\t" + failed[0]
    return "ERROR\t" + (out.strip().splitlines() or ["?"])[-1]


def main() -> int:
    sources = {f: (WEB / f).read_text() for f in (SEAL, SESSION, BFF, OIDC, CONFIG, SERVER)}
    spans = lambda old, new: list(zip(old, new)) if isinstance(old, list) else [(old, new)]
    bad = [n for n, f, old, new in MUTANTS if any(sources[f].count(o) != 1 for o, _ in spans(old, new))]
    if bad:
        print("patterns not found exactly once:", bad)
        return 2
    wrong = total = 0
    with tempfile.TemporaryDirectory() as tmp:
        copy = pathlib.Path(tmp) / "web"
        shutil.copytree(WEB, copy, ignore=shutil.ignore_patterns("node_modules", ".next", "test-results"), symlinks=True)
        os.symlink(WEB / "node_modules", copy / "node_modules")
        if run(copy) != "SURVIVED":
            print("baseline fails")
            return 2
        for name, f, old, new in MUTANTS:
            if ONLY and name not in ONLY:
                continue
            total += 1
            text = sources[f]
            for o, n in spans(old, new):
                text = text.replace(o, n)
            (copy / f).write_text(text)
            v = run(copy)
            (copy / f).write_text(sources[f])
            ok = not v.startswith("ERROR") and (v == "SURVIVED") == (name in EQUIVALENT)
            wrong += not ok
            print(f"{name}\t{v}" + ("" if ok else "\tUNEXPECTED"), flush=True)
    print(f"total={total} unexpected={wrong}")
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
