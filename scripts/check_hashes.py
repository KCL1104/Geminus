#!/usr/bin/env python3
"""Independent reference for the platform hashes (D-006, D-019, D-034).

Computes model, patch, config, manifest, result and content hashes with the
Trail of Bits `rfc8785` package and a quantizer written separately from the Go
one, then checks them against testdata/hashing/expected-hashes.json and the
hash members of the contract fixtures. internal/model/hashing tests read the
same expected file, so CI fails unless two independent JCS implementations
agree.

    python scripts/check_hashes.py           # verify (make spec-hashes)
    python scripts/check_hashes.py --write   # regenerate expected-hashes.json + rfc8785-documents.jsonl
    python scripts/check_hashes.py --documents 100000 /tmp/docs.jsonl  # extended corpus
"""

from __future__ import annotations

import hashlib
import json
import sys
from decimal import ROUND_HALF_UP, Decimal
from pathlib import Path

import rfc8785

ROOT = Path(__file__).resolve().parents[1]
EXPECTED = ROOT / "testdata/hashing/expected-hashes.json"
UNIT_MS = {"millisecond": 1, "second": 1000, "minute": 60_000, "hour": 3_600_000, "day": 86_400_000}
MAX_TICKS = 365 * 86_400_000
MAX_SAFE = 2**53 - 1


def strict_load(path: Path):
    def no_duplicates(pairs):
        keys = [k for k, _ in pairs]
        if len(keys) != len(set(keys)):
            raise ValueError(f"{path}: duplicate member")
        return dict(pairs)

    return json.loads(path.read_bytes().decode("utf-8"), object_pairs_hook=no_duplicates)


def jcs_hash(value) -> str:
    return "sha256:" + hashlib.sha256(rfc8785.dumps(value)).hexdigest()


def ticks(duration: dict) -> int:
    # Exact decimal value of the binary64 product, then round half up: the
    # spec's rule written without float rounding tricks (Go uses math.Round).
    product = duration["value"] * UNIT_MS[duration["unit"]]
    t = int(Decimal(product).quantize(Decimal(1), rounding=ROUND_HALF_UP))
    if duration["value"] < 0 or t > MAX_TICKS:
        raise ValueError(f"duration out of range: {duration}")
    return 1 if t == 0 and duration["value"] > 0 else t


def config_hash(cfg: dict) -> str:
    if not 0 <= cfg["seed"] <= MAX_SAFE:
        raise ValueError("seed out of range")
    return jcs_hash({
        "horizon_ticks": ticks(cfg["horizon"]),
        "warmup_ticks": ticks(cfg["warmup"]),
        "replications": cfg["replications"],
        "seed": cfg["seed"],
        "trace_level": cfg["trace_level"],
    })


def manifest_hash(bindings: list) -> tuple[str, list]:
    ordered = sorted(bindings, key=lambda b: b["logical_ref"].encode("utf-8"))
    refs = [b["logical_ref"] for b in ordered]
    if len(refs) != len(set(refs)):
        raise ValueError("duplicate logical_ref")
    return jcs_hash(ordered), refs


def apply_patch(doc, patch: list):
    """RFC 6902 subset (add/replace/remove/test) on a copy, for effective_model_hash."""
    doc = json.loads(json.dumps(doc))
    for op in patch:
        tokens = [t.replace("~1", "/").replace("~0", "~") for t in op["path"].split("/")[1:]]
        parent = doc
        for t in tokens[:-1]:
            parent = parent[int(t)] if isinstance(parent, list) else parent[t]
        last = tokens[-1]
        key = (len(parent) if last == "-" else int(last)) if isinstance(parent, list) else last
        if op["op"] == "test":
            if parent[key] != op["value"]:
                raise ValueError(f"test failed at {op['path']}")
        elif op["op"] == "replace":
            parent[key]  # must exist
            parent[key] = op["value"]
        elif op["op"] == "add":
            parent.insert(key, op["value"]) if isinstance(parent, list) else parent.__setitem__(key, op["value"])
        elif op["op"] == "remove":
            del parent[key]
        else:
            raise ValueError(f"unsupported op {op['op']}")
    return doc


