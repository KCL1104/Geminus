#!/usr/bin/env python3
"""T-050 review round 1: run chosen tests, verbose and without -failfast,
with one mutant of the harness (checks/evidence/2026-10-26-T-050/
t050-mutants.py, by name) applied through `go test -overlay` on a scratch
copy of the tree (never the worktree), the regression locks skipped
(-skip Digests), and write the log: the red of a test added in round 1.

    red_under_mutant.py <scratch repo> <log> <mutant> <package> <-run regexp> [locks]

With a sixth argument "locks" the locks are not skipped (to run only them
under a mutant, -run Digests).
"""
import importlib.util
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import time

repo, log, name, pkg, run = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5]
skip = [] if sys.argv[6:] == ["locks"] else ["-skip", "Digests"]
spec = importlib.util.spec_from_file_location("h", repo / "checks/evidence/2026-10-26-T-050/t050-mutants.py")
sys.argv = [sys.argv[0], str(repo)]  # the harness reads its repo from argv
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)
(_, f, old, new, *_), = [m for m in h.MUT if m[0] == name]
src = (repo / f).read_text()
assert src.count(old) == 1, (name, src.count(old))
env = dict(os.environ, PATH="/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:" + os.environ["PATH"], GOTOOLCHAIN="local")
with tempfile.TemporaryDirectory() as tmp:
    m = pathlib.Path(tmp) / "m.go"
    m.write_text(src.replace(old, new))
    ov = pathlib.Path(tmp) / "m.json"
    ov.write_text(json.dumps({"Replace": {str(repo / f): str(m)}}))
    cmd = ["go", "test", "-count=1", "-v", *skip, "-overlay", str(ov), "-run", run, pkg]
    t0 = time.time()
    p = subprocess.run(cmd, cwd=repo, capture_output=True, text=True, env=env)
with log.open("w") as out:
    out.write(f"# {time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(t0))} cwd=scratch copy (git archive of HEAD) at {repo}\n")
    out.write(f"# mutant {name} ({f}): {old!r} -> {new!r}\n")
    out.write("# cmd: " + " ".join(cmd).replace(str(ov), "<overlay>") + "\n")
    out.write(p.stdout + p.stderr)
    out.write(f"# exit={p.returncode} elapsed={round(time.time() - t0)}s\n")
print(name, "exit", p.returncode)
