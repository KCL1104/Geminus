#!/usr/bin/env python3
"""The T-056 follow-up's red run of the deploy check, in memory only: the
extended scripts/check_deploy.py against variables.json as it was at a
commit (default ea7d220, before the photo settings were declared), and the
check as it was at that commit against the variables.json on disk. Both must
fail. Run from the repository root:
    python -I checks/evidence/2026-10-26-T-056-apivision/red_deploy.py [commit]
"""
import importlib.util
import pathlib
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[3]
commit = sys.argv[1] if len(sys.argv) > 1 else "ea7d220"
VARS = ROOT / "deploy/railway/variables.json"


def show(path):
    return subprocess.run(["git", "-C", str(ROOT), "show", f"{commit}:{path}"], check=True, capture_output=True, text=True).stdout


def load(source):
    with tempfile.NamedTemporaryFile("w", suffix=".py", delete=False) as f:
        f.write(source)
    spec = importlib.util.spec_from_file_location("check_deploy_at", f.name)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    mod.ROOT = ROOT
    return mod


def run(mod, vars_text=None):
    real = pathlib.Path.read_text

    def read_text(self, *a, **k):
        if vars_text is not None and self.resolve() == VARS:
            return vars_text
        return real(self, *a, **k)
    pathlib.Path.read_text = read_text
    try:
        mod.railway(mod.code_variables())
        return None
    except AssertionError as e:
        return str(e)
    finally:
        pathlib.Path.read_text = real


fails = 0
new = load((ROOT / "scripts/check_deploy.py").read_text(encoding="utf-8"))
got = run(new, show("deploy/railway/variables.json"))
print(f"the extended check, variables.json at {commit}: {got or 'PASS'}")
fails += got is not None
old = load(show("scripts/check_deploy.py"))
got = run(old)
print(f"the check at {commit}, variables.json on disk: {got or 'PASS'}")
fails += got is not None
got = run(new)
print(f"the extended check, variables.json on disk: {got or 'PASS'}")
print(f"{fails}/2 red runs fail as expected")
sys.exit(0 if fails == 2 and got is None else 1)
