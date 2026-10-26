"""Validate the T-032 board-line fixtures against the repo's JSON schemas (one-off, evidence)."""
import json
import sys
from pathlib import Path
from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource

root = Path(sys.argv[1])
schemas = {}
for p in (root / "schemas").glob("*.json"):
    schemas[p.name] = json.loads(p.read_text())
registry = Registry()
for name, s in schemas.items():
    res = Resource.from_contents(s)
    registry = registry.with_resource(name, res)
    if "$id" in s:
        registry = registry.with_resource(s["$id"], res)

def check(fixture, schema_file, ref):
    data = json.loads((root / "apps/web/features/results/fixtures" / fixture).read_text())
    if fixture.endswith("result.json"):
        data = dict(data)
    schema = {"$ref": f"{schema_file}#{ref}"} if ref else {"$ref": schema_file}
    v = Draft202012Validator(schema, registry=registry, format_checker=FormatChecker())
    errors = sorted(v.iter_errors(data), key=lambda e: list(e.path))
    print(fixture, "OK" if not errors else f"{len(errors)} error(s)")
    for e in errors[:5]:
        print("  ", list(e.path), e.message[:200])
    return not errors

ok = True
ok &= check("board-line-model.json", "model-ir.schema.json", "")
ok &= check("board-line-baseline-result.json", "simulation.schema.json", "/$defs/Result")
ok &= check("board-line-scenario-result.json", "simulation.schema.json", "/$defs/Result")
ok &= check("board-line-baseline-trace.json", "simulation.schema.json", "/$defs/TraceArtifact")
ok &= check("board-line-scenario-trace.json", "simulation.schema.json", "/$defs/TraceArtifact")
ok &= check("board-line-comparison.json", "http.schema.json", "/$defs/Comparison")
sys.exit(0 if ok else 1)
