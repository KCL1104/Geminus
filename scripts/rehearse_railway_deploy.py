#!/usr/bin/env python3
"""Offline rehearsal of `deploy/railway/deploy.sh --apply` (T-039): runs the
deployer's whole --apply path against stand-ins for `railway`, `psql` and
`go` (scripts written to a temporary directory and put first on PATH; no
network, no Railway API, no database), twice: an empty project, then the
same project again (idempotency). It checks what the deployer controls, not
what Railway does:

- every secret it makes or copies (database passwords, SESSION_KEYS, the
  bucket's keys) reaches the CLI only on stdin, never as an argument, and
  never appears in its output;
- it never sets or reads the owner's variables (BFF_OIDC_CLIENT_SECRET, the
  AI keys, GEMINUS_AI_LIVE) and the API never gets an AI key or the switch;
- the order: logins before the migrate deployment, the migrate deployment
  before the seed, the seed before the API, worker and web, the TCP proxy
  removed at the end;
- a second run creates nothing again, keeps the generated secrets and seeds
  no second organization;
- verification fails clearly while the owner's BFF_OIDC_CLIENT_SECRET is
  missing (the owner check names it), and fails when the sign-in does not
  redirect to the issuer even with it set;
- a CLI error that quotes a secret is printed redacted.

The stand-ins answer with the JSON shapes the CLI's source (v5.64.2) prints
where it was read, otherwise with plausible ones: a pass here says the
deployer is consistent, not that Railway accepts every call (NOT RUN until
the coordinator's --apply).
"""
import importlib.util
import io
import json
import os
import sys
import tempfile
import textwrap
from contextlib import redirect_stdout
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

