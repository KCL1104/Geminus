#!/usr/bin/env python3
"""T-039 review round 1: the mutation pass over the round's fixes and checks
(review L7: the definitions are committed with the results).

Each mutant replaces text that occurs exactly once in one file of a copy of
the repository and runs one check in that copy:

- "rehearsal": scripts/rehearse_railway_deploy.py (the deployer, deploy.sh
  and their stand-ins; offline);
- "deploy": scripts/check_deploy.py (the static checks of the deployment
  files).

A mutant is KILLED when the check exits non-zero and SURVIVED when it exits
0. A mutant marked equivalent is expected to survive (its text says why);
it is reported EQUIVALENT if it does and KILLED otherwise, and is not a
failure. Before the mutants, both checks must pass on the unmutated copy.
Every file is restored after its mutant and its SHA-256 compared with the
one before.

Run it in a `git archive HEAD` copy, never the worktree, with a stand-in
`git` first on PATH (the copy is no repository; the deployer's preflight
runs `git status` and `git rev-parse`):

    env -u RAILWAY_TOKEN -u RAILWAY_API_TOKEN PATH=<dir with a git stand-in>:$PATH \\
        python3 -I mutants.py <copy> [--only R1,D3] > log

No network call: the rehearsal's stand-ins answer every railway, psql and go
call; the Railway tokens are removed from the environment of every check.
"""
import hashlib
import os
import subprocess
import sys
from pathlib import Path

DEP = "deploy/railway/railway_deploy.py"
SH = "deploy/railway/deploy.sh"
IGN = ".dockerignore"
VARS = "deploy/railway/variables.json"

