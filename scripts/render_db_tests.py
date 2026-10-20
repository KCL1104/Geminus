#!/usr/bin/env python3
"""Render db/tests/*.sql from db/tests/templates/*.sql.tmpl.

The SQL suites need many valid D-001 IDs (<prefix>_<ULID>) and real RFC 8785
model hashes; writing them by hand invites typos that silently test nothing.
Templates use {{NAME}} placeholders filled from the table below.

    python scripts/render_db_tests.py          # write db/tests/*.sql
    python scripts/render_db_tests.py --check  # fail if they are stale (make spec-check)
"""

from __future__ import annotations

import hashlib
import json
import re
import sys
from pathlib import Path

import rfc8785

ROOT = Path(__file__).resolve().parents[1]
TEMPLATES = ROOT / "db/tests/templates"
ULID_ID = re.compile(r"^[a-z]+_[0-7][0-9A-HJKMNP-TV-Z]{25}$")


def test_id(prefix: str, tenant: str, n: int) -> str:
    """Deterministic, valid ULID-shaped test ID: <prefix>_01M4YDQK00T<tenant><n>."""
    value = f"{prefix}_01M4YDQK00T{tenant}{n:014d}"
    if not ULID_ID.match(value):
        raise ValueError(value)
    return value


IDS = {
    "ORG_A": ("org", "A", 0), "ORG_B": ("org", "B", 0), "USR_A": ("usr", "A", 1), "USR_B": ("usr", "B", 1),
    "PRJ_A": ("prj", "A", 1), "PRJ_B": ("prj", "B", 1), "PRJ_X": ("prj", "A", 99),
    "MDL_A": ("mdl", "A", 1), "MDL_A2": ("mdl", "A", 2), "MDL_B": ("mdl", "B", 1), "MDL_X": ("mdl", "A", 99),
    "MV_A1": ("mv", "A", 1), "MV_A2": ("mv", "A", 2), "MV_X": ("mv", "A", 21), "MV_A21": ("mv", "A", 22),
    "MV_B1": ("mv", "B", 1), "DS_A": ("ds", "A", 1), "DSV_A1": ("dsv", "A", 1), "DSV_A2": ("dsv", "A", 2),
    "AIP_A1": ("aip", "A", 1), "AIP_A2": ("aip", "A", 2), "IMP_A1": ("imp", "A", 1), "IMP_A2": ("imp", "A", 2), "ANA_A1": ("ana", "A", 1), "SIM_A1": ("sim", "A", 1),
    "AUD_A1": ("aud", "A", 1), "REQ_A1": ("req", "A", 1), "USR_A2": ("usr", "A", 2), "JOB_A1": ("job", "A", 1),
    "AIP_A9": ("aip", "A", 9), "ORG_P": ("org", "P", 0), "USR_P": ("usr", "P", 1),
    "AUD_A3": ("aud", "A", 3), "AUD_A4": ("aud", "A", 4), "AUD_P1": ("aud", "P", 1), "AUD_P2": ("aud", "P", 2),
    "JOB_A2": ("job", "A", 2), "JOB_A3": ("job", "A", 3), "JOB_A4": ("job", "A", 4), "JOB_A5": ("job", "A", 5),
    "JOB_A6": ("job", "A", 6), "JOB_A7": ("job", "A", 7), "JOB_A8": ("job", "A", 8),
    # D-045 (self.sql): more organizations and users, and IDs no row has.
    "ORG_C": ("org", "C", 0), "ORG_D": ("org", "D", 0), "ORG_X": ("org", "X", 0),
    "USR_C": ("usr", "C", 1), "USR_X": ("usr", "X", 1), "PRJ_C": ("prj", "C", 1), "PRJ_D": ("prj", "D", 1),
    # T-023 (runs.sql, constraints.sql): runs, their jobs and trace artifacts.
    "SIM_A2": ("sim", "A", 2), "SIM_A3": ("sim", "A", 3), "SIM_A4": ("sim", "A", 4), "SIM_A5": ("sim", "A", 5),
    "SIM_A6": ("sim", "A", 6), "SIM_A7": ("sim", "A", 7), "SIM_X": ("sim", "A", 99),
    "JOB_A12": ("job", "A", 12), "JOB_A13": ("job", "A", 13), "JOB_A14": ("job", "A", 14), "JOB_A15": ("job", "A", 15),
    "JOB_A16": ("job", "A", 16), "JOB_A17": ("job", "A", 17), "JOB_A18": ("job", "A", 18),
    "ART_A1": ("art", "A", 1), "ART_A2": ("art", "A", 2),
    # T-016 (imports.sql): uploads, their profile jobs and an ID no row has.
    "IMP_A3": ("imp", "A", 3), "IMP_A4": ("imp", "A", 4), "IMP_A5": ("imp", "A", 5), "IMP_A6": ("imp", "A", 6),
    "IMP_A9": ("imp", "A", 9), "JOB_A21": ("job", "A", 21), "JOB_A22": ("job", "A", 22),
    # T-016 review M1, M3: imports that follow their profile jobs.
    "IMP_A7": ("imp", "A", 7), "IMP_A8": ("imp", "A", 8), "IMP_A10": ("imp", "A", 10),
    "JOB_A23": ("job", "A", 23), "JOB_A24": ("job", "A", 24), "JOB_A25": ("job", "A", 25), "JOB_A26": ("job", "A", 26),
    # T-024 (proposals.sql, pool_rows.sql): proposals and their ID allocation pools (D-046).
    "AIP_A3": ("aip", "A", 3), "AIP_A4": ("aip", "A", 4), "AIP_B1": ("aip", "B", 1),
    # T-025 (proposal_jobs.sql, constraints.sql): proposals that follow their model_proposal jobs.
    "AIP_A5": ("aip", "A", 5), "AIP_A6": ("aip", "A", 6), "AIP_A7": ("aip", "A", 7), "AIP_A8": ("aip", "A", 8),
    "AIP_A10": ("aip", "A", 10), "AIP_A11": ("aip", "A", 11),
    "JOB_A31": ("job", "A", 31), "JOB_A32": ("job", "A", 32), "JOB_A33": ("job", "A", 33), "JOB_A34": ("job", "A", 34),
    "JOB_A35": ("job", "A", 35), "JOB_A36": ("job", "A", 36), "JOB_A37": ("job", "A", 37), "JOB_A38": ("job", "A", 38),
    "JOB_A41": ("job", "A", 41), "JOB_A42": ("job", "A", 42),
    # T-026 (proposal_decisions.sql): decided proposals, their jobs and effects.
    "AIP_A21": ("aip", "A", 21), "AIP_A22": ("aip", "A", 22), "AIP_A23": ("aip", "A", 23), "AIP_A24": ("aip", "A", 24),
    "AIP_A25": ("aip", "A", 25), "AIP_A26": ("aip", "A", 26), "AIP_A27": ("aip", "A", 27), "AIP_A28": ("aip", "A", 28),
    "JOB_A51": ("job", "A", 51), "JOB_A52": ("job", "A", 52), "JOB_A53": ("job", "A", 53), "JOB_A54": ("job", "A", 54),
    "JOB_A55": ("job", "A", 55), "JOB_A56": ("job", "A", 56), "JOB_A57": ("job", "A", 57),
    "MV_A3": ("mv", "A", 3), "MV_A4": ("mv", "A", 4), "MV_A5": ("mv", "A", 5),
    "SCN_A1": ("scn", "A", 1), "SCN_A2": ("scn", "A", 2), "SCN_A3": ("scn", "A", 3),
    # T-017 (import_mapping.sql, imports.sql, roles.sql): mapped imports, their profile and validation jobs.
    "IMP_A11": ("imp", "A", 11), "IMP_A12": ("imp", "A", 12), "IMP_A13": ("imp", "A", 13), "IMP_A14": ("imp", "A", 14),
    "IMP_A15": ("imp", "A", 15), "IMP_A16": ("imp", "A", 16),
    # JOB_A51–JOB_A57 are T-026's above: each SQL suite rolls back, so the
    # same IDs serve both files.
    "JOB_A58": ("job", "A", 58),
    "JOB_A59": ("job", "A", 59), "JOB_A60": ("job", "A", 60), "JOB_A61": ("job", "A", 61), "JOB_A62": ("job", "A", 62),
    "JOB_A63": ("job", "A", 63), "JOB_A64": ("job", "A", 64), "JOB_A65": ("job", "A", 65), "JOB_A66": ("job", "A", 66),
    "JOB_A67": ("job", "A", 67), "JOB_A68": ("job", "A", 68),
}


