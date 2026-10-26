#!/usr/bin/env python3
"""T-057 Auth0 slice (H-034, H-035 (2)): the mutation pass over the deployer's
Auth0 step, deploy.sh's --install-auth0-cli, check_deploy.py's new rules and
the Go side (provision-tenant's member, the audit catalogue).

Each mutant replaces text that occurs exactly once in one file of a copy of
the repository and runs one check in that copy:

- "rehearsal": scripts/rehearse_railway_deploy.py (the deployer, deploy.sh
  and their stand-ins; offline);
- "deploy": scripts/check_deploy.py;
- "go": go test -count=1 on internal/storage/provisioning, cmd/provision-tenant
  and internal/audit, against the disposable database in
  GEMINUS_TEST_DATABASE_URL (dt_test_t057a*).

KILLED: the check exits non-zero; SURVIVED: it exits 0. A mutant marked
equivalent is expected to survive (its text says why) and is reported
EQUIVALENT if it does. Both checks pass on the unmutated copy first; every
file is restored after its mutant and its SHA-256 compared.

Run it in a `git archive HEAD` copy (never the worktree), with a git
stand-in first on PATH, without the Railway tokens:

    python3 -I mutants.py <copy> [--only R1,D3] > log
"""
import hashlib
import os
import subprocess
import sys
from pathlib import Path

DEP = "deploy/railway/railway_deploy.py"
SH = "deploy/railway/deploy.sh"
VARS = "deploy/railway/variables.json"
PROV = "internal/storage/provisioning/provisioning.go"
CMD = "cmd/provision-tenant/main.go"
AUDIT = "internal/audit/audit.go"