def result_digest(result: dict) -> str:
    payload = {k: result[k] for k in ("engine_version", "metrics", "replication_summaries", "measurement_window")}
    payload["reproducibility"] = {k: v for k, v in result["reproducibility"].items() if k != "result_digest"}
    return jcs_hash(payload)


DOCUMENTS = ROOT / "testdata/hashing/rfc8785-documents.jsonl"
# Characters where UTF-16 code unit order differs from code point / UTF-8
# order (astral vs U+E000..U+FFFF), plus escapes and controls.
KEY_CHARS = ["a", "b", "A", "_", "-", "1", "\u0000", "\u001f", "\"", "\\", "/", "\u007f", "\u0080", "\u00f6",
             "\u20ac", "\ue000", "\uff61", "\ufb33", "\U0001f600", "\U00010000", "\U0010ffff", "\u2028"]


def random_document(rng, depth=0):
    """A random JSON value plus a random (valid) textual spelling of it."""
    kind = rng.choice(["object", "array", "number", "string", "literal"] if depth < 4 else ["number", "string", "literal"])
    if kind == "object":
        return {"".join(rng.choice(KEY_CHARS) for _ in range(rng.randint(0, 4))): random_document(rng, depth + 1)
                for _ in range(rng.randint(0, 6))}
    if kind == "array":
        return [random_document(rng, depth + 1) for _ in range(rng.randint(0, 5))]
    if kind == "string":
        return "".join(rng.choice(KEY_CHARS) for _ in range(rng.randint(0, 8)))
    if kind == "literal":
        return rng.choice([None, True, False])
    shape = rng.randint(0, 3)
    if shape == 0:
        return rng.randint(-MAX_SAFE, MAX_SAFE)
    if shape == 1:
        import struct
        while True:
            f = struct.unpack("<d", rng.getrandbits(64).to_bytes(8, "little"))[0]
            if f == f and f not in (float("inf"), float("-inf")):
                return f
    if shape == 2:
        return round(rng.uniform(-1e6, 1e6), rng.randint(0, 6))
    return rng.random() * 10 ** rng.randint(-8, 22)


def documents(count: int, seed: int = 8785) -> list[str]:
    import random

    rng = random.Random(seed)
    lines = []
    for _ in range(count):
        doc = random_document(rng)
        spelled = json.dumps(doc, ensure_ascii=rng.random() < 0.5, indent=rng.choice([None, 1, 3]),
                             separators=rng.choice([None, (",", ":"), (" ,", " : ")]))
        lines.append(json.dumps({"input": spelled, "canonical_hex": rfc8785.dumps(doc).hex()}, ensure_ascii=True))
    return lines


def compute() -> dict:
    out: dict = {"comment": "Generated by scripts/check_hashes.py (Python rfc8785 " + rfc8785_version() + "). Do not edit by hand."}
    models = {}
    for p in sorted((ROOT / "testdata/golden-models").glob("*.json")):
        models[p.relative_to(ROOT).as_posix()] = jcs_hash(strict_load(p))
    cases = strict_load(ROOT / "testdata/contracts/cases.json")["cases"]
    patches = {"[]": jcs_hash([])}
    for c in cases:
        if not c["valid"] or c["layer"] != "schema":
            continue
        if c["schema"] == "schemas/model-ir.schema.json":
            models[c["instance"]] = jcs_hash(strict_load(ROOT / c["instance"]))
        elif c["schema"] == "schemas/json-patch.schema.json":
            patches[c["instance"]] = jcs_hash(strict_load(ROOT / c["instance"]))
    out["model_hash"] = models
    out["patch_hash"] = patches
    version = strict_load(ROOT / "testdata/contracts/valid-model-version.json")
    scenario = strict_load(ROOT / "testdata/contracts/valid-scenario.json")
    out["effective_model_hash"] = {
        "valid-model-version.json ir + []": jcs_hash(apply_patch(version["ir"], [])),
        "valid-model-version.json ir + valid-scenario.json patch": jcs_hash(apply_patch(version["ir"], scenario["patch"])),
    }

    configs = strict_load(ROOT / "testdata/hashing/configs.json")["configs"]
    out["config_hash"] = {c["name"]: config_hash(c["config"]) for c in configs}
    for c in configs:
        if "same_as" in c and out["config_hash"][c["name"]] != out["config_hash"][c["same_as"]]:
            raise SystemExit(f"config {c['name']!r} should hash like {c['same_as']!r}")

    manifest = strict_load(ROOT / "testdata/hashing/manifest.json")
    digest, order = manifest_hash(manifest["bindings"])
    if order != manifest["expected_order"]:
        raise SystemExit(f"manifest byte order {order} != expected {manifest['expected_order']}")
    out["dataset_manifest_hash"] = {"testdata/hashing/manifest.json": digest, "[]": manifest_hash([])[0]}

    result = ROOT / "testdata/contracts/valid-result.json"
    out["result_digest"] = {result.relative_to(ROOT).as_posix(): result_digest(strict_load(result))}
    snap = ROOT / "testdata/contracts/valid-dataset-snapshot.json"
    out["content_hash"] = {snap.relative_to(ROOT).as_posix(): "sha256:" + hashlib.sha256(snap.read_bytes()).hexdigest()}
    return out


