"""Two more mutants after the bundled-JSON finding; appends to the evidence file."""
import subprocess
import sys
from pathlib import Path

web = Path(sys.argv[1])
evidence = Path(sys.argv[2])
cases = [
    ("a fixture's JSON bundled into the review page", "app/ui-gallery/results/preview.tsx", 'import { useState } from "react";', 'import { useState } from "react";\nimport model from "../../../features/results/fixtures/board-line-model.json";\nvoid model;'),
    ("the review page passes other data than the files hold", "app/ui-gallery/results/page.tsx", "return { model, results: { baseline: baselineResult, scenario: scenarioResult }", "return { model, results: { baseline: scenarioResult, scenario: scenarioResult }"),
]
lines = ["", "After the bundled-JSON finding (tests/results-preview.test.tsx):"]
for name, f, old, new in cases:
    p = web / f
    o = p.read_text()
    assert o.count(old) == 1, name
    p.write_text(o.replace(old, new))
    try:
        r = subprocess.run(["npx", "vitest", "run", "tests/results-preview.test.tsx"], cwd=web, capture_output=True, text=True)
    finally:
        p.write_text(o)
    lines.append(("killed" if r.returncode else "SURVIVED") + ": " + name)
with evidence.open("a") as fh:
    fh.write("\n".join(lines) + "\n")
print("\n".join(lines))
