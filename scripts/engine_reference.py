#!/usr/bin/env python3
"""Independent reference for the engine's numerical profile (T-020).

Re-implements, without sharing code with the Go engine, SplitMix64, seed
derivation, tick quantization, the samplers (constant, exponential,
triangular, empirical; lognormal and Weibull since T-048, D-056), the event
order (with priority 15, D-048), the source-arrival process, the
breakdown streams (D-051) and the release-schedule profile (D-053, T-049:
Λ⁻¹ with the correction of r, whole poisson and deterministic arrival runs)
(docs/SIMULATION_SPEC.md §2 and §4), and writes or
checks testdata/simulation/engine-vectors.json. The Go tests must reproduce
every vector exactly. This is NOT a DES engine or a SimPy differential test.

It shares one thing with the engine by design: the numerical profile. The
natural logarithm, the exponential and AS241's inverse normal CDF are the
pinned algorithms below, not the platform's functions, so equality is
bit-exact on every input rather than on the few draws stored; the
large-sample digests check that over 10^6 inputs each and 10^5 draws per
sampler. Agreement shows both implement the same profile; it is not evidence
that the profile is statistically right (that is the SimPy reference's job,
scripts/simpy_reference.py, from T-047, and the Go tests of the lognormal
and Weibull samplers against their CDFs, T-048).

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


# pinned_exp is Go's pure-Go math.exp (src/math/exp.go, BSD-3-Clause, see
# docs/THIRD_PARTY_NOTICES.md, Copyright 2009 The Go Authors), a simplified
# version of FreeBSD's /usr/src/lib/msun/src/e_exp.c, which came with this
# notice:
#
# ====================================================
# Copyright (C) 2004 by Sun Microsystems, Inc. All rights reserved.
#
# Permission to use, copy, modify, and distribute this
# software is freely granted, provided that this notice
# is preserved.
# ====================================================
#
# Every operation is one binary64 rounding (the Go engine rounds each
# product explicitly, so no fused multiply-add); ldexp is exact or, for a
# subnormal result, one correct rounding in both languages (D-056, D-075).
EXP_LN2_HI, EXP_LN2_LO = 6.93147180369123816490e-01, 1.90821492927058770002e-10
EXP_LOG2E = 1.44269504088896338700e+00
EXP_OVERFLOW, EXP_UNDERFLOW = 7.09782712893383973096e+02, -7.45133219101941108420e+02
EXP_NEAR_ZERO = 2.0**-28
EXP_P = (1.66666666666666657415e-01, -2.77777777770155933842e-03, 6.61375632143793436117e-05,
         -1.65339022054652515390e-06, 4.13813679705723846039e-08)


def pinned_exp(x: float) -> float:
    if math.isnan(x):
        return x
    if x > EXP_OVERFLOW:
        return math.inf
    if x < EXP_UNDERFLOW:
        return 0.0
    if -EXP_NEAR_ZERO < x < EXP_NEAR_ZERO:
        return 1 + x
    k = int(EXP_LOG2E * x - 0.5) if x < 0 else int(EXP_LOG2E * x + 0.5)
    hi = x - k * EXP_LN2_HI
    lo = k * EXP_LN2_LO
    r = hi - lo
    t = r * r
    p1, p2, p3, p4, p5 = EXP_P
    c = r - t * (p1 + t * (p2 + t * (p3 + t * (p4 + t * p5))))
    y = 1 - ((lo - (r * c) / (2 - c)) - hi)
    return math.ldexp(y, k)


# ndtri is Wichura's algorithm AS241 (PPND16, Applied Statistics 37 (1988)
# 477-484), the inverse of the standard normal CDF, as R's qnorm writes it:
# Horner from the highest coefficient, one binary64 rounding per operation.
AS241_A = (3.3871328727963666080e0, 1.3314166789178437745e+2, 1.9715909503065514427e+3, 1.3731693765509461125e+4,
           4.5921953931549871457e+4, 6.7265770927008700853e+4, 3.3430575583588128105e+4, 2.5090809287301226727e+3)
AS241_B = (1.0, 4.2313330701600911252e+1, 6.8718700749205790830e+2, 5.3941960214247511077e+3,
           2.1213794301586595867e+4, 3.9307895800092710610e+4, 2.8729085735721942674e+4, 5.2264952788528545610e+3)
AS241_C = (1.42343711074968357734e0, 4.63033784615654529590e0, 5.76949722146069140550e0, 3.64784832476320460504e0,
           1.27045825245236838258e0, 2.41780725177450611770e-1, 2.27238449892691845833e-2, 7.74545014278341407640e-4)
AS241_D = (1.0, 2.05319162663775882187e0, 1.67638483018380384940e0, 6.89767334985100004550e-1,
           1.48103976427480074590e-1, 1.51986665636164571966e-2, 5.47593808499534494600e-4, 1.05075007164441684324e-9)
AS241_E = (6.65790464350110377720e0, 5.46378491116411436990e0, 1.78482653991729133580e0, 2.96560571828504891230e-1,
           2.65321895265761230930e-2, 1.24266094738807843860e-3, 2.71155556874348757815e-5, 2.01033439929228813265e-7)
AS241_F = (1.0, 5.99832206555887937690e-1, 1.36929880922735805310e-1, 1.48753612908506148525e-2,
           7.86869131145613259100e-4, 1.84631831751005468180e-5, 1.42151175831644588870e-7, 2.04426310338993978564e-15)


def horner(coefficients: tuple, r: float) -> float:
    v = coefficients[-1]
    for c in reversed(coefficients[:-1]):
        v = v * r + c
    return v


def ndtri(p: float) -> float:
    q = p - 0.5
    if abs(q) <= 0.425:
        r = 0.180625 - q * q
        return q * horner(AS241_A, r) / horner(AS241_B, r)
    r = p if q < 0 else 1 - p
    r = math.sqrt(-pinned_log(r))
    if r <= 5:
        r = r - 1.6
        v = horner(AS241_C, r) / horner(AS241_D, r)
    else:
        r = r - 5
        v = horner(AS241_E, r) / horner(AS241_F, r)
    return -v if q < 0 else v


def lognormal_params(mean: float, cv: float) -> tuple[float, float]:
    """D-056: sigma^2 = ln(1 + cv^2), mu = ln(mean) - sigma^2/2, with the
    pinned logarithm."""
    s2 = pinned_log(1 + cv * cv)
    return pinned_log(mean) - s2 / 2, math.sqrt(s2)


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
        elif self.kind == "lognormal":  # D-056: inverse CDF, one draw per sample
            mu, sigma = lognormal_params(dist["mean"], dist["cv"])
            self.value = lambda u: pinned_exp(mu + sigma * ndtri(u))
        elif self.kind == "weibull":  # D-056: scale * (-ln(1-U))^(1/shape) as exp(ln(y)/shape)
            shape, scale = dist["shape"], dist["scale"]
            self.value = lambda u: scale * pinned_exp(pinned_log(-pinned_log(1 - u)) / shape)
        else:
            raise ValueError(self.kind)

    def ticks_of(self, v: float) -> int:
        # D-033, D-056: an exponential, lognormal or Weibull sample is never 0 ticks.
        t = quantize(v, self.unit)
        return max(t, 1) if self.kind in ("exponential", "lognormal", "weibull") else t

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
    # T-048 (D-056): appended, so the cases above keep their node IDs and vectors.
    {"name": "lognormal repair time", "unit": "minute", "distribution": {"type": "lognormal", "mean": 30, "cv": 0.5}},
    {"name": "lognormal cv 10", "unit": "minute", "distribution": {"type": "lognormal", "mean": 4, "cv": 10}},
    {"name": "lognormal tiny mean is still 1 tick", "unit": "millisecond", "distribution": {"type": "lognormal", "mean": 1e-300, "cv": 1}},
    {"name": "lognormal tail beyond the cap", "unit": "day", "distribution": {"type": "lognormal", "mean": 300, "cv": 3}},
    {"name": "lognormal tiny cv", "unit": "second", "distribution": {"type": "lognormal", "mean": 90, "cv": 1e-9}},
    {"name": "weibull failure clock", "unit": "hour", "distribution": {"type": "weibull", "shape": 1.5, "scale": 8}},
    {"name": "weibull shape 0.1", "unit": "minute", "distribution": {"type": "weibull", "shape": 0.1, "scale": 2}},
    {"name": "weibull shape 20", "unit": "second", "distribution": {"type": "weibull", "shape": 20, "scale": 30}},
    {"name": "weibull shape 1", "unit": "minute", "distribution": {"type": "weibull", "shape": 1, "scale": 48}},
    {"name": "weibull tiny scale is still 1 tick", "unit": "millisecond", "distribution": {"type": "weibull", "shape": 3, "scale": 1e-300}},
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


# T-048 (D-056): the pinned exp and AS241 like the logarithm above, bit for
# bit on 10^6 inputs each and on edge inputs: overflow and underflow, the
# near-zero shortcut, subnormal results, the reduction's rounding of k;
# AS241 near 0, 0.5 and 1 and at its two branch boundaries.
EXP_INPUTS = 1_000_000
EXP_SEED = 0x20261025
SUBNORMAL_EXP = -708.3964185322641  # exp is about 2^-1022 here
EXP_EDGES = [0.0, -0.0, 1.0, -1.0, 0.5, -0.5, 10.0, -10.0, 2.0**-28, -(2.0**-28), from_bits(bits(2.0**-28) - 1),
             -from_bits(bits(2.0**-28) - 1), EXP_OVERFLOW, from_bits(bits(EXP_OVERFLOW) + 1), from_bits(bits(EXP_OVERFLOW) - 1),
             EXP_UNDERFLOW, -from_bits(bits(-EXP_UNDERFLOW) + 1), SUBNORMAL_EXP, -720.0, -740.0, -745.0,
             0.34657359027997264, -0.34657359027997264, 1.5 * 0.6931471805599453, -2.5 * 0.6931471805599453, 1e-300,
             36.04365338911715, -36.7368005696771]
EXP_CONSTANTS = {"ln2_hi": EXP_LN2_HI, "ln2_lo": EXP_LN2_LO, "log2e": EXP_LOG2E, "overflow": EXP_OVERFLOW,
                 "underflow": EXP_UNDERFLOW, "near_zero": EXP_NEAR_ZERO, "p1": EXP_P[0], "p2": EXP_P[1], "p3": EXP_P[2],
                 "p4": EXP_P[3], "p5": EXP_P[4]}


def exp_profile() -> dict:
    """pinned_exp on EXP_INPUTS values (U - 0.5) * 2^(i mod 11), so from
    near 0 to +-512, hashed as binary64 bits (LE), plus the edges."""
    rng = SplitMix64(EXP_SEED)
    h = hashlib.sha256()
    for i in range(EXP_INPUTS):
        h.update(bits(pinned_exp((rng.uniform() - 0.5) * 2.0 ** (i % 11))).to_bytes(8, "little"))
    return {"seed": f"{EXP_SEED:016x}", "inputs": EXP_INPUTS, "digest": h.hexdigest(),
            "constants": {k: f"{bits(v):016x}" for k, v in EXP_CONSTANTS.items()},
            "edges": [{"x": f"{bits(x):016x}", "exp": f"{bits(pinned_exp(x)):016x}"} for x in EXP_EDGES]}


NDTRI_INPUTS = 1_000_000
NDTRI_SEED = 0x20261026
U_MIN, U_MAX = 2.0**-53, 1 - 2.0**-53  # SplitMix64's (0, 1) mapping, both ends
NDTRI_EDGES = [U_MIN, U_MAX, 0.5, from_bits(bits(0.5) + 1), from_bits(bits(0.5) - 1), 0.075, from_bits(bits(0.075) + 1),
               from_bits(bits(0.075) - 1), 0.925, from_bits(bits(0.925) + 1), from_bits(bits(0.925) - 1),
               math.exp(-25.0), from_bits(bits(math.exp(-25.0)) + 1), 1 - math.exp(-25.0), 1e-10, 1 - 1e-10, 0.3, 0.9, 0.975, 0.025,
               1e-300, 2.0**-1074]
AS241_CONSTANTS = {f"{name}{i}": v for name, cs in (("a", AS241_A), ("b", AS241_B), ("c", AS241_C), ("d", AS241_D),
                                                     ("e", AS241_E), ("f", AS241_F)) for i, v in enumerate(cs)}


def ndtri_profile() -> dict:
    rng = SplitMix64(NDTRI_SEED)
    h = hashlib.sha256()
    for _ in range(NDTRI_INPUTS):
        h.update(bits(ndtri(rng.uniform())).to_bytes(8, "little"))
    return {"seed": f"{NDTRI_SEED:016x}", "inputs": NDTRI_INPUTS, "digest": h.hexdigest(),
            "constants": {k: f"{bits(v):016x}" for k, v in AS241_CONSTANTS.items()},
            "edges": [{"p": f"{bits(p):016x}", "ndtri": f"{bits(ndtri(p)):016x}"} for p in NDTRI_EDGES]}


def lognormal_vectors() -> list[dict]:
    """mu and sigma of every lognormal sampler case, by bit pattern."""
    out = []
    for case in SAMPLER_CASES:
        d = case["distribution"]
        if d["type"] == "lognormal":
            mu, sigma = lognormal_params(d["mean"], d["cv"])
            out.append({"mean": d["mean"], "cv": d["cv"], "mu": f"{bits(mu):016x}", "sigma": f"{bits(sigma):016x}"})
    return out


# D-051: unit u of a resource draws its times to failure and to repair from
# its own streams (resource ID, "ttf/<u>") and (resource ID, "ttr/<u>").
BREAKDOWN_STREAMS = {"resource_id": "press", "capacity": 3, "unit": "minute",
                     "time_to_failure": {"type": "weibull", "shape": 1.5, "scale": 600},
                     "time_to_repair": {"type": "lognormal", "mean": 30, "cv": 0.5}}


def breakdown_streams() -> dict:
    rep = replication_seed(*REP_SEED_INDEX)
    b = BREAKDOWN_STREAMS
    units = []
    for u in range(b["capacity"]):
        entry = {"unit": u}
        for purpose, member in (("ttf", "time_to_failure"), ("ttr", "time_to_repair")):
            rng = stream(rep, b["resource_id"], f"{purpose}/{u}")
            draw = sampler(b[member], b["unit"])
            entry[purpose] = [draw(rng) for _ in range(DRAWS)]
            entry[purpose + "_state_after"] = f"{rng.state:016x}"
        units.append(entry)
    return {**b, "draws": DRAWS, "units": units}


# D-055 (T-050; review round 1 M3): unit u of a resource that changes over
# draws its changeover times from its own stream (resource ID, "setup/<u>").
# G-SU5's resource, default and seed (testdata/simulation/setup-goldens.json),
# replication 0: TestSetupStreamVectors checks these draws against the
# stream itself and against the changeovers the kernel makes on G-SU5's
# model, so the stream's name is pinned outside the Go constant.
SETUP_STREAMS = {"resource_id": "smt", "capacity": 3, "unit": "minute", "base_seed": 4242, "replication": 0,
                 "default": {"type": "exponential", "mean": 3}}
SETUP_DRAWS = 20


def setup_streams() -> dict:
    s = SETUP_STREAMS
    rep = replication_seed(s["base_seed"], s["replication"])
    draw = sampler(s["default"], s["unit"])
    units = []
    for u in range(s["capacity"]):
        rng = stream(rep, s["resource_id"], f"setup/{u}")
        units.append({"unit": u, "setup": [draw(rng) for _ in range(SETUP_DRAWS)], "setup_state_after": f"{rng.state:016x}"})
    return {**s, "draws": SETUP_DRAWS, "units": units}


EVENT_PUSHES = [
    (5, "immediate"), (5, "dispatch"), (3, "immediate"), (5, "service_complete"), (5, "immediate"),
    (3, "dispatch"), (0, "immediate"), (5, "service_complete"), (3, "service_complete"), (5, "dispatch"),
]


# D-048: failures and repairs run at priority 15, after arrivals and delay
# completions (10), before the dispatch (20).
EVENT_PUSHES_15 = [(5, 15), (5, 20), (5, 10), (3, 15), (5, 0), (5, 15), (3, 20), (3, 10), (0, 15), (5, 10)]


def event_order_15() -> list[int]:
    h = []
    for seq, (tick, pri) in enumerate(EVENT_PUSHES_15, start=1):
        heapq.heappush(h, (tick, pri, seq))
    return [heapq.heappop(h)[2] for _ in range(len(h))]


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


# T-049 (D-053): release schedules. The rate profile of review L4, ported
# operation for operation from its text, not from the Go code.
class RateSchedule:
    def __init__(self, sched: dict, unit: str) -> None:
        self.unit, self.period, self.poisson = unit, float(sched["period"]), sched["process"] == "poisson"
        segs = sched["segments"]
        self.starts, self.ends, self.rates, self.cum_start, self.cum_end = [], [], [], [], []
        cum = 0.0
        for j, s in enumerate(segs):
            end = float(segs[j + 1]["start"]) if j + 1 < len(segs) else self.period
            self.starts.append(float(s["start"]))
            self.ends.append(end)
            self.rates.append(float(s["rate"]))
            self.cum_start.append(cum)
            cum = cum + self.rates[-1] * (end - self.starts[-1])  # one rounded product, then the sum
            self.cum_end.append(cum)
        self.total = cum  # P = Λ(period)

    def x(self, rng: SplitMix64) -> float:
        """X_k: 1 (deterministic, no draw) or -ln(1 - U) (poisson, one draw)."""
        return -pinned_log(1 - rng.uniform()) if self.poisson else 1.0

    def tau(self, a: float) -> dict:
        """Λ⁻¹(a): k = ceil(a/P) - 1, r = a - kP, corrected once into (0, P];
        the first segment with a positive rate and cumEnd >= r; τ clamped to
        the segment. Returns the steps, for the vectors."""
        k = float(math.ceil(a / self.total) - 1)
        r = a - k * self.total
        corrected = 0
        if r <= 0:
            k, r, corrected = k - 1, r + self.total, -1
        elif r > self.total:
            k, r, corrected = k + 1, r - self.total, 1
        j = next(i for i in range(len(self.rates)) if self.rates[i] > 0 and self.cum_end[i] >= r)
        base = k * self.period
        lo, hi = base + self.starts[j], base + self.ends[j]
        tau = min(max(lo + (r - self.cum_start[j]) / self.rates[j], lo), hi)
        return {"k": k, "r": r, "corrected": corrected, "segment": j, "tau": tau, "tick": quantize(tau, self.unit)}


RELEASE_CORRECTION = {"process": "deterministic", "period": 1, "segments": [{"start": 0, "rate": 0.3}]}
RELEASE_CORRECTION_A = [0.3, 0.6, 0.9, 1.2, 3.0, 30.0, 300.0, 502.5, 502.6, 1000.2, 12345.6, 0.1 + 0.2, 7 * 0.3, 299.99999999999994]
RELEASE_PROFILE = {"process": "deterministic", "period": 1440,
                   "segments": [{"start": 0, "rate": 0.3}, {"start": 333.3, "rate": 0}, {"start": 480, "rate": 0.7}, {"start": 1000.1, "rate": 0.1}]}
RELEASE_PROFILE_A = [0.5, 99.99, 99.990000000001, 100.0, 100.01, 300.0, 400.0, 400.5, 663.2, 707.5, 707.6, 1e4, 123456.789]
# G-R1's boundary rule with a rate-0 segment between two positive ones: a
# that ends the first segment exactly (r = cumEnd_0) is released at the
# first segment's end (the ceiling), not at the start of the third.
RELEASE_BOUNDARY = {"process": "deterministic", "period": 6, "segments": [{"start": 0, "rate": 1}, {"start": 3, "rate": 0}, {"start": 4, "rate": 1}]}
RELEASE_BOUNDARY_A = [1.0, 3.0, 3.5, 5.0, 8.0, 10.0, 13.0]
# The clamp: P = 0.1 x 3 rounds to 0.30000000000000004, so a = P gives
# r / rate = 3.0000000000000004, past the segment's end; τ is clamped to 3.
RELEASE_CLAMP = {"process": "deterministic", "period": 3, "segments": [{"start": 0, "rate": 0.1}]}
RELEASE_CLAMP_A = [0.1, 0.2, 0.30000000000000004, 0.6000000000000001]
RELEASE_RUNS = [
    {"id": "G-R2", "source": "src", "schedule": {"process": "poisson", "period": 1440,
                                                 "segments": [{"start": 0, "rate": 0.5}, {"start": 480, "rate": 0.4}, {"start": 960, "rate": 0.2}]},
     "unit": "minute", "base_seed": 4902, "replication_index": 2, "horizon": {"value": 2880, "unit": "minute"}},
    {"id": "G-R2-zero", "source": "line.b", "schedule": {"process": "poisson", "period": 60,
                                                        "segments": [{"start": 0, "rate": 0}, {"start": 12.5, "rate": 3.75}, {"start": 30, "rate": 0}, {"start": 45, "rate": 0.125}]},
     "unit": "minute", "base_seed": 7, "replication_index": 0, "horizon": {"value": 600, "unit": "minute"}},
    {"id": "G-R-takt", "source": "src", "schedule": RELEASE_PROFILE, "unit": "minute", "base_seed": 1, "replication_index": 0,
     "horizon": {"value": 4320, "unit": "minute"}},
    {"id": "G-R-fast", "source": "src", "schedule": {"process": "poisson", "period": 0.5,
                                                     "segments": [{"start": 0, "rate": 900}, {"start": 0.25, "rate": 100}]},
     "unit": "second", "base_seed": 11, "replication_index": 1, "horizon": {"value": 3, "unit": "second"}},
]


def release_run(run: dict) -> dict:
    """A source with a rate schedule alone: the k-th arrival at
    max(tick(Λ⁻¹(a_k)), the last + 1) (the first's floor tick 0), a_k
    unquantized; the draw of the arrival after the horizon is spent."""
    s = RateSchedule(run["schedule"], run["unit"])
    rng = stream(replication_seed(run["base_seed"], run["replication_index"]), run["source"], "arrival")
    horizon = ticks(run["horizon"]["value"], run["horizon"]["unit"])
    a, last, out = 0.0, -1, []
    while True:
        a += s.x(rng)
        at = max(s.tau(a)["tick"], last + 1)
        if at > horizon:
            break
        out.append(at)
        last = at
    return {**run, "total_bits": f"{bits(s.total):016x}", "arrival_ticks": out, "a_bits_after": f"{bits(a):016x}",
            "stream_state_after": f"{rng.state:016x}"}


def release_vectors() -> dict:
    def steps(sched: dict, unit: str, values: list) -> list:
        s = RateSchedule(sched, unit)
        out = []
        for a in values:
            v = s.tau(a)
            out.append({"a_bits": f"{bits(a):016x}", "k": v["k"], "r_bits": f"{bits(v['r']):016x}", "corrected": v["corrected"],
                        "segment": v["segment"], "tau_bits": f"{bits(v['tau']):016x}", "tick": v["tick"]})
        return out
    return {
        "correction": {"schedule": RELEASE_CORRECTION, "unit": "minute", "total_bits": f"{bits(RateSchedule(RELEASE_CORRECTION, 'minute').total):016x}",
                       "steps": steps(RELEASE_CORRECTION, "minute", RELEASE_CORRECTION_A)},
        "profile": {"schedule": RELEASE_PROFILE, "unit": "minute", "total_bits": f"{bits(RateSchedule(RELEASE_PROFILE, 'minute').total):016x}",
                    "steps": steps(RELEASE_PROFILE, "minute", RELEASE_PROFILE_A)},
        "boundary": {"schedule": RELEASE_BOUNDARY, "unit": "minute", "total_bits": f"{bits(RateSchedule(RELEASE_BOUNDARY, 'minute').total):016x}",
                     "steps": steps(RELEASE_BOUNDARY, "minute", RELEASE_BOUNDARY_A)},
        "clamp": {"schedule": RELEASE_CLAMP, "unit": "minute", "total_bits": f"{bits(RateSchedule(RELEASE_CLAMP, 'minute').total):016x}",
                  "steps": steps(RELEASE_CLAMP, "minute", RELEASE_CLAMP_A)},
        "runs": [release_run(r) for r in RELEASE_RUNS],
    }


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
        # T-048 (D-048, D-051, D-056), after the vectors above, which are unchanged.
        "exp_profile": exp_profile(),
        "ndtri_profile": ndtri_profile(),
        "lognormal_params": lognormal_vectors(),
        "breakdown_streams": breakdown_streams(),
        "event_order_resource_state": {"pushes": [{"tick": t, "priority": p} for t, p in EVENT_PUSHES_15],
                                       "pop_sequence": event_order_15()},
        # T-049 (D-053), after the vectors above, which are unchanged.
        "release_schedules": release_vectors(),
        # T-050 (D-055; review round 1 M3), after the vectors above, which are unchanged.
        "setup_streams": setup_streams(),
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
                      "log_inputs": LOG_INPUTS, "digest_draws_per_sampler": DIGEST_DRAWS,
                      "exp_inputs": EXP_INPUTS, "ndtri_inputs": NDTRI_INPUTS,
                      "breakdown_stream_units": len(data["breakdown_streams"]["units"]),
                      "setup_stream_units": len(data["setup_streams"]["units"]),
                      "release_steps": sum(len(data["release_schedules"][k]["steps"]) for k in ("correction", "profile", "boundary", "clamp")),
                      "release_arrivals": sum(len(r["arrival_ticks"]) for r in data["release_schedules"]["runs"])}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
