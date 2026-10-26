#!/usr/bin/env python3
"""Offline rehearsal of `deploy/railway/deploy.sh --apply` (T-039): runs the
deployer's whole --apply path against stand-ins for `railway`, `psql` and
`go` (scripts written to a temporary directory and put first on PATH; no
network, no Railway API, no database): an empty project, then the same
project again (idempotency), then scenarios on copies of that project. It
checks what the deployer controls, not what Railway does:

- every secret it makes or copies (database passwords, SESSION_KEYS, the
  bucket's keys) reaches the CLI only on stdin, never as an argument, and
  never appears in its output; the Railway token is redacted when the CLI
  quotes it, and no other program (psql, go) gets it in its environment;
- it never sets the owner's variables (BFF_OIDC_CLIENT_SECRET, the AI keys,
  GEMINUS_AI_LIVE), and when the CLI returns their values (the owner did
  not seal them) the values appear nowhere in the deployer: not in its
  state, its output, its errors or anything its methods return; it names
  the unsealed ones and tells the owner to seal them; the API never gets an
  AI key or the switch;
- the order: logins before the migrate deployment, the migrate deployment
  before the seed, the seed before the API, worker and web, the TCP proxy
  removed at the end; public domains only for api and web;
- the TCP proxy is never left open silently (review round 1, M1): a failed
  or silent removal, a proxy open before the run, a proxy whose id the CLI
  did not print, a listing that fails at the end, and SIGTERM or SIGHUP in
  the middle of the run all end loudly (non-zero, the proxy's id and the
  command that removes it), unless GEMINUS_KEEP_DB_PROXY=1 keeps an open one;
- a second run creates nothing again, keeps the generated secrets and seeds
  no second organization;
- verification fails clearly while the owner's BFF_OIDC_CLIENT_SECRET is
  missing (the owner check names it), and fails when the sign-in does not
  redirect to the issuer even with it set;
- a CLI error that quotes a secret is printed redacted;
- deploy.sh refuses to run with xtrace on.

The stand-ins answer with the JSON shapes the CLI's source (v5.64.2) prints
where it was read (variable list, tcp-proxy), otherwise with plausible ones:
a pass here says the deployer is consistent, not that Railway accepts every
call (NOT RUN until the coordinator's --apply).
"""
import functools
import importlib.util
import inspect
import io
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import textwrap
from contextlib import redirect_stdout
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TOKEN = "REHEARSAL-TOKEN-NOT-REAL"