# (id, what it breaks, file, [(old, new)], check, equivalent)
MUTANTS = [
    # --- M1: the TCP proxy ---------------------------------------------------
    ("R1", "M1: a failed `tcp-proxy delete` is ignored", DEP,
     [("        if self.code != 0:\n            self.proxy_problems.append(f\"could not remove this run's TCP proxy",
       "        if False:\n            self.proxy_problems.append(f\"could not remove this run's TCP proxy")], "rehearsal", False),
    ("R2", "M1: the TCP proxies are never listed at the end", DEP,
     [("        if self.proxies_listed or not self.database_seen:\n            return self.proxy_problems",
       "        if True:\n            return self.proxy_problems")], "rehearsal", False),
    ("R3", "M1: a proxy open before the run is accepted silently", DEP,
     [("        for pid in ids:\n", "        for pid in [i for i in ids if i not in self.proxies_before]:\n")], "rehearsal", False),
    ("R4", "M1: an open proxy is always kept (as if GEMINUS_KEEP_DB_PROXY=1)", DEP,
     [('keep = os.environ.get("GEMINUS_KEEP_DB_PROXY") == "1"', "keep = True")], "rehearsal", False),
    ("R5", "M1: GEMINUS_KEEP_DB_PROXY=1 is ignored", DEP,
     [('keep = os.environ.get("GEMINUS_KEEP_DB_PROXY") == "1"', "keep = False")], "rehearsal", False),
    ("R6", "M1: a listing that fails counts as no proxy", DEP,
     [("ids = self.proxy_ids(listing) if self.code == 0 else None", "ids = self.proxy_ids(listing) or []")], "rehearsal", False),
    ("R7", "M1: SIGTERM and SIGHUP keep their default (no cleanup)", DEP,
     [("        signal.signal(s, exit_on_signal)", "        pass")], "rehearsal", False),
    ("R8", "M1: a create without an id is not resolved by listing", DEP,
     [("        if self.proxy_created is None:\n            # The CLI prints", "        if False:\n            # The CLI prints")],
     "rehearsal", False),
    ("R9", "M1: a failed step does not list the proxies", DEP,
     [("            extra = [p for p in self.check_proxies() if p not in str(e)]", "            extra = []")], "rehearsal", False),
    ("R10", "M1: an interrupted run does not report the proxies", DEP,
     [("            for p in self.check_proxies():\n                self.say(f\"deploy: {p}\")",
       "            for p in []:\n                self.say(f\"deploy: {p}\")")], "rehearsal", False),
    ("R11", "M1: the proxy from before the run is removed (the documented decision is to keep and report it)", DEP,
     [("            self.proxies_before = before\n", "            self.proxies_before = before\n            self.proxy_created = before[0]\n")],
     "rehearsal", False),
    ("R12", "M1: verify does not list the proxies", DEP,
     [("        problems += self.check_proxies()\n", "")], "rehearsal", False),
    ("R13", "M1: the end of a successful run does not list them again (equivalent: verify listed them and would have "
            "failed on any problem, so the list is always empty there; kept so the guarantee does not depend on verify)", DEP,
     [("        left = self.check_proxies()\n        if left:", "        left = []\n        if left:")], "rehearsal", True),
    ("R14", "M1: the proxy is opened outside the cleanup again (equivalent here: no stand-in fails or signals between "
            "the create and the try)", DEP,
     [("        try:\n            self.proxy_open()\n            admin = self.admin()",
       "        self.proxy_open()\n        try:\n            admin = self.admin()")], "rehearsal", True),
    # --- M2: what a variable listing keeps --------------------------------------
    ("R15", "M2: every listed value is kept as received", DEP,
     [('                    out[k] = "" if v == "" else Seen.SET', "                    out[k] = v")], "rehearsal", False),
    ("R16", "M2: the owner's values are kept (and registered as secrets)", DEP,
     [("                if k in keep and k not in self.owner and (v is None or isinstance(v, str)):",
       "                if (k in keep or k in self.owner) and (v is None or isinstance(v, str)):")], "rehearsal", False),
    ("R17", "M2: the listing's raw output is returned by a method and parsed after", DEP,
     [('        got = self.run(["railway", "variable", "list", "--service", service, *self.env(), "--json"], quiet=True, parse=receive)',
       '        got = receive(json.loads(self.run(["railway", "variable", "list", "--service", service, *self.env(), "--json"], '
       'quiet=True)).items())')], "rehearsal", False),
    ("R18", "M2: an unsealed owner secret is not named", DEP,
     [('                elif e.get("secret") and have.get(name) is Seen.SET:', "                elif False:")], "rehearsal", False),
    ("R19", "M2: an owner variable that is no secret is named as unsealed", DEP,
     [('                elif e.get("secret") and have.get(name) is Seen.SET:', "                elif have.get(name) is Seen.SET:")],
     "rehearsal", False),
    ("R20", "M2: a kept value is not registered for redaction", DEP,
     [("                    out[k] = self.secret(v) if v else v", "                    out[k] = v")], "rehearsal", False),
    # --- L1, L2, L5 ------------------------------------------------------------------
    ("R21", "L1: the xtrace check after `set +x` again (never refuses)", SH,
     [('case "$-" in *x*) set +x; echo "deploy.sh: refusing', 'set +x\ncase "$-" in *x*) echo "deploy.sh: refusing')], "rehearsal", False),
    ("R22", "L2: the Railway token is not registered for redaction", DEP,
     [("                self.secret(os.environ[k])\n", "                pass\n")], "rehearsal", False),
    ("R23", "L2: every program gets the Railway token", DEP,
     [('        if argv0 != "railway":', "        if False:")], "rehearsal", False),
    ("R24", "L5: a public domain for every service", DEP,
     [('            dom = self.spec["services"][s].get("public_domain")',
       '            dom = self.spec["services"][s].get("public_domain") or {"port": 8080}')], "rehearsal", False),
    # --- L3, L5(b), the H-033 name: the deployment files against check_deploy.py ----
    ("D1", "L3: env files below the root back in the build context", IGN, [("**/.env\n**/.env.*\n", "**/.env.*\n")], "deploy", False),
    ("D2", "L3: no exception for the example (the required pattern missing)", IGN, [("!**/.env.example\n", "")], "deploy", False),
    ("D3", "L3: a local env file brought back", IGN, [("!**/.env.example\n", "!**/.env.example\n!apps/web/.env\n")], "deploy", False),
    ("D4", "L3: the exception before the exclusions (the example excluded)", IGN,
     [("**/.env\n**/.env.*\n!**/.env.example\n", "!**/.env.example\n**/.env\n**/.env.*\n")], "deploy", False),
    ("D5", "L3: only the root's env files excluded (the round's starting point)", IGN, [("**/.env\n**/.env.*\n!**/.env.example\n", "")],
     "deploy", False),
    ("D6", "L5: a public domain on the worker", VARS,
     [('      "config": "deploy/railway/worker.railway.json",\n',
       '      "config": "deploy/railway/worker.railway.json",\n      "public_domain": {"port": 8080},\n')], "deploy", False),
    ("D7", "L5: a public domain on migrate", VARS,
     [('      "config": "deploy/railway/migrate.railway.json",\n',
       '      "config": "deploy/railway/migrate.railway.json",\n      "public_domain": {"port": 8080},\n')], "deploy", False),
    ("D8", "L5: no public domain on the web", VARS, [('      "public_domain": {"port": 3000},\n', "")], "deploy", False),
    ("D9", "L5: the API's domain on another port than its PORT", VARS, [('"public_domain": {"port": 8080}', '"public_domain": {"port": 3000}')],
     "deploy", False),
    ("D10", "L5: no healthcheck on the API", "deploy/railway/api.railway.json", [('    "healthcheckPath": "/readyz",\n', "")], "deploy", False),
    ("D11", "L5: the web's healthcheck elsewhere", "deploy/railway/web.railway.json",
     [('"healthcheckPath": "/",', '"healthcheckPath": "/healthz",')], "deploy", False),
    ("D12", "L5: a healthcheck on the worker (it has no HTTP listener)", "deploy/railway/worker.railway.json",
     [('    "restartPolicyType": "ON_FAILURE",', '    "healthcheckPath": "/",\n    "restartPolicyType": "ON_FAILURE",')], "deploy", False),
    ("D13", "H-033: the worker's Meta key under the old name", VARS,
     [('"GEMINUS_MUSE_API_KEY": {"secret": true, "source": "owner"', '"GEMINUS_META_API_KEY": {"secret": true, "source": "owner"')],
     "deploy", False),
    ("D14", "H-033: the API does not forbid the Muse key", VARS,
     [('"forbidden": ["GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "GEMINUS_MUSE_API_KEY",',
       '"forbidden": ["GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B",')], "deploy", False),
]

