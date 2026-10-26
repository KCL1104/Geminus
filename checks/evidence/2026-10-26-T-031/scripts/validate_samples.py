"""Validate the T-031 review page's sample proposals against the repo's JSON
schemas (one-off, evidence): each sample proposal (and a generating one's
answer, and the stand-in's proposals after an approval and after a 409) as
proposal.schema.json, the stand-in's approvals as http.schema.json Approval,
the base and each candidate IR (the base with a sample's patch) as
model-ir.schema.json. Input: the dump a scratch vitest wrote
(sample-proposals.json); run with the repo's venv:

    .venv/bin/python checks/evidence/2026-10-26-T-031/scripts/validate_samples.py <repo root> <dump>
"""
import json
import sys
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource

root = Path(sys.argv[1])
dump = json.loads(Path(sys.argv[2]).read_text())
registry = Registry()
for p in (root / "schemas").glob("*.json"):
    s = json.loads(p.read_text())
    res = Resource.from_contents(s)
    registry = registry.with_resource(p.name, res)
    if "$id" in s:
        registry = registry.with_resource(s["$id"], res)


def check(label, data, ref):
    v = Draft202012Validator({"$ref": ref}, registry=registry, format_checker=FormatChecker())
    errors = sorted(v.iter_errors(data), key=lambda e: list(e.path))
    print(f"{label}: {'OK' if not errors else f'{len(errors)} error(s)'}")
    for e in errors[:5]:
        print("   ", list(e.path), e.message[:200])
    return not errors


ok = True
for name, p in dump["proposals"].items():
    ok &= check(f"proposal {name}", p, "proposal.schema.json")
for name, a in dump["approvals"].items():
    ok &= check(f"approval {name}", a, "http.schema.json#/$defs/Approval")
ok &= check("base IR (the sample line)", dump["base"], "model-ir.schema.json")
seen = {}
for name, ir in dump["candidates"].items():
    key = json.dumps(ir, sort_keys=True)
    if key in seen:
        print(f"candidate IR {name}: same as {seen[key]}")
        continue
    seen[key] = name
    ok &= check(f"candidate IR {name}", ir, "model-ir.schema.json")
sys.exit(0 if ok else 1)