def id_pool(first: int) -> str:
    """A D-046 pool as JSON: 16 lowercase-ULID-shaped IDs of each kind, <kind>_01m4ydqk00ta<n>."""
    pool = {kind: [f"{kind}_01m4ydqk00ta{first + i:014d}" for i in range(16)] for kind in ("node", "flow", "resource")}
    for kind, ids in pool.items():
        for value in ids:
            if not re.fullmatch(rf"{kind}_[0-7][0-9a-hjkmnp-tv-z]{{25}}", value):
                raise ValueError(value)
    return json.dumps(pool, separators=(",", ":"))


def values() -> dict[str, str]:
    v = {name: test_id(*spec) for name, spec in IDS.items()}
    minimal = json.loads((ROOT / "testdata/contracts/valid-minimal-ir.json").read_text(encoding="utf-8"))
    for name, model, title in [("IR_A1", "MDL_A", "Model A v1"), ("IR_A2", "MDL_A", "Model A v2"),
                               ("IR_A21", "MDL_A2", "Model A2 v1"), ("IR_B1", "MDL_B", "Model B v1")]:
        doc = dict(minimal, model_id=v[model], name=title)
        # D-040: stored as the canonical text the hash covers.
        canonical = rfc8785.dumps(doc)
        v[name] = canonical.decode("utf-8")
        v[name + "_HASH"] = "sha256:" + hashlib.sha256(canonical).hexdigest()
    v["INV_IR"], v["INV_IR_HASH"] = v["IR_A1"], v["IR_A1_HASH"]
    # Dataset object hashes are shape-only: the database cannot see object bytes.
    v["H1"], v["H2"], v["H3"] = ("sha256:" + c * 64 for c in "123")
    # A run without datasets has the manifest hash of [] (D-019).
    v["EMPTY_MANIFEST_HASH"] = "sha256:" + hashlib.sha256(rfc8785.dumps([])).hexdigest()
    # CB-14: the same ID with its '_' replaced, which LIKE's '_' wildcard would accept.
    v["ORG_A_WILD"] = v["ORG_A"].replace("_", "X", 1)
    v["POOL_1"], v["POOL_2"] = id_pool(1), id_pool(101)
    return v


def render() -> dict[Path, str]:
    v = values()
    out = {}
    for tmpl in sorted(TEMPLATES.glob("*.sql.tmpl")):
        text = re.sub(r"\{\{(\w+)\}\}", lambda m: v[m.group(1)], tmpl.read_text(encoding="utf-8"))
        out[ROOT / "db/tests" / tmpl.name.removesuffix(".tmpl")] = text
    return out


def main() -> int:
    rendered = render()
    if "--check" in sys.argv:
        stale = [p.relative_to(ROOT).as_posix() for p, text in rendered.items()
                 if not p.exists() or p.read_text(encoding="utf-8") != text]
        print(json.dumps({"status": "FAIL" if stale else "PASS_DB_TESTS_FRESH", "files": len(rendered), "stale": stale}))
        return 1 if stale else 0
    for path, text in rendered.items():
        path.write_text(text, encoding="utf-8")
        print(f"wrote {path.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
