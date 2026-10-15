#!/usr/bin/env python3
"""Check fixture arithmetic only. This is NOT a DES engine or differential test."""
import hashlib
import json
import math
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def quantile(values: list[float], p: float) -> float:
    values = sorted(values)
    h = (len(values) - 1) * p
    lo, hi = math.floor(h), math.ceil(h)
    return values[lo] + (h - lo) * (values[hi] - values[lo])


def main() -> None:
    source = ROOT / "testdata/simulation/expected-cases.json"
    cases = json.loads(source.read_text(encoding="utf-8"))["cases"]
    assertions = 0
    for case in cases:
        model = json.loads((source.parent / case["model"]).read_text(encoding="utf-8"))
        arrival = next(n for n in model["nodes"] if n["type"] == "source")
        process = next(n for n in model["nodes"] if n["type"] == "process")
        interval = arrival["arrival"]["distribution"]
        service = process["service_time"]["distribution"]
        assert interval["type"] == service["type"] == "constant"
        assert model["time_unit"] == case["config"]["horizon"]["unit"] == "minute"
        assert len(model["resources"]) == 1 and model["resources"][0]["capacity"] == 1
        n = arrival["arrival"]["max_entities"]
        gap, duration = interval["value"], service["value"]
        horizon = case["config"]["horizon"]["value"]
        arrivals = [gap * (i + 1) for i in range(n)]
        completions, waits = [], []
        previous = 0.0
        for at in arrivals:
            start = max(at, previous)
            waits.append(start - at)
            previous = start + duration
            completions.append(previous)
        # Fixtures deliberately finish all work: no warmup/censoring generalization.
        assert case["config"]["warmup"]["value"] == 0
        assert completions[-1] <= horizon
        lead = [c - a for c, a in zip(completions, arrivals)]
        actual = {
            "completed_count": n,
            "first_arrival_minutes": arrivals[0],
            "last_completion_minutes": completions[-1],
            "queue_wait_mean_minutes": sum(waits) / n,
            "queue_wait_p50_minutes": quantile(waits, 0.5),
            "queue_wait_p95_minutes": quantile(waits, 0.95),
            "lead_time_mean_minutes": sum(lead) / n,
            "lead_time_p50_minutes": quantile(lead, 0.5),
            "lead_time_p95_minutes": quantile(lead, 0.95),
            "utilization": n * duration / horizon,
            "censored_entities": 0,
        }
        for key, expected in case["expected"].items():
            assert math.isclose(actual[key], expected, rel_tol=1e-12, abs_tol=1e-12), (case["id"], key)
            assertions += 1
        assert case["result_digest"] is None, "Cannot attest a Go engine digest from arithmetic."

    vectors = json.loads((ROOT / "testdata/simulation/rng-vectors.json").read_text())
    mask = (1 << 64) - 1
    state = int(vectors["splitmix64_initial_state_hex"], 16)
    for expected in vectors["outputs_hex"]:
        state = (state + 0x9e3779b97f4a7c15) & mask
        z = state
        z = ((z ^ (z >> 30)) * 0xbf58476d1ce4e5b9) & mask
        z = ((z ^ (z >> 27)) * 0x94d049bb133111eb) & mask
        actual = z ^ (z >> 31)
        assert f"{actual:016x}" == expected
    for vector in vectors["replication_seed_vectors"]:
        payload = b"dt-rep-v1\0" + vectors["base_seed"].to_bytes(8, "little") + vector["index"].to_bytes(4, "little")
        seed = int.from_bytes(hashlib.sha256(payload).digest()[:8], "little")
        assert f"{seed:016x}" == vector["seed_hex_le_value"]
    replication_seed = int(vectors["stream_replication_seed"], 16)
    assert replication_seed == int(next(v for v in vectors["replication_seed_vectors"] if v["index"] == 0)["seed_hex_le_value"], 16)
    for vector in vectors["stream_seed_vectors"]:
        node, purpose = vector["node_id"].encode(), vector["purpose"].encode()
        payload = (b"dt-stream-v1\0" + replication_seed.to_bytes(8, "little") + len(node).to_bytes(4, "little") + node
                   + len(purpose).to_bytes(4, "little") + purpose)
        seed = int.from_bytes(hashlib.sha256(payload).digest()[:8], "little")
        assert f"{seed:016x}" == vector["seed_hex"], vector
        state = seed
        for expected in vector["first_uniforms"]:
            state = (state + 0x9e3779b97f4a7c15) & mask
            z = state
            z = ((z ^ (z >> 30)) * 0xbf58476d1ce4e5b9) & mask
            z = ((z ^ (z >> 27)) * 0x94d049bb133111eb) & mask
            u = (((z ^ (z >> 31)) >> 12) + 0.5) / 2**52
            assert u == expected and 0 < u < 1, vector
    report = {
        "status": "PASS_REFERENCE_ARITHMETIC_ONLY",
        "deterministic_fixture_cases": len(cases),
        "expected_value_comparisons": assertions,
        "splitmix64_vectors": len(vectors["outputs_hex"]),
        "replication_seed_vectors": len(vectors["replication_seed_vectors"]),
        "stream_seed_vectors": len(vectors["stream_seed_vectors"]),
        "go_des_engine": "NOT_RUN",
        "simpy_differential": "NOT_RUN",
        "jcs_hash_verification": "NOT_RUN",
    }
    (ROOT / "checks/reference-arithmetic.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
