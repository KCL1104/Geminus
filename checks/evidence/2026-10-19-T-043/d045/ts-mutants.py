#!/usr/bin/env python3
"""T-043 / D-045 TypeScript mutation check: each mutant weakens one rule of
the client's getMe (apps/web/lib/api/client.ts) and must make the API
client or BFF tests fail.

Mutants are applied to a copy of apps/web (its node_modules linked, not
copied) under a directory that links the schemas, fixtures and planning
files the tests read; the repository is never modified. A mutant that does
not run is a harness error, never a kill.

Usage: python3 checks/evidence/2026-10-19-T-043/d045/ts-mutants.py [--only=NAME ...] [repo-root]
"""
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

ARGS = [a for a in sys.argv[1:] if not a.startswith("--only=")]
ONLY = {a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")}
REPO = pathlib.Path(ARGS[0] if ARGS else pathlib.Path(__file__).resolve().parents[4]).resolve()
WEB = REPO / "apps" / "web"
CLIENT = "lib/api/client.ts"
TESTS = ["tests/api.test.ts", "tests/bff.test.ts"]

MUTANTS = [
    ("me-sends-the-selector", 'path: "/me", organization: false, signal', 'path: "/me", signal'),
    ("me-needs-a-session", "const session = scoped ? cfg.session() : null;\n    if (scoped && session === null)",
     "const session = cfg.session();\n    if (session === null)"),
    ("selector-never-sent", '    if (session !== null) headers["X-Organization-ID"] = session.organizationId;\n', ""),
    ("me-wrong-path", 'path: "/me", organization: false', 'path: "/users/me", organization: false'),
    ("scoped-without-a-session", "if (scoped && session === null)", "if (false && session === null)"),
    ("me-without-the-csrf-header", '      "X-Geminus-Request": "1",\n', ""),
]
EQUIVALENT: dict[str, str] = {}


def run(web: pathlib.Path) -> str:
    r = subprocess.run(["npx", "vitest", "run", *TESTS], cwd=web, capture_output=True, text=True, timeout=600)
    out = r.stdout + r.stderr
    if r.returncode == 0:
        return "SURVIVED"
    failed = [l.strip() for l in out.splitlines() if l.strip().startswith("×")]
    if failed:
        return "KILLED\t" + failed[0]
    return "ERROR\t" + (out.strip().splitlines() or ["?"])[-1]


def main() -> int:
    src = (WEB / CLIENT).read_text()
    bad = [n for n, old, _ in MUTANTS if src.count(old) != 1]
    if bad:
        print("patterns not found exactly once:", bad)
        return 2
    wrong = total = 0
    with tempfile.TemporaryDirectory() as tmp:
        root = pathlib.Path(tmp)
        for d in ("schemas", "testdata", "planning"):
            os.symlink(REPO / d, root / d)
        web = root / "apps" / "web"
        shutil.copytree(WEB, web, ignore=shutil.ignore_patterns("node_modules", ".next", "test-results"), symlinks=True)
        os.symlink(WEB / "node_modules", web / "node_modules")
        if run(web) != "SURVIVED":
            print("baseline fails")
            return 2
        print("baseline\t" + " ".join(TESTS) + " pass", flush=True)
        for name, old, new in MUTANTS:
            if ONLY and name not in ONLY:
                continue
            total += 1
            (web / CLIENT).write_text(src.replace(old, new))
            v = run(web)
            (web / CLIENT).write_text(src)
            ok = not v.startswith("ERROR") and (v == "SURVIVED") == (name in EQUIVALENT)
            wrong += not ok
            print(f"{name}\t{v}" + ("" if ok else "\tUNEXPECTED"), flush=True)
    print(f"total={total} unexpected={wrong}")
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
