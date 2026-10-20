#!/usr/bin/env python3
"""Independent reference for the engine's numerical profile (T-020).

Re-implements, without sharing code with the Go engine, SplitMix64, seed
derivation, tick quantization, the four samplers, the event order and the
source-arrival process (docs/SIMULATION_SPEC.md §2 and §4), and writes or
checks testdata/simulation/engine-vectors.json. The Go tests must reproduce
every vector exactly. This is NOT a DES engine or a SimPy differential test.

It shares one thing with the engine by design: the numerical profile. The
natural logarithm is the pinned algorithm below, not the platform's log, so
equality is bit-exact on every input rather than on the few draws stored; the
large-sample digests check that over 10^6 log inputs and 10^5 draws per
sampler. Agreement shows both implement the same profile; it is not evidence
that the profile is statistically right (that is the SimPy reference's job,
scripts/simpy_reference.py, from T-047).

    python scripts/engine_reference.py          # check the stored vectors
    python scripts/engine_reference.py --write  # regenerate them
"""
import hashlib
import heapq
import json
import math
import struct
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


def bits(x: float) -> int:
    return struct.unpack("<Q", struct.pack("<d", x))[0]


def from_bits(b: int) -> float:
    return struct.unpack("<d", struct.pack("<Q", b))[0]


# pinned_log is Go's pure-Go math.log (src/math/log.go, BSD-3-Clause, see
# docs/THIRD_PARTY_NOTICES.md,
# Copyright 2009 The Go Authors), itself a simpler version of FreeBSD's
# /usr/src/lib/msun/src/e_log.c, which came with this notice:
#
# ====================================================
# Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
#
# Developed at SunPro, a Sun Microsystems, Inc. business.
# Permission to use, copy, modify, and distribute this
# software is freely granted, provided that this notice
# is preserved.
# ====================================================
#
# CPython float operations round every step to binary64 and never fuse, which
# is the profile the Go engine enforces with explicit conversions.
LN2_HI, LN2_LO = from_bits(0x3FE62E42FEE00000), from_bits(0x3DEA39EF35793C76)
L1, L2, L3, L4 = (from_bits(b) for b in (0x3FE5555555555593, 0x3FD999999997FA04, 0x3FD2492494229359, 0x3FCC71C51D8E78AF))
L5, L6, L7 = (from_bits(b) for b in (0x3FC7466496CB03DE, 0x3FC39A09D078C69F, 0x3FC2F112DF3E5244))
SQRT_HALF = from_bits(0x3FE6A09E667F3BCD)


def pinned_log(x: float) -> float:
    if math.isnan(x) or x == math.inf:
        return x
    if x < 0:
        return math.nan
    if x == 0:
        return -math.inf
    f1, ki = math.frexp(x)
    if f1 < SQRT_HALF:
        f1 *= 2
        ki -= 1
    f = f1 - 1
    k = float(ki)
    s = f / (2 + f)
    s2 = s * s
    s4 = s2 * s2
    t1 = s2 * (L1 + s4 * (L3 + s4 * (L5 + s4 * L7)))
    t2 = s4 * (L2 + s4 * (L4 + s4 * L6))
    r = t1 + t2
    hfsq = 0.5 * f * f
    return k * LN2_HI - ((hfsq - (s * (hfsq + r) + k * LN2_LO)) - f)


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


class Sampler:
    """One distribution: draw(rng) returns ticks. A continuous sampler also
    has value(u), the duration in the model unit before quantization."""

    def __init__(self, dist: dict, unit: str, empirical_unit: str | None = None) -> None:
        self.kind, self.unit, self.fixed, self.value = dist["type"], unit, None, None
        if self.kind == "constant":
            self.fixed = ticks(dist["value"], unit)
        elif self.kind == "exponential":
            mean = dist["mean"]
            self.value = lambda u: -mean * pinned_log(1 - u)
        elif self.kind == "triangular":
            lo, mode, hi = dist["min"], dist["mode"], dist["max"]
            if lo == hi:
                self.fixed = ticks(lo, unit)
            else:
                width = hi - lo

                def tri(u: float) -> float:
                    if u < (mode - lo) / width:
                        v = lo + math.sqrt(u * width * (mode - lo))
                    else:
                        v = hi - math.sqrt((1 - u) * width * (hi - mode))
                    return min(max(v, lo), hi)  # rounding can step outside [min, max]
                self.value = tri
        elif self.kind == "empirical":
            self.values = [ticks(s, empirical_unit) for s in dist["samples"]]
        else:
            raise ValueError(self.kind)

    def ticks_of(self, v: float) -> int:
        t = quantize(v, self.unit)
        return max(t, 1) if self.kind == "exponential" else t

    def draw(self, rng: SplitMix64) -> int:
        if self.fixed is not None:
            return self.fixed
        u = rng.uniform()
        if self.kind == "empirical":
            return self.values[min(int(u * len(self.values)), len(self.values) - 1)]
        return self.ticks_of(self.value(u))


def sampler(dist: dict, unit: str, empirical_unit: str | None = None):
    return Sampler(dist, unit, empirical_unit).draw


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
DIGEST_DRAWS = 100_000
LOG_INPUTS = 1_000_000
LOG_SEED = 0x20261015
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
                    "state_after": f"{rng.state:016x}", **sampler_digest(dist, case, rep, i)})
    return out


