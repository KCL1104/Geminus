#!/usr/bin/env python3
"""T-053 schema follow-up, review round 1 (reviewer): schema mutants.

Does a schema change that reintroduces an empty schema, an open value in a
place no const member tells apart, or an open object fail offline? Each
mutant edits a canonical schema in a `git archive HEAD` copy (never a
checkout), runs the adapter package's tests (-count=1): KILLED when they
fail; BY-DESIGN when they pass and the change is one the projection
encodes on purpose (listed, expected). Every file is restored and its hash
compared. No network call.

    python3 schema_mutants.py <copy>
"""
import hashlib
import os
import subprocess
import sys

A = "./internal/ai/provider/anthropic/"
PO = "schemas/proposal-output.schema.json"
JP = "schemas/json-patch.schema.json"
AO = "schemas/analysis-output.schema.json"

REMOVE_ALT = '''            "op": {
            "const": "remove"
          },'''

MUTANTS = [
    ("X1", "an open member in the needs_input alternative (encoded at a 4th place)", PO,
     [('"questions": {\n          "type": "array",', '"note": {},\n        "questions": {\n          "type": "array",')], "kill"),
    ("X2", "an open alternative inside anyOf (no const tells it apart)", PO,
     [('"confidence": {\n          "type": "number",\n          "minimum": 0,\n          "maximum": 1\n        }',
       '"confidence": {\n          "anyOf": [{"type": "number"}, {}]\n        }')], "kill"),
    ("X3", "the remove alternative becomes an open object", JP,
     [('''          "path"
        ],
        "additionalProperties": false
      }
    ]''', '''          "path"
        ]
      }
    ]''')], "kill"),
    ("X4", "an open member in analysis-output", AO,
     [('"properties": {\n    "baseline"', '"properties": {\n    "extra": {},\n    "baseline"')], "kill"),
    ("X5", "an assumption may be any JSON value (encoded array items)", PO,
     [('"assumptions": {\n          "type": "array",\n          "items": {\n            "type": "string",\n            "minLength": 1,\n            "maxLength": 2000\n          },\n          "minItems": 0,\n          "maxItems": 100\n        },\n        "risk_level"',
       '"assumptions": {\n          "type": "array",\n          "items": {},\n          "minItems": 0,\n          "maxItems": 100\n        },\n        "risk_level"')], "kill"),
    ("X6", "the add value is an open object (type object, no additionalProperties)", JP,
     [('''            "const": "add"
          },
          "path": {
            "type": "string",
            "minLength": 1,
            "maxLength": 2048,
            "pattern": "^/(?:[^~]|~[01])*$"
          },
          "value": {}''', '''            "const": "add"
          },
          "path": {
            "type": "string",
            "minLength": 1,
            "maxLength": 2048,
            "pattern": "^/(?:[^~]|~[01])*$"
          },
          "value": {"type": "object"}''')], "kill"),
    ("X7", "the add value has only a dropped constraint", JP,
     [('''            "const": "add"
          },
          "path": {
            "type": "string",
            "minLength": 1,
            "maxLength": 2048,
            "pattern": "^/(?:[^~]|~[01])*$"
          },
          "value": {}''', '''            "const": "add"
          },
          "path": {
            "type": "string",
            "minLength": 1,
            "maxLength": 2048,
            "pattern": "^/(?:[^~]|~[01])*$"
          },
          "value": {"minLength": 1}''')], "kill"),
    ("X8", "the add value is true instead of {} (an open value as before: encoded the same)", JP,
     [('''            "const": "add"
          },
          "path": {
            "type": "string",
            "minLength": 1,
            "maxLength": 2048,
            "pattern": "^/(?:[^~]|~[01])*$"
          },
          "value": {}''', '''            "const": "add"
          },
          "path": {
            "type": "string",
            "minLength": 1,
            "maxLength": 2048,
            "pattern": "^/(?:[^~]|~[01])*$"
          },
          "value": true''')], "by-design"),
    ("X9", "the op discriminator is no longer required in remove (alternatives untold)", JP,
     [('''          "op",
          "path"
        ],''', '''          "path"
        ],''')], "kill"),
    ("X10", "additionalProperties is an empty schema in the needs_input alternative", PO,
     [('''        "questions",
        "requires_confirmation"
      ],
      "additionalProperties": false''', '''        "questions",
        "requires_confirmation"
      ],
      "additionalProperties": {}''')], "kill"),
]


def digest(path):
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


def failing(out):
    return sorted({l.split()[2] for l in out.splitlines() if l.startswith("--- FAIL: ")})[:6]


def main():
    root = os.path.abspath(sys.argv[1])
    if os.path.exists(os.path.join(root, ".git")):
        sys.exit("refusing: run it in a `git archive` copy, not a checkout")
    env = {k: v for k, v in os.environ.items() if k in ("PATH", "HOME", "GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "GOFLAGS")}
    counts = {}
    for mid, what, rel, edits, expect in MUTANTS:
        path = os.path.join(root, rel)
        before = digest(path)
        with open(path, encoding="utf-8") as f:
            orig = f.read()
        mutated, bad = orig, None
        for old, new in edits:
            n = mutated.count(old)
            if n != 1:
                bad = f"pattern occurs {n} times"
                break
            mutated = mutated.replace(old, new)
        if bad:
            result, detail = "ERROR", bad
        else:
            try:
                with open(path, "w", encoding="utf-8") as f:
                    f.write(mutated)
                p = subprocess.run(["go", "test", "-count=1", "-p", "1", A], capture_output=True, text=True, cwd=root, env=env)
                out = p.stdout + p.stderr
                if p.returncode != 0:
                    result, detail = "KILLED", ", ".join(failing(out)) or out.strip().splitlines()[-1][:160]
                elif expect == "by-design":
                    result, detail = "BY-DESIGN", "tests pass: the projection encodes this open value on purpose"
                else:
                    result, detail = "SURVIVED", "tests pass"
            finally:
                with open(path, "w", encoding="utf-8") as f:
                    f.write(orig)
        if digest(path) != before:
            result, detail = "ERROR", "the file was not restored"
        counts[result] = counts.get(result, 0) + 1
        print(f"{mid}\t{result}\t{what}\t{rel}\t{detail}", flush=True)
    print("summary\t" + " ".join(f"{k}={v}" for k, v in sorted(counts.items())))
    return 0 if sum(counts.get(k, 0) for k in ("SURVIVED", "ERROR")) == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
