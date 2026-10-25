#!/usr/bin/env python3
"""T-049 review round 1 L2: does the independent Python implementation of
R2, R3, R4, next_fit, X_end and TTRmax (scripts/calendar_reference.py)
catch wrong Go rules? Each mutant is one exact search/replace applied with
`go test -overlay` (the working tree is never modified) while
TestCalendarRulesExport writes the Go results; then the script compares.
KILLED-BY-REFERENCE: the script found a difference; KILLED-BY-GO: the Go
test itself failed (e.g. Compile refusing what the validator accepts);
SURVIVED: neither. The M2 and L1 mutants (rv-r4-first-period-only,
rv-lcm-limit-strict) are the reviewer's; the others are from the reviewer's
and the T-049 harnesses, plus three for next_fit, X_end and TTRmax.

    PYTHON=/path/to/python python3 checks/evidence/2026-10-25-T-049-r1/t049-r1-l2-mutants.py [repo]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

REPO = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[3]
PY = os.environ.get("PYTHON", "python3")
VC = "internal/model/validate/calendars.go"
IRV = "internal/model/validate/ir.go"
CAL = "internal/model/calendar/calendar.go"
C = "internal/simulation/engine/compile.go"
S = "internal/simulation/engine/sample.go"
MUT = [
    # R4 (review round 1 M2) and the LCM limit (L1)
    ("rv-r4-first-period-only", VC, "\t\t\t\tspan, _ := calendar.LCM(p.Period(), partner.Period(), model.MaxDurationTicks)\n",
     "\t\t\t\tspan := max(p.Period(), partner.Period())\n"),
    ("rv-lcm-limit-strict", CAL, "\tif l := q * b; l <= limit {\n", "\tif l := q * b; l < limit {\n"),
    # R2
    ("rv-r2-bundle-period-max", VC, "\t\t\t\tperiod, _ = calendar.LCM(period, periods[r], model.MaxDurationTicks) // divides lcm\n",
     "\t\t\t\tperiod = max(period, periods[r])\n"),
    ("rv-r2-reads-exceptions", VC, "\t\tif _, fit := calendar.NextFit(bundle, 0, period-1, false); !fit {\n",
     "\t\tif _, fit := calendar.NextFit(bundle, 0, period-1, true); !fit {\n"),
    ("r2-off", VC, "\t\tif _, fit := calendar.NextFit(bundle, 0, period-1, false); !fit {\n",
     "\t\tif _, fit := calendar.NextFit(bundle, 0, period-1, false); false && !fit {\n"),
    ("lcm-unbounded", VC, "\t\tif lcm, ok = calendar.LCM(lcm, p, model.MaxDurationTicks); !ok {\n", "\t\tif lcm, ok = calendar.LCM(lcm, p, 1<<62); !ok {\n"),
    # R3
    ("r3-allows-two-units", IRV, "\t\t\tif q.Units != 1 {\n", "\t\t\tif q.Units > 2 {\n"),
    ("r3-allows-two-resources", IRV, "\t\tif len(found) > 1 {\n", "\t\tif len(found) > 2 {\n"),
    # R4
    ("r4-off", VC, "\t\t\tif len(short) > 0 {\n", "\t\t\tif false && len(short) > 0 {\n"),
    ("r4-reads-exceptions", VC, "\t\t\t\t\tfunc(t int64) (int64, bool) { return partner.NextBelow(t, o.Units, false) }); bad {\n",
     "\t\t\t\t\tfunc(t int64) (int64, bool) { return partner.NextBelow(t, o.Units, true) }); bad {\n"),
    ("rv-r4-partner-one-unit", VC, "return partner.NextBelow(t, o.Units, false) }); bad {", "return partner.NextBelow(t, 1, false) }); bad {"),
    ("rv-r4-skips-uncalendared-breakdown", VC, "\t\t\tif ir.Resources[r].Breakdowns == nil {\n\t\t\t\tcontinue\n",
     "\t\t\tif ir.Resources[r].Breakdowns == nil || ir.Resources[r].CalendarID == \"\" {\n\t\t\t\tcontinue\n"),
    ("r4-after-r3-refusal", VC, "\t\tif r3[i] {\n\t\t\tcontinue\n\t\t}\n", ""),
    # X_end, next_fit and TTRmax
    ("rv-xend-last-resource-only", C, "\t\t\t\t\tc.xEnd = max(c.xEnd, pat.ExceptionsEnd())\n", "\t\t\t\t\tc.xEnd = pat.ExceptionsEnd()\n"),
    ("rv-xend-exception-start", CAL, "\treturn p.exc[len(p.exc)-1].End\n}", "\treturn p.exc[len(p.exc)-1].Start\n}"),
    ("next-fit-skips-an-exception-end", CAL, "\t\t\tt = e.End\n\t\t\tcontinue\n", "\t\t\tt = e.End + 1\n\t\t\tcontinue\n"),
    ("next-fit-open-exception-is-off", CAL, "\t\t\tif e.Open {\n\t\t\t\tv = p.capacity\n\t\t\t}\n", ""),
    ("ttr-bound-of-triangular-is-mode", S, "\t\treturn quantize(s.max, s.unitMs)\n", "\t\treturn quantize(s.mode, s.unitMs)\n"),
]


def main():
    lines, ok = [], True
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new) in enumerate(MUT):
            src = (REPO / f).read_text()
            if src.count(old) != 1:
                lines.append(f"{name}\tBAD-MUTANT ({src.count(old)} matches)")
                ok = False
                print(lines[-1], flush=True)
                continue
            m = pathlib.Path(tmp) / f"m{i}.go"
            m.write_text(src.replace(old, new))
            ov = pathlib.Path(tmp) / f"m{i}.json"
            ov.write_text(json.dumps({"Replace": {str(REPO / f): str(m)}}))
            out = pathlib.Path(tmp) / f"go{i}.json"
            env = dict(os.environ, GEMINUS_CALENDAR_REFERENCE_OUT=str(out))
            g = subprocess.run(["go", "test", "-count=1", "-overlay", str(ov), "-run", "^TestCalendarRulesExport$", "./internal/simulation/engine/"],
                               cwd=REPO, capture_output=True, text=True, env=env)
            if "build failed" in g.stdout + g.stderr or "setup failed" in g.stdout + g.stderr:
                status, why = "BUILD-FAILED", ""
                ok = False
            elif g.returncode != 0:
                status = "KILLED-BY-GO"
                why = next((l.strip() for l in (g.stdout + g.stderr).splitlines() if "calendar_rules_export_test.go" in l), "")
            else:
                p = subprocess.run([PY, "-I", str(REPO / "scripts/calendar_reference.py"), "--go-results", str(out)],
                                   capture_output=True, text=True)
                text = p.stdout + p.stderr
                if p.returncode != 0:
                    status = "KILLED-BY-REFERENCE"
                    why = next((l for l in text.splitlines() if l.startswith("seed ")), text.splitlines()[-1] if text else "")
                else:
                    status, why = "SURVIVED", ""
                    ok = False
            lines.append(f"{name}\t{status}\t{why[:160]}")
            print(lines[-1], flush=True)
    killed = sum(1 for l in lines if "\tKILLED" in l)
    print(f"{killed}/{len(MUT)} killed")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