def sampler_digest(dist: dict, case: dict, rep: int, i: int) -> dict:
    """SHA-256 over DIGEST_DRAWS draws on a fresh stream: per draw the tick
    count (int64 LE) and, for continuous samplers, the unquantized value's
    binary64 bits (LE). Any change to a formula, operation order or the log
    changes it, even when no tick changes."""
    s = Sampler(dist, case["unit"], case.get("samples_unit"))
    rng = stream(rep, f"node_{i}", "service")
    h = hashlib.sha256()
    for _ in range(DIGEST_DRAWS):
        if s.value is None:
            t = s.draw(rng)
            h.update(t.to_bytes(8, "little", signed=True))
            continue
        v = s.value(rng.uniform())
        h.update(s.ticks_of(v).to_bytes(8, "little", signed=True) + bits(v).to_bytes(8, "little"))
    return {"digest_draws": DIGEST_DRAWS, "digest": h.hexdigest(), "digest_state_after": f"{rng.state:016x}"}


# Edge inputs of the logarithm: the ends of 1-U, both sides of the sqrt(1/2)
# reduction boundary, and exact powers of two. sqrt(1/2)*2^32 is the one
# x = sqrt(1/2)*2^k whose result changes if the boundary test becomes <=.
LOG_EDGES = [2.0**-53, 1 - 2.0**-53, 0.5, 0.25, 1.0, SQRT_HALF, from_bits(bits(SQRT_HALF) - 1),
             from_bits(bits(SQRT_HALF) + 1), from_bits(bits(1.0) - 1), 0.1, 0.9, 1e-10, 2.0**-1022,
             math.ldexp(SQRT_HALF, 32)]
LOG_CONSTANTS = {"ln2_hi": LN2_HI, "ln2_lo": LN2_LO, "l1": L1, "l2": L2, "l3": L3, "l4": L4, "l5": L5, "l6": L6,
                 "l7": L7, "sqrt_half": SQRT_HALF}


def log_profile() -> dict:
    """pinned_log on LOG_INPUTS values 1-U from SplitMix64(LOG_SEED), hashed
    as binary64 bits (LE), plus exact edge vectors."""
    rng = SplitMix64(LOG_SEED)
    h = hashlib.sha256()
    for _ in range(LOG_INPUTS):
        h.update(bits(pinned_log(1 - rng.uniform())).to_bytes(8, "little"))
    return {"seed": f"{LOG_SEED:016x}", "inputs": LOG_INPUTS, "digest": h.hexdigest(),
            "constants": {k: f"{bits(v):016x}" for k, v in LOG_CONSTANTS.items()},
            "edges": [{"x": f"{bits(x):016x}", "log": f"{bits(pinned_log(x)):016x}"} for x in LOG_EDGES]}


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
    # A source that reached max_entities, or whose next arrival fell after the
    # horizon, has drawn exactly as often as these states say.
    return out, {s["id"]: f"{rngs[s['id']].state:016x}" for s in sources}


TRACE_PREFIX = b"dt-trace-v1\0"
TRACE_RATE = 0.125  # an entity is sampled when its hash < 2^64 / 8


def trace_selected(entity_id: str) -> bool:
    """Trace sampling (SIMULATION_SPEC §9, D-039 PROPOSED): SHA-256 of the
    prefix and the entity ID's UTF-8 bytes; the first 8 bytes, little-endian,
    below 2^64 * rate. No RNG stream is consumed."""
    h = hashlib.sha256(TRACE_PREFIX + entity_id.encode()).digest()
    return int.from_bytes(h[:8], "little") < (1 << 64) // 8


TRACE_IDS = [("r0/arrival/", 100), ("r12/a_src.x/", 40)]


def trace_selection() -> dict:
    return {"prefix": "dt-trace-v1\\0", "rate": TRACE_RATE,
            "cases": [{"prefix": prefix, "count": n, "selected": [k for k in range(1, n + 1) if trace_selected(f"{prefix}{k}")]}
                      for prefix, n in TRACE_IDS]}


def build() -> dict:
    expected, states = arrivals()
    return {
        "status": "INDEPENDENT_PYTHON_REFERENCE (scripts/engine_reference.py); the Go engine must match exactly",
        "replication_seed": {"base_seed": REP_SEED_INDEX[0], "index": REP_SEED_INDEX[1]},
        "beyond_cap_ticks": BEYOND_CAP,
        "sampler_vectors": sampler_vectors(),
        "log_profile": log_profile(),
        "trace_selection": trace_selection(),
        "event_order": {"pushes": [{"tick": t, "priority": PRIORITY[p]} for t, p in EVENT_PUSHES], "pop_sequence": event_order()},
        "arrivals": {"run": ARRIVAL_RUN, "sources": ARRIVAL_SOURCES, "expected": expected, "stream_states_after": states},
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
                      "event_pushes": len(EVENT_PUSHES), "arrivals": len(data["arrivals"]["expected"]),
                      "log_inputs": LOG_INPUTS, "digest_draws_per_sampler": DIGEST_DRAWS}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