RAILWAY = r'''#!/usr/bin/env python3
import json, os, sys
state_path = os.environ["REHEARSAL_STATE"]
st = json.load(open(state_path)) if os.path.exists(state_path) else {"projects": [], "services": [], "buckets": [], "vars": {}, "domains": {}, "proxies": [], "calls": [], "deploys": []}
a = sys.argv[1:]
stdin = sys.stdin.read() if "--stdin" in a else None
st["calls"].append({"argv": a, "stdin": stdin is not None})
def opt(name):
    return a[a.index(name) + 1] if name in a else None
out = {}
if a[:1] == ["--version"]:
    print("railway 5.64.2"); json.dump(st, open(state_path, "w")); sys.exit(0)
cmd = a[0]
if cmd == "list":
    out = [{"id": "prj_1", "name": n} for n in st["projects"]]
elif cmd == "init":
    st["projects"].append(opt("--name"))
elif cmd in ("link", "status"):
    out = {"project": {"id": "prj_1"}, "environment": {"id": "env_1", "name": "production"}}
elif cmd == "environment" and a[1] == "list":
    out = {"environments": [{"id": "env_1", "name": "production"}]}
elif cmd == "environment" and a[1] == "edit":
    sc = [a[i + 1:i + 4] for i, x in enumerate(a) if x == "--service-config"]
    for svc, path, value in sc:
        if path.endswith(".isSealed"):
            st.setdefault("sealed", []).append([svc, path.split(".")[1]])
    out = {"committed": True}
elif cmd == "service" and a[1] == "list":
    out = [{"id": "svc_" + n, "name": n} for n in st["services"]]
elif cmd == "add":
    name = "Postgres" if opt("--database") else opt("--service")
    st["services"].append(name)
    if name == "Postgres":
        st["vars"]["Postgres"] = {"PGHOST": "postgres.railway.internal", "PGPORT": "5432", "PGDATABASE": "railway"}
    out = {"id": "svc_" + name, "name": name}
elif cmd == "bucket" and a[1] == "list":
    out = [{"id": "bkt_" + n, "name": n} for n in st["buckets"]]
elif cmd == "bucket" and a[1] == "create":
    st["buckets"].append(a[2])
elif cmd == "bucket" and a[1] == "credentials":
    out = {"endpoint": "https://t3.storageapi.dev", "accessKeyId": "tid_REHEARSALACCESSKEY", "secretAccessKey": "tsec_REHEARSALSECRETACCESSKEY0123456789",
           "bucketName": "geminus-objects-abc123", "region": "auto", "urlStyle": "virtual-hosted"}
elif cmd == "domain" and len(a) > 1 and a[1] == "list":
    d = st["domains"].get(opt("--service"))
    out = {"domains": [{"domain": d}] if d else []}
elif cmd == "domain":
    st["domains"][opt("--service")] = opt("--service") + "-production-1a2b.up.railway.app"
    out = {"domain": st["domains"][opt("--service")]}
elif cmd == "variable" and a[1] == "list":
    svc = opt("--service")
    v = dict(st["vars"].get(svc, {}))
    for s, name in st.get("sealed", []):
        if s == svc and name in v:
            v[name] = None
    out = v
elif cmd == "variable" and a[1] == "set":
    svc = opt("--service")
    if stdin is not None and os.environ.get("REHEARSAL_ECHO_STDIN"):
        # A CLI that quotes the value back in its error: the deployer must redact it.
        print(f"error: {a[2]}={stdin} rejected", file=sys.stderr)
        json.dump(st, open(state_path, "w"))
        sys.exit(1)
    if "--stdin" in a:
        st["vars"].setdefault(svc, {})[a[2]] = stdin
    else:
        k, _, v = a[2].partition("=")
        st["vars"].setdefault(svc, {})[k] = v
elif cmd == "tcp-proxy" and a[1] == "list":
    out = {"proxies": [{"id": p} for p in st["proxies"]]}
elif cmd == "tcp-proxy" and a[1] == "create":
    st["proxies"].append("tcp_1")
    st["vars"]["Postgres"]["DATABASE_PUBLIC_URL"] = "postgresql://postgres:REHEARSALADMINPW@shuttle.proxy.rlwy.net:41234/railway"
    out = {"applicationPort": 5432, "proxy": {"id": "tcp_1"}}
elif cmd == "tcp-proxy" and a[1] == "delete":
    st["proxies"] = [p for p in st["proxies"] if p != a[2]]
    st["vars"]["Postgres"].pop("DATABASE_PUBLIC_URL", None)
elif cmd == "up":
    st["deploys"].append(opt("--service"))
elif cmd == "deployment":
    out = [{"id": "dep_1", "status": "SUCCESS"}]
json.dump(st, open(state_path, "w"))
print(json.dumps(out))
'''

PSQL = r'''#!/usr/bin/env python3
import json, os, sys
log = os.environ["REHEARSAL_PSQL_LOG"]
a = sys.argv[1:]
stdin = sys.stdin.read() if "-" in a else ""
entry = {"argv": a, "has_password_env": bool(os.environ.get("PGPASSWORD")),
         "login_passwords": sorted(k for k in os.environ if k.endswith("_DB_PASSWORD") or k == "GEMINUS_NEW_PASSWORD")}
with open(log, "a") as f:
    f.write(json.dumps(entry) + "\n")
if "--version" in a:
    print("psql (PostgreSQL) 16.15")
elif "-Atc" in a:
    q = a[a.index("-Atc") + 1]
    if "pg_roles" in q:
        print("\n".join(json.load(open(os.environ["REHEARSAL_ROLES"]))) if os.path.exists(os.environ["REHEARSAL_ROLES"]) else "")
    elif "geminus_migrations" in q:
        print("16")
    else:
        print("1")
elif "deploy/sql/create_logins.sql" in a:
    json.dump(["geminus_api", "geminus_migrator", "geminus_provisioner", "geminus_worker"], open(os.environ["REHEARSAL_ROLES"], "w"))
'''