RAILWAY = r'''#!/usr/bin/env python3
import json, os, sys
state_path = os.environ["REHEARSAL_STATE"]
st = json.load(open(state_path)) if os.path.exists(state_path) else {"projects": [], "services": [], "buckets": [], "vars": {}, "domains": {}, "proxies": [], "calls": [], "deploys": []}
a = sys.argv[1:]
stdin = sys.stdin.read() if "--stdin" in a else None
st["calls"].append({"argv": a, "stdin": stdin is not None, "token": bool(os.environ.get("RAILWAY_API_TOKEN") or os.environ.get("RAILWAY_TOKEN"))})
def opt(name):
    return a[a.index(name) + 1] if name in a else None
def fail(msg):
    print(msg, file=sys.stderr)
    json.dump(st, open(state_path, "w"))
    sys.exit(1)
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
    if os.environ.get("REHEARSAL_ECHO_TOKEN"):
        # A CLI that quotes the token in its error: the deployer must redact it.
        fail("error: the token " + (os.environ.get("RAILWAY_API_TOKEN") or os.environ.get("RAILWAY_TOKEN") or "") + " is not authorized")
    out = [{"id": "svc_" + n, "name": n} for n in st["services"]]
elif cmd == "add":
    name = "Postgres" if opt("--database") else opt("--service")
    st["services"].append(name)
    if name == "Postgres":
        # As the Postgres template has them (the administrator's credentials among them).
        st["vars"]["Postgres"] = {"PGHOST": "postgres.railway.internal", "PGPORT": "5432", "PGDATABASE": "railway", "PGUSER": "postgres",
                                  "POSTGRES_DB": "railway", "POSTGRES_USER": "postgres", "PGPASSWORD": "REHEARSALADMINPW",
                                  "POSTGRES_PASSWORD": "REHEARSALADMINPW",
                                  "DATABASE_URL": "postgresql://postgres:REHEARSALADMINPW@postgres.railway.internal:5432/railway"}
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
    # src/commands/variable.rs (v5.64.2): a flat {name: value} map, sealed values null,
    # Railway's own variables included.
    svc = opt("--service")
    v = {"RAILWAY_SERVICE_NAME": svc, "RAILWAY_ENVIRONMENT_NAME": "production", "RAILWAY_PROJECT_NAME": "geminus-demo",
         "RAILWAY_PRIVATE_DOMAIN": svc.lower() + ".railway.internal"}
    v.update(st["vars"].get(svc, {}))
    for s, name in st.get("sealed", []):
        if s == svc and name in v:
            v[name] = None
    out = v
elif cmd == "variable" and a[1] == "set":
    svc = opt("--service")
    if stdin is not None and os.environ.get("REHEARSAL_ECHO_STDIN"):
        # A CLI that quotes the value back in its error: the deployer must redact it.
        fail(f"error: {a[2]}={stdin} rejected")
    if "--stdin" in a:
        st["vars"].setdefault(svc, {})[a[2]] = stdin
    else:
        k, _, v = a[2].partition("=")
        st["vars"].setdefault(svc, {})[k] = v
elif cmd == "tcp-proxy" and a[1] == "list":
    if os.environ.get("REHEARSAL_PROXY_LIST_FAILS_LATE") and st.get("proxy_deleted"):
        fail("error: could not reach Railway")
    # src/commands/tcp_proxy.rs (v5.64.2): {"proxies": [TcpProxy]}.
    out = {"proxies": [{"id": p, "domain": "shuttle.proxy.rlwy.net", "proxyPort": 41234, "applicationPort": 5432,
                        "endpoint": "shuttle.proxy.rlwy.net:41234", "syncStatus": "ACTIVE"} for p in st["proxies"]]}
elif cmd == "tcp-proxy" and a[1] == "create":
    st["proxies"].append("tcp_1")
    st["vars"]["Postgres"]["DATABASE_PUBLIC_URL"] = "postgresql://postgres:REHEARSALADMINPW@shuttle.proxy.rlwy.net:41234/railway"
    proxy = {"id": "tcp_1", "domain": "shuttle.proxy.rlwy.net", "proxyPort": 41234, "applicationPort": 5432,
             "endpoint": "shuttle.proxy.rlwy.net:41234", "syncStatus": "ACTIVE"}
    # The CLI prints "proxy": null when the proxy is not available yet.
    out = {"applicationPort": 5432, "staged": False, "committed": True, "proxy": None if os.environ.get("REHEARSAL_CREATE_NO_ID") else proxy}
elif cmd == "tcp-proxy" and a[1] == "delete":
    if os.environ.get("REHEARSAL_DELETE_FAILS"):
        fail("error: could not delete the TCP proxy")
    st["proxy_deleted"] = True
    if not os.environ.get("REHEARSAL_DELETE_SILENT"):  # silent: says deleted, is not
        st["proxies"] = [p for p in st["proxies"] if p != a[2]]
        st["vars"]["Postgres"].pop("DATABASE_PUBLIC_URL", None)
    out = {"deleted": True, "id": a[2], "endpoint": "shuttle.proxy.rlwy.net:41234", "applicationPort": 5432, "staged": False, "committed": True}
elif cmd == "up":
    if os.environ.get("REHEARSAL_SIGNAL_AT_LOGINS"):
        fail("error: the signal scenario never deploys")
    st["deploys"].append(opt("--service"))
elif cmd == "deployment":
    out = [{"id": "dep_1", "status": "SUCCESS"}]
json.dump(st, open(state_path, "w"))
print(json.dumps(out))
'''

