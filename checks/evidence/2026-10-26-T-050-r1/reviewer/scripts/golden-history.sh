#!/bin/bash
# For each T-050 commit that touched the golden fixtures, compare every case's "expected" (and model, config)
# with the same case in the previous version, to see whether any existing expectation changed after it was added.
WT=/home/user/Geminus/.claude/worktrees/agent-abba1aa5d52ed7843
S=/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad/gh
mkdir -p "$S"
cd "$WT" || exit 1
for f in batch-goldens.json setup-goldens.json; do
  for c in 88f34e3 fd8aa08 ce01c55 fbb828a 4715f49 79c9423; do
    git show "$c:testdata/simulation/$f" > "$S/$c-$f" 2>/dev/null || rm -f "$S/$c-$f"
  done
done
python3 - "$S" <<'EOF'
import json, sys, pathlib
S = pathlib.Path(sys.argv[1])
order = ["88f34e3", "fd8aa08", "ce01c55", "fbb828a", "4715f49", "79c9423"]
for f in ["batch-goldens.json", "setup-goldens.json"]:
    prev = None
    for c in order:
        p = S / f"{c}-{f}"
        if not p.exists():
            continue
        doc = json.loads(p.read_text())
        cases = {x["id"]: x for x in doc["cases"]}
        if prev is not None:
            pc, pcommit = prev
            added = sorted(set(cases) - set(pc))
            removed = sorted(set(pc) - set(cases))
            changed = []
            for k in sorted(set(cases) & set(pc)):
                for member in ("expected", "model", "config"):
                    if cases[k].get(member) != pc[k].get(member):
                        changed.append(f"{k}.{member}")
                if cases[k].get("derivation") != pc[k].get("derivation"):
                    changed.append(f"{k}.derivation")
            refs_changed = doc.get("reference") != json.loads((S / f"{pcommit}-{f}").read_text()).get("reference")
            print(f"{f} {pcommit} -> {c}: added {added} removed {removed} changed {changed} reference_block_changed={refs_changed}")
        prev = (cases, c)
EOF
rm -rf "$S"
