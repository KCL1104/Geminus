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
  GEMINUS_AI_LIVE, AI_POLICY_APPROVAL, AI_MODEL, and the photo path's
  AI_VISION_PROVIDER, AI_VISION_POLICY_APPROVAL, AI_VISION_MODEL: D-124), and when the CLI returns their values (the owner did
  not seal them) the values appear nowhere in the deployer: not in its
  state, its output, its errors or anything its methods return; it names
  the unsealed ones and tells the owner to seal them; it never gives the API
  an AI key or the switch (the owner may set the switch there: T-053 I5);
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

The runs above are GEMINUS_AUTH0_SETUP=manual: the manual fallback of H-034,
which is the deployer's behavior before the Auth0 step (the owner fills
BFF_OIDC_CLIENT_SECRET). The Auth0 step (T-057, H-034, H-035 (2);
GEMINUS_AUTH0_SETUP=cli) is rehearsed against a stand-in for the pinned Auth0
CLI 1.37.0 in agent mode (JSON on stdout, the JSON error envelope on stderr;
its state is the tenant the CLI would change), on its own projects:

- no secret (the application's client secret, the judge's password, the
  Railway token) is in any argument of any program, in the output (except
  the judge's password, shown exactly once, on stderr, at the end), in the
  deployer's state, its result or anything its methods return or raise;
  secrets the CLI prints that the deployer does not need (another
  application's client secret, an API's signing secret, a connection's
  client secret, a token) are dropped as the JSON is parsed;
- the settings sent are exactly H-034's (the API: RS256, RFC 9068, 600 s,
  offline access; the Regular Web Application: Client Secret (Post),
  authorization code and refresh token only, rotation with a 30 s overlap,
  43200 s idle and 604800 s absolute lifetimes, callback and logout URL only
  on the web's domain, no web origins or CORS; sign-up off on the database
  connection, the other options kept; the application enabled on that
  connection only), and the client secret reaches Railway only on stdin and
  is sealed;
- a rerun creates nothing and, when nothing drifted, changes nothing; a
  drifted tenant (an extra callback, an extra grant, sign-up on again,
  another connection enabled) is put back;