GO = r'''#!/usr/bin/env python3
import os, sys
with open(os.environ["REHEARSAL_GO_LOG"], "a") as f:
    f.write(" ".join(sys.argv[1:]) + " | url-env:" + str(bool(os.environ.get("GEMINUS_PROVISION_DATABASE_URL"))) + "\n")
print('{"organization_id":"org_01M5W4RHAFH8Q7Y8PNQT1RAF4T","owner_user_id":"usr_01M5W4RHAGH9JGFGMTQ8H6SK6H"}')
'''


def read(path) -> str:
    return path.read_text() if path.exists() else ""


def load():
    sys.dont_write_bytecode = True  # no __pycache__ in deploy/railway
    spec = importlib.util.spec_from_file_location("railway_deploy", ROOT / "deploy/railway/railway_deploy.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def main() -> int:
    mod = load()
    failures = []
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        bindir = tmp / "bin"
        bindir.mkdir()
        for name, body in (("railway", RAILWAY), ("psql", PSQL), ("go", GO)):
            (bindir / name).write_text(body)
            (bindir / name).chmod(0o755)
        os.environ.update(PATH=f"{bindir}:{os.environ['PATH']}", REHEARSAL_STATE=str(tmp / "state.json"),
                          REHEARSAL_PSQL_LOG=str(tmp / "psql.log"), REHEARSAL_GO_LOG=str(tmp / "go.log"),
                          REHEARSAL_ROLES=str(tmp / "roles.json"), GEMINUS_ALLOW_DIRTY="1",
                          RAILWAY_API_TOKEN="REHEARSAL-TOKEN-NOT-REAL",
                          GEMINUS_AUTH0_ISSUER="https://geminus-demo.us.auth0.com/", GEMINUS_AUTH0_AUDIENCE="https://api.geminus-demo.invalid",
                          GEMINUS_AUTH0_CLIENT_ID="rehearsalclientid", GEMINUS_SEED_OWNER_SUBJECT="auth0|rehearsal-owner",
                          GEMINUS_RAILWAY_BUCKET_REGION="sjc")
        os.environ.pop("RAILWAY_TOKEN", None)
        # The verification's HTTP requests and waits, without the network.
        answers = {"/healthz": (200, {}), "/readyz": (200, {}), "/v1/me": (401, {}), "/": (200, {}),
                   "/auth/login": (503, {})}
        mod.Run.get = staticmethod(lambda url: answers["/" + url.split("/", 3)[3] if url.count("/") > 2 else "/"])
        mod.time.sleep = lambda s: None

        def apply():
            run = mod.Run(dry=False)
            buf = io.StringIO()
            with redirect_stdout(buf):
                try:
                    run.main()
                    result = "done"
                except mod.Failed as e:
                    result = "FAIL: " + str(e)
            return run, buf.getvalue(), result

        # Run 1: an empty account; the owner has not set BFF_OIDC_CLIENT_SECRET.
        run1, out1, res1 = apply()
        if "verification" not in res1:
            print(textwrap.indent(out1[-3000:], "run 1 | "))
            print("REHEARSAL FAILED: run 1 stopped before verification: " + res1[:600])
            return 1
        st = json.load(open(tmp / "state.json"))
        calls = [c["argv"] for c in st["calls"]]
        hidden = [s for s in run1.hidden if s]
        if not res1.startswith("FAIL: verification") or "the owner has not set BFF_OIDC_CLIENT_SECRET on web" not in res1:
            failures.append(f"run 1 should fail verification on the owner's missing client secret: {res1[:600]}")
            print(textwrap.indent(out1[-3000:], "run 1 | "))
        if len(hidden) < 8:
            failures.append(f"only {len(hidden)} secrets were tracked")
        flat = json.dumps(calls) + out1 + read(tmp / "psql.log") + read(tmp / "go.log")
        for s in hidden + ["REHEARSAL-TOKEN-NOT-REAL", "tsec_REHEARSALSECRETACCESSKEY0123456789", "REHEARSALADMINPW"]:
            if s in flat:
                failures.append(f"a secret appears in an argument or the output: {s[:6]}…")
        stdin_sets = {(c["argv"][c["argv"].index("--service") + 1], c["argv"][2]) for c in st["calls"] if c["stdin"]}
        want_stdin = {("migrate", "GEMINUS_MIGRATOR_DB_PASSWORD"), ("api", "GEMINUS_API_DB_PASSWORD"), ("worker", "GEMINUS_WORKER_DB_PASSWORD"),
                      ("web", "SESSION_KEYS"), ("api", "OBJECT_STORAGE_S3_SECRET_ACCESS_KEY"), ("worker", "OBJECT_STORAGE_S3_SECRET_ACCESS_KEY"),
                      ("api", "OBJECT_STORAGE_S3_ACCESS_KEY_ID"), ("worker", "OBJECT_STORAGE_S3_ACCESS_KEY_ID")}
        if stdin_sets != want_stdin:
            failures.append(f"secrets on stdin: {sorted(stdin_sets ^ want_stdin)} differ")
        if {tuple(x) for x in st.get("sealed", [])} != want_stdin:
            failures.append(f"sealed: {sorted({tuple(x) for x in st.get('sealed', [])} ^ want_stdin)} differ")
        owner = {"BFF_OIDC_CLIENT_SECRET", "GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "GEMINUS_MUSE_API_KEY", "GEMINUS_AI_LIVE"}
        for svc, vs in st["vars"].items():
            if owner & set(vs):
                failures.append(f"the deployer set the owner's {sorted(owner & set(vs))} on {svc}")
        if {"GEMINUS_AI_LIVE", "GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_MUSE_API_KEY"} & set(st["vars"].get("api", {})):
            failures.append("the API got an AI key or the live switch")
        if st["vars"]["api"].get("AI_PROVIDER") != "disabled" or st["vars"]["worker"].get("WORKER_ORGANIZATION_IDS") != "org_01M5W4RHAFH8Q7Y8PNQT1RAF4T":
            failures.append("AI_PROVIDER or WORKER_ORGANIZATION_IDS not as expected")
        if st["vars"]["web"]["BFF_OIDC_AUTHORIZATION_URL"] != "https://geminus-demo.us.auth0.com/authorize" or \
                st["vars"]["api"]["OBJECT_STORAGE_S3_BUCKET"] != "geminus-objects-abc123":
            failures.append("filled values are wrong")
        if st["deploys"] != ["migrate", "api", "worker", "web"]:
            failures.append(f"deploy order {st['deploys']}")
        order = [i for i, c in enumerate(calls) if c[:1] == ["up"] and "migrate" in c]
        if read(tmp / "go.log").count("provision-tenant") != 1 or not order:
            failures.append("the seed ran other than once")
        if st["proxies"]:
            failures.append("the TCP proxy was left open")
        if any(c[:2] == ["variable", "list"] for c in calls) and "variable list" in out1:
            failures.append("a variable listing was printed")
        psql = [json.loads(line) for line in read(tmp / "psql.log").splitlines()]
        if not all(e["has_password_env"] for e in psql if "--version" not in e["argv"]):
            failures.append("psql ran without the administrator's password in its environment")

        # Run 2: the owner set the client secret in the UI; nothing is created twice.
        st["vars"]["web"]["BFF_OIDC_CLIENT_SECRET"] = "owner-set-REHEARSAL"
        json.dump(st, open(tmp / "state.json", "w"))
        answers["/auth/login"] = (303, {"Location": "https://geminus-demo.us.auth0.com/authorize?client_id=x"})
        before = {k: v for k, v in st["vars"].items()}
        run2, out2, res2 = apply()
        st2 = json.load(open(tmp / "state.json"))
        if res2 != "done":
            failures.append(f"run 2: {res2[:300]}")
        calls2 = [c["argv"] for c in st2["calls"][len(st["calls"]):]]
        for c in calls2:
            if c[:1] in (["init"], ["add"]) or c[:2] == ["bucket", "create"] or (c[:1] == ["domain"] and c[1:2] != ["list"]):
                failures.append(f"run 2 created again: {c[:3]}")
        if read(tmp / "go.log").count("provision-tenant") != 1:
            failures.append("run 2 seeded a second organization")
        if st2["vars"]["web"]["BFF_OIDC_CLIENT_SECRET"] != "owner-set-REHEARSAL":
            failures.append("run 2 touched the owner's secret")
        for svc, name in (("api", "GEMINUS_API_DB_PASSWORD"), ("web", "SESSION_KEYS")):
            if st2["vars"][svc][name] != before[svc][name]:
                failures.append(f"run 2 replaced {svc} {name}")
        if any(c["stdin"] for c in st2["calls"][len(st["calls"]):]):
            failures.append("run 2 set a secret again (they are sealed and in use)")

        # Run 3: the secret is set but the sign-in does not reach the issuer.
        res3 = []
        for answer in ((503, {}), (303, {"Location": "https://elsewhere.invalid/authorize?client_id=x"})):
            answers["/auth/login"] = answer
            _, _, r = apply()
            res3.append(r.splitlines()[0] if r else r)
            if not r.startswith("FAIL: verification") or "sign-in does not redirect" not in r or "the owner has not set" in r:
                failures.append(f"run 3 ({answer[0]} {answer[1].get('Location', '')}) should fail on the sign-in only: {r[:300]}")

        # Run 5 first (a fresh project): GEMINUS_RAILWAY_SEAL=off seals nothing.
        os.environ.update(REHEARSAL_STATE=str(tmp / "state5.json"), REHEARSAL_ROLES=str(tmp / "roles5.json"),
                          REHEARSAL_GO_LOG=str(tmp / "go5.log"), GEMINUS_RAILWAY_SEAL="off")
        _, out5, res5 = apply()
        os.environ.pop("GEMINUS_RAILWAY_SEAL")
        st5 = json.load(open(tmp / "state5.json"))
        if st5.get("sealed") or "GEMINUS_RAILWAY_SEAL=off" not in out5 or res5.startswith("FAIL: `"):
            failures.append(f"run 5 (GEMINUS_RAILWAY_SEAL=off) sealed {st5.get('sealed')}: {res5[:200]}")

        # Run 4: a fresh project whose CLI quotes the stdin value back in an error.
        os.environ.update(REHEARSAL_STATE=str(tmp / "state4.json"), REHEARSAL_ECHO_STDIN="1")
        run4, out4, res4 = apply()
        os.environ.pop("REHEARSAL_ECHO_STDIN")
        hidden4 = [s for s in run4.hidden if s]
        if not res4.startswith("FAIL: `railway variable set`") or "[redacted]" not in out4 or not hidden4:
            failures.append(f"run 4 should fail at the first secret with the CLI's error redacted: {res4[:300]}")
        for s in hidden4:
            if s in out4 + res4:
                failures.append(f"run 4 printed a secret from the CLI's error: {s[:6]}…")

        print(textwrap.indent(out1[-1500:], "run 1 | "))
        print(textwrap.indent(out2[-800:], "run 2 | "))
        print(textwrap.indent(out4[-400:], "run 4 | "))
        print(f"run 1: {len(calls)} CLI calls, {len(psql)} psql calls, result {res1.splitlines()[0]}")
        print(f"run 2: {len(calls2)} CLI calls, result {res2}")
        print(f"run 3: {res3}")
        print(f"run 4: {res4.splitlines()[0]}")
    if failures:
        print("REHEARSAL FAILED:\n  - " + "\n  - ".join(failures))
        return 1
    print("rehearsal: PASS (the deployer against stand-ins; Railway itself NOT RUN)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