PSQL = r'''#!/usr/bin/env python3
import json, os, signal, sys, time
log = os.environ["REHEARSAL_PSQL_LOG"]
a = sys.argv[1:]
stdin = sys.stdin.read() if "-" in a else ""
entry = {"argv": a, "has_password_env": bool(os.environ.get("PGPASSWORD")),
         "token_env": sorted(k for k in ("RAILWAY_TOKEN", "RAILWAY_API_TOKEN") if k in os.environ),
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
    sig = os.environ.get("REHEARSAL_SIGNAL_AT_LOGINS")
    if sig:
        # The operator's session ends (SIGTERM) or its terminal closes (SIGHUP) mid-run.
        os.kill(os.getppid(), getattr(signal, "SIG" + sig))
        time.sleep(2)
        sys.exit(0)
    json.dump(["geminus_api", "geminus_migrator", "geminus_provisioner", "geminus_worker"], open(os.environ["REHEARSAL_ROLES"], "w"))
'''

GO = r'''#!/usr/bin/env python3
import os, sys
with open(os.environ["REHEARSAL_GO_LOG"], "a") as f:
    f.write(" ".join(sys.argv[1:]) + " | url-env:" + str(bool(os.environ.get("GEMINUS_PROVISION_DATABASE_URL")))
            + " | token-env:" + str(sorted(k for k in ("RAILWAY_TOKEN", "RAILWAY_API_TOKEN") if k in os.environ)) + "\n")
print('{"organization_id":"org_01M5W4RHAFH8Q7Y8PNQT1RAF4T","owner_user_id":"usr_01M5W4RHAGH9JGFGMTQ8H6SK6H"}')
'''

