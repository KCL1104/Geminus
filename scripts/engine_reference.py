#!/usr/bin/env python3
"""Independent reference for the engine's numerical profile (T-020).

Re-implements, without sharing code with the Go engine, SplitMix64, seed
derivation, tick quantization, the four samplers, the event order and the
source-arrival process (docs/SIMULATION_SPEC.md §2 and §4), and writes or
checks testdata/simulation/engine-vectors.json. The Go tests must reproduce
every vector exactly. This is NOT a DES engine or a SimPy differential test.

    python scripts/engine_reference.py          # check the stored vectors
    python scripts/engine_reference.py --write  # regenerate them
"""
import hashlib
import heapq
import json
import math
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
VECTORS = ROOT / "testdata/simulation/engine-vectors.json"
MASK = (1 << 64) - 1
UNIT_MS = {"millisecond": 1, "second": 1000, "minute": 60_000, "hour": 3_600_000, "day": 86_400_000}
MAX_TICKS = 365 * 86_400_000
BEYOND_CAP = MAX_TICKS + 1
PRIORITY = {"service_complete": 0, "immediate": 10, "dispatch": 20}


class SplitMix64:
    def __init__(self, state: int) -> None:
        self.state = state

    def next(self) -> int:
        self.state = (self.state + 0x9E3779B97F4A7C15) & MASK
        z = self.state
        z = ((z ^ (z >> 30)) * 0xBF58476D1CE4E5B9) & MASK
        z = ((z ^ (z >> 27)) * 0x94D049BB133111EB) & MASK
        return z ^ (z >> 31)

    def uniform(self) -> float:
        return ((self.next() >> 12) + 0.5) / 2**52


def replication_seed(base: int, index: int) -> int:
    payload = b"dt-rep-v1\0" + base.to_bytes(8, "little") + index.to_bytes(4, "little")
    return int.from_bytes(hashlib.sha256(payload).digest()[:8], "little")


def stream(rep_seed: int, node: str, purpose: str) -> SplitMix64:
    n, p = node.encode(), purpose.encode()
    payload = (b"dt-stream-v1\0" + rep_seed.to_bytes(8, "little") + len(n).to_bytes(4, "little") + n
               + len(p).to_bytes(4, "little") + p)
    return SplitMix64(int.from_bytes(hashlib.sha256(payload).digest()[:8], "little"))


def round_half_up(x: float) -> int:
    # Exact for x >= 0 at these magnitudes: x - floor(x) has no rounding error.
    f = math.floor(x)
    return f + 1 if x - f >= 0.5 else f


def ticks(value: float, unit: str) -> int:
    """Input quantizer (model.Ticks): refuses values over the cap."""
    t = round_half_up(value * UNIT_MS[unit])
    assert t <= MAX_TICKS, (value, unit)
    return 1 if t == 0 and value > 0 else t


def quantize(d: float, unit: str) -> int:
    """Sample quantizer: like ticks(), but saturates beyond the cap."""
    t = round_half_up(d * UNIT_MS[unit])
    if t > MAX_TICKS:
        return BEYOND_CAP
    return 1 if t == 0 and d > 0 else t


def sampler(dist: dict, unit: str, empirical_unit: str | None = None):
    kind = dist["type"]
    if kind == "constant":
        v = ticks(dist["value"], unit)
        return lambda rng: v
    if kind == "exponential":
        mean = dist["mean"]
        return lambda rng: max(quantize(-mean * math.log(1 - rng.uniform()), unit), 1)
    if kind == "triangular":
        lo, mode, hi = dist["min"], dist["mode"], dist["max"]
        if lo == hi:
            v = ticks(lo, unit)
            return lambda rng: v
        width = hi - lo

        def tri(rng):
            u = rng.uniform()
            if u < (mode - lo) / width:
                return quantize(lo + math.sqrt(u * width * (mode - lo)), unit)
            return quantize(hi - math.sqrt((1 - u) * width * (hi - mode)), unit)
        return tri
    if kind == "empirical":
        values = [ticks(s, empirical_unit) for s in dist["samples"]]
        return lambda rng: values[min(int(rng.uniform() * len(values)), len(values) - 1)]
    raise ValueError(kind)


SAMPLER_CASES = [
    {"name": "constant draws nothing", "unit": "minute", "distribution": {"type": "constant", "value": 5}},
    {"name": "exponential template interarrival", "unit": "minute", "distribution": {"type": "exponential", "mean": 48}},
    {"name": "exponential sub-tick mean clamps to 1", "unit": "millisecond", "distribution": {"type": "exponential", "mean": 0.0004}},
    {"name": "exponential underflow is still 1 tick", "unit": "millisecond", "distribution": {"type": "exponential", "mean": 5e-324}},
    {"name": "exponential tail beyond the cap", "unit": "day", "distribution": {"type": "exponential", "mean": 300}},
    {"name": "triangular", "unit": "hour", "distribution": {"type": "triangular", "min": 1, "mode": 2, "max": 4}},
    {"name": "triangular min=mode=0", "unit": "minute", "distribution": {"type": "triangular", "min": 0, "mode": 0, "max": 10}},
    {"name": "triangular mode=max", "unit": "second", "distribution": {"type": "triangular", "min": 2, "mode": 7, "max": 7}},
    {"name": "triangular degenerate draws nothing", "unit": "minute", "distribution": {"type": "triangular", "min": 3, "mode": 3, "max": 3}},
    {"name": "empirical", "unit": "minute", "distribution": {"type": "empirical", "samples_ref": "dataset:review_duration"},
     "samples": [0, 240, 360.5], "samples_unit": "minute"},
]
DRAWS = 12
REP_SEED_INDEX = (20260415, 3)


