#!/usr/bin/env python3
"""The hackathon deployment on Railway (T-039; H-031 Railway only; H-032 the
coordinator applies it). Run through deploy/railway/deploy.sh, which checks
the shell and installs the pinned CLI on request; see docs/runbooks/deploy.md
§7.

Steps of --apply, with the pinned Railway CLI (5.64.2; commands and flags as
its source at v5.64.2 defines them):

  1. project and environment: with RAILWAY_TOKEN (a project token) they are
     the token's and must exist; with RAILWAY_API_TOKEN (an account or
     workspace token) the project GEMINUS_RAILWAY_PROJECT is found by name or
     created (`railway init`) and linked, and the environment
     GEMINUS_RAILWAY_ENVIRONMENT created if absent and linked;
  2. PostgreSQL (`railway add --database postgres`), the bucket
     (`railway bucket create`, region GEMINUS_RAILWAY_BUCKET_REGION) and the
     services migrate, api, worker, web (`railway add --service`), each only
     if absent; each service's config-as-code file
     (deploy/railway/<service>.railway.json: Dockerfile, healthcheck, restart
     policy) with `railway environment edit --service-config <service>
     configFile ...`; generated public domains for api and web;
  3. variables from deploy/railway/variables.json, with --skip-deploys:
     values with this run's inputs filled in; generated secrets (database
     passwords, SESSION_KEYS) made here with Python's secrets module only when
     absent and sent on stdin (`railway variable set KEY --stdin`); the
     bucket's credentials copied the same way when absent; variables the
     owner fills (Auth0 client secret, AI keys, the live switch) are never
     set;
  4. the database logins: a TCP proxy on PostgreSQL for this run, then
     db/bootstrap_roles.sql and deploy/sql/create_logins.sql with psql as the
     database's administrator (the passwords only in psql's environment); the
     generated and copied secrets are then sealed (GEMINUS_RAILWAY_SEAL=off
     leaves them unsealed: only for the recovery of runbook §7.7, because
     Railway's documentation does not say whether a reference to a sealed
     variable is expanded);
  5. migrations: the `migrate` service (geminus-migrate as the migration
     login) is deployed, and the run waits until the database records every
     migration of this checkout. Not an API pre-deploy command: that would
     give the API service the migration login (ADR-029);
  6. seed: one synthetic organization with the owner as its first owner
     (cmd/provision-tenant as the provisioning login, whose password is new
     for each run and stored nowhere), only if the worker has no
     WORKER_ORGANIZATION_IDS yet; then this run's TCP proxy is removed;
  7. `railway up` of this checkout for api, worker and web, waiting for each
     (Railway's healthcheck for api and web);
  8. verify: /healthz, /readyz, /v1/me without a token, the web's / and its
     sign-in redirect to Auth0, the worker still running, the owner's
     required variables (by name; the owner's secrets that are not sealed are
     named, with how to seal them), and no TCP proxy open on PostgreSQL.

The TCP proxy (review round 1, M1): this run's proxy is removed when steps
4-6 end, also when they fail and on SIGTERM or SIGHUP (turned into an exit
that runs the cleanup; SIGKILL cannot be caught). Every --apply then lists
PostgreSQL's TCP proxies before it ends, whatever happened before: any proxy
still open (this run's, if its removal failed, or one that was open before
the run, which the script uses but never removes, since it cannot tell
whose it is or whether an interrupted run left it) is named with the command
that removes it, and the run exits 1, unless GEMINUS_KEEP_DB_PROXY=1
accepts open proxies (they are still named). A removal the CLI reports as
failed, or a listing that fails, is an exit 1 too.

Variable listings (review round 1, M2): `railway variable list --json` is
the only way to see which variables exist, and the CLI returns every value
that is not sealed. The listing is parsed as it arrives: only the values
this script needs (its own generated passwords, Postgres's
DATABASE_PUBLIC_URL) are kept, and each is registered as a secret (redacted
from the output, refused as an argument); every other value, the owner's
among them, is replaced by a marker (set, or sealed) while the JSON is
parsed, so it is in no dict, no output, no error and no exception. The
Railway token is registered the same way, and only the `railway` CLI gets
it in its environment.

It never prints a token, a password or a key, nor the output of a command
that holds one (variable lists, bucket credentials); commands are shown by
their arguments, which never carry a secret (checked before every call).
--dry-run calls nothing remote: it checks the inputs, the tools and the
repository's deployment files and prints the steps and commands.
"""
import argparse
import base64
import enum
import json
import os
import re
import secrets
import shlex
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CLI_VERSION = "5.64.2"
SERVICES = ("migrate", "api", "worker", "web")
TOKENS = ("RAILWAY_TOKEN", "RAILWAY_API_TOKEN")
LOGINS = (("geminus_migrator", "GEMINUS_MIGRATOR_DB_PASSWORD"), ("geminus_api", "GEMINUS_API_DB_PASSWORD"),
          ("geminus_worker", "GEMINUS_WORKER_DB_PASSWORD"))
