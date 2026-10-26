#!/usr/bin/env python3
"""Applies one mutant at a time to apps/web, runs the given vitest files, restores the file.
A mutant is killed when vitest exits non-zero. Never leaves a file changed."""
import subprocess
import sys
from pathlib import Path

WEB = Path(sys.argv[1])
TESTS = ["tests/bootstrap.test.ts", "tests/auth-bootstrap.test.tsx", "tests/layout-session.test.tsx", "tests/ui.test.tsx", "tests/simplicity.test.tsx", "tests/bff.test.ts", "tests/i18n-dom.test.tsx"]

M = [
    ("M1 one organization is not chosen by itself", "lib/auth/bootstrap.ts", "const only = organizations.length === 1 ? organizations[0] : undefined;", "const only = undefined;"),
    ("M2 the remembered organization is used without /me listing it", "lib/auth/bootstrap.ts", "organizations.find((o) => o.id === chosen)", "(chosen ? { id: chosen, name: chosen, role: \"owner\" as const } : undefined)"),
    ("M3 a 403 from /me reads as signed out", "lib/auth/bootstrap.ts", "if (me.problem.kind === \"forbidden\") return { state: \"no_access\", problem: me.problem };", "if (me.problem.kind === \"forbidden\") return { state: \"signed_out\" };"),
    ("M4 no memberships reads as ready-less choose", "lib/auth/bootstrap.ts", "if (organizations.length === 0) return { state: \"no_access\", problem: null };", ""),
    ("M5 an unknown role reads as viewer", "lib/auth/bootstrap.ts", "roles.has(m.role) ? (m.role as Role) : null", "roles.has(m.role) ? (m.role as Role) : \"editor\""),
    ("M6 a broken /me answer is accepted", "lib/auth/bootstrap.ts", "if (organizations.some((o) => o.id === m.organization_id)) return null;", ""),
    ("M7 the return path is not checked", "lib/return-to.ts", "encodeURIComponent(safeReturnTo(here))", "encodeURIComponent(here ?? \"/\")"),
    ("M8 any value is an organization", "lib/auth/organization.ts", "typeof value === \"string\" && isID(\"org\", value) ? value : null", "typeof value === \"string\" && value !== \"\" ? value : null"),
    ("M9 the organization cookie lasts a year", "lib/auth/organization.ts", "`${organizationCookie}=${id}${tail}`", "`${organizationCookie}=${id}; Max-Age=31536000${tail}`"),
    ("M10 sign-out without the CSRF header", "lib/auth/sign-out.ts", "\"X-Geminus-Request\": \"1\"", "\"X-Requested-With\": \"1\""),
    ("M11 sign-out goes wherever the answer says", "lib/auth/sign-out.ts", "return url.protocol === \"https:\" || url.protocol === \"http:\" ? url.toString() : \"/\";", "return url.toString();"),
    ("M12 sign-out forgets nothing", "lib/auth/sign-out.ts", "deps.forget();", ""),
    ("M13 a refused sign-out is a success", "lib/auth/sign-out.ts", "if (!res.ok) return problemFromResponse(res.status, res.headers, body);", ""),
    ("M14 /me is asked without a session", "app/_components/auth-bootstrap.tsx", "(signedIn ? api.getMe({ signal }) : Promise.resolve(notAsked))", "api.getMe({ signal })"),
    ("M15 signed out waits for /me", "app/_components/auth-bootstrap.tsx", "(signedIn ? bootstrapFrom(me, chosen) : signedOut)", "bootstrapFrom(me, chosen)"),
    ("M16 a choice is not remembered", "app/_components/auth-bootstrap.tsx", "document.cookie = organizationCookieValue(org, secure());", ""),
    ("M17 the signed-out page has no sign-in", "app/_components/states.tsx", "          <SignInLink />\n        </>", "        </>"),
    ("M18 a 401 load has no sign-in", "app/_components/states.tsx", "{remote.problem.kind === \"unauthenticated\" && <SignInLink />}", ""),
    ("M19 the choice shows tenant data", "app/_components/states.tsx", "return <OrganizationChoice organizations={b.organizations} truncated={b.truncated} onChoose={(id) => account?.choose(id)} />;", "return <>{children}</>;"),
    ("M20 sign-out shown while signed out", "app/_components/account.tsx", "if (account === undefined || state === \"signed_out\" || state === \"loading\") return null;", "if (account === undefined) return null;"),
    ("M21 the switcher shown with one organization", "app/_components/account.tsx", "organizations.length < 2) return null;", "organizations.length < 1) return null;"),
    ("M22 a failed sign-out says nothing", "app/_components/account.tsx", "{failed && <span role=\"alert\">{t(\"auth.signOutFailed\")}</span>}", ""),
    ("M23 the layout trusts any session cookie", "app/layout.tsx", "sealed !== undefined && (await hasSession(sealed))", "sealed !== undefined"),
    ("M24 the layout hands over the raw organization cookie", "app/layout.tsx", "parseOrganization(jar.get(organizationCookie)?.value)", "(jar.get(organizationCookie)?.value ?? null) as never"),
    ("M25 hasSession says yes to anything", "lib/session/bff.ts", "return (await readSession(keys, value, deps.now())) !== null;", "return value !== undefined;"),
    ("M26 shared names are not told apart", "app/_components/account.tsx", "shared.has(o.name) ? t(\"organization.named\", { name: o.name, id: o.id }) : o.name", "o.name"),
    ("M27 no-access shows the role guidance", "app/_components/states.tsx", "guidance=\"access.none\"", ""),
]

results = []
for name, rel, old, new in M:
    path = WEB / rel
    text = path.read_text()
    if text.count(old) != 1:
        results.append((name, "HARNESS ERROR: pattern not found once"))
        continue
    try:
        path.write_text(text.replace(old, new, 1))
        r = subprocess.run(["npx", "vitest", "run", *TESTS], cwd=WEB, capture_output=True, text=True, timeout=600)
        results.append((name, "killed" if r.returncode != 0 else "SURVIVED"))
    finally:
        path.write_text(text)
    print(f"{name}: {results[-1][1]}", flush=True)

print()
print(f"{sum(1 for _, s in results if s == 'killed')}/{len(results)} killed")