# The owner's unsealed values on run 2 (the CLI returns them): none may reach the deployer's state.
OWNER_VALUES = {
    ("web", "BFF_OIDC_CLIENT_SECRET"): "OWNER-CLIENT-SECRET-SENTINEL-3f9a",
    ("worker", "GEMINUS_ANTHROPIC_API_KEY_A"): "OWNER-ANTHROPIC-A-SENTINEL-77c1",
    ("worker", "GEMINUS_ANTHROPIC_API_KEY_B"): "OWNER-ANTHROPIC-B-SENTINEL-08d2",
    ("worker", "GEMINUS_MUSE_API_KEY"): "OWNER-MUSE-KEY-SENTINEL-5e6b",
    ("worker", "GEMINUS_AI_LIVE"): "OWNER-LIVE-SWITCH-SENTINEL-on",
    # Not in variables.json: a value the deployer has no use for is not kept either.
    ("worker", "OWNER_EXTRA_SETTING"): "UNLISTED-VALUE-SENTINEL-41aa",
}
OWNER_SECRETS = ("BFF_OIDC_CLIENT_SECRET", "GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "GEMINUS_MUSE_API_KEY")


def read(path) -> str:
    return path.read_text() if path.exists() else ""


def load():
    sys.dont_write_bytecode = True  # no __pycache__ in deploy/railway
    spec = importlib.util.spec_from_file_location("railway_deploy", ROOT / "deploy/railway/railway_deploy.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def holds(obj, needles, seen=None) -> set:
    """The needles found anywhere in obj: containers, exceptions (with their
    causes and contexts), objects' attributes and their string forms."""
    seen = set() if seen is None else seen
    if id(obj) in seen:
        return set()
    seen.add(id(obj))
    found = set()
    if isinstance(obj, (str, bytes)):
        text = obj.decode("utf-8", "replace") if isinstance(obj, bytes) else obj
        return {n for n in needles if n in text}
    if isinstance(obj, dict):
        for k, v in obj.items():
            found |= holds(k, needles, seen) | holds(v, needles, seen)
    elif isinstance(obj, (list, tuple, set, frozenset)):
        for v in obj:
            found |= holds(v, needles, seen)
    elif isinstance(obj, BaseException):
        found |= holds(list(obj.args), needles, seen) | holds(str(obj), needles, seen)
        found |= holds(obj.__cause__, needles, seen) | holds(obj.__context__, needles, seen)
        found |= holds(getattr(obj, "__dict__", {}), needles, seen)
    elif obj is not None and not isinstance(obj, (int, float, bool, type)) and not inspect.isroutine(obj) and not inspect.ismodule(obj):
        found |= holds(getattr(obj, "__dict__", {}), needles, seen) | holds(repr(obj), needles, seen)
    return found


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
                          RAILWAY_API_TOKEN=TOKEN,
                          GEMINUS_AUTH0_ISSUER="https://geminus-demo.us.auth0.com/", GEMINUS_AUTH0_AUDIENCE="https://api.geminus-demo.invalid",
                          GEMINUS_AUTH0_CLIENT_ID="rehearsalclientid", GEMINUS_SEED_OWNER_SUBJECT="auth0|rehearsal-owner",
                          GEMINUS_RAILWAY_BUCKET_REGION="sjc")
        for k in ("RAILWAY_TOKEN", "GEMINUS_KEEP_DB_PROXY", "GEMINUS_RAILWAY_SEAL"):
            os.environ.pop(k, None)
        # The verification's HTTP requests and waits, without the network.
        answers = {"/healthz": (200, {}), "/readyz": (200, {}), "/v1/me": (401, {}), "/": (200, {}),
                   "/auth/login": (503, {})}
        mod.Run.get = staticmethod(lambda url: answers["/" + url.split("/", 3)[3] if url.count("/") > 2 else "/"])
        mod.time.sleep = lambda s: None

        # Everything the deployer's methods return or raise (run 2 scans it for the owner's values).
        returned = []

        def record(fn):
            @functools.wraps(fn)
            def wrapper(*args, **kwargs):
                try:
                    r = fn(*args, **kwargs)
                except BaseException as e:
                    returned.append(e)
                    raise
                if not inspect.isgenerator(r):
                    returned.append(r)
                return r
            return wrapper
        for name, attr in list(vars(mod.Run).items()):
            if name.startswith("__"):
                continue
            if isinstance(attr, staticmethod):
                setattr(mod.Run, name, staticmethod(record(attr.__func__)))
            elif inspect.isfunction(attr):
                setattr(mod.Run, name, record(attr))

        def apply(**env):
            saved = {k: os.environ.get(k) for k in env}
            os.environ.update(env)
            run = mod.Run(dry=False)
            buf = io.StringIO()
            try:
                with redirect_stdout(buf):
                    try:
                        run.main()
                        result = "done"
                    except mod.Failed as e:
                        result = "FAIL: " + str(e)
            finally:
                for k, v in saved.items():
                    if v is None:
                        os.environ.pop(k, None)
                    else:
                        os.environ[k] = v
            return run, buf.getvalue(), result

        def state(name="state.json"):
            return json.load(open(tmp / name))

        def scenario(name, base):
            """A copy of the project as `base` left it, as this scenario's state."""
            json.dump(base, open(tmp / f"state-{name}.json", "w"))
            os.environ["REHEARSAL_STATE"] = str(tmp / f"state-{name}.json")
            return f"state-{name}.json"

        # Run 1: an empty account; the owner has not set BFF_OIDC_CLIENT_SECRET.
        run1, out1, res1 = apply()
        if "verification" not in res1:
            print(textwrap.indent(out1[-3000:], "run 1 | "))
            print("REHEARSAL FAILED: run 1 stopped before verification: " + res1[:600])
            return 1
        st = state()
        calls = [c["argv"] for c in st["calls"]]
        hidden = [s for s in run1.hidden if s]
        if not res1.startswith("FAIL: verification") or "the owner has not set BFF_OIDC_CLIENT_SECRET on web" not in res1:
            failures.append(f"run 1 should fail verification on the owner's missing client secret: {res1[:600]}")
            print(textwrap.indent(out1[-3000:], "run 1 | "))
        if len(hidden) < 8:
            failures.append(f"only {len(hidden)} secrets were tracked")
        if TOKEN not in run1.hidden:
            failures.append("the Railway token is not among the values to redact")
        flat = json.dumps(calls) + out1 + read(tmp / "psql.log") + read(tmp / "go.log")
        for s in hidden + [TOKEN, "tsec_REHEARSALSECRETACCESSKEY0123456789", "REHEARSALADMINPW"]:
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
        domains = sorted({c[c.index("--service") + 1] for c in calls if c[:1] == ["domain"] and c[1:2] != ["list"]})
        if domains != ["api", "web"]:
            failures.append(f"public domains were created for {domains}, want api and web only (L5)")
        order = [i for i, c in enumerate(calls) if c[:1] == ["up"] and "migrate" in c]
        if read(tmp / "go.log").count("provision-tenant") != 1 or not order:
            failures.append("the seed ran other than once")
        if st["proxies"]:
            failures.append("the TCP proxy was left open")
        deleted_at = [i for i, c in enumerate(calls) if c[:2] == ["tcp-proxy", "delete"]]
        if not deleted_at or not any(c[:2] == ["tcp-proxy", "list"] for c in calls[deleted_at[-1]:]):
            failures.append("the TCP proxies were not listed after the removal (M1)")
        if any(c[:2] == ["variable", "list"] for c in calls) and "variable list" in out1:
            failures.append("a variable listing was printed")
        psql = [json.loads(line) for line in read(tmp / "psql.log").splitlines()]
        if not all(e["has_password_env"] for e in psql if "--version" not in e["argv"]):
            failures.append("psql ran without the administrator's password in its environment")
        if any(e["token_env"] for e in psql) or "token-env:[]" not in read(tmp / "go.log") or "token-env:['" in read(tmp / "go.log"):
            failures.append("psql or go got the Railway token in its environment (L2)")
        if not all(c["token"] for c in st["calls"]):
            failures.append("a railway call ran without the token")

        # Run 2: the owner set the client secret, the AI keys and the switch in the UI without
        # sealing them, so the CLI returns their values; nothing is created twice.
        for (svc, name), value in OWNER_VALUES.items():
            st["vars"][svc][name] = value
        json.dump(st, open(tmp / "state.json", "w"))
        answers["/auth/login"] = (303, {"Location": "https://geminus-demo.us.auth0.com/authorize?client_id=x"})
        before = {k: v for k, v in st["vars"].items()}
        returned.clear()
        run2, out2, res2 = apply()
        st2 = state()
        if res2 != "done":
            failures.append(f"run 2: {res2[:300]}")
        sentinels = set(OWNER_VALUES.values())
        for where, obj in (("its state", vars(run2)), ("its output", out2), ("its result", res2),
                           ("what its methods returned or raised", returned)):
            leaked = holds(obj, sentinels)
            if leaked:
                failures.append(f"run 2: the owner's values are in {where}: {sorted(s[:12] for s in leaked)} (M2)")
        notes = [line for line in out2.splitlines() if "not sealed" in line]
        for name in OWNER_SECRETS:
            if not any(name in line for line in notes):
                failures.append(f"run 2: the unsealed {name} is not named (M2)")
        if any("GEMINUS_AI_LIVE" in line or "OWNER_EXTRA_SETTING" in line for line in notes):
            failures.append("run 2: a variable that is no secret of the owner's is reported unsealed")
        calls2 = [c["argv"] for c in st2["calls"][len(st["calls"]):]]
        for c in calls2:
            if c[:1] in (["init"], ["add"]) or c[:2] == ["bucket", "create"] or (c[:1] == ["domain"] and c[1:2] != ["list"]):
                failures.append(f"run 2 created again: {c[:3]}")
        if read(tmp / "go.log").count("provision-tenant") != 1:
            failures.append("run 2 seeded a second organization")
        for (svc, name), value in OWNER_VALUES.items():
            if st2["vars"][svc].get(name) != value:
                failures.append(f"run 2 touched the owner's {name}")
        for svc, name in (("api", "GEMINUS_API_DB_PASSWORD"), ("web", "SESSION_KEYS")):
            if st2["vars"][svc][name] != before[svc][name]:
                failures.append(f"run 2 replaced {svc} {name}")
        if any(c["stdin"] for c in st2["calls"][len(st["calls"]):]):
            failures.append("run 2 set a secret again (they are sealed and in use)")

        # Run 2b: the owner sealed them: nothing is reported unsealed.
        st2.setdefault("sealed", []).extend([svc, name] for (svc, name) in OWNER_VALUES if name in OWNER_SECRETS)
        json.dump(st2, open(tmp / "state.json", "w"))
        _, out2b, res2b = apply()
        if res2b != "done" or "not sealed" in out2b:
            failures.append(f"run 2b (the owner's secrets sealed): {res2b[:200]} {[l for l in out2b.splitlines() if 'not sealed' in l]}")
        base = state()  # a deployed project, every proxy closed: the scenarios below start from copies of it
        base["calls"] = []
        base.pop("proxy_deleted", None)

        # Run 3: the secret is set but the sign-in does not reach the issuer.
        res3 = []
        for answer in ((503, {}), (303, {"Location": "https://elsewhere.invalid/authorize?client_id=x"})):
            answers["/auth/login"] = answer
            _, _, r = apply()
            res3.append(r.splitlines()[0] if r else r)
            if not r.startswith("FAIL: verification") or "sign-in does not redirect" not in r or "the owner has not set" in r:
                failures.append(f"run 3 ({answer[0]} {answer[1].get('Location', '')}) should fail on the sign-in only: {r[:300]}")
        answers["/auth/login"] = (303, {"Location": "https://geminus-demo.us.auth0.com/authorize?client_id=x"})

        # The TCP proxy (review round 1, M1). P1: the removal fails.
        delete_cmd = "railway tcp-proxy delete {} --yes --service Postgres --environment production"
        shown = []  # each scenario's result, for the log
        name = scenario("p1", base)
        _, out, res = apply(REHEARSAL_DELETE_FAILS="1")
        shown.append(("P1 the removal fails", res))
        if not res.startswith("FAIL") or "could not remove this run's TCP proxy tcp_1" not in res or delete_cmd.format("tcp_1") not in res:
            failures.append(f"P1 (the removal fails) must fail naming tcp_1 and its removal: {res[:400]}")
        if "deploy: done" in out or state(name)["proxies"] != ["tcp_1"]:
            failures.append("P1: said done, or the stand-in's proxy is not the one reported")
        # P2: a proxy open before the run (left by a killed run, or someone's): kept, reported every run.
        left = dict(base, proxies=["tcp_left_by_killed_run"])
        left["vars"] = dict(base["vars"], Postgres=dict(base["vars"]["Postgres"],
                            DATABASE_PUBLIC_URL="postgresql://postgres:REHEARSALADMINPW@shuttle.proxy.rlwy.net:41234/railway"))
        name = scenario("p2", left)
        for i in (1, 2):
            _, out, res = apply()
            shown.append((f"P2.{i} a proxy from before the run", res))
            if not res.startswith("FAIL") or "tcp_left_by_killed_run" not in res or delete_cmd.format("tcp_left_by_killed_run") not in res \
                    or "before this run" not in res:
                failures.append(f"P2.{i} (a proxy from before the run) must fail naming it and its removal: {res[:400]}")
        p2 = state(name)
        if p2["proxies"] != ["tcp_left_by_killed_run"] or any(c["argv"][:2] == ["tcp-proxy", "create"] for c in p2["calls"]) \
                or any(c["argv"][:2] == ["tcp-proxy", "delete"] for c in p2["calls"]):
            failures.append(f"P2: the proxy from before the run must be used, not removed, no second one made: {p2['proxies']}")
        _, out, res = apply(GEMINUS_KEEP_DB_PROXY="1")
        shown.append(("P2.3 the same with GEMINUS_KEEP_DB_PROXY=1", res + "\n" + "\n".join(l for l in out.splitlines() if "warning" in l)))
        if res != "done" or not any("tcp_left_by_killed_run" in line and "GEMINUS_KEEP_DB_PROXY" in line for line in out.splitlines()):
            failures.append(f"P2 with GEMINUS_KEEP_DB_PROXY=1 must succeed and still name the open proxy: {res[:300]}")
        if state(name)["proxies"] != ["tcp_left_by_killed_run"]:
            failures.append("P2 with GEMINUS_KEEP_DB_PROXY=1 changed the proxies")
        # P3: the CLI says it removed the proxy but it is still there.
        name = scenario("p3", base)
        _, out, res = apply(REHEARSAL_DELETE_SILENT="1")
        shown.append(("P3 a removal that did not remove", res))
        if not res.startswith("FAIL") or "tcp_1" not in res or delete_cmd.format("tcp_1") not in res or "deploy: done" in out:
            failures.append(f"P3 (a removal that did not remove) must fail naming tcp_1: {res[:400]}")
        # P4: `tcp-proxy create` prints no id (proxy: null): found by listing, removed.
        name = scenario("p4", base)
        _, out, res = apply(REHEARSAL_CREATE_NO_ID="1")
        shown.append(("P4 create prints no id", res + f" (proxies left: {state(name)['proxies']})"))
        if res != "done" or state(name)["proxies"]:
            failures.append(f"P4 (no id from create) must find and remove the proxy: {res[:300]} {state(name)['proxies']}")
        # P5: the proxies cannot be listed at the end: not knowing is a failure.
        name = scenario("p5", base)
        _, out, res = apply(REHEARSAL_PROXY_LIST_FAILS_LATE="1")
        shown.append(("P5 the final listing fails", res))
        if not res.startswith("FAIL") or "could not list the TCP proxies" not in res:
            failures.append(f"P5 (the final listing fails) must fail: {res[:300]}")
        # P6: SIGTERM and SIGHUP in the middle of the run (the deployer as deploy.sh runs it,
        # in its own process): the proxy is removed, the run ends non-zero. No network: the
        # stand-in refuses `railway up`, and HTTPS goes to a closed local port.
        res6 = []
        for sig in ("TERM", "HUP"):
            name = scenario("p6" + sig, base)
            env = dict(os.environ, REHEARSAL_SIGNAL_AT_LOGINS=sig, HTTPS_PROXY="http://127.0.0.1:9", https_proxy="http://127.0.0.1:9")
            p = subprocess.run([sys.executable, "-I", str(ROOT / "deploy/railway/railway_deploy.py"), "--apply"],
                               capture_output=True, text=True, env=env, cwd=ROOT, timeout=300)
            signum = getattr(signal, "SIG" + sig)
            res6.append((sig, p.returncode))
            shown.append((f"P6 SIG{sig} at the logins (exit {p.returncode}, proxies left: {state(name)['proxies']})",
                           "\n".join(p.stdout.splitlines()[-7:])))
            if p.returncode != 128 + signum or "interrupted" not in p.stdout or "removing this run's TCP proxy" not in p.stdout \
                    or "deploy: done" in p.stdout or state(name)["proxies"]:
                failures.append(f"P6 SIG{sig}: exit {p.returncode} (want {128 + signum}), proxies {state(name)['proxies']}: "
                                f"{(p.stdout + p.stderr)[-400:]}")
            if TOKEN in p.stdout + p.stderr:
                failures.append(f"P6 SIG{sig}: the token was printed")

        # E1: the CLI quotes the token in an error (L2): redacted.
        scenario("e1", base)
        _, out, res = apply(REHEARSAL_ECHO_TOKEN="1")
        shown.append(("E1 the CLI quotes the token", "\n".join(l for l in out.splitlines() if "failed" in l) + "\n" + res))
        if not res.startswith("FAIL") or TOKEN in out + res or "[redacted]" not in out:
            failures.append(f"E1 (the CLI quotes the token): {res[:200]} redacted={'[redacted]' in out}")

        # Run 5 first (a fresh project): GEMINUS_RAILWAY_SEAL=off seals nothing.
        os.environ.update(REHEARSAL_STATE=str(tmp / "state5.json"), REHEARSAL_ROLES=str(tmp / "roles5.json"),
                          REHEARSAL_GO_LOG=str(tmp / "go5.log"))
        _, out5, res5 = apply(GEMINUS_RAILWAY_SEAL="off")
        st5 = state("state5.json")
        if st5.get("sealed") or "GEMINUS_RAILWAY_SEAL=off" not in out5 or res5.startswith("FAIL: `"):
            failures.append(f"run 5 (GEMINUS_RAILWAY_SEAL=off) sealed {st5.get('sealed')}: {res5[:200]}")

        # Run 4: a fresh project whose CLI quotes the stdin value back in an error.
        os.environ.update(REHEARSAL_STATE=str(tmp / "state4.json"))
        run4, out4, res4 = apply(REHEARSAL_ECHO_STDIN="1")
        hidden4 = [s for s in run4.hidden if s]
        if not res4.startswith("FAIL: `railway variable set`") or "[redacted]" not in out4 or not hidden4:
            failures.append(f"run 4 should fail at the first secret with the CLI's error redacted: {res4[:300]}")
        for s in hidden4:
            if s in out4 + res4:
                failures.append(f"run 4 printed a secret from the CLI's error: {s[:6]}…")

        # deploy.sh with xtrace on (L1): refused before anything runs.
        bash = shutil.which("bash")
        env = {k: v for k, v in os.environ.items() if k not in ("RAILWAY_TOKEN", "RAILWAY_API_TOKEN", "SHELLOPTS")}
        xt = []
        for argv, extra, want in (([bash, "-x"], {}, 2), ([bash], {"SHELLOPTS": "xtrace"}, 2), ([bash], {}, 0)):
            p = subprocess.run([*argv, str(ROOT / "deploy/railway/deploy.sh"), "--help"], capture_output=True, text=True,
                               env=dict(env, **extra), timeout=60)
            xt.append(p.returncode)
            if p.returncode != want or (want == 2) != ("refusing to run with xtrace on" in p.stderr):
                failures.append(f"deploy.sh {' '.join(argv[1:])} {extra} --help: exit {p.returncode}, want {want} (L1): {p.stderr[-300:]}")

        print(textwrap.indent(out1[-1500:], "run 1 | "))
        print(textwrap.indent(out2[-1200:], "run 2 | "))
        print(textwrap.indent(out4[-400:], "run 4 | "))
        print(f"run 1: {len(calls)} CLI calls, {len(psql)} psql calls, result {res1.splitlines()[0]}")
        print(f"run 2: {len(calls2)} CLI calls, result {res2}; the owner's values looked for in its state, output, result "
              f"and {len(returned)} returned or raised values")
        print(f"run 3: {res3}")
        print(f"run 4: {res4.splitlines()[0]}")
        for label, text in shown:
            print(textwrap.indent(text, f"{label} | "))
        print(f"signals {res6}; deploy.sh --help with bash -x, SHELLOPTS=xtrace, plain: exits {xt}")
    if failures:
        print("REHEARSAL FAILED:\n  - " + "\n  - ".join(failures))
        return 1
    print("rehearsal: PASS (the deployer against stand-ins; Railway itself NOT RUN)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
