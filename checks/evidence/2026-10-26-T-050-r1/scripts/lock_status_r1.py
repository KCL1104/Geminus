#!/usr/bin/env python3
"""T-050 review round 1: the two des-go-0.5.0 locks' status text says which
cases they hold now (only the status member changes; the digests are added
by add_lock_entries.py)."""
import json
import pathlib
import sys

repo = pathlib.Path(sys.argv[1])
NOTE = (" G-BT21, G-SU11 and G-SU12 were added in T-050 review round 1 (findings L1, M4 and M3: a batch process without resources, "
        "a changeover starting exactly at W, a changeover ending after the run's last observation); each entry was taken after the case's "
        "hand-derived expectations passed (checks/evidence/2026-10-26-T-050-r1/first-run-new-goldens.log), the other entries are unchanged "
        "(checks/evidence/2026-10-26-T-050-r1/scripts/add_lock_entries.py refuses otherwise).")

f = repo / "testdata/simulation/kernel-observation-digests-0.5.0.json"
d = json.loads(f.read_text())
s = d["status"]
old = "the validator accepts (all but G-BT13)"
assert s.count(old) == 1 and "review round 1" not in s
s = s.replace(old, "the validator accepts (all but G-BT13, and G-BT18, G-BT19, G-BT20 and G-BT22 of review round 1, which run past the validator)")
d["status"] = s + NOTE
f.write_text(json.dumps(d, indent=2, ensure_ascii=False) + "\n")

f = repo / "testdata/simulation/engine-golden-digests-0.5.0.json"
d = json.loads(f.read_text())
s = d["status"]
old = "batch-goldens.json (15) and setup-goldens.json (8)"
assert s.count(old) == 1 and "review round 1" not in s
d["status"] = s.replace(old, "batch-goldens.json (16) and setup-goldens.json (10)") + NOTE
f.write_text(json.dumps(d, indent=2, ensure_ascii=False) + "\n")
print("status updated")
