"""Validate the H-073 slice 3 electronics-line fixtures against the repo's JSON schemas (one-off, evidence)."""
import json
import sys
from pathlib import Path
from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource

root = Path(sys.argv[1])
registry = Registry()
for p in (root / "schemas").glob("*.json"):
    s = json.loads(p.read_text())
    res = Resource.from_contents(s)
    registry = registry.with_resource(p.name, res)
    if "$id" in s:
        registry = registry.with_resource(s["$id"], res)


def check(fixture, schema_file, ref):
    data = json.loads((root / "apps/web/features/results/fixtures" / fixture).read_text())
    schema = {"$ref": f"{schema_file}#{ref}"} if ref else {"$ref": schema_file}
    v = Draft202012Validator(schema, registry=registry, format_checker=FormatChecker())
    errors = sorted(v.iter_errors(data), key=lambda e: list(e.path))
    print(fixture, "OK" if not errors else f"{len(errors)} error(s)")
    for e in errors[:5]:
        print("  ", list(e.path), e.message[:200])
    return not errors


ok = True
ok &= check("electronics-line-model.json", "model-ir.schema.json", "")
ok &= check("electronics-line-comparison.json", "http.schema.json", "/$defs/Comparison")
ok &= check("electronics-line-baseline-result.json", "simulation.schema.json", "/$defs/Result")
ok &= check("electronics-line-scenario-result.json", "simulation.schema.json", "/$defs/Result")
sys.exit(0 if ok else 1)
