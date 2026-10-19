"""Run one verification command from the T-024 worktree root, keep its output.

Usage: python3 -I t024_run.py <evidence-dir> <log-name> [VAR=value ...] -- <command...>
Writes <evidence-dir>/<log-name> (the command, then its combined output) and
appends "<log-name>: exit <code> (<command>)" to <evidence-dir>/exit-codes.txt.
VAR=value pairs before -- are added to the environment; values are never
written to the log unless they are part of the command itself.
"""
import os
import subprocess
import sys
import time
from pathlib import Path


def main():
    out_dir, name = Path(sys.argv[1]), sys.argv[2]
    rest = sys.argv[3:]
    sep = rest.index("--")
    env = dict(os.environ)
    shown = []
    for pair in rest[:sep]:
        k, v = pair.split("=", 1)
        env[k] = v
        shown.append(k)
    cmd = rest[sep + 1:]
    out_dir.mkdir(parents=True, exist_ok=True)
    start = time.time()
    r = subprocess.run(cmd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    took = time.time() - start
    header = f"$ {' '.join(cmd)}\n# extra environment: {', '.join(shown) or 'none'}\n"
    footer = f"\n# exit {r.returncode} after {took:.0f}s\n"
    (out_dir / name).write_text(header + r.stdout + footer)
    with open(out_dir / "exit-codes.txt", "a") as f:
        f.write(f"{name}: exit {r.returncode} ({' '.join(cmd)}; env: {', '.join(shown) or 'none'})\n")
    tail = r.stdout.splitlines()[-25:]
    print("\n".join(tail))
    print(f"exit {r.returncode} after {took:.0f}s")
    sys.exit(r.returncode)


if __name__ == "__main__":
    main()