# (id, what it breaks, file, [(old, new)], check, equivalent)
MUTANTS = [
    # --- secrets: the client secret, the judge's password, the Railway token -------------
    ("R1", "the client secret as an argument of `railway variable set`", DEP,
     [('self.set_from_stdin("web", "BFF_OIDC_CLIENT_SECRET", got[0] if got else "<the client secret>")',
       'self.rw("variable", "set", f"BFF_OIDC_CLIENT_SECRET={got[0] if got else 1}", "--service", "web", *self.env(), "--skip-deploys", "--json")')],
     "rehearsal", False),
    ("R2", "the client secret kept in the deployer's state", DEP,
     [('            box["client_secret"].clear()\n', '            self.kept = list(box["client_secret"])\n            box["client_secret"].clear()\n')],
     "rehearsal", False),
    ("R3", "the captured client secret left in the parsed output (a return value)", DEP,
     [("                    capture[k].append(v)\n                    out[k] = Seen.SET", "                    capture[k].append(v)\n                    out[k] = v")],
     "rehearsal", False),
    ("R4", "client_secret not among the dropped fields (another application's secret kept)", DEP,
     [('A0_SECRET_FIELDS = frozenset({"client_secret", ', 'A0_SECRET_FIELDS = frozenset({')], "rehearsal", False),
    ("R5", "device_code not among the dropped fields (equivalent: the 1.37.0 CLI's agent-mode login prints only "
           "verification_uri, user_code, expires_in and interval (internal/cli/login.go); the stand-in adds a device_code "
           "decoy, but a0_login shows only verification_uri and user_code and its parsed lines stay local, never returned, "
           "stored or printed, so the decoy is unobservable either way; the field is dropped as defence in depth)", DEP,
     [('"private_key", "device_code", "secret"})', '"private_key", "secret"})')], "rehearsal", True),
    ("R6", "the sign-in shows the CLI's whole line", DEP,
     [("                if not isinstance(obj, dict):\n                    continue\n                if not shown",
       "                if not isinstance(obj, dict):\n                    continue\n                self.say('   ' + line.strip())\n                if not shown")],
     "rehearsal", False),
    ("R7", "the CLI's SHA-256 is not checked", DEP, [("        if digest != AUTH0_CLI_SHA256:\n", "        if False:\n")], "rehearsal", False),
    ("R8", "no sign-out when the step fails", DEP,
     [("        except BaseException:\n            for p in self.a0_close():\n                self.say(f\"deploy: {p}\")\n            raise",
       "        except BaseException:\n            raise")], "rehearsal", False),
    ("R9", "the CLI's home is not removed", DEP,
     [("        shutil.rmtree(self.a0_home, ignore_errors=True)\n        if self.a0_home.exists():",
       "        if self.a0_home.exists():")], "rehearsal", False),
    ("R10", "a home left by an interrupted run is not signed out first", DEP,
     [("            self.a0_home = home\n            self.a0_signout()\n", "            self.a0_home = home\n")], "rehearsal", False),
    ("R11", "the judge's password is not registered for redaction", DEP,
     [("            pw = judge_password()\n            self.secret(pw)\n", "            pw = judge_password()\n")], "rehearsal", False),
    ("R12", "the judge's password shown on stdout", DEP, [("            sys.stderr.write(\"\\ndeploy: the judge's", "            sys.stdout.write(\"\\ndeploy: the judge's")],
     "rehearsal", False),
    ("R13", "the judge's password shown twice", DEP,
     [("            sys.stderr.flush()\n        finally:\n            shown.clear()",
       "            sys.stderr.write(shown[-1])\n            sys.stderr.flush()\n        finally:\n            shown.clear()")], "rehearsal", False),
    ("R14", "a call that carried a secret shows the CLI's message", DEP, [("            if sensitive:\n                return what +", "            if False:\n                return what +")],
     "rehearsal", False),
    ("R15", "the Railway token kept as a plain value (the registry)", DEP,
     [("        if isinstance(value, str) and value:\n            self._by_len", "        if isinstance(value, str) and value:\n            self.values = getattr(self, 'values', []) + [value]\n            self._by_len")],
     "rehearsal", False),
    ("R16", "child_env's own answer gets the Railway token (no copy)", DEP,
     [("        child = dict(self.child_env(argv[0], env))", "        child = self.child_env(argv[0], env)")], "rehearsal", False),
    ("R17", "the Auth0 CLI gets the whole environment", DEP, [('        if argv0 == "auth0":\n            keep', '        if False:\n            keep')], "rehearsal", False),
    ("R18", "the Auth0 CLI's analytics on", DEP, [('env.update(AUTH0_CLI_ANALYTICS="false", DBUS_SESSION_BUS_ADDRESS="disabled:")',
                                               'env.update(DBUS_SESSION_BUS_ADDRESS="disabled:")')], "rehearsal", False),
    # --- H-034's settings ---------------------------------------------------------------------
    ("R19", "the API's dialect is not RFC 9068", DEP, [('"token_dialect": "rfc9068_profile", "token_lifetime": 600,', '"token_dialect": "access_token", "token_lifetime": 600,')],
     "rehearsal", False),
    ("R20", "the API's token lifetime 86400 s", DEP, [('"token_dialect": "rfc9068_profile", "token_lifetime": 600,', '"token_dialect": "rfc9068_profile", "token_lifetime": 86400,')],
     "rehearsal", False),
    ("R21", "the API without offline access", DEP, [('                "allow_offline_access": True, "skip_consent', '                "allow_offline_access": False, "skip_consent')], "rehearsal", False),
    ("R22", "the application authenticates with Client Secret (Basic)", DEP,
     [('"oidc_conformant": True, "token_endpoint_auth_method": "client_secret_post",', '"oidc_conformant": True, "token_endpoint_auth_method": "client_secret_basic",')],
     "rehearsal", False),
    ("R23", "an implicit grant too", DEP, [('                "grant_types": ["authorization_code", "refresh_token"],', '                "grant_types": ["authorization_code", "refresh_token", "implicit"],')],
     "rehearsal", False),
    ("R24", "no rotation overlap (leeway 0)", DEP, [('"expiration_type": "expiring", "leeway": 30, "token_lifetime": 604800,', '"expiration_type": "expiring", "leeway": 0, "token_lifetime": 604800,')],
     "rehearsal", False),
    ("R25", "the idle lifetime 30 days", DEP, [('"idle_token_lifetime": 43200,', '"idle_token_lifetime": 2592000,')], "rehearsal", False),
    ("R26", "the callback on another path", DEP, [('"callbacks": [f"https://{web}/auth/callback"],', '"callbacks": [f"https://{web}/callback"],')], "rehearsal", False),
    ("R27", "a web origin allowed", DEP, [('"web_origins": [], "allowed_origins": [], "jwt_configuration"', '"web_origins": [f"https://{web}"], "allowed_origins": [], "jwt_configuration"')],
     "rehearsal", False),
    ("R28", "sign-up not turned off", DEP, [('                body = {"options": dict(options, disable_signup=True)}', '                body = {"options": dict(options)}')], "rehearsal", False),
    ("R29", "the connection's other options not kept", DEP, [('                body = {"options": dict(options, disable_signup=True)}', '                body = {"options": {"disable_signup": True}}')],
     "rehearsal", False),
    ("R30", "the connection's display name not kept", DEP,
     [('                if isinstance(full.get("display_name"), str):\n                    body["display_name"] = full["display_name"]\n', '')], "rehearsal", False),
    ("R31", "the application left on other connections", DEP,
     [("            if (cid == conn_id) != (client_id in on):\n                status = cid == conn_id",
       "            if cid == conn_id and client_id not in on:\n                status = cid == conn_id")], "rehearsal", False),
    ("R32", "the owner's connection not checked first", DEP, [("            self.a0_owner_check(conn)\n", "")], "rehearsal", False),
    ("R33", "another tenant's sign-in accepted", DEP, [('        if result.get("domain") != d:\n', '        if False:\n')], "rehearsal", False),
    # --- find or update, never twice ----------------------------------------------------------
    ("R34", "the application created again on every run", DEP, [("        created = not found\n", "        created = True\n")], "rehearsal", False),
    ("R35", "the application updated on every run", DEP,
     [("            client_id, drift = found[0].get(\"client_id\"), self.a0_drift(found[0], want)\n            if drift:",
       "            client_id, drift = found[0].get(\"client_id\"), self.a0_drift(found[0], want)\n            if True:")], "rehearsal", False),
    ("R36", "the judge's password reset on every run", DEP, [("        if not mine or reset:\n", "        if True:\n")], "rehearsal", False),
    ("R37", "the client secret read again on every run", DEP,
     [('        if not created and "BFF_OIDC_CLIENT_SECRET" in have and have.get("BFF_OIDC_CLIENT_ID") == client_id:',
       '        if False:')], "rehearsal", False),
    ("R38", "the client secret not sealed", DEP, [('        self.seal_now([("web", "BFF_OIDC_CLIENT_SECRET")], "T-057 deploy.sh: seal the Auth0 client secret")\n', '')],
     "rehearsal", False),
    ("R39", "every list compared in any order, not only the grant types (equivalent: every other list H-034 wants -- "
            "callbacks, allowed_logout_urls, web_origins, allowed_origins -- has at most one element, so two lists of "
            "the same elements in another order cannot exist; Auth0's answers have no list where a scalar is wanted)", DEP,
     [("        if at.endswith(\"grant_types\") and isinstance(have, list):", "        if isinstance(have, list):")], "rehearsal", True),
    ("R46", "the grant types compared in order (Auth0 listing them in another order is drift)", DEP,
     [("        if at.endswith(\"grant_types\") and isinstance(have, list):", "        if False:")], "rehearsal", False),
    # --- the judge's membership -----------------------------------------------------------------
    ("R40", "the judge an owner of the demo organization", DEP, [('JUDGE_ROLE = "editor"', 'JUDGE_ROLE = "owner"')], "rehearsal", False),
    ("R41", "the seed without the judge", DEP, [('            if self.judge_subject else []\n        out = self.run(["go", "run", "./cmd/provision-tenant", "organization"',
                                                 '            if False else []\n        out = self.run(["go", "run", "./cmd/provision-tenant", "organization"')],
     "rehearsal", False),
    ("R42", "any membership of the judge accepted", DEP,
     [("        if len(got) == 1 and len(got[0]) == 2 and got[0][0] in demo and got[0][1] == JUDGE_ROLE:", "        if True:")], "rehearsal", False),
    ("R43", "a judge without membership not reported", DEP, [("            self.late.append(f\"the judge's account {self.judge_subject} ({uid}) has no membership", "            print(f\"the judge's account {self.judge_subject} ({uid}) has no membership")],
     "rehearsal", False),
    ("R44", "the other tenants a sign-in left are not signed out", DEP, [("        for t in left or []:\n", "        for t in []:\n")], "rehearsal", False),
    ("R45", "a sign-out that left the session passes", DEP, [("        if left is None or left:\n            problems.append", "        if False:\n            problems.append")],
     "rehearsal", False),
    # --- deploy.sh --install-auth0-cli ---------------------------------------------------------------
    ("S1", "the archive's SHA-256 not checked (only the binary's)", SH,
     [('echo "$AUTH0_CLI_ARCHIVE_SHA256  $tmp/auth0.tar.gz" | sha256sum -c --quiet - || { echo "deploy.sh: the Auth0 CLI archive\'s SHA-256 is not the pinned one; nothing installed" >&2; exit 1; }',
       'true')], "rehearsal", False),
    ("S2", "neither SHA-256 checked", SH,
     [('echo "$AUTH0_CLI_ARCHIVE_SHA256  $tmp/auth0.tar.gz" | sha256sum -c --quiet - || { echo "deploy.sh: the Auth0 CLI archive\'s SHA-256 is not the pinned one; nothing installed" >&2; exit 1; }',
       'true'),
      ('echo "$AUTH0_CLI_SHA256  $tmp/auth0" | sha256sum -c --quiet - || { echo "deploy.sh: the auth0 binary\'s SHA-256 is not the pinned one; nothing installed" >&2; exit 1; }',
       'true')], "rehearsal", False),
    # --- check_deploy.py's rules --------------------------------------------------------------------------
    ("D1", "another variable from the Auth0 step (the worker's Meta key)", VARS,
     [('"GEMINUS_MUSE_API_KEY": {"secret": true, "source": "owner", "required": false}',
       '"GEMINUS_MUSE_API_KEY": {"secret": true, "source": "auth0", "required": false}')], "deploy", False),
    ("D2", "deploy.sh pins another binary than the deployer", SH, [("AUTH0_CLI_SHA256=2bc2ef7c", "AUTH0_CLI_SHA256=3bc2ef7c")], "deploy", False),
    ("D3", "the deployer pins another version", DEP, [('AUTH0_CLI_VERSION = "1.37.0"', 'AUTH0_CLI_VERSION = "1.38.0"')], "deploy", False),
    ("D4", "the archive from the latest release", SH,
     [('AUTH0_CLI_URL="https://github.com/auth0/auth0-cli/releases/download/v${AUTH0_CLI_VERSION}/', 'AUTH0_CLI_URL="https://github.com/auth0/auth0-cli/releases/latest/download/v${AUTH0_CLI_VERSION}/')],
     "deploy", False),
    ("D5", "the install without the binary's check", SH,
     [('    echo "$AUTH0_CLI_SHA256  $tmp/auth0" | sha256sum -c --quiet - || { echo "deploy.sh: the auth0 binary\'s SHA-256 is not the pinned one; nothing installed" >&2; exit 1; }\n', '')],
     "deploy", False),
    # --- Go: provision-tenant's member, the audit catalogue ----------------------------------------------------
    ("G1", "a member may be a second owner", PROV, [('if member.Role != "editor" && member.Role != "viewer" {', 'if member.Role == "" {')], "go", False),
    ("G2", "the member may be the owner again", PROV, [("		if member.Issuer == issuer && member.Subject == subject {\n", "		if false {\n")], "go", False),
    ("G3", "the member's identity not checked", PROV, [("		if err := identity(member.Issuer, member.Subject); err != nil {\n			return \"\", \"\", \"\", err\n		}\n", "")], "go", False),
    ("G4", "the member inserted as an owner", PROV, [("VALUES ($1,$2,$3)`, orgID, memberID, member.Role)", "VALUES ($1,$2,'owner')`, orgID, memberID)")], "go", False),
    ("G5", "the member not in the audit event", PROV, [('			meta["member_user_id"], meta["member_role"] = memberID, member.Role\n', '')], "go", False),
    ("G6", "the catalogue lets a member be an owner", AUDIT, [('"member_role": oneOf("editor", "viewer")', '"member_role": roles')], "go", False),
    ("G7", "the command accepts partial member flags (equivalent: provisioning refuses the incomplete identity "
           "or role with ErrInvalidInput, which the command also maps to exit 2; the command's own check only refuses "
           "before connecting)", CMD,
     [("	if withMember && (member.Issuer == \"\" || member.Subject == \"\" || member.Role == \"\") {", "	if false {")], "go", True),
    ("G8", "the command ignores the member flags", CMD, [("	} else if withMember {\n", "	} else if false {\n")], "go", False),
]