CHECKS = {
    "rehearsal": ["scripts/rehearse_railway_deploy.py"],
    "deploy": ["scripts/check_deploy.py"],
}


def sha(p: Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()


def run_check(root: Path, check: str):
    env = {k: v for k, v in os.environ.items() if k not in ("RAILWAY_TOKEN", "RAILWAY_API_TOKEN")}
    p = subprocess.run([sys.executable, "-I", *CHECKS[check]], cwd=root, env=env, capture_output=True, text=True, timeout=900)
    out = (p.stdout + p.stderr).strip().splitlines()
    first = next((line for line in out if "FAILED" in line), out[-1] if out else "")
    detail = ""
    if "REHEARSAL FAILED" in first:
        i = out.index(first)
        detail = " ".join(line.strip() for line in out[i + 1:i + 3])
    return p.returncode, (first + " " + detail).strip()[:400]


def main() -> int:
    root = Path(sys.argv[1]).resolve()
    only = set(sys.argv[sys.argv.index("--only") + 1].split(",")) if "--only" in sys.argv else None
    if (root / ".git").exists():
        print("refusing: run in a `git archive` copy, not a repository")
        return 2
    for check in CHECKS:
        code, msg = run_check(root, check)
        print(f"BASELINE {check}: exit {code} {msg[:200]}")
        if code != 0:
            print("the unmutated copy must pass")
            return 2
    killed = survived = equivalent = invalid = 0
    restored = True
    for mid, what, rel, edits, check, equiv in MUTANTS:
        if only and mid not in only:
            continue
        path = root / rel
        before_text, before_sha = path.read_text(encoding="utf-8"), sha(path)
        text = before_text
        ok = True
        for old, new in edits:
            if text.count(old) != 1:
                ok = False
                break
            text = text.replace(old, new)
        if not ok:
            print(f"INVALID {mid} {what}: the text to replace does not occur exactly once in {rel}")
            invalid += 1
            continue
        try:
            path.write_text(text, encoding="utf-8")
            code, msg = run_check(root, check)
        finally:
            path.write_text(before_text, encoding="utf-8")
        if sha(path) != before_sha:
            restored = False
        if code != 0:
            killed += 1
            print(f"KILLED {mid} {what} :: {msg}")
        elif equiv:
            equivalent += 1
            print(f"EQUIVALENT {mid} {what}")
        else:
            survived += 1
            print(f"SURVIVED {mid} {what}")
    total = killed + survived
    print(f"{killed}/{total} killed, {equivalent} equivalent (expected to survive), {invalid} invalid")
    print("restored files: " + ("PASS" if restored else "FAIL"))
    return 0 if survived == 0 and invalid == 0 and restored else 1


if __name__ == "__main__":
    sys.exit(main())