def sampler_vectors() -> list[dict]:
    rep = replication_seed(*REP_SEED_INDEX)
    out = []
    for i, case in enumerate(SAMPLER_CASES):
        dist = dict(case["distribution"])
        if dist["type"] == "empirical":
            dist["samples"] = case["samples"]
        rng = stream(rep, f"node_{i}", "service")
        draw = sampler(dist, case["unit"], case.get("samples_unit"))
        values = [draw(rng) for _ in range(DRAWS)]
        out.append({**case, "node_id": f"node_{i}", "purpose": "service", "expected_ticks": values,
                    "state_after": f"{rng.state:016x}"})
    return out


EVENT_PUSHES = [
    (5, "immediate"), (5, "dispatch"), (3, "immediate"), (5, "service_complete"), (5, "immediate"),
    (3, "dispatch"), (0, "immediate"), (5, "service_complete"), (3, "service_complete"), (5, "dispatch"),
]


def event_order() -> list[int]:
    h = []
    for seq, (tick, pri) in enumerate(EVENT_PUSHES, start=1):
        heapq.heappush(h, (tick, PRIORITY[pri], seq))
    return [heapq.heappop(h)[2] for _ in range(len(h))]


ARRIVAL_SOURCES = [  # listed out of ID order on purpose: initialization sorts by ID
    {"id": "b_src", "distribution": {"type": "exponential", "mean": 30}, "max_entities": 6},
    {"id": "a_src", "distribution": {"type": "constant", "value": 45}},
    {"id": "c_src", "distribution": {"type": "triangular", "min": 10, "mode": 20, "max": 90}, "max_entities": 3},
]
ARRIVAL_RUN = {"base_seed": 7, "replication_index": 1, "unit": "minute", "horizon": {"value": 180, "unit": "minute"}}


def arrivals() -> list[list]:
    """Sources only: each arrival creates entity r<rep>/<source>/<k> and
    schedules the next one; events after the horizon are never scheduled."""
    rep_index = ARRIVAL_RUN["replication_index"]
    rep = replication_seed(ARRIVAL_RUN["base_seed"], rep_index)
    horizon = ticks(ARRIVAL_RUN["horizon"]["value"], ARRIVAL_RUN["horizon"]["unit"])
    sources = sorted(ARRIVAL_SOURCES, key=lambda s: s["id"].encode())
    draws = {s["id"]: sampler(s["distribution"], ARRIVAL_RUN["unit"]) for s in sources}
    rngs = {s["id"]: stream(rep, s["id"], "arrival") for s in sources}
    limit = {s["id"]: s.get("max_entities") for s in sources}
    count = {s["id"]: 0 for s in sources}
    heap, seq = [], 0

    def schedule_next(src: str, now: int) -> None:
        nonlocal seq
        if limit[src] is not None and count[src] >= limit[src]:
            return
        at = now + draws[src](rngs[src])
        if at <= horizon:
            seq += 1
            heapq.heappush(heap, (at, PRIORITY["immediate"], seq, src))

    for s in sources:
        schedule_next(s["id"], 0)
    out = []
    while heap:
        at, _, _, src = heapq.heappop(heap)
        count[src] += 1
        out.append([at, f"r{rep_index}/{src}/{count[src]}"])
        schedule_next(src, at)
    return out


def build() -> dict:
    return {
        "status": "INDEPENDENT_PYTHON_REFERENCE (scripts/engine_reference.py); the Go engine must match exactly",
        "replication_seed": {"base_seed": REP_SEED_INDEX[0], "index": REP_SEED_INDEX[1]},
        "beyond_cap_ticks": BEYOND_CAP,
        "sampler_vectors": sampler_vectors(),
        "event_order": {"pushes": [{"tick": t, "priority": PRIORITY[p]} for t, p in EVENT_PUSHES], "pop_sequence": event_order()},
        "arrivals": {"run": ARRIVAL_RUN, "sources": ARRIVAL_SOURCES, "expected": arrivals()},
    }


def main() -> int:
    data = build()
    text = json.dumps(data, indent=2) + "\n"
    if "--write" in sys.argv[1:]:
        VECTORS.write_text(text)
        print(f"wrote {VECTORS.relative_to(ROOT)}")
        return 0
    if not VECTORS.exists() or VECTORS.read_text() != text:
        print(json.dumps({"status": "FAIL", "reason": f"{VECTORS.relative_to(ROOT)} differs from the reference; rerun with --write and review"}))
        return 1
    print(json.dumps({"status": "PASS_ENGINE_REFERENCE_VECTORS", "sampler_cases": len(data["sampler_vectors"]),
                      "event_pushes": len(EVENT_PUSHES), "arrivals": len(data["arrivals"]["expected"])}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
