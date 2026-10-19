#!/usr/bin/env python3
"""T-043 mutation check: each mutant weakens one rule of the browser session
(apps/web/lib/session: sealing, cookies, CSRF, the proxy, refresh, the OIDC
client, configuration) and must make the session or BFF tests fail.

Mutants are applied to a copy of apps/web (its node_modules linked, not
copied); the repository is never modified. A mutant that does not compile
or run is a harness error, never a kill.

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
SEAL, SESSION, BFF, OIDC, CONFIG = (f"lib/session/{n}.ts" for n in ("seal", "session", "bff", "oidc", "config"))
TESTS = ["tests/session.test.ts", "tests/bff.test.ts"]

MUTANTS = [
    # Sealing
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
    ("future-session", SESSION, " || s.createdAt > now) return null;", ") return null;"),
    ("login-never-expires", SESSION, "now - l.createdAt >= LOGIN_MS", "false"),
    ("return-to-other-host", SESSION, " || path.startsWith(\"//\")) return \"/\";", ") return \"/\";"),
    ("return-to-backslash", SESSION, "/[\\\\\\x00-\\x1f\\x7f]/", "/[\\x00-\\x1f\\x7f]/"),
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
    ("api-down-is-500", BFF, "return apiError(\"SERVICE_UNAVAILABLE\", \"the API is unavailable\", now);", "throw new Error(\"the API is unavailable\");"),
    # Refresh
    ("never-refresh", BFF, "if (session.accessExpiresAt - now < REFRESH_MARGIN_MS) {", "if (false) {"),
    ("refresh-not-shared", BFF, "    refreshes.set(key, { at: now, result });\n", ""),
    ("refresh-shared-forever", BFF, "  for (const [k, v] of refreshes) if (now - v.at >= REFRESH_REUSE_MS) refreshes.delete(k);\n", ""),
    ("refresh-failure-remembered", BFF, "result.catch(() => refreshes.delete(key));", "result.catch(() => undefined);"),
    ("logout-without-revocation", BFF, "          await revoke(oidc, session.refreshToken);\n", ""),
    ("callback-uses-the-request-host", BFF, "finishLogin(oidc, new URL(`${oidc.redirectUri}${query}`), login, now)", "finishLogin(oidc, new URL(request.url), login, now)"),
    ("provider-error-ignored", BFF, "      if (new URLSearchParams(query).has(\"error\")) {\n        deps.log(\"login.refused_by_provider\");\n        return failed(\"refused\");\n      }\n", ""),
    # The OIDC client
    ("id-token-signature-unchecked", OIDC, "  client.enableNonRepudiationChecks(config);\n", ""),
    ("no-audience", OIDC, "    scope: o.scopes,\n    audience: o.audience,\n", "    scope: o.scopes,\n"),
    ("no-subject-check", OIDC, "if (claims === undefined || typeof claims.sub !== \"string\" || claims.sub === \"\") throw", "if (claims === undefined) throw"),
    ("any-expiry", OIDC, "if (s === undefined || !Number.isSafeInteger(s) || s <= 0) throw", "if (false) throw"),
    # Configuration
    ("http-anywhere", CONFIG, "!(url.protocol === \"http:\" && loopback.has(url.hostname))", "!(url.protocol === \"http:\")"),
    ("mixed-http", CONFIG, "if (!loopbackHttp && all.some((u) => u.protocol === \"http:\")) {", "if (false) {"),
    ("scopes-without-openid", CONFIG, "!scopes.includes(\"openid\") || ", ""),
    ("origin-with-a-path", CONFIG, "  if (kind === \"origin\" && url.pathname !== \"/\") return { ok: false, why: \"must be an origin, without a path\" };\n", ""),
]
EQUIVALENT = {
    "proxy-forwards-authorization": "the proxy sets its own Authorization after copying the listed headers, so a browser's never reaches the API (the test sends one and checks the session's token arrives)",
    "no-subject-check": "oauth4webapi already refuses an ID token without sub (a required claim), so the BFF's own check cannot be reached by a token the client accepted; kept as the documented contract",
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
    sources = {f: (WEB / f).read_text() for f in (SEAL, SESSION, BFF, OIDC, CONFIG)}
    bad = [n for n, f, old, _ in MUTANTS if sources[f].count(old) != 1]
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
            (copy / f).write_text(sources[f].replace(old, new))
            v = run(copy)
            (copy / f).write_text(sources[f])
            ok = not v.startswith("ERROR") and (v == "SURVIVED") == (name in EQUIVALENT)
            wrong += not ok
            print(f"{name}\t{v}" + ("" if ok else "\tUNEXPECTED"), flush=True)
    print(f"total={total} unexpected={wrong}")
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