def rfc8785_version() -> str:
    from importlib.metadata import version

    return version("rfc8785")


def fixture_consistency(expected: dict) -> list[str]:
    """Hash members of contract fixtures must equal the hash of their content."""
    errors = []
    version = strict_load(ROOT / "testdata/contracts/valid-model-version.json")
    if version["hash"] != jcs_hash(version["ir"]):
        errors.append("valid-model-version.json: hash is not model_hash(ir)")
    if version["dataset_bindings"] and manifest_hash(version["dataset_bindings"])[1] != [b["logical_ref"] for b in version["dataset_bindings"]]:
        errors.append("valid-model-version.json: dataset_bindings are not in byte order")
    for name in ("valid-scenario.json", "valid-manual-scenario.json"):  # T-028: a person's scenario too
        scenario = strict_load(ROOT / "testdata/contracts" / name)
        if scenario["patch_hash"] != jcs_hash(scenario["patch"]):
            errors.append(f"{name}: patch_hash is not patch_hash(patch)")
    result = strict_load(ROOT / "testdata/contracts/valid-result.json")
    if result["reproducibility"]["result_digest"] != result_digest(result):
        errors.append("valid-result.json: result_digest does not match its payload")
    return errors


def main() -> int:
    if "--documents" in sys.argv:  # extended run: --documents COUNT PATH
        i = sys.argv.index("--documents")
        Path(sys.argv[i + 2]).write_text("\n".join(documents(int(sys.argv[i + 1]))) + "\n", encoding="ascii")
        return 0
    got = compute()
    docs = "\n".join(documents(400)) + "\n"
    if "--write" in sys.argv:
        EXPECTED.write_text(json.dumps(got, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
        DOCUMENTS.write_text(docs, encoding="ascii")
        print(f"wrote {EXPECTED.relative_to(ROOT)} and {DOCUMENTS.relative_to(ROOT)}")
        return 0
    want = strict_load(EXPECTED)
    errors = []
    for family in sorted(set(got) | set(want)):
        if family == "comment":
            continue
        g, w = got.get(family, {}), want.get(family, {})
        for key in sorted(set(g) | set(w)):
            if g.get(key) != w.get(key):
                errors.append(f"{family}[{key}]: computed {g.get(key)}, expected file has {w.get(key)}")
    errors += fixture_consistency(got)
    if DOCUMENTS.read_text(encoding="ascii") != docs:
        errors.append(f"{DOCUMENTS.relative_to(ROOT)} is stale; run scripts/check_hashes.py --write")
    count = sum(len(v) for k, v in got.items() if k != "comment")
    print(json.dumps({"status": "FAIL" if errors else "PASS_INDEPENDENT_HASHES", "hashes": count, "errors": errors}, indent=2))
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