CHECKS = {
    "rehearsal": [sys.executable, "-I", "scripts/rehearse_railway_deploy.py"],
    "deploy": [sys.executable, "-I", "scripts/check_deploy.py"],
    "go": ["go", "test", "-count=1", "./internal/storage/provisioning/", "./cmd/provision-tenant/", "./internal/audit/"],
}


def sha(p: Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()


def run_check(root: Path, check: str):
    env = {k: v for k, v in os.environ.items() if k not in ("RAILWAY_TOKEN", "RAILWAY_API_TOKEN")}
    p = subprocess.run(CHECKS[check], cwd=root, env=env, capture_output=True, text=True, timeout=1200)
    out = (p.stdout + p.stderr).strip().splitlines()
    first = next((line for line in out if line.startswith(("REHEARSAL FAILED", "DEPLOY CHECK FAILED", "--- FAIL", "Traceback"))),
                 out[-1] if out else "")
    detail = ""
    if first.startswith(("REHEARSAL FAILED", "--- FAIL")):
        i = out.index(first)
        detail = " ".join(line.strip() for line in out[i + 1:i + 3])
    elif first.startswith("Traceback"):
        detail = out[-1]
    return p.returncode, (first + " " + detail).strip()[:400]


def main() -> int:
    root = Path(sys.argv[1]).resolve()
    only = set(sys.argv[sys.argv.index("--only") + 1].split(",")) if "--only" in sys.argv else None
    if (root / ".git").exists():
        print("refusing: run in a `git archive` copy, not a repository")
        return 2
    chosen = [m for m in MUTANTS if not only or m[0] in only]
    if "--validate" in sys.argv:
        bad = [m[0] for m in chosen if any((root / m[2]).read_text(encoding="utf-8").count(old) != 1 for old, _ in m[3])]
        print(f"{len(chosen)} mutants, not exactly once: {bad}")
        return 1 if bad else 0
    for check in sorted({m[4] for m in chosen}):
        code, msg = run_check(root, check)
        print(f"BASELINE {check}: exit {code} {msg[:200]}", flush=True)
        if code != 0:
            print("the unmutated copy must pass")
            return 2
    killed = survived = equivalent = invalid = 0
    restored = True
    for mid, what, rel, edits, check, equiv in chosen:
        path = root / rel
        before_text, before_sha = path.read_text(encoding="utf-8"), sha(path)
        text, ok = before_text, True
        for old, new in edits:
            if text.count(old) != 1:
                ok = False
                break
            text = text.replace(old, new)
        if not ok:
            print(f"INVALID {mid} {what}: the text to replace does not occur exactly once in {rel}", flush=True)
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
            print(f"KILLED {mid} {what} :: {msg}", flush=True)
        elif equiv:
            equivalent += 1
            print(f"EQUIVALENT {mid} {what}", flush=True)
        else:
            survived += 1
            print(f"SURVIVED {mid} {what}", flush=True)
    total = killed + survived
    print(f"{killed}/{total} killed, {equivalent} equivalent (expected to survive), {invalid} invalid")
    print("restored files: " + ("PASS" if restored else "FAIL"))
    return 0 if survived == 0 and invalid == 0 and restored else 1


if __name__ == "__main__":
    sys.exit(main())