INPUTS = {
    "GEMINUS_AUTH0_ISSUER": "the Auth0 issuer, https://<tenant>.<region>.auth0.com/ (with the final /)",
    "GEMINUS_AUTH0_AUDIENCE": "the Auth0 API Identifier",
    "GEMINUS_AUTH0_CLIENT_ID": "the Auth0 Application's Client ID",
    "GEMINUS_SEED_OWNER_SUBJECT": "the owner's Auth0 user_id (auth0|...), the demo organization's first owner",
    "GEMINUS_RAILWAY_BUCKET_REGION": "the bucket's region: sjc, iad, ams or sin (fixed once created)",
}
DEFAULTS = {
    "GEMINUS_RAILWAY_PROJECT": "geminus-demo",
    "GEMINUS_RAILWAY_ENVIRONMENT": "production",
    "GEMINUS_SEED_ORG_NAME": "Demo line (synthetic)",
}
WAIT_SECONDS = 900


class Failed(Exception):
    """A step failed; the message says which and what to do."""


class Seen(enum.Enum):
    """What a variable listing keeps of a value the deployer does not need."""
    SET = "set (value not kept)"
    SEALED = "sealed"

    def __repr__(self) -> str:
        return f"<{self.value}>"


def exit_on_signal(signum, frame):
    """SIGTERM and SIGHUP end the run as an exit, so its cleanup runs (M1);
    a second signal is ignored while the cleanup removes the TCP proxy."""
    for s in (signal.SIGTERM, signal.SIGHUP):
        signal.signal(s, signal.SIG_IGN)
    print(f"deploy: interrupted ({signal.Signals(signum).name}): cleaning up, then exiting", flush=True)
    raise SystemExit(128 + signum)


