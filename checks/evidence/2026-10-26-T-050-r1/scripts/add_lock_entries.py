#!/usr/bin/env python3
"""T-050 review round 1: add the new cases' entries to the two des-go-0.5.0
regression locks from the lock tests' own failure output
(locks-before-update.log), after their hand-derived goldens passed.

It refuses unless every case already in a lock has exactly its locked value
in that output (no existing digest changes) and the new IDs are exactly the
ones given; it only adds entries. Usage:

    add_lock_entries.py <log> <repo> <new id>...
"""
import json
import pathlib
import re
import sys

log, repo, new = pathlib.Path(sys.argv[1]).read_text(), pathlib.Path(sys.argv[2]), set(sys.argv[3:])

# engine: "got  [ID=count digest ID=count digest ...]"
got_line = next(l for l in log.splitlines() if l.strip().startswith("got  ["))
obs = {m[0]: (int(m[1]), m[2]) for m in re.findall(r"([A-Z][A-Z0-9-]+)=(\d+) ([0-9a-f]{64})", got_line)}
# des: "digests {...}"
dig_line = next(l for l in log.splitlines() if "lock050_test.go" in l and " digests {" in l)
res = json.loads(dig_line[dig_line.index("{"):])

okf = repo / "testdata/simulation/kernel-observation-digests-0.5.0.json"
ok = json.loads(okf.read_text())
locked_obs = {c["id"]: (c["observations"], c["sha256"]) for c in ok["cases"]}
assert set(obs) - set(locked_obs) == new, ("observation lock: new ids", set(obs) - set(locked_obs))
assert set(locked_obs) <= set(obs)
changed = [i for i in locked_obs if obs[i] != locked_obs[i]]
assert not changed, ("observation lock: existing entries changed", changed)

rf = repo / "testdata/simulation/engine-golden-digests-0.5.0.json"
rl = json.loads(rf.read_text())
new_res = set(res) - set(rl["digests"])
assert new_res <= new and set(rl["digests"]) <= set(res), ("result lock", new_res)
changed = [i for i in rl["digests"] if res[i] != rl["digests"][i]]
assert not changed, ("result lock: existing digests changed", changed)

# Insert the new observation entries after the last case of their own file
# (batch cases before the batch reference cases, setup cases likewise),
# keeping every existing entry where it is.
cases = ok["cases"]
for i in sorted(new, key=lambda s: (s[:4], int(s.split("-")[1][2:]))):
    prefix = i[:4]  # "G-BT" or "G-SU"
    last = max(k for k, c in enumerate(cases) if c["id"].startswith(prefix))
    cases.insert(last + 1, {"id": i, "observations": obs[i][0], "sha256": obs[i][1]})
okf.write_text(json.dumps(ok, indent=2, ensure_ascii=False) + "\n")

for i in new_res:
    rl["digests"][i] = res[i]
rl["digests"] = dict(sorted(rl["digests"].items()))
rf.write_text(json.dumps(rl, indent=2, ensure_ascii=False) + "\n")
print(f"observation lock: added {sorted(new)} ({len(cases)} cases); result lock: added {sorted(new_res)} ({len(rl['digests'])} digests); no existing entry changed")