- the CLI runs only after its SHA-256 matched the pinned one; the owner's
  sign-in shows the URL and code and nothing else; signing in to another
  tenant, a failure, SIGTERM (before and after the judge's account exists)
  and the CLI's error that quotes the password all end with the CLI signed
  out and its home removed (also one left by an interrupted run);
- the judge's account is created once, with a generated password shown once,
  reset only on request, and becomes an editor of the demo organization only
  (cmd/provision-tenant's -member-*, in the organization's own
  transaction); a demo organization seeded before the account existed, or a
  membership elsewhere, fails the run with what to do;
- deploy.sh --install-auth0-cli refuses an archive whose SHA-256 is not the
  pinned one, before anything in it runs.

The stand-ins answer with the JSON shapes the CLI's source (v5.64.2) prints
where it was read (variable list, tcp-proxy), otherwise with plausible ones;
the Auth0 stand-in with the shapes of the Auth0 CLI's source at v1.37.0 and
the Management API (checks/evidence/2026-10-26-T-057-auth0/sources.txt):
a pass here says the deployer is consistent, not that Railway or Auth0
accepts every call (NOT RUN until the coordinator's --apply).
"""
import functools
import hashlib
import importlib.util
import inspect
import io
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import tarfile
import tempfile
import textwrap
from contextlib import redirect_stderr, redirect_stdout
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
    if os.environ.get("REHEARSAL_SEAL_FAILS") and any(path == "variables.BFF_OIDC_CLIENT_SECRET.isSealed" for _, path, _ in sc):
        fail("error: the stand-in refuses to seal BFF_OIDC_CLIENT_SECRET")
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
    if os.environ.get("REHEARSAL_SIGNAL_AT_LOGINS") or os.environ.get("REHEARSAL_UP_FAILS"):
        fail("error: the deployment could not start")
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
elif "twin.memberships" in stdin:
    # The judge's memberships, as the administrator lists them (-v issuer=... -v subject=...).
    v = dict(x.split("=", 1) for x in (a[i + 1] for i, y in enumerate(a) if y == "-v") if "=" in x)
    path = os.environ.get("REHEARSAL_TWIN_DB", "")
    rows = json.load(open(path))["memberships"] if path and os.path.exists(path) else []
    rows = rows + json.loads(os.environ.get("REHEARSAL_EXTRA_MEMBERSHIPS", "[]"))
    for org, iss, sub, role in sorted(rows):
        if iss == v.get("issuer") and sub == v.get("subject"):
            print(f"{org} {role}")
elif "-Atc" in a:
    q = a[a.index("-Atc") + 1]
    if "pg_roles" in q:
        print("\n".join(json.load(open(os.environ["REHEARSAL_ROLES"]))) if os.path.exists(os.environ["REHEARSAL_ROLES"]) else "")
    elif "geminus_migrations" in q:
        print(os.environ["REHEARSAL_MIGRATIONS"])  # every migration of the checkout is recorded
    else:
        print("1")
elif "deploy/sql/create_logins.sql" in a:
    if os.environ.get("REHEARSAL_PAUSE_AT_LOGINS"):  # M2: the rehearsal closes the deployer's stdout meanwhile
        time.sleep(1)
    sig = os.environ.get("REHEARSAL_SIGNAL_AT_LOGINS")
    if sig:
        # The operator's session ends (SIGTERM) or its terminal closes (SIGHUP) mid-run.
        os.kill(os.getppid(), getattr(signal, "SIG" + sig))
        time.sleep(2)
        sys.exit(0)
    json.dump(["geminus_api", "geminus_migrator", "geminus_provisioner", "geminus_worker"], open(os.environ["REHEARSAL_ROLES"], "w"))
'''

GO = r'''#!/usr/bin/env python3
import json, os, sys
a = sys.argv[1:]
with open(os.environ["REHEARSAL_GO_LOG"], "a") as f:
    f.write(" ".join(a) + " | url-env:" + str(bool(os.environ.get("GEMINUS_PROVISION_DATABASE_URL")))
            + " | token-env:" + str(sorted(k for k in ("RAILWAY_TOKEN", "RAILWAY_API_TOKEN") if k in os.environ)) + "\n")
# The memberships cmd/provision-tenant would write (REHEARSAL_TWIN_DB), which the psql stand-in answers from.
path = os.environ.get("REHEARSAL_TWIN_DB", "")
db = json.load(open(path)) if path and os.path.exists(path) else {"users": {}, "memberships": []}
def opt(name):
    return a[a.index(name) + 1] if name in a else None
def uid(subject):
    if subject not in db["users"]:
        db["users"][subject] = "usr_01M5W4RHAGH9JGFGMTQ8H6SK6" + "HJKMNPQRST"[len(db["users"]) % 10]
    return db["users"][subject]
if "organization" in a:
    org = "org_01M5W4RHAFH8Q7Y8PNQT1RAF4T"
    out = {"organization_id": org, "owner_user_id": uid(opt("-owner-subject"))}
    db["memberships"].append([org, opt("-owner-issuer"), opt("-owner-subject"), "owner"])
    if opt("-member-subject"):
        out["member_user_id"] = uid(opt("-member-subject"))
        db["memberships"].append([org, opt("-member-issuer"), opt("-member-subject"), opt("-member-role")])
else:
    created = opt("-subject") not in db["users"]
    out = {"created": created, "user_id": uid(opt("-subject"))}
if path:
    json.dump(db, open(path, "w"))
print(json.dumps(out, separators=(",", ":"), sort_keys=True))
'''

AUTH0 = r"""#!/usr/bin/env python3
# A stand-in for the pinned Auth0 CLI 1.37.0 in agent mode (its source at v1.37.0: JSON on stdout,
# {"error": {...}} on stderr and exit 1 on a failure). REHEARSAL_A0_STATE is the tenant it changes;
# REHEARSAL_A0_LOG gets each call's arguments, whether it read stdin (never what), and its environment's
# shape. The CLI's session lives in $HOME/.config/auth0/config.json, as the real CLI keeps it without a
# keyring (internal/config/config.go, internal/cli/login.go). The deployer gives the CLI a small
# environment of its own, so this stand-in reads its settings (state, log, the scenario's knobs) from
# auth0-rehearsal.json in the rehearsal's bin directory (written in below: the deployer runs a copy, L2).
import json, os, signal, sys, time
a = sys.argv[1:]
E = json.load(open(os.path.join("__REHEARSAL_BIN__", "auth0-rehearsal.json")))
state_path, log_path = E["REHEARSAL_A0_STATE"], E["REHEARSAL_A0_LOG"]
st = json.load(open(state_path))
flags = {"--agent-mode", "--no-input", "--no-color"}
groups = ("apis", "apps", "connections", "users", "tenants", "enabled-clients")
words, rest, tenant, i = [], [], None, 0
while i < len(a):
    if a[i] == "--tenant":
        tenant = a[i + 1]
        i += 2
        continue
    if a[i] in flags:
        pass
    elif not words or (words[-1] in groups and not a[i].startswith("-")):
        words.append(a[i])
    else:
        rest.append(a[i])
    i += 1
key = " ".join(words)
stdin = sys.stdin.read() if "@-" in a else None
home = os.environ.get("HOME", "")
cfg_path = os.path.join(home, ".config", "auth0", "config.json")
with open(log_path, "a") as f:
    f.write(json.dumps({"argv": a, "key": key, "stdin": stdin is not None, "home": home, "exe": os.path.abspath(__file__),
                        "tokens": sorted(k for k in os.environ if k.startswith("RAILWAY")),
                        "analytics": os.environ.get("AUTH0_CLI_ANALYTICS"), "dbus": os.environ.get("DBUS_SESSION_BUS_ADDRESS"),
                        "agent": not (flags - set(a))}) + "\n")
def save():
    json.dump(st, open(state_path, "w"))
def fail(message, code="api"):
    save()
    print(json.dumps({"error": {"code": code, "reason": "rehearsal", "message": message, "status": 400}}), file=sys.stderr)
    sys.exit(1)
def out(obj):
    save()
    print(json.dumps(obj, indent=2))
    sys.exit(0)
def opt(name):
    return a[a.index(name) + 1] if name in a else None
if flags - set(a):
    fail("the stand-in expects --agent-mode --no-input --no-color", "usage")
skey = key + (" --reveal-secrets" if "--reveal-secrets" in a else "")
if E.get("REHEARSAL_A0_PAUSE") == skey:  # M2: the rehearsal closes the deployer's stdout meanwhile
    time.sleep(1)
if E.get("REHEARSAL_A0_SIGNAL") == skey:
    os.kill(os.getppid(), getattr(signal, "SIG" + E.get("REHEARSAL_A0_SIG", "TERM")))
    time.sleep(3)
if E.get("REHEARSAL_A0_FAIL") == skey:
    fail("API request failed: the stand-in refuses " + key)
if E.get("REHEARSAL_A0_ECHO_STDIN") == skey:
    st.setdefault("echoed", []).append(stdin or "")  # so the rehearsal knows what to look for
    fail("API request failed: " + (stdin or ""))
session = json.load(open(cfg_path)) if os.path.exists(cfg_path) else None
if key == "login":
    domain = E.get("REHEARSAL_A0_LOGIN_DOMAIN") or opt("--domain")
    # One JSON object per line, as the CLI's OutputPreformattedJSON(json.Marshal(...)) prints them.
    print(json.dumps({"verification_uri": "https://auth0.auth0.com/activate?user_code=QWER-TYUI", "user_code": "QWER-TYUI",
                      "expires_in": 900, "interval": 5, "device_code": "REHEARSAL-DEVICE-CODE-NOT-SHOWN"}), flush=True)
    os.makedirs(os.path.dirname(cfg_path), mode=0o700, exist_ok=True)
    session = session or {"default_tenant": domain, "tenants": {}}
    session["tenants"][domain] = {"domain": domain, "access_token": "REHEARSAL-AUTH0-CLI-ACCESS-TOKEN"}
    json.dump(session, open(cfg_path, "w"))
    save()
    print(json.dumps({"logged_in": True, "tenant": domain.split(".")[0], "domain": domain, "expires_at": "2026-10-27T00:00:00Z"}))
    sys.exit(0)
if key == "logout":
    if not session or rest[0] not in session["tenants"]:
        fail("failed to find tenant: " + rest[0], "usage")
    if not E.get("REHEARSAL_A0_LOGOUT_KEEPS"):
        del session["tenants"][rest[0]]
    json.dump(session, open(cfg_path, "w"))
    # As the real CLI without a keyring (internal/cli/logout.go): the tenant is gone from the config,
    # then deleting its keyring entries fails.
    fail("failed to delete tenant secrets: failed to delete access token from keyring: no secret service", "unknown")
if key == "tenants list":
    if session is None:
        fail("failed to load tenants: config.json file is missing", "unknown")
    out([{"name": d, "active": d == session.get("default_tenant")} for d in session["tenants"]])
if not session or tenant not in session["tenants"]:
    fail("not logged in to " + str(tenant) + ". Try `auth0 login`", "auth")
data = json.loads(stdin) if stdin else None
def write(payload):
    st["writes"].append([key, rest[:1], payload])
def find(items, field, value):
    return next((x for x in items if x.get(field) == value), None)
def client_view(c, reveal=False):
    return {k: v for k, v in c.items() if reveal or k != "client_secret"}
if key == "apis list":
    out(st["apis"])
if key == "apis show":
    api = find(st["apis"], "id", rest[0]) or find(st["apis"], "identifier", rest[0])
    out(api) if api else fail("not found", "not_found")
if key == "apis create":
    write(data)
    api = dict(data, id="rs_rehearsal0001", is_system=False)
    st["apis"].append(api)
    out(api)
if key == "apis update":
    write(data)
    api = find(st["apis"], "id", rest[0])
    api.update(data)
    out(api)
if key == "apps list":
    out(st["apps"])
if key == "apps show":
    c = find(st["apps"], "client_id", rest[0])
    out(client_view(c, "--reveal-secrets" in a)) if c else fail("not found", "not_found")
if key == "apps create":
    write(data)
    c = dict(data, client_id="a0rehearsalclient000000000000001", client_secret="A0-REHEARSAL-CLIENT-SECRET-6f2e0b9c4d7a1e3f5a8b0c2d4e6f8a1b",
             signing_keys=[{"cert": "-----BEGIN CERTIFICATE-----REHEARSAL", "kid": "k1"}])
    st["apps"].append(c)
    for conn in st["connections"]:  # the worst case: a new application is enabled on every connection
        conn["enabled_clients"].append(c["client_id"])
    out(client_view(c))
if key == "apps update":
    write(data)
    c = find(st["apps"], "client_id", rest[0])
    c.update(data)
    out(client_view(c))
if key == "connections list":
    out([{k: v for k, v in c.items() if k != "enabled_clients"} for c in st["connections"]])
if key == "connections show":
    c = find(st["connections"], "id", rest[0])
    out({k: v for k, v in c.items() if k != "enabled_clients"})
if key == "connections update":
    write(data)
    c = find(st["connections"], "id", rest[0])
    if "options" in data:  # the Management API replaces the whole options object
        c["options"] = dict(data["options"])
        if E.get("REHEARSAL_A0_IGNORE_SIGNUP"):
            c["options"]["disable_signup"] = False
    c["display_name"] = data.get("display_name", c["name"])
    out({k: v for k, v in c.items() if k != "enabled_clients"})
if key == "connections enabled-clients show":
    out([{"client_id": x} for x in find(st["connections"], "id", rest[0])["enabled_clients"]])
if key == "connections enabled-clients update":
    write(data)
    c = find(st["connections"], "id", rest[0])
    for x in data:
        if x["status"] and x["client_id"] not in c["enabled_clients"]:
            c["enabled_clients"].append(x["client_id"])
        elif not x["status"] and x["client_id"] in c["enabled_clients"]:
            c["enabled_clients"].remove(x["client_id"])
    save()
    sys.exit(0)
if key == "users search-by-email":
    out([u for u in st["users"] if u.get("email") == rest[0]])
if key == "users show":
    u = find(st["users"], "user_id", rest[0])
    out(u) if u else fail("not found", "not_found")
if key == "users create":
    write({k: ("<on stdin>" if k == "password" else v) for k, v in data.items()})
    u = {"user_id": "auth0|rehearsaljudge0001", "email": data["email"], "name": data.get("name"), "email_verified": data.get("email_verified"),
         "identities": [{"connection": data["connection"], "provider": "auth0", "user_id": "rehearsaljudge0001", "isSocial": False}]}
    st["users"].append(u)
    st["passwords"][u["user_id"]] = data["password"]
    out(u)
if key == "users update":
    write({k: ("<on stdin>" if k == "password" else v) for k, v in data.items()})
    u = find(st["users"], "user_id", rest[0])
    if "password" in data:
        st["passwords"][u["user_id"]] = data["password"]
    out(u)
fail("the stand-in does not know " + key, "usage")
"""

# A fresh tenant: the Management API, someone else's HS256 API (a listing prints its signing secret), the
# default application (its client secret too), the database connection with sign-up on, a Google
# connection (with its client secret), and the owner's own user in the database connection.
A0_DOMAIN = "geminus-demo.us.auth0.com"
A0_DB = "Username-Password-Authentication"
A0_DECOYS = ("DEFAULT-APP-CLIENT-SECRET-SENTINEL-19ee", "OTHER-API-SIGNING-SECRET-SENTINEL-77aa", "GOOGLE-CONNECTION-SECRET-SENTINEL-3c1d",
             "REHEARSAL-AUTH0-CLI-ACCESS-TOKEN", "REHEARSAL-DEVICE-CODE-NOT-SHOWN")


def fresh_tenant() -> dict:
    return {
        "apis": [{"id": "rs_mgmt", "name": "Auth0 Management API", "identifier": f"https://{A0_DOMAIN}/api/v2/", "is_system": True,
                  "signing_alg": "RS256", "token_lifetime": 86400},
                 {"id": "rs_other", "name": "Someone else's API", "identifier": "https://other.invalid", "signing_alg": "HS256",
                  "signing_secret": "OTHER-API-SIGNING-SECRET-SENTINEL-77aa", "token_lifetime": 7200}],
        "apps": [{"client_id": "defaultapp00000000000000000000001", "name": "Default App", "app_type": "regular_web",
                  "client_secret": "DEFAULT-APP-CLIENT-SECRET-SENTINEL-19ee", "callbacks": []}],
        "connections": [{"id": "con_db", "name": A0_DB, "display_name": A0_DB, "strategy": "auth0",
                         "options": {"passwordPolicy": "good", "brute_force_protection": True, "disable_signup": False, "mfa": {"active": True},
                                     "strategy_version": 2, "configuration": {},
                                     "authentication_methods": {"password": {"enabled": True}, "passkey": {"enabled": False}}},
                         "enabled_clients": ["defaultapp00000000000000000000001"]},
                        {"id": "con_google", "name": "google-oauth2", "display_name": "google-oauth2", "strategy": "google-oauth2",
                         "options": {"client_id": "", "client_secret": "GOOGLE-CONNECTION-SECRET-SENTINEL-3c1d", "scope": ["email", "profile"]},
                         "enabled_clients": ["defaultapp00000000000000000000001"]}],
        "users": [{"user_id": "auth0|rehearsal-owner", "email": "owner@example.invalid",
                   "identities": [{"connection": A0_DB, "provider": "auth0", "user_id": "rehearsal-owner", "isSocial": False}]}],
        "passwords": {},
        "writes": [],
    }


# What H-034 asks Auth0 for, written here from the decision (not read from the deployer).
A0_WEB = "web-production-1a2b.up.railway.app"
A0_AUDIENCE = "https://api.geminus-demo.invalid"
WANT_API = {"name": "Geminus demo API", "signing_alg": "RS256", "token_dialect": "rfc9068_profile", "token_lifetime": 600,
            "allow_offline_access": True, "skip_consent_for_verifiable_first_party_clients": True}
WANT_APP = {"name": "Geminus demo web", "app_type": "regular_web", "oidc_conformant": True, "token_endpoint_auth_method": "client_secret_post",
            "grant_types": ["authorization_code", "refresh_token"], "callbacks": [f"https://{A0_WEB}/auth/callback"],
            "allowed_logout_urls": [f"https://{A0_WEB}/"], "web_origins": [], "allowed_origins": [], "jwt_configuration": {"alg": "RS256"},
            "refresh_token": {"rotation_type": "rotating", "expiration_type": "expiring", "leeway": 30, "token_lifetime": 604800,
                              "infinite_token_lifetime": False, "idle_token_lifetime": 43200, "infinite_idle_token_lifetime": False}}
WANT_JUDGE = {"connection": A0_DB, "email": "judge@geminus-demo.invalid", "password": "<on stdin>", "email_verified": True, "verify_email": False,
              "name": "Geminus demo judge"}
A0_JUDGE = "auth0|rehearsaljudge0001"
A0_CLIENT = "a0rehearsalclient000000000000001"

# Runs the deployer in its own process with the pinned SHA-256 of the Auth0 CLI replaced by the stand-in's
# (the module's constant; nothing in the deployer reads it from the environment).
WRAPPER = r"""import importlib.util, sys
spec = importlib.util.spec_from_file_location("railway_deploy", sys.argv[1])
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
mod.AUTH0_CLI_SHA256 = sys.argv[2]
sys.argv = [sys.argv[1], "--apply"]
sys.exit(mod.main())
"""

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
        for name, body in (("railway", RAILWAY), ("psql", PSQL), ("go", GO), ("auth0", AUTH0)):
            (bindir / name).write_text(body.replace("__REHEARSAL_BIN__", str(bindir)))
            (bindir / name).chmod(0o755)
        a0_sha = hashlib.sha256((bindir / "auth0").read_bytes()).hexdigest()
        pinned_a0_sha = mod.AUTH0_CLI_SHA256
        # The runs up to run 5 are the manual fallback (GEMINUS_AUTH0_SETUP=manual): the deployer as it was
        # before the Auth0 step, the owner fills BFF_OIDC_CLIENT_SECRET. XDG_STATE_HOME keeps the Auth0
        # CLI's home in this temporary directory.
        os.environ.update(PATH=f"{bindir}:{os.environ['PATH']}", REHEARSAL_STATE=str(tmp / "state.json"),
                          REHEARSAL_PSQL_LOG=str(tmp / "psql.log"), REHEARSAL_GO_LOG=str(tmp / "go.log"),
                          REHEARSAL_ROLES=str(tmp / "roles.json"), GEMINUS_ALLOW_DIRTY="1",
                          REHEARSAL_MIGRATIONS=str(len(list((ROOT / "migrations").glob("*.up.sql")))),
                          RAILWAY_API_TOKEN=TOKEN,
                          GEMINUS_AUTH0_ISSUER="https://geminus-demo.us.auth0.com/", GEMINUS_AUTH0_AUDIENCE="https://api.geminus-demo.invalid",
                          GEMINUS_AUTH0_CLIENT_ID="rehearsalclientid", GEMINUS_SEED_OWNER_SUBJECT="auth0|rehearsal-owner",
                          GEMINUS_RAILWAY_BUCKET_REGION="sjc", GEMINUS_AUTH0_SETUP="manual", XDG_STATE_HOME=str(tmp / "xdg-state"),
                          REHEARSAL_A0_STATE=str(tmp / "a0-unused.json"), REHEARSAL_A0_LOG=str(tmp / "a0-unused.log"))
        for k in ("RAILWAY_TOKEN", "GEMINUS_KEEP_DB_PROXY", "GEMINUS_RAILWAY_SEAL", "GEMINUS_JUDGE_SUBJECT", "GEMINUS_JUDGE_EMAIL",
                  "GEMINUS_JUDGE_RESET_PASSWORD", "GEMINUS_AUTH0_CONNECTION", "REHEARSAL_TWIN_DB"):
            os.environ.pop(k, None)
        a0_home = tmp / "xdg-state" / "geminus-deploy" / "auth0-cli"
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

        errs = []  # each run's stderr (the judge's password is shown there, once)

        def apply(dry=False, **env):
            """One run in this process; env's None values are unset for it."""
            saved = {k: os.environ.get(k) for k in env}
            for k, v in env.items():
                if v is None:
                    os.environ.pop(k, None)
                else:
                    os.environ[k] = v
            run = mod.Run(dry=dry)
            buf, ebuf = io.StringIO(), io.StringIO()
            try:
                with redirect_stdout(buf), redirect_stderr(ebuf):
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
            errs.append(ebuf.getvalue())
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
        # The values to redact are kept as keyed digests only (T-057: no secret in the deployer's state):
        # `in` asks whether a value is registered, found() whether a registered value occurs in a text.
        if not res1.startswith("FAIL: verification") or "the owner has not set BFF_OIDC_CLIENT_SECRET on web" not in res1:
            failures.append(f"run 1 should fail verification on the owner's missing client secret: {res1[:600]}")
            print(textwrap.indent(out1[-3000:], "run 1 | "))
        if len(run1.hidden) < 8:
            failures.append(f"only {len(run1.hidden)} secrets were tracked")
        if TOKEN not in run1.hidden:
            failures.append("the Railway token is not among the values to redact")
        if "postgresql://postgres:REHEARSALADMINPW@shuttle.proxy.rlwy.net:41234/railway" not in run1.hidden:
            failures.append("Postgres's DATABASE_PUBLIC_URL, kept for the logins, is not among the values to redact")
        flat = json.dumps(calls) + out1 + read(tmp / "psql.log") + read(tmp / "go.log")
        if run1.hidden.found(flat):
            failures.append(f"a secret appears in an argument or the output (at {run1.hidden.spans(flat)[:3]})")
        for s in [TOKEN, "tsec_REHEARSALSECRETACCESSKEY0123456789", "REHEARSALADMINPW"]:
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
        owner = {"BFF_OIDC_CLIENT_SECRET", "GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "GEMINUS_MUSE_API_KEY", "GEMINUS_AI_LIVE",
                 "AI_POLICY_APPROVAL", "AI_MODEL", "AI_VISION_PROVIDER", "AI_VISION_POLICY_APPROVAL", "AI_VISION_MODEL"}
        for svc, vs in st["vars"].items():
            if owner & set(vs):
                failures.append(f"the deployer set the owner's {sorted(owner & set(vs))} on {svc}")
        if {"GEMINUS_AI_LIVE", "GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_MUSE_API_KEY"} & set(st["vars"].get("api", {})):
            failures.append("the deployer gave the API an AI key or the live switch")
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
        scanned = len(returned)
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
        if not res.startswith("FAIL: verification:") or "could not remove this run's TCP proxy tcp_1" not in res or delete_cmd.format("tcp_1") not in res:
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
        if not res.startswith("FAIL: verification:") or "tcp_1" not in res or delete_cmd.format("tcp_1") not in res or "deploy: done" in out:
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
        # P7: a step fails before verification while a proxy is open: the failure names the proxy too.
        name = scenario("p7", left)
        _, out, res = apply(REHEARSAL_UP_FAILS="1")
        shown.append(("P7 a failed step with a proxy open", res))
        if not res.startswith("FAIL: `railway up") or delete_cmd.format("tcp_left_by_killed_run") not in res:
            failures.append(f"P7 (a step fails, a proxy is open) must name the proxy in the failure: {res[:400]}")
        # P6: SIGTERM and SIGHUP in the middle of the run (the deployer as deploy.sh runs it,
        # in its own process): the proxy is removed, the run ends non-zero. No network: the
        # stand-in refuses `railway up`, and HTTPS goes to a closed local port.
        res6 = []
        for sig, extra in (("TERM", {}), ("HUP", {}), ("TERM", {"REHEARSAL_DELETE_FAILS": "1"})):
            label = sig + (" with the removal failing" if extra else "")
            name = scenario("p6" + sig + str(len(extra)), base)
            env = dict(os.environ, REHEARSAL_SIGNAL_AT_LOGINS=sig, HTTPS_PROXY="http://127.0.0.1:9", https_proxy="http://127.0.0.1:9", **extra)
            p = subprocess.run([sys.executable, "-I", str(ROOT / "deploy/railway/railway_deploy.py"), "--apply"],
                               capture_output=True, text=True, env=env, cwd=ROOT, timeout=300)
            signum = getattr(signal, "SIG" + sig)
            res6.append((label, p.returncode))
            shown.append((f"P6 SIG{label} at the logins (exit {p.returncode}, proxies left: {state(name)['proxies']})",
                           "\n".join(p.stdout.splitlines()[-7:])))
            if p.returncode != 128 + signum or "interrupted" not in p.stdout or "removing this run's TCP proxy" not in p.stdout \
                    or "deploy: done" in p.stdout or (state(name)["proxies"] != (["tcp_1"] if extra else [])):
                failures.append(f"P6 SIG{label}: exit {p.returncode} (want {128 + signum}), proxies {state(name)['proxies']}: "
                                f"{(p.stdout + p.stderr)[-400:]}")
            # The report itself, not the echo of the delete command (`$ railway tcp-proxy delete ...`).
            reported = [line for line in p.stdout.splitlines() if not line.lstrip().startswith("$")]
            if extra and not (any("could not remove this run's TCP proxy tcp_1" in line and delete_cmd.format("tcp_1") in line for line in reported)
                              and any("TCP proxy tcp_1 on Postgres is still open" in line for line in reported)):
                failures.append(f"P6 SIG{label}: the proxy left open is not reported with its removal: {p.stdout[-400:]}")
            if TOKEN in p.stdout + p.stderr:
                failures.append(f"P6 SIG{label}: the token was printed")

        # M2 (review round 1 of the Auth0 step): the deployer's stdout cannot be written. The rehearsal closes
        # the pipe once the marker is printed (the stand-in pauses there, so the next write fails with EPIPE; a
        # hung-up terminal's EIO is the same OSError); the cleanups must still run, and the run must end with
        # its own exit code (74, or 128 + the signal), no traceback.
        def lost(argv, env, marker):
            with open(tmp / "lost-stderr", "w+") as errf:
                p = subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=errf, text=True, env=env, cwd=ROOT)
                seen = ""
                for line in p.stdout:
                    seen += line
                    if marker in line:
                        break
                p.stdout.close()
                p.wait(timeout=300)
                errf.seek(0)
                return p.returncode, seen, errf.read()
        name = scenario("m2proxy", base)
        env = dict(os.environ, REHEARSAL_PAUSE_AT_LOGINS="1", HTTPS_PROXY="http://127.0.0.1:9", https_proxy="http://127.0.0.1:9")
        code, seen, err = lost([sys.executable, "-I", str(ROOT / "deploy/railway/railway_deploy.py"), "--apply"], env, "create_logins.sql")
        shown.append((f"M2 stdout closed at the logins (exit {code}, proxies left: {state(name)['proxies']})", "stderr: " + (err[-300:] or "(empty)")))
        if code != 74 or "Traceback" in err or state(name)["proxies"] or not state(name).get("proxy_deleted"):
            failures.append(f"M2 (stdout closed with a proxy open): exit {code} (want 74), proxies {state(name)['proxies']}: {err[-400:]}")

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
        # Run 5b: the same project again: the unsealed generated passwords it reads back are redacted.
        run5b, out5b, _ = apply(GEMINUS_RAILWAY_SEAL="off")
        for svc, var in (("migrate", "GEMINUS_MIGRATOR_DB_PASSWORD"), ("api", "GEMINUS_API_DB_PASSWORD"), ("worker", "GEMINUS_WORKER_DB_PASSWORD")):
            value = st5["vars"][svc][var]
            if value not in run5b.hidden or value in out5b:
                failures.append(f"run 5b: {svc} {var}, read back unsealed, is not registered for redaction (M2)")

        # Run 4: a fresh project whose CLI quotes the stdin value back in an error.
        os.environ.update(REHEARSAL_STATE=str(tmp / "state4.json"))
        run4, out4, res4 = apply(REHEARSAL_ECHO_STDIN="1")
        if not res4.startswith("FAIL: `railway variable set`") or "[redacted]" not in out4 or not len(run4.hidden):
            failures.append(f"run 4 should fail at the first secret with the CLI's error redacted: {res4[:300]}")
        if run4.hidden.found(out4 + res4):
            failures.append(f"run 4 printed a secret from the CLI's error (at {run4.hidden.spans(out4 + res4)[:3]})")

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

        if (tmp / "a0-unused.log").exists():
            failures.append("a GEMINUS_AUTH0_SETUP=manual run ran the Auth0 CLI")

        # ---- The Auth0 step (T-057, H-034, H-035 (2)): GEMINUS_AUTH0_SETUP=cli, on projects of its own ----
        a0_shown = []  # each scenario's result, for the log
        mod.AUTH0_CLI_SHA256 = a0_sha  # the stand-in is the pinned CLI here; A4 puts the real pin back
        answers["/auth/login"] = (303, {"Location": f"https://{A0_DOMAIN}/authorize?client_id={A0_CLIENT}"})
        cli_env = {"GEMINUS_AUTH0_SETUP": "cli", "GEMINUS_AUTH0_CLIENT_ID": None}
        issuer = os.environ["GEMINUS_AUTH0_ISSUER"]

        def a0_files(tag, railway=None, tenant=None):
            """A scenario's state files: a Railway project (empty unless given) and a tenant (fresh unless given)."""
            files = {"REHEARSAL_STATE": f"state-{tag}.json", "REHEARSAL_A0_STATE": f"a0-{tag}.json", "REHEARSAL_A0_LOG": f"a0-{tag}.log",
                     "REHEARSAL_GO_LOG": f"go-{tag}.log", "REHEARSAL_TWIN_DB": f"twin-{tag}.json", "REHEARSAL_ROLES": f"roles-{tag}.json",
                     "REHEARSAL_PSQL_LOG": f"psql-{tag}.log"}
            files = {k: str(tmp / v) for k, v in files.items()}
            if railway is not None:
                json.dump(railway, open(files["REHEARSAL_STATE"], "w"))
            json.dump(tenant or fresh_tenant(), open(files["REHEARSAL_A0_STATE"], "w"))
            return files

        def a0_calls(files):
            return [json.loads(line) for line in read(Path(files["REHEARSAL_A0_LOG"])).splitlines()]

        def a0_state(files):
            return json.load(open(files["REHEARSAL_A0_STATE"]))

        def rw_state(files):
            p = Path(files["REHEARSAL_STATE"])
            return json.load(open(p)) if p.exists() else {"calls": [], "vars": {}, "sealed": []}

        def a0_conf(files, **env):
            """The Auth0 stand-in's settings (it gets no environment of the rehearsal's: the deployer's own)."""
            conf = {k: v for k, v in {**files, **env}.items() if k.startswith("REHEARSAL_A0_")}
            (bindir / "auth0-rehearsal.json").write_text(json.dumps(conf))

        def cli(files, **env):
            a0_conf(files, **env)
            returned.clear()
            run, out, res = apply(**cli_env, **files, **env)
            return run, out, errs[-1], res

        def logged_out(label, files, out):
            """The run ended with the Auth0 CLI signed out and its home gone, every call in agent mode, in that
            home, without the Railway token, analytics or a session bus."""
            calls = a0_calls(files)
            keys = [c["key"] for c in calls]
            if "logout" not in keys or a0_home.exists() or keys[-1] not in ("logout", "tenants list"):
                failures.append(f"{label}: the Auth0 CLI was not signed out last, or its home {a0_home} is left: {keys[-4:]}")
            if any(c["home"] != str(a0_home) or c["tokens"] or c["analytics"] != "false" or c["dbus"] != "disabled:" or not c["agent"]
                   for c in calls):
                failures.append(f"{label}: an Auth0 CLI call ran with another home, the Railway token, analytics, a session bus or without agent mode")
            if any(c["exe"] == str(bindir / "auth0") or not c["exe"].endswith("/auth0") for c in calls):
                failures.append(f"{label}: the auth0 on PATH ran, not the private copy of the bytes whose SHA-256 was checked (L2)")
            if "signing the Auth0 CLI out" not in out:
                failures.append(f"{label}: the sign-out is not reported")

        def no_leaks(label, run, out, err, res, files, once=None, also=()):
            """No secret in any argument, the deployer's state, its result, what its methods returned or raised, or
            the output; `once` (the judge's new password) exactly once, on stderr."""
            t = a0_state(files)
            secret = next((c.get("client_secret") for c in t["apps"] if c.get("client_id") == A0_CLIENT), None)
            echoed = [json.loads(e).get("password") for e in t.get("echoed", []) if e]
            needles = {s for s in (secret, *t["passwords"].values(), *echoed, TOKEN, *A0_DECOYS, *also) if s}
            argv = json.dumps([c["argv"] for c in a0_calls(files)]) + json.dumps([c["argv"] for c in rw_state(files)["calls"]])
            argv += read(Path(files["REHEARSAL_PSQL_LOG"])) + read(Path(files["REHEARSAL_GO_LOG"]))
            for where, obj in (("an argument", argv), ("its state", vars(run)), ("its result", res),
                               ("what its methods returned or raised", returned)):
                leaked = holds(obj, needles)
                if leaked:
                    failures.append(f"{label}: a secret is in {where}: {sorted(x[:12] for x in leaked)}")
            for s in needles:
                want = 1 if s == once else 0
                if (out + err).count(s) != want or (want and s not in err):
                    failures.append(f"{label}: {s[:8]}… is in the output {(out + err).count(s)} times, want {want}" + (" (on stderr)" if want else ""))
            if once is None and "password:" in out + err:
                failures.append(f"{label}: a password was shown")
            return needles

        def strong(pw):
            return (isinstance(pw, str) and len(pw) >= 20 and re.search(r"[a-z]", pw) and re.search(r"[A-Z]", pw) and re.search(r"[0-9]", pw)
                    and re.search(r"[^A-Za-z0-9]", pw) and not re.search(r"(.)\1\1", pw) and re.fullmatch(r"[\x21-\x7e]+", pw))

        # A1: a fresh project and a fresh tenant.
        fa = a0_files("a")
        runA, outA, errA, resA = cli(fa)
        tA, sA = a0_state(fa), rw_state(fa)
        a0_shown.append(("A1 a fresh tenant and project", resA))
        if resA != "done":
            failures.append(f"A1: {resA[:600]}")
            print(textwrap.indent((outA + errA)[-3000:], "A1 | "))
        writes = tA["writes"]
        if [w[0] for w in writes] != ["apis create", "apps create", "connections update", "connections enabled-clients update", "users create"]:
            failures.append(f"A1: the writes to Auth0 are {[w[0] for w in writes]}")
        else:
            if writes[0][2] != dict(WANT_API, identifier=A0_AUDIENCE):
                failures.append(f"A1: the API was created with {writes[0][2]}, want H-034's settings")
            if writes[1][2] != WANT_APP:
                failures.append(f"A1: the application was created with {writes[1][2]}, want H-034's settings")
            options = dict(fresh_tenant()["connections"][0]["options"], disable_signup=True)
            if writes[2][1:] != [["con_db"], {"options": options, "display_name": A0_DB}]:
                failures.append(f"A1: the database connection was updated with {writes[2][1:]}, want sign-up off and its other options kept")
            if writes[3][1:] != [["con_google"], [{"client_id": A0_CLIENT, "status": False}]]:
                failures.append(f"A1: enabled clients {writes[3][1:]}, want the application off the Google connection")
            if writes[4][2] != WANT_JUDGE:
                failures.append(f"A1: the judge's account was created with {writes[4][2]}")
        conns = {c["id"]: c for c in tA["connections"]}
        if conns["con_db"]["options"].get("disable_signup") is not True or A0_CLIENT not in conns["con_db"]["enabled_clients"] \
                or A0_CLIENT in conns["con_google"]["enabled_clients"]:
            failures.append("A1: sign-up is on, or the application is not on the database connection only")
        pwA = tA["passwords"].get(A0_JUDGE)
        if not strong(pwA):
            failures.append("A1: the judge's password is not a strong generated one (20+ visible characters of four kinds, no three alike in a row)")
        secretA = next((c["client_secret"] for c in tA["apps"] if c["client_id"] == A0_CLIENT), None)
        if pwA not in runA.hidden or secretA not in runA.hidden:
            failures.append("A1: the judge's password or the client secret is not registered for redaction")
        web, api_vars = sA["vars"].get("web", {}), sA["vars"].get("api", {})
        set_on_stdin = [c for c in sA["calls"] if c["argv"][:3] == ["variable", "set", "BFF_OIDC_CLIENT_SECRET"]]
        if not secretA or web.get("BFF_OIDC_CLIENT_SECRET") != secretA or len(set_on_stdin) != 1 or not set_on_stdin[0]["stdin"]:
            failures.append("A1: web's BFF_OIDC_CLIENT_SECRET is not the application's client secret, set once on stdin")
        if ["web", "BFF_OIDC_CLIENT_SECRET"] not in sA.get("sealed", []):
            failures.append("A1: web's BFF_OIDC_CLIENT_SECRET is not sealed")
        if web.get("BFF_OIDC_CLIENT_ID") != A0_CLIENT or web.get("BFF_OIDC_AUDIENCE") != A0_AUDIENCE or \
                api_vars.get("AUTH_OIDC_AUDIENCE") != A0_AUDIENCE or api_vars.get("AUTH_OIDC_ISSUER") != issuer or \
                web.get("BFF_OIDC_JWKS_URL") != issuer + ".well-known/jwks.json" or api_vars.get("AUTH_OIDC_JWKS_URL") != issuer + ".well-known/jwks.json":
            failures.append("A1: the Auth0 step's outputs did not reach BFF_OIDC_* and AUTH_OIDC_*")
        goA = read(Path(fa["REHEARSAL_GO_LOG"]))
        member = f"-member-issuer {issuer} -member-subject {A0_JUDGE} -member-role editor"
        if goA.count("provision-tenant organization") != 1 or member not in goA or "provision-tenant user" in goA:
            failures.append(f"A1: the seed did not give the demo organization the judge as an editor, once: {goA}")
        twin = json.load(open(fa["REHEARSAL_TWIN_DB"])) if os.path.exists(fa["REHEARSAL_TWIN_DB"]) else {"memberships": []}
        if [m for m in twin["memberships"] if m[2] == A0_JUDGE] != [["org_01M5W4RHAFH8Q7Y8PNQT1RAF4T", issuer, A0_JUDGE, "editor"]]:
            failures.append(f"A1: the judge's memberships are {twin['memberships']}")
        if "the judge's account: an editor of the demo organization" not in outA:
            failures.append("A1: the judge's membership was not checked")
        login_lines = [line for line in outA.splitlines() if "auth0.auth0.com" in line]
        if len(login_lines) != 1 or "https://auth0.auth0.com/activate?user_code=QWER-TYUI" not in login_lines[0] or "QWER-TYUI" not in login_lines[0]:
            failures.append(f"A1: the sign-in URL and code are not shown once: {login_lines}")
        no_leaks("A1", runA, outA, errA, resA, fa, once=pwA)
        logged_out("A1", fa, outA)
        if f"email:    {WANT_JUDGE['email']}" not in errA:
            failures.append("A1: the judge's email is not shown with the password")

        # A2: the same tenant and project again: nothing is created or changed, nothing secret is read.
        callsA, goA_len = len(a0_calls(fa)), len(read(Path(fa["REHEARSAL_GO_LOG"])))
        # Auth0 may list the grant types in another order: that is no drift.
        t = a0_state(fa)
        turned = [c for c in t["apps"] if c.get("client_id") == A0_CLIENT and isinstance(c.get("grant_types"), list)]
        for c in turned:
            c["grant_types"].reverse()
        json.dump(t, open(fa["REHEARSAL_A0_STATE"], "w"))
        if [c["grant_types"] for c in turned] != [["refresh_token", "authorization_code"]]:
            failures.append(f"A2: the stand-in's application is not there to list its grant types in another order: {turned}")
        runB, outB, errB, resB = cli(fa)
        tB, sB = a0_state(fa), rw_state(fa)
        a0_shown.append(("A2 the same again", resB + f" ({len(tB['writes']) - len(writes)} writes to Auth0)"))
        if resB != "done" or tB["writes"] != writes:
            failures.append(f"A2 (a rerun) wrote to Auth0 {tB['writes'][len(writes):]}: {resB[:300]}")
        if any("--reveal-secrets" in c["argv"] for c in a0_calls(fa)[callsA:]) or any(c["stdin"] for c in sB["calls"][len(sA["calls"]):]):
            failures.append("A2: the rerun read the client secret again or set a secret on Railway")
        if "provision-tenant" in read(Path(fa["REHEARSAL_GO_LOG"]))[goA_len:] or tB["passwords"].get(A0_JUDGE) != pwA:
            failures.append("A2: the rerun seeded again or changed the judge's password")
        if "its password is unchanged and not shown" not in outB:
            failures.append("A2: the rerun does not say the judge's password is unchanged")
        no_leaks("A2", runB, outB, errB, resB, fa)
        logged_out("A2", fa, outB)

        # A3: the tenant drifted (an extra callback on loopback, an extra grant, no overlap, a web origin; the API's
        # dialect and lifetime; sign-up on again; the application off the database connection and on Google).
        t = a0_state(fa)
        app = next((c for c in t["apps"] if c["client_id"] == A0_CLIENT), None)
        api = next((x for x in t["apis"] if x.get("identifier") == A0_AUDIENCE), None)
        if app and api and isinstance(app.get("refresh_token"), dict):
            app["callbacks"].append("http://localhost:3000/auth/callback")
            app["grant_types"].append("implicit")
            app["refresh_token"]["leeway"] = 0
            app["web_origins"] = ["https://elsewhere.invalid"]
            api.update(token_dialect="access_token", token_lifetime=86400)
        dbc, gc = (next(c for c in t["connections"] if c["id"] == i) for i in ("con_db", "con_google"))
        dbc["options"]["disable_signup"] = False
        if A0_CLIENT in dbc["enabled_clients"]:
            dbc["enabled_clients"].remove(A0_CLIENT)
        gc["enabled_clients"].append(A0_CLIENT)
        json.dump(t, open(fa["REHEARSAL_A0_STATE"], "w"))
        runC, outC, errC, resC = cli(fa)
        tC = a0_state(fa)
        newC = tC["writes"][len(writes):]
        a0_shown.append(("A3 a drifted tenant", resC + f" ({[w[0] for w in newC]})"))
        wantC = [["apis update", ["rs_rehearsal0001"], WANT_API], ["apps update", [A0_CLIENT], WANT_APP],
                 ["connections update", ["con_db"], {"options": dict(fresh_tenant()["connections"][0]["options"], disable_signup=True),
                                                    "display_name": A0_DB}],
                 ["connections enabled-clients update", ["con_db"], [{"client_id": A0_CLIENT, "status": True}]],
                 ["connections enabled-clients update", ["con_google"], [{"client_id": A0_CLIENT, "status": False}]]]
        if resC != "done" or newC != wantC:
            failures.append(f"A3 (a drifted tenant) must be put back with exactly H-034's settings: {resC[:300]} {newC}")
        appC = next((c for c in tC["apps"] if c["client_id"] == A0_CLIENT), {})
        if appC.get("callbacks") != WANT_APP["callbacks"] or appC.get("web_origins") or "implicit" in appC.get("grant_types", []):
            failures.append(f"A3: the application still has {appC.get('callbacks')} {appC.get('web_origins')} {appC.get('grant_types')}")
        no_leaks("A3", runC, outC, errC, resC, fa)
        logged_out("A3", fa, outC)

        # A12: the judge's password reset on request, with a CLI home an interrupted run left behind.
        a0_home.mkdir(parents=True)
        (a0_home / ".config" / "auth0").mkdir(parents=True)
        (a0_home / ".config" / "auth0" / "config.json").write_text(json.dumps(
            {"default_tenant": A0_DOMAIN, "tenants": {A0_DOMAIN: {"domain": A0_DOMAIN, "access_token": "LEFTOVER-CLI-TOKEN-SENTINEL"}}}))
        before, callsR = len(tC["writes"]), len(a0_calls(fa))
        runR, outR, errR, resR = cli(fa, GEMINUS_JUDGE_RESET_PASSWORD="1")
        tR = a0_state(fa)
        pwR = tR["passwords"].get(A0_JUDGE)
        a0_shown.append(("A12 the password reset, a CLI home left by an interrupted run", resR))
        if resR != "done" or tR["writes"][before:] != [["users update", [A0_JUDGE], {"password": "<on stdin>", "connection": A0_DB}]] \
                or pwR == pwA or not strong(pwR):
            failures.append(f"A12 (reset) must set one new strong password on stdin: {resR[:300]} {tR['writes'][before:]}")
        keysR = [c["key"] for c in a0_calls(fa)[callsR:]]
        if "left by an interrupted run" not in outR or keysR[:1] != ["logout"] or keysR.count("logout") < 2:
            failures.append("A12: the CLI home left by an interrupted run was not signed out and removed first")
        no_leaks("A12", runR, outR, errR, resR, fa, once=pwR, also=(pwA, "LEFTOVER-CLI-TOKEN-SENTINEL"))
        logged_out("A12", fa, outR)

        # A4: the auth0 on PATH is not the pinned build: nothing runs it.
        mod.AUTH0_CLI_SHA256 = pinned_a0_sha
        f = a0_files("pin")
        _, out, err, res = cli(f)
        mod.AUTH0_CLI_SHA256 = a0_sha
        a0_shown.append(("A4 a CLI that is not the pinned build", res))
        if not res.startswith("FAIL: preflight") or "not the pinned Auth0 CLI 1.37.0" not in res or a0_calls(f):
            failures.append(f"A4 (checksum mismatch) must stop before running the CLI: {res[:300]} {len(a0_calls(f))} calls")

        # A5: a step fails (the application cannot be created).
        f = a0_files("fail")
        runF, out, err, res = cli(f, REHEARSAL_A0_FAIL="apps create")
        a0_shown.append(("A5 a failed step", res))
        if not res.startswith("FAIL: `auth0 apps create` failed") or "BFF_OIDC_CLIENT_SECRET" in rw_state(f)["vars"].get("web", {}):
            failures.append(f"A5 (a failed step) must stop there: {res[:300]}")
        no_leaks("A5", runF, out, err, res, f)
        logged_out("A5", f, out)

        # A7: the owner approved the sign-in for another tenant.
        f = a0_files("other")
        _, out, err, res = cli(f, REHEARSAL_A0_LOGIN_DOMAIN="someone-else.eu.auth0.com")
        a0_shown.append(("A7 signed in to another tenant", res))
        if "signed in to 'someone-else.eu.auth0.com', not geminus-demo.us.auth0.com" not in res or a0_state(f)["writes"] \
                or not any(c["key"] == "logout" and "someone-else.eu.auth0.com" in c["argv"] for c in a0_calls(f)):
            failures.append(f"A7 (another tenant) must change nothing and sign that tenant out: {res[:300]}")
        logged_out("A7", f, out)

        # A14: the sign-out leaves the session in the CLI's config: the run says so and fails (the home is removed).
        f = a0_files("keeps")
        _, out, err, res = cli(f, REHEARSAL_A0_LOGOUT_KEEPS="1")
        a0_shown.append(("A14 a sign-out that keeps the session", res))
        if not res.startswith("FAIL: Auth0: the Auth0 CLI may still be signed in") or a0_home.exists():
            failures.append(f"A14 (the sign-out keeps the session) must fail and still remove the home: {res[:300]}")

        # A8: Auth0 does not keep sign-up off.
        f = a0_files("signup")
        _, out, err, res = cli(f, REHEARSAL_A0_IGNORE_SIGNUP="1")
        a0_shown.append(("A8 sign-up not kept off", res))
        if f"sign-up on {A0_DB} is still on" not in res:
            failures.append(f"A8 (sign-up not kept off) must fail: {res[:300]}")
        logged_out("A8", f, out)

        # A15 (review L1): Railway refuses to seal the client secret: the run fails (not done) and says how
        # to recover; the rerun seals it without reading it again.
        f = a0_files("seal")
        _, out, err, res = cli(f, REHEARSAL_SEAL_FAILS="1")
        a0_shown.append(("A15.1 Railway refuses to seal the client secret", res))
        if not res.startswith("FAIL") or "BFF_OIDC_CLIENT_SECRET is set but Railway did not seal it" not in res or "deploy: done" in out:
            failures.append(f"A15 (the seal refused) must fail naming the recovery: {res[:300]}")
        reveals = sum(c["key"] == "apps show" and "--reveal-secrets" in c["argv"] for c in a0_calls(f))
        _, out, err, res = cli(f)
        a0_shown.append(("A15.2 the rerun", res))
        if res != "done" or ["web", "BFF_OIDC_CLIENT_SECRET"] not in rw_state(f).get("sealed", []) \
                or sum(c["key"] == "apps show" and "--reveal-secrets" in c["argv"] for c in a0_calls(f)) != reveals:
            failures.append(f"A15 (the rerun) must seal the client secret without reading it again: {res[:300]}")
        logged_out("A15", f, out)

        # A9: the CLI quotes the judge's password in its error.
        f = a0_files("echo")
        runE, out, err, res = cli(f, REHEARSAL_A0_ECHO_STDIN="users create")
        a0_shown.append(("A9 the CLI quotes the password in its error", res + "\n" + "\n".join(l for l in out.splitlines() if "failed" in l)))
        if not res.startswith("FAIL: `auth0 users create` failed") or not a0_state(f).get("echoed"):
            failures.append(f"A9 must fail at the judge's account: {res[:300]}")
        if "the CLI's message is not shown (the call carried a secret)" not in out or "API request failed" in out:
            failures.append("A9: the error of a call that carried a secret is shown (it is redacted, but must not be shown at all)")
        no_leaks("A9", runE, out, err, res, f)
        logged_out("A9", f, out)

        # A13: the owner's own user is not in the database connection (it would be locked out).
        tenant = fresh_tenant()
        tenant["users"][0]["identities"][0]["connection"] = "google-oauth2"
        f = a0_files("owner", tenant=tenant)
        _, out, err, res = cli(f)
        a0_shown.append(("A13 the owner outside the database connection", res))
        if "is not in the database connection" not in res or a0_state(f)["writes"]:
            failures.append(f"A13 (the owner elsewhere) must fail before any change: {res[:300]}")
        logged_out("A13", f, out)

        # A10: the demo organization was seeded before the judge's account existed (base: a manual deployment).
        f = a0_files("late", railway=base)
        _, out, err, res = cli(f)
        go_late = read(Path(f["REHEARSAL_GO_LOG"]))
        a0_shown.append(("A10 the demo organization seeded before the judge's account", res))
        if not res.startswith("FAIL: verification") or "has no membership" not in res or "org_01M5W4RHAFH8Q7Y8PNQT1RAF4T" not in res \
                or go_late.count(f"provision-tenant user -issuer {issuer} -subject {A0_JUDGE}") != 1 or "organization" in go_late:
            failures.append(f"A10 must name the missing membership and how to add it: {res[:400]} {go_late}")
        logged_out("A10", f, out)

        # A11: the judge's account is also a member of another organization.
        f = a0_files("elsewhere")
        _, out, err, res = cli(f, REHEARSAL_EXTRA_MEMBERSHIPS=json.dumps([["org_01M5W4RHAFH8Q7Y8PNQT1RAF4X", issuer, A0_JUDGE, "viewer"]]))
        a0_shown.append(("A11 the judge also in another organization", res))
        if not res.startswith("FAIL: verification") or "org_01M5W4RHAFH8Q7Y8PNQT1RAF4X" not in res or "only" not in res:
            failures.append(f"A11 (a membership elsewhere) must fail: {res[:400]}")

        # M1: the manual fallback with the judge's account made in the dashboard (GEMINUS_JUDGE_SUBJECT): the seed
        # makes it an editor of the demo organization; the Auth0 CLI never runs.
        f = a0_files("manual")
        a0_conf(f)
        _, out, res = apply(**f, GEMINUS_JUDGE_SUBJECT="auth0|dashboard-judge")
        go_m = read(Path(f["REHEARSAL_GO_LOG"]))
        twin = json.load(open(f["REHEARSAL_TWIN_DB"])) if os.path.exists(f["REHEARSAL_TWIN_DB"]) else {"memberships": []}
        a0_shown.append(("M1 the manual fallback with GEMINUS_JUDGE_SUBJECT", res.splitlines()[0] + " " + res.splitlines()[1][:120]))
        if f"-member-issuer {issuer} -member-subject auth0|dashboard-judge -member-role editor" not in go_m or a0_calls(f) \
                or [m for m in twin["memberships"] if m[2] == "auth0|dashboard-judge"] != [["org_01M5W4RHAFH8Q7Y8PNQT1RAF4T", issuer, "auth0|dashboard-judge", "editor"]] \
                or "the owner has not set BFF_OIDC_CLIENT_SECRET on web" not in res:
            failures.append(f"M1 (manual, a judge's account from the dashboard): {res[:300]} {go_m}")

        # D1: a dry run with the Auth0 step: the plan, no call.
        f = a0_files("dry")
        _, out, err, res = cli(f, dry=True)
        a0_shown.append(("D1 the dry run", res + "\n" + "\n".join(l for l in out.splitlines() if "auth0 " in l)[:1500]))
        if res != "done" or a0_calls(f) or "2b. Auth0" not in out or f"auth0 login --domain {A0_DOMAIN}" not in out or "--reveal-secrets" not in out:
            failures.append(f"D1 (dry run) must print the Auth0 plan and call nothing: {res[:300]}")

        # A6: SIGTERM in the Auth0 step, before and after the judge's account exists (the deployer in its own
        # process, as deploy.sh runs it; no network: HTTPS goes to a closed local port).
        wrapper = tmp / "wrapper.py"
        wrapper.write_text(WRAPPER)
        res6a = []
        for tag, at, judged in (("sig1", "connections update", False), ("sig2", "apps show --reveal-secrets", True)):
            f = a0_files(tag)
            a0_conf(f, REHEARSAL_A0_SIGNAL=at)
            env = {k: v for k, v in os.environ.items() if k != "GEMINUS_AUTH0_CLIENT_ID"}
            env.update(f, GEMINUS_AUTH0_SETUP="cli", REHEARSAL_A0_SIGNAL=at, HTTPS_PROXY="http://127.0.0.1:9", https_proxy="http://127.0.0.1:9")
            p = subprocess.run([sys.executable, "-I", "-B", str(wrapper), str(ROOT / "deploy/railway/railway_deploy.py"), a0_sha],
                               capture_output=True, text=True, env=env, cwd=ROOT, timeout=300)
            res6a.append((at, p.returncode))
            pw = a0_state(f)["passwords"].get(A0_JUDGE)
            a0_shown.append((f"A6 SIGTERM at {at} (exit {p.returncode})", "\n".join(p.stdout.splitlines()[-5:])))
            if p.returncode != 143 or "interrupted" not in p.stdout or "deploy: done" in p.stdout:
                failures.append(f"A6 SIGTERM at {at}: exit {p.returncode}, want 143: {(p.stdout + p.stderr)[-400:]}")
            if judged and (not pw or p.stderr.count(pw) != 1 or pw in p.stdout):
                failures.append(f"A6 SIGTERM at {at}: the judge's password is not shown exactly once, on stderr")
            if not judged and (pw or "password:" in p.stdout + p.stderr):
                failures.append(f"A6 SIGTERM at {at}: a password exists or was shown before the judge's account")
            for s in (TOKEN, *A0_DECOYS, next((c.get("client_secret") for c in a0_state(f)["apps"] if c.get("client_id") == A0_CLIENT), None)):
                if s and s in p.stdout + p.stderr:
                    failures.append(f"A6 SIGTERM at {at}: {s[:8]}… was printed")
            if "BFF_OIDC_CLIENT_SECRET" in rw_state(f)["vars"].get("web", {}):
                failures.append(f"A6 SIGTERM at {at}: the client secret reached Railway")
            logged_out(f"A6 SIGTERM at {at}", f, p.stdout)

        # M2 in the Auth0 step: stdout closed (EPIPE), and closed then SIGHUP (the terminal hung up).
        res_m2 = []
        for tag, sig, want in (("epipe", None, 74), ("hup", "HUP", 129)):
            f = a0_files("m2" + tag)
            conf = dict(REHEARSAL_A0_PAUSE="connections list", **({"REHEARSAL_A0_SIGNAL": "connections list", "REHEARSAL_A0_SIG": sig} if sig else {}))
            a0_conf(f, **conf)
            env = {k: v for k, v in os.environ.items() if k != "GEMINUS_AUTH0_CLIENT_ID"}
            env.update(f, GEMINUS_AUTH0_SETUP="cli", HTTPS_PROXY="http://127.0.0.1:9", https_proxy="http://127.0.0.1:9", **conf)
            code, seen, err = lost([sys.executable, "-I", "-B", str(wrapper), str(ROOT / "deploy/railway/railway_deploy.py"), a0_sha],
                                   env, "auth0 connections list")
            res_m2.append((tag, code))
            keys = [c["key"] for c in a0_calls(f)]
            a0_shown.append((f"M2 {tag} in the Auth0 step (exit {code})", f"last calls: {keys[-3:]}; stderr: {err[-200:] or '(empty)'}"))
            if code != want or "Traceback" in err:
                failures.append(f"M2 {tag}: exit {code} (want {want}) or a traceback: {err[-400:]}")
            if "logout" not in keys or a0_home.exists() or "connections update" in keys:
                failures.append(f"M2 {tag}: the CLI was not signed out, its home is left, or the run went on writing: {keys[-4:]}")

        # I1: deploy.sh --install-auth0-cli with an archive that is not the pinned one: nothing installed, nothing in it run.
        dl = tmp / "dl"
        dl.mkdir()
        bogus = dl / "auth0"
        bogus.write_text(f"#!/bin/sh\ntouch {tmp / 'bogus-ran'}\n")
        bogus.chmod(0o755)
        with tarfile.open(dl / "bogus.tar.gz", "w:gz") as tf:
            tf.add(bogus, arcname="auth0")
        curl_dir = tmp / "curlbin"
        curl_dir.mkdir()
        (curl_dir / "curl").write_text("#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -o ] && out=$2; shift; done\n"
                                       f"cp {dl / 'bogus.tar.gz'} \"$out\"\n")
        (curl_dir / "curl").chmod(0o755)
        env = {k: v for k, v in os.environ.items() if k not in ("RAILWAY_TOKEN", "RAILWAY_API_TOKEN")}
        env["PATH"] = f"{curl_dir}:{env['PATH']}"
        dest = tmp / "auth0-install"
        p = subprocess.run([shutil.which("bash"), str(ROOT / "deploy/railway/deploy.sh"), "--install-auth0-cli", str(dest)],
                           capture_output=True, text=True, env=env, timeout=120)
        a0_shown.append((f"I1 --install-auth0-cli with an archive that is not the pinned one (exit {p.returncode})", p.stderr.strip()[-300:]))
        if p.returncode != 1 or "the Auth0 CLI archive's SHA-256 is not the pinned one; nothing installed" not in p.stderr or dest.exists() \
                or (tmp / "bogus-ran").exists():
            failures.append(f"I1 (--install-auth0-cli, a wrong archive) must install and run nothing: exit {p.returncode} {p.stderr[-300:]}")

        print(textwrap.indent(out1[-1500:], "run 1 | "))
        print(textwrap.indent(out2[-1200:], "run 2 | "))
        print(textwrap.indent(out4[-400:], "run 4 | "))
        print(f"run 1: {len(calls)} CLI calls, {len(psql)} psql calls, result {res1.splitlines()[0]}")
        print(f"run 2: {len(calls2)} CLI calls, result {res2}; the owner's values looked for in its state, output, result "
              f"and the {scanned} values its methods returned or raised")
        print(f"run 3: {res3}")
        print(f"run 4: {res4.splitlines()[0]}")
        for label, text in shown:
            print(textwrap.indent(text, f"{label} | "))
        print(textwrap.indent(outA[outA.find("2b. Auth0"):outA.find("3. variables")], "A1 | "))
        print(textwrap.indent(outA[-1800:], "A1 | "))
        print(textwrap.indent(re.sub(r"(password: ).*", r"\1<the judge's password: here, on stderr, once>", errA), "A1 stderr | "))
        for label, text in a0_shown:
            print(textwrap.indent(text, f"{label} | "))
        print(f"Auth0 step: A1 {len(writes)} writes to Auth0, A2 {len(tB['writes']) - len(writes)}; signals {res6a}; stdout lost {res_m2}")
        print(f"signals {res6}; deploy.sh --help with bash -x, SHELLOPTS=xtrace, plain: exits {xt}")
    if failures:
        print("REHEARSAL FAILED:\n  - " + "\n  - ".join(failures))
        return 1
    print("rehearsal: PASS (the deployer against stand-ins; Railway itself NOT RUN)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