class Run:
    def __init__(self, dry: bool):
        self.dry = dry
        self.hidden: list[str] = []  # secret values: never printed, never in argv
        for k in TOKENS:  # the CLI reads the token itself; an error that quotes it is redacted
            if os.environ.get(k):
                self.secret(os.environ[k])
        self.cfg = {k: os.environ.get(k, "") for k in list(INPUTS) + list(DEFAULTS)}
        for k, v in DEFAULTS.items():
            self.cfg[k] = self.cfg[k] or v
        self.project_token = bool(os.environ.get("RAILWAY_TOKEN"))
        self.spec = json.loads((ROOT / "deploy/railway/variables.json").read_text(encoding="utf-8"))
        self.owner = frozenset(n for s in self.spec["services"].values() for n, e in s["variables"].items() if e.get("source") == "owner")
        self.database_seen = False  # Postgres exists: its TCP proxies can be listed
        self.proxies_before: list[str] = []
        self.proxy_created = None
        self.proxies_listed = False
        self.proxy_problems: list[str] = []
        self.code = 0

    # -- output and commands -------------------------------------------------

    def say(self, msg: str) -> None:
        for s in self.hidden:
            if s:
                msg = msg.replace(s, "[redacted]")
        print(msg, flush=True)

    def secret(self, value: str) -> str:
        self.hidden.append(value)
        return value

    @staticmethod
    def child_env(argv0: str, extra=None) -> dict:
        """A command's environment: the Railway token only for the CLI (L2)."""
        env = dict(os.environ, CI="true", RAILWAY_NO_AUTO_UPDATE="1")
        if argv0 != "railway":
            for k in TOKENS:
                env.pop(k, None)
        env.update(extra or {})
        return env

    def run(self, argv, *, stdin=None, env=None, check=True, quiet=False, local=False, parse=None):
        """Runs argv (local: a read-only local command, run in a dry run too).
        With parse (a json object_pairs_hook), returns the output parsed with
        it (None if it is not JSON), never the output itself."""
        for a in argv:
            if any(s and s in a for s in self.hidden):
                raise Failed("refusing to pass a secret as a command-line argument")
        if not quiet:
            self.say("  $ " + " ".join(shlex.quote(a) for a in argv) + ("   (value on stdin)" if stdin is not None and argv[0] == "railway" else ""))
        if self.dry and not local:
            self.code = 0
            return None if parse else ""
        p = subprocess.run(argv, input=stdin, capture_output=True, text=True, env=self.child_env(argv[0], env), cwd=ROOT)
        self.code = p.returncode
        if check and p.returncode != 0:
            err = (p.stderr or "").strip().splitlines()[-5:]
            self.say("    failed (exit %d): %s" % (p.returncode, " | ".join(err)))
            raise Failed(f"`{argv[0]} {' '.join(argv[1:3])}` failed")
        if parse is None:
            return p.stdout
        try:
            return json.loads(p.stdout, object_pairs_hook=parse)
        except ValueError:
            return None  # the error would hold the output: not kept

    def rw(self, *args, stdin=None, check=True, quiet=False):
        """A Railway CLI call; its JSON output is parsed, never printed."""
        out = self.run(["railway", *args], stdin=stdin, check=check, quiet=quiet)
        if self.dry or not out.strip():
            return None
        try:
            return json.loads(out)
        except json.JSONDecodeError:
            return out

    def env(self) -> list:
        """--environment, except with a project token (its own environment)."""
        return [] if self.project_token else ["--environment", self.cfg["GEMINUS_RAILWAY_ENVIRONMENT"]]

    # -- tolerant readers of the CLI's JSON ------------------------------------

    @staticmethod
    def walk(obj):
        if isinstance(obj, dict):
            yield obj
            for v in obj.values():
                yield from Run.walk(v)
        elif isinstance(obj, list):
            for v in obj:
                yield from Run.walk(v)

    def find_id(self, obj, name: str):
        for d in self.walk(obj):
            if str(d.get("name", "")).lower() == name.lower() and d.get("id"):
                return d["id"]
        return None

    def variables_of(self, service: str, keep=frozenset()) -> dict:
        """`railway variable list --json` of a service (src/commands/variable.rs
        at v5.64.2: a flat {name: value} map, null when sealed) as name -> the
        value for the names in keep (registered as a secret; None when sealed),
        and Seen.SET or Seen.SEALED for every other name ("" stays ""). The
        values not kept, the owner's among them, are dropped while the JSON is
        parsed (M2)."""
        if keep & self.owner:
            raise Failed("internal: the owner's variables are never kept")
        if self.dry:
            return {}

        def receive(pairs):
            out = {}
            for k, v in pairs:
                if k in keep and k not in self.owner and (v is None or isinstance(v, str)):
                    out[k] = self.secret(v) if v else v
                elif v is None:
                    out[k] = Seen.SEALED
                else:
                    out[k] = "" if v == "" else Seen.SET
            return out
        got = self.run(["railway", "variable", "list", "--service", service, *self.env(), "--json"], quiet=True, parse=receive)
        if not isinstance(got, dict):
            raise Failed(f"`railway variable list --service {service} --json` did not print the expected map")
        return got

    @staticmethod
    def domain_of(listing):
        for d in Run.walk(listing):
            for k in ("domain", "name", "host"):
                v = d.get(k)
                if isinstance(v, str) and re.fullmatch(r"[a-z0-9.-]+\.railway\.app", v):
                    return v
        m = re.search(r"[a-z0-9.-]+\.railway\.app", json.dumps(listing) if listing is not None else "")
        return m.group(0) if m else None

    # -- 0. preflight ---------------------------------------------------------

    def preflight(self) -> None:
        problems, absent = [], [f"input {k}: {why}" for k, why in INPUTS.items() if not self.cfg[k]]
        if not (os.environ.get("RAILWAY_TOKEN") or os.environ.get("RAILWAY_API_TOKEN")):
            absent.append("RAILWAY_TOKEN (a project token) or RAILWAY_API_TOKEN (an account or workspace token)")
        if absent and not self.dry:
            problems += ["missing " + a for a in absent]
        elif absent:
            # A dry run shows the plan with placeholders and says what --apply will need.
            self.say("dry run: --apply will also need:\n  - " + "\n  - ".join(absent))
            for k in INPUTS:
                self.cfg[k] = self.cfg[k] or f"<{k}>"
        iss = self.cfg["GEMINUS_AUTH0_ISSUER"]
        if iss and not iss.startswith("<") and not re.fullmatch(r"https://[a-z0-9.-]+/", iss):
            problems.append("GEMINUS_AUTH0_ISSUER must be https://<host>/ with the final /")
        region = self.cfg["GEMINUS_RAILWAY_BUCKET_REGION"]
        if region and not region.startswith("<") and region not in ("sjc", "iad", "ams", "sin"):
            problems.append("GEMINUS_RAILWAY_BUCKET_REGION must be sjc, iad, ams or sin")
        subject = self.cfg["GEMINUS_SEED_OWNER_SUBJECT"]
        if subject and not re.fullmatch(r"[\x21-\x7e]{1,255}", subject):
            problems.append("GEMINUS_SEED_OWNER_SUBJECT must be 1-255 visible ASCII characters")
        for tool in ("psql", "go", "git"):
            if not shutil.which(tool):
                problems.append(f"{tool} is not installed (psql 15 or later: deploy/sql/create_logins.sql uses \\getenv)")
        if shutil.which("psql"):
            v = re.search(r"(\d+)\.", self.run(["psql", "--version"], check=False, quiet=True, local=True) or "")
            if not v or int(v.group(1)) < 15:
                problems.append("psql 15 or later is needed")
        if not self.dry:
            if not shutil.which("railway"):
                problems.append(f"the Railway CLI is not installed (deploy/railway/deploy.sh --install-cli DIR installs {CLI_VERSION})")
            elif CLI_VERSION not in self.run(["railway", "--version"], check=False, quiet=True):
                problems.append(f"the Railway CLI must be {CLI_VERSION} (pinned)")
            dirty = self.run(["git", "status", "--porcelain"], quiet=True).strip()
            if dirty and os.environ.get("GEMINUS_ALLOW_DIRTY") != "1":
                problems.append("the checkout has uncommitted changes, and `railway up` uploads the working tree (GEMINUS_ALLOW_DIRTY=1 accepts it)")
        if os.environ.get("RAILWAY_TOKEN") and os.environ.get("RAILWAY_API_TOKEN"):
            self.say("note: both tokens are set; the CLI takes the project and environment of RAILWAY_TOKEN")
        try:
            self.run([sys.executable, "-I", "scripts/check_deploy.py"], quiet=True, local=True)
        except Failed:
            problems.append("scripts/check_deploy.py failed (its message is above)")
        if problems:
            raise Failed("preflight:\n  - " + "\n  - ".join(problems))
        self.say("preflight: inputs, tools and deployment files are in order")

    # -- 1-3. project, resources, variables -----------------------------------

    def project(self) -> None:
        self.say("1. project and environment")
        if self.project_token:
            self.rw("status", "--json")
            return
        name, env = self.cfg["GEMINUS_RAILWAY_PROJECT"], self.cfg["GEMINUS_RAILWAY_ENVIRONMENT"]
        pid = self.find_id(self.rw("list", "--json"), name)
        if pid is None:
            ws = os.environ.get("GEMINUS_RAILWAY_WORKSPACE")
            self.rw("init", "--name", name, *(["--workspace", ws] if ws else []), "--json")
            pid = self.find_id(self.rw("list", "--json"), name)
            if pid is None and not self.dry:
                raise Failed(f"project {name} was not created")
        # A project's first environment is production; the target is linked next.
        self.rw("link", "--project", pid or "<project id>", "--environment", "production", "--json")
        if self.find_id(self.rw("environment", "list", "--json"), env) is None:
            self.rw("environment", "new", env, "--json")
        self.rw("environment", "link", env, "--json")

    def resources(self) -> None:
        self.say("2. PostgreSQL, the bucket, the services, their config files, domains")
        services = self.rw("service", "list", *self.env(), "--json")
        if self.find_id(services, self.spec["database"]) is None:
            self.rw("add", "--database", "postgres", "--json")
        self.database_seen = True
        if self.find_id(self.rw("bucket", "list", *self.env(), "--json"), self.spec["bucket"]) is None:
            self.rw("bucket", "create", self.spec["bucket"], "--region", self.cfg["GEMINUS_RAILWAY_BUCKET_REGION"], *self.env(), "--json")
        for s in SERVICES:
            if self.find_id(services, s) is None:
                self.rw("add", "--service", s, "--json")
        configs = []
        for s in SERVICES:
            configs += ["--service-config", s, "configFile", "/" + self.spec["services"][s]["config"]]
        self.rw("environment", "edit", *self.env(), *configs, "--message", "T-039 deploy.sh: config-as-code files", "--json")
        for s in SERVICES:
            dom = self.spec["services"][s].get("public_domain")
            if dom and not self.domain_of(self.rw("domain", "list", "--service", s, *self.env(), "--json")):
                self.rw("domain", "--service", s, "--port", str(dom["port"]), *self.env(), "--json")

    def bucket_credentials(self) -> dict:
        if self.dry:
            return {k: f"<bucket {k}>" for k in ("endpoint", "bucketName", "region", "urlStyle", "accessKeyId", "secretAccessKey")}
        creds = self.rw("bucket", "credentials", "--bucket", self.spec["bucket"], *self.env(), "--json", quiet=True)
        if not isinstance(creds, dict) or not creds.get("secretAccessKey") or not creds.get("accessKeyId"):
            raise Failed("the bucket's credentials could not be read (`railway bucket credentials`)")
        self.secret(creds["secretAccessKey"])
        self.secret(creds["accessKeyId"])
        creds["urlStyle"] = "path" if "path" in str(creds.get("urlStyle", "")).lower() else "virtual-hosted"
        return creds

    def fill(self, template: str, creds: dict, seed: dict) -> str:
        def sub(m):
            key = m.group(1)
            if key.startswith("bucket:"):
                return str(creds[key[7:]])
            if key.startswith("seed:"):
                return seed.get(key[5:], "")
            return self.cfg[key]
        return re.sub(r"(?<!\$)\{\{([^}]+)\}\}", sub, template)

    def generate(self, entry: dict) -> str:
        if entry.get("format") == "session-key":
            return self.secret("k1:" + base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("="))
        return self.secret(secrets.token_hex(32))

    def set_from_stdin(self, service: str, name: str, value: str) -> None:
        self.rw("variable", "set", name, "--stdin", "--service", service, *self.env(), "--skip-deploys", "--json", stdin=value)

    def variables(self, creds: dict) -> dict:
        """Sets each service's variables; returns {login: (service, name, password or None when sealed)}."""
        self.say("3. variables (secrets on stdin, never printed)")
        passwords = {}
        for s in SERVICES:
            spec = self.spec["services"][s]["variables"]
            have = self.variables_of(s, keep=frozenset(n for n, e in spec.items() if e.get("source") == "generated"))
            for name, e in spec.items():
                src = e.get("source")
                if src == "owner":
                    continue  # the owner's: never set; its value was dropped on receipt
                if src == "generated":
                    if name in have:
                        value = have[name] or None  # None: sealed, in use
                    else:
                        value = self.generate(e)
                        self.set_from_stdin(s, name, value)
                    if e.get("login"):
                        passwords[e["login"]] = (s, name, value)
                elif src and src.startswith("bucket:"):
                    if name not in have:
                        self.set_from_stdin(s, name, creds[src[7:]])
                elif not (e.get("when") == "absent" and name in have):
                    value = self.fill(e["value"], creds, {})
                    if value:  # an empty fill (the seed's organization) is set after the seed
                        self.rw("variable", "set", f"{name}={value}", "--service", s, *self.env(), "--skip-deploys", "--json")
        return passwords

    # -- 4. database logins ------------------------------------------------------

    @staticmethod
    def proxy_ids(listing):
        """The ids in `railway tcp-proxy list --json` (src/commands/tcp_proxy.rs
        at v5.64.2: {"proxies": [{"id": ...}, ...]}); None if it is not that."""
        if not isinstance(listing, dict) or not isinstance(listing.get("proxies"), list):
            return None
        return [str(p["id"]) for p in listing["proxies"] if isinstance(p, dict) and p.get("id")]

    def proxy_delete_cmd(self, pid: str) -> str:
        return " ".join(shlex.quote(a) for a in ["railway", "tcp-proxy", "delete", pid, "--yes", "--service", self.spec["database"], *self.env()])

    def proxy_open(self) -> None:
        self.say("4. database logins (a TCP proxy on PostgreSQL for this run)")
        listing = self.rw("tcp-proxy", "list", "--service", self.spec["database"], *self.env(), "--json")
        before = [] if self.dry else self.proxy_ids(listing)
        if before is None:
            raise Failed("`railway tcp-proxy list` did not print the expected {\"proxies\": [...]}")
        if before:
            # One proxy per service (the CLI's own note). Used, never removed: it may be someone's,
            # or one an interrupted run left; the end of the run names it and exits 1 (M1).
            self.proxies_before = before
            self.say(f"   a TCP proxy was open before this run ({', '.join(before)}): used, not removed; reported at the end")
            return
        made = self.rw("tcp-proxy", "create", "--port", "5432", "--service", self.spec["database"], *self.env(), "--json")
        if self.dry:
            self.proxy_created = "<proxy id>"
            return
        proxy = made.get("proxy") if isinstance(made, dict) else None
        self.proxy_created = str(proxy["id"]) if isinstance(proxy, dict) and proxy.get("id") else None
        if self.proxy_created is None:
            # The CLI prints "proxy": null when the proxy is not available yet: find it by listing.
            now = self.proxy_ids(self.rw("tcp-proxy", "list", "--service", self.spec["database"], *self.env(), "--json")) or []
            self.proxy_created = next((p for p in now if p not in before), None)

    def proxy_close(self) -> None:
        if not self.proxy_created:
            return
        self.say("   removing this run's TCP proxy")
        self.rw("tcp-proxy", "delete", self.proxy_created, "--yes", "--service", self.spec["database"], *self.env(), "--json", check=False)
        if self.code != 0:
            self.proxy_problems.append(f"could not remove this run's TCP proxy {self.proxy_created} (railway exit {self.code}): "
                                       f"remove it with `{self.proxy_delete_cmd(self.proxy_created)}`")

    def check_proxies(self) -> list:
        """Lists PostgreSQL's TCP proxies (M1); returns the proxy problems so far:
        a failed removal, a listing that failed, any proxy still open (unless
        GEMINUS_KEEP_DB_PROXY=1, which names it in a warning instead)."""
        if self.proxies_listed or not self.database_seen:
            return self.proxy_problems
        self.proxies_listed = True
        if self.dry:
            self.say("   would list Postgres's TCP proxies: any still open (also one from before this run) "
                     "fails the run unless GEMINUS_KEEP_DB_PROXY=1")
            return []
        listing = self.rw("tcp-proxy", "list", "--service", self.spec["database"], *self.env(), "--json", check=False, quiet=True)
        ids = self.proxy_ids(listing) if self.code == 0 else None
        if ids is None:
            self.proxy_problems.append("could not list the TCP proxies of Postgres (`railway tcp-proxy list`): check Railway → "
                                       "Postgres → Settings → Networking and remove any TCP proxy")
            return self.proxy_problems
        keep = os.environ.get("GEMINUS_KEEP_DB_PROXY") == "1"
        for pid in ids:
            whose = ("this run's" if pid == self.proxy_created else
                     "open before this run (used, not removed: it may be someone's, or left by an interrupted run)"
                     if pid in self.proxies_before else "not this run's")
            msg = f"TCP proxy {pid} on Postgres is still open, {whose}: remove it with `{self.proxy_delete_cmd(pid)}`"
            if keep:
                self.say(f"   warning: {msg} (GEMINUS_KEEP_DB_PROXY=1: kept)")
            else:
                self.proxy_problems.append(msg + " (or set GEMINUS_KEEP_DB_PROXY=1 to keep it)")
        if not ids:
            self.say("   TCP proxies of Postgres: none open")
        return self.proxy_problems

    def admin(self) -> dict:
        """psql's environment for the database's administrator, through the proxy."""
        if self.dry:
            self.say("  (reads Postgres's DATABASE_PUBLIC_URL from `railway variable list --service Postgres --json`; never printed)")
            return {}
        deadline = time.time() + 300
        while True:
            url = self.variables_of(self.spec["database"], keep=frozenset({"DATABASE_PUBLIC_URL"})).get("DATABASE_PUBLIC_URL")
            if isinstance(url, str) and url:
                break
            if time.time() > deadline:
                raise Failed("Postgres has no DATABASE_PUBLIC_URL: the TCP proxy did not become active (redeploy Postgres, then run again)")
            time.sleep(10)
        u = urllib.parse.urlparse(url)
        password = urllib.parse.unquote(u.password or "")
        self.secret(password)
        env = {"PGHOST": u.hostname or "", "PGPORT": str(u.port or 5432), "PGUSER": urllib.parse.unquote(u.username or ""),
               "PGPASSWORD": password, "PGDATABASE": u.path.lstrip("/"), "PGSSLMODE": "require", "PGCONNECT_TIMEOUT": "10"}
        for _ in range(18):  # the proxy may need a moment after the variable appears
            self.run(["psql", "-X", "-Atc", "SELECT 1"], env=env, check=False, quiet=True)
            if self.code == 0:
                return env
            time.sleep(10)
        raise Failed("the database does not answer through the TCP proxy")

    def psql(self, env: dict, *args, stdin=None) -> str:
        return self.run(["psql", "-X", "-v", "ON_ERROR_STOP=1", *args], env=env, stdin=stdin)

    def logins(self, admin: dict, passwords: dict) -> str:
        """Creates the four logins, aligns their passwords; returns the provisioner's (new for each run)."""
        self.psql(admin, "-q", "-f", "db/bootstrap_roles.sql")
        existing = set() if self.dry else set(self.psql(admin, "-Atc", r"SELECT rolname FROM pg_roles WHERE rolname LIKE 'geminus\_%'").split())
        env, align = dict(admin), {}
        for login, var in LOGINS:
            service, name, value = passwords.get(login, (None, var, None))
            if value is None and login not in existing and not self.dry:
                # Sealed (unreadable) while the login is missing: a new password for both.
                value = self.generate({})
                self.set_from_stdin(service, name, value)
            if value is not None and login in existing:
                align[login] = value
            # A login that exists keeps its password; create_logins.sql only creates missing ones.
            env[var] = value if value is not None else self.generate({})
        provisioner = self.generate({})
        env["GEMINUS_PROVISIONER_DB_PASSWORD"] = provisioner
        self.psql(env, "-q", "-f", "deploy/sql/create_logins.sql")
        align["geminus_provisioner"] = provisioner
        for login, value in align.items():
            script = f"\\getenv p GEMINUS_NEW_PASSWORD\nSELECT format('ALTER ROLE {login} PASSWORD %L', :'p') \\gexec\n"
            self.psql(dict(admin, GEMINUS_NEW_PASSWORD=value), "-q", "-f", "-", stdin=script)
        return provisioner

    def seal(self) -> None:
        if os.environ.get("GEMINUS_RAILWAY_SEAL", "on") == "off":
            self.say("   GEMINUS_RAILWAY_SEAL=off: the secrets stay unsealed (docs/runbooks/deploy.md §7.7)")
            return
        configs = []
        for s in SERVICES:
            for name, e in self.spec["services"][s]["variables"].items():
                if e.get("secret") and e.get("source") != "owner":
                    configs += ["--service-config", s, f"variables.{name}.isSealed", "true"]
        self.run(["railway", "environment", "edit", *self.env(), *configs, "--message", "T-039 deploy.sh: seal", "--json"], check=False)
        if self.code != 0:
            self.say("   note: the CLI did not seal them; seal the generated and copied secrets in Railway's UI (variables.json: secret, not owner)")

    # -- 5-7. deployments ---------------------------------------------------------

    def status(self, service: str) -> str:
        listing = self.rw("deployment", "list", "--service", service, *self.env(), "--json", quiet=True)
        return next((str(d["status"]).upper() for d in self.walk(listing) if "status" in d), "")

    def deploy(self, service: str, sha: str, done=None) -> None:
        """`railway up` of this checkout; waits until done() (default: Railway's SUCCESS)."""
        self.rw("up", "--service", service, *self.env(), "--detach", "--json", "--message", f"T-039 deploy.sh {sha}")
        if self.dry:
            return
        deadline = time.time() + WAIT_SECONDS
        while time.time() < deadline:
            time.sleep(15)
            status = self.status(service)
            if status in ("FAILED", "CRASHED", "REMOVED", "SKIPPED"):
                raise Failed(f"{service}: deployment {status} (`railway logs --service {service}`)")
            if (done() if done else status in ("SUCCESS", "COMPLETED")):
                self.say(f"   {service}: {status}")
                return
        raise Failed(f"{service}: no result after {WAIT_SECONDS} s")

    def migrations(self, admin: dict, sha: str) -> None:
        self.say("5. migrations (the migrate service, as the migration login)")
        want = len(list((ROOT / "migrations").glob("*.up.sql")))

        def recorded() -> bool:
            out = self.run(["psql", "-X", "-Atc", "SELECT count(*) FROM geminus_migrations.applied"], env=admin, check=False, quiet=True)
            return self.code == 0 and out.strip() == str(want)
        try:
            self.deploy("migrate", sha, done=recorded)
        except Failed as e:
            raise Failed(f"{e}; if its log says the password authentication failed, Railway did not expand the sealed "
                         "password in GEMINUS_MIGRATE_DATABASE_URL: docs/runbooks/deploy.md §7.7 (recovery)") from None
        self.say(f"   would wait until {want} migrations are recorded" if self.dry else f"   {want} migrations recorded")

    def seed(self, admin: dict, provisioner: str) -> None:
        self.say("6. seed (one synthetic organization)")
        if self.variables_of("worker").get("WORKER_ORGANIZATION_IDS"):
            self.say("   the worker already serves an organization: none added")
            return
        url = self.secret("postgresql://geminus_provisioner:%s@%s:%s/%s?sslmode=require" % (
            urllib.parse.quote(provisioner, safe=""), admin.get("PGHOST", "<host>"), admin.get("PGPORT", "<port>"), admin.get("PGDATABASE", "<db>")))
        out = self.run(["go", "run", "./cmd/provision-tenant", "organization", "-name", self.cfg["GEMINUS_SEED_ORG_NAME"],
                        "-owner-issuer", self.cfg["GEMINUS_AUTH0_ISSUER"], "-owner-subject", self.cfg["GEMINUS_SEED_OWNER_SUBJECT"]],
                       env={"GEMINUS_PROVISION_DATABASE_URL": url})
        org = "<organization_id>" if self.dry else (re.search(r'"organization_id":"(org_[0-9A-Z]{26})"', out) or [None, None])[1]
        if not org:
            raise Failed("cmd/provision-tenant printed no organization")
        self.say(f"   organization {org}")
        self.rw("variable", "set", f"WORKER_ORGANIZATION_IDS={org}", "--service", "worker", *self.env(), "--skip-deploys", "--json")

    # -- 8. verify ---------------------------------------------------------------

    @staticmethod
    def get(url: str):
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, *args, **kwargs):
                return None
        try:
            with urllib.request.build_opener(NoRedirect).open(url, timeout=30) as r:
                return r.status, r.headers
        except urllib.error.HTTPError as e:
            return e.code, e.headers
        except (urllib.error.URLError, OSError) as e:
            return 0, {"error": str(e)}

    def verify(self) -> None:
        self.say("8. verify")
        if self.dry:
            for line in ("GET https://<api>/healthz 200", "GET https://<api>/readyz 200", "GET https://<api>/v1/me 401",
                         "GET https://<web>/ 200", "GET https://<web>/auth/login 302/303 to <issuer>authorize",
                         "the worker's deployment still SUCCESS",
                         "the owner's required variables exist and its secrets are sealed (names only; values dropped on receipt)"):
                self.say("   would check " + line)
            self.check_proxies()
            return
        problems, unsealed = [], []
        for s in SERVICES:
            have = self.variables_of(s)
            for name, e in self.spec["services"][s]["variables"].items():
                if e.get("source") != "owner":
                    continue
                if e.get("required") and name not in have:
                    problems.append(f"the owner has not set {name} on {s} (Railway → {s} → Variables)")
                elif e.get("secret") and have.get(name) is Seen.SET:
                    unsealed.append(f"{name} on {s}")
        if unsealed:
            self.say("   note: the owner's secrets not sealed: " + ", ".join(unsealed) + ". `railway variable list` returns an unsealed "
                     "value to whoever lists the variables (this script drops it on receipt: kept, printed and stored nowhere); "
                     "seal each in Railway (the service → Variables → the variable's 3-dot menu → Seal; it cannot be unsealed, "
                     "the value can still be replaced)")
        api = self.domain_of(self.rw("domain", "list", "--service", "api", *self.env(), "--json", quiet=True))
        web = self.domain_of(self.rw("domain", "list", "--service", "web", *self.env(), "--json", quiet=True))
        if not api or not web:
            problems.append("the public domains of api and web were not found")
        else:
            for path, want in (("/healthz", 200), ("/readyz", 200), ("/v1/me", 401)):
                code, _ = self.get(f"https://{api}{path}")
                self.say(f"   https://{api}{path} -> {code}")
                if code != want:
                    problems.append(f"https://{api}{path} answered {code}, want {want}")
            code, _ = self.get(f"https://{web}/")
            self.say(f"   https://{web}/ -> {code}")
            if code != 200:
                problems.append(f"https://{web}/ answered {code}, want 200")
            code, headers = self.get(f"https://{web}/auth/login")
            loc = headers.get("Location", "") if headers else ""
            self.say(f"   https://{web}/auth/login -> {code} {loc.split('?')[0]}")
            if code not in (302, 303) or not loc.startswith(self.cfg["GEMINUS_AUTH0_ISSUER"] + "authorize"):
                problems.append("the sign-in does not redirect to Auth0 (503 means the BFF configuration is incomplete; "
                                "most likely the owner's BFF_OIDC_CLIENT_SECRET is missing)")
        time.sleep(30)
        if (st := self.status("worker")) != "SUCCESS":
            problems.append(f"the worker's deployment is {st or 'unknown'} (`railway logs --service worker`)")
        problems += self.check_proxies()
        if web:
            self.say(f"   Auth0 Application: Allowed Callback URL https://{web}/auth/callback, Allowed Logout URL https://{web}/")
        if problems:
            raise Failed("verification:\n  - " + "\n  - ".join(problems))

    # -- the whole run -------------------------------------------------------------

    def steps(self) -> None:
        self.preflight()
        sha = self.run(["git", "rev-parse", "--short", "HEAD"], quiet=True, local=True).strip() or "<commit>"
        if self.dry:
            self.say("dry run: nothing remote is called; the steps and commands of --apply follow")
        self.project()
        self.resources()
        passwords = self.variables(self.bucket_credentials())
        try:
            self.proxy_open()
            admin = self.admin()
            provisioner = self.logins(admin, passwords)
            self.seal()
            self.migrations(admin, sha)
            self.seed(admin, provisioner)
        finally:
            self.proxy_close()
        self.say("7. deploy api, worker, web")
        for s in ("api", "worker", "web"):
            self.deploy(s, sha)
        self.verify()

    def main(self) -> None:
        """The run; whatever ends it (done, a failed step, SIGTERM or SIGHUP as an
        exit), it ends by listing Postgres's TCP proxies (M1), unless verify did."""
        try:
            self.steps()
        except Failed as e:
            extra = [p for p in self.check_proxies() if p not in str(e)]  # verify's message has its own
            if extra:
                raise Failed(f"{e}\n  - TCP proxy: " + "\n  - TCP proxy: ".join(extra)) from None
            raise
        except BaseException:
            for p in self.check_proxies():
                self.say(f"deploy: TCP proxy: {p}")
            raise
        left = self.check_proxies()
        if left:
            raise Failed("the deployment's steps ran, but:\n  - TCP proxy: " + "\n  - TCP proxy: ".join(left))
        self.say(("dry run: done, nothing was called" if self.dry else "deploy: done") +
                 ". Variables that are the owner's: docs/runbooks/deploy.md §7.3.")


def main() -> int:
    ap = argparse.ArgumentParser(prog="deploy/railway/deploy.sh", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    mode = ap.add_mutually_exclusive_group(required=True)
    mode.add_argument("--dry-run", action="store_true", help="check the inputs, tools and files and print the plan; call nothing remote")
    mode.add_argument("--apply", action="store_true", help="create or update the deployment (needs a Railway token)")
    args = ap.parse_args()
    run = Run(dry=args.dry_run)
    for s in (signal.SIGTERM, signal.SIGHUP):
        signal.signal(s, exit_on_signal)
    try:
        run.main()
    except Failed as e:
        run.say(f"deploy: FAIL: {e}")
        return 1
    except SystemExit as e:  # exit_on_signal: the cleanup ran
        run.say(f"deploy: INTERRUPTED (exit {e.code}): rerun deploy/railway/deploy.sh --apply")
        return int(e.code) if isinstance(e.code, int) else 1
    except KeyboardInterrupt:  # SIGINT: the cleanup ran too
        run.say("deploy: INTERRUPTED (SIGINT): rerun deploy/railway/deploy.sh --apply")
        return 130
    return 0


if __name__ == "__main__":
    sys.exit(main())
