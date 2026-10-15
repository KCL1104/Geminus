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
    "AIP_A1": ("aip", "A", 1), "AIP_A2": ("aip", "A", 2), "IMP_A1": ("imp", "A", 1), "SIM_A1": ("sim", "A", 1),
    "AUD_A1": ("aud", "A", 1), "REQ_A1": ("req", "A", 1),
}


def values() -> dict[str, str]:
    v = {name: test_id(*spec) for name, spec in IDS.items()}
    minimal = json.loads((ROOT / "testdata/contracts/valid-minimal-ir.json").read_text(encoding="utf-8"))
    for name, model, title in [("IR_A1", "MDL_A", "Model A v1"), ("IR_A2", "MDL_A", "Model A v2"),
                               ("IR_A21", "MDL_A2", "Model A2 v1"), ("IR_B1", "MDL_B", "Model B v1")]:
        doc = dict(minimal, model_id=v[model], name=title)
        v[name] = json.dumps(doc, separators=(",", ":"), ensure_ascii=False)
        v[name + "_HASH"] = "sha256:" + hashlib.sha256(rfc8785.dumps(doc)).hexdigest()
    v["INV_IR"], v["INV_IR_HASH"] = v["IR_A1"], v["IR_A1_HASH"]
    # Dataset object hashes are shape-only: the database cannot see object bytes.
    v["H1"], v["H2"], v["H3"] = ("sha256:" + c * 64 for c in "123")
    # CB-14: the same ID with its '_' replaced, which LIKE's '_' wildcard would accept.
    v["ORG_A_WILD"] = v["ORG_A"].replace("_", "X", 1)
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
