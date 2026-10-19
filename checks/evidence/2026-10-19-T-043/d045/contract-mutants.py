#!/usr/bin/env python3
"""T-043 / D-045 contract mutation check: each mutant weakens one part of
the GET /v1/me contract (openapi/openapi.yaml, planning/api-operations.json)
and must make scripts/check_contracts.py (make spec-contracts) fail.

Mutants are applied to a copy of the files the check reads (scripts,
schemas, openapi, testdata, planning); the repository is never modified.

Usage: python3 checks/evidence/2026-10-19-T-043/d045/contract-mutants.py [repo-root]
"""
import pathlib
import shutil
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[4]).resolve()
OAS, INV = "openapi/openapi.yaml", "planning/api-operations.json"
PARAM = "      - $ref: '#/components/parameters/NoOrganizationContext'\n"

MUTANTS = [
    ("me-without-its-scope", OAS, "      x-tenant-scope: user\n", ""),
    ("me-requires-the-selector", OAS, PARAM, "      - $ref: '#/components/parameters/OrganizationContext'\n"),
    ("me-without-the-selector-parameter", OAS, "      x-required-roles: []\n      parameters:\n" + PARAM, "      x-required-roles: []\n      parameters: []\n"),
    ("selector-takes-a-value", OAS, "      schema:\n        not: {}\n", "      schema:\n        type: string\n"),
    ("me-with-a-role", OAS, "      x-required-roles: []\n      parameters:\n" + PARAM, "      x-required-roles:\n      - viewer\n      parameters:\n" + PARAM),
    ("example-off-schema", OAS, "                      role: viewer\n", "                      role: admin\n"),
    ("own-400-lists-another-status", OAS, "malformed. Codes: INVALID_REQUEST.'", "malformed. Codes: FORBIDDEN.'"),
    ("me-without-403", OAS, "        '403':\n          description: 'The token is valid but no user", "        '409':\n          description: 'The token is valid but no user"),
    ("inventory-without-the-scope", INV, '"roles": [],\n      "scope": "user"', '"roles": []'),
    ("inventory-with-a-role", INV, '"roles": [],\n      "scope": "user"', '"roles": ["viewer"],\n      "scope": "user"'),
]


def run(root: pathlib.Path) -> str:
    r = subprocess.run([sys.executable, str(root / "scripts" / "check_contracts.py")], capture_output=True, text=True)
    if r.returncode == 0:
        return "SURVIVED"
    last = (r.stderr.strip().splitlines() or ["?"])[-1]
    return "KILLED\t" + last[:160]


def main() -> int:
    sources = {f: (REPO / f).read_text(encoding="utf-8") for f in (OAS, INV)}
    bad = [n for n, f, old, _ in MUTANTS if sources[f].count(old) != 1]
    if bad:
        print("patterns not found exactly once:", bad)
        return 2
    wrong = 0
    with tempfile.TemporaryDirectory() as tmp:
        root = pathlib.Path(tmp)
        for d in ("scripts", "schemas", "openapi", "testdata", "planning"):
            shutil.copytree(REPO / d, root / d, ignore=shutil.ignore_patterns("__pycache__", "check-architecture"))
        (root / "checks").mkdir()
        if (b := run(root)) != "SURVIVED":
            print("baseline fails:", b)
            return 2
        print("baseline\tcheck_contracts.py passes", flush=True)
        for name, f, old, new in MUTANTS:
            (root / f).write_text(sources[f].replace(old, new), encoding="utf-8")
            v = run(root)
            (root / f).write_text(sources[f], encoding="utf-8")
            ok = v != "SURVIVED"
            wrong += not ok
            print(f"{name}\t{v}" + ("" if ok else "\tUNEXPECTED"), flush=True)
    print(f"total={len(MUTANTS)} unexpected={wrong}")
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
