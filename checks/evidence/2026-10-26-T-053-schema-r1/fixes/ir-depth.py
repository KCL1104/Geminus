#!/usr/bin/env python3
"""T-053 schema follow-up, review round 1, M1: how deep can a model IR be?

The justification of MaxEncodedValueDepth (encoded.go) rests on it. Two
measures, read only from the repository:

  1. schemas/model-ir.schema.json walked with its $refs: the deepest JSON
     nesting its own structure allows ([] and {} one level each), and the
     level of each open container (an object with additionalProperties:
     true, which validate.MaxContainerDepth = 8 bounds on its own);
  2. the deepest valid model-ir fixture (testdata/contracts/cases.json) and
     golden model (testdata/golden-models/*.json).

    python3 ir-depth.py <repository root>
"""
import json
import os
import sys

root = sys.argv[1]
docs = {f: json.load(open(os.path.join(root, "schemas", f))) for f in os.listdir(os.path.join(root, "schemas")) if f.endswith(".schema.json")}


def resolve(ref, file):
    target, _, frag = ref.partition("#")
    target = target or file
    v = docs[target]
    for tok in [t for t in frag.split("/") if t]:
        v = v[tok.replace("~1", "/").replace("~0", "~")]
    return v, target


containers = {}


def depth(s, file, path, level, stack=()):
    """The deepest level a value valid under s reaches, s's value being at level."""
    if not isinstance(s, dict):
        return level - 1
    if "$ref" in s:
        key = (s["$ref"], file)
        if key in stack:
            sys.exit(f"recursive schema at {path}")
        t, tf = resolve(s["$ref"], file)
        return depth(t, tf, path, level, stack + (key,))
    best = level - 1  # a scalar adds no level
    for k in ("oneOf", "anyOf", "allOf"):
        for sub in s.get(k, []):
            best = max(best, depth(sub, file, path, level, stack))
    if "properties" in s or s.get("type") == "object":
        best = max(best, level)
        if s.get("additionalProperties") is True:
            containers[path] = level
        for name, sub in s.get("properties", {}).items():
            best = max(best, depth(sub, file, path + "/" + name, level + 1, stack))
    if "items" in s or s.get("type") == "array":
        best = max(best, level)
        if "items" in s:
            best = max(best, depth(s["items"], file, path + "/*", level + 1, stack))
    return best


def value_depth(v):
    if isinstance(v, dict):
        return 1 + max((value_depth(x) for x in v.values()), default=0)
    if isinstance(v, list):
        return 1 + max((value_depth(x) for x in v), default=0)
    return 0


structural = depth(docs["model-ir.schema.json"], "model-ir.schema.json", "", 1)
print(f"model-ir.schema.json: deepest level of its own structure: {structural}")
for path, level in sorted(containers.items()):
    print(f"  open container {path} at level {level}: with MaxContainerDepth 8 it reaches level {level - 1 + 8}")
print(f"deepest IR: {max([structural] + [level - 1 + 8 for level in containers.values()])} levels")

files = []
cases = json.load(open(os.path.join(root, "testdata/contracts/cases.json")))
for c in cases if isinstance(cases, list) else cases.get("cases", []):
    if c.get("schema") == "schemas/model-ir.schema.json" and c.get("valid"):
        files.append(c["instance"])
gm = os.path.join(root, "testdata/golden-models")
files += [os.path.join("testdata/golden-models", f) for f in sorted(os.listdir(gm)) if f.endswith(".json")]
deepest = max(((value_depth(json.load(open(os.path.join(root, f)))), f) for f in files), default=(0, None))
print(f"{len(files)} valid model-ir fixtures and golden models; the deepest: {deepest[0]} levels ({deepest[1]})")
