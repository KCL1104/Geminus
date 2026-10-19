"""Resolve every conflict hunk in the given files as 'both sides': the
upstream (HEAD) lines first, then the replayed commit's lines.

Usage: python3 -I t024_union.py <file>...
Prints, for each file, how many hunks it resolved. Refuses files with
diff3 base sections (|||||||), which need a manual decision.
"""
import sys
from pathlib import Path


def resolve(text):
    out, ours, theirs, state, hunks = [], [], [], None, 0
    for line in text.splitlines(keepends=True):
        if line.startswith("<<<<<<< "):
            state, ours, theirs = "ours", [], []
        elif line.startswith("|||||||") and state == "ours":
            raise SystemExit("diff3 base section found; resolve by hand")
        elif line.startswith("=======") and state == "ours":
            state = "theirs"
        elif line.startswith(">>>>>>> ") and state == "theirs":
            out.extend(ours + theirs)
            state, hunks = None, hunks + 1
        elif state == "ours":
            ours.append(line)
        elif state == "theirs":
            theirs.append(line)
        else:
            out.append(line)
    if state is not None:
        raise SystemExit("unterminated conflict")
    return "".join(out), hunks


for name in sys.argv[1:]:
    p = Path(name)
    text, n = resolve(p.read_text())
    p.write_text(text)
    print(f"{name}: {n} hunks")
