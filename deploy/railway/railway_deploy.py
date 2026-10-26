#!/usr/bin/env python3
"""The hackathon deployment on Railway (T-039; H-030 Railway only; H-032 the
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
     owner fills (Auth0 client secret, AI keys, the live switch) are never set
     or read: only their names are checked at the end;
  4. the database logins: a TCP proxy on PostgreSQL for this run only, then
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
     sign-in redirect to Auth0, the worker still running, and the owner's
     required variables.

It never prints a token, a password or a key, nor the output of a command
that holds one (variable lists, bucket credentials); commands are shown by
their arguments, which never carry a secret (checked before every call).
--dry-run calls nothing remote: it checks the inputs, the tools and the
repository's deployment files and prints the steps and commands.
"""
import argparse
import base64
import json
import os
import re
import secrets
import shlex
import shutil
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


class Run:
    def __init__(self, dry: bool):
        self.dry = dry
        self.hidden: list[str] = []  # secret values: never printed, never in argv
        self.cfg = {k: os.environ.get(k, "") for k in list(INPUTS) + list(DEFAULTS)}
        for k, v in DEFAULTS.items():
            self.cfg[k] = self.cfg[k] or v
        self.project_token = bool(os.environ.get("RAILWAY_TOKEN"))
        self.spec = json.loads((ROOT / "deploy/railway/variables.json").read_text(encoding="utf-8"))
        self.proxy_created = None
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

    def run(self, argv, *, stdin=None, env=None, check=True, quiet=False, local=False) -> str:
        """Runs argv (local: a read-only local command, run in a dry run too)."""
        for a in argv:
            if any(s and s in a for s in self.hidden):
                raise Failed("refusing to pass a secret as a command-line argument")
        if not quiet:
            self.say("  $ " + " ".join(shlex.quote(a) for a in argv) + ("   (value on stdin)" if stdin is not None and argv[0] == "railway" else ""))
        if self.dry and not local:
            self.code = 0
            return ""
        full_env = dict(os.environ, CI="true", RAILWAY_NO_AUTO_UPDATE="1")
        full_env.update(env or {})
        p = subprocess.run(argv, input=stdin, capture_output=True, text=True, env=full_env, cwd=ROOT)
        self.code = p.returncode
        if check and p.returncode != 0:
            err = (p.stderr or "").strip().splitlines()[-5:]
            self.say("    failed (exit %d): %s" % (p.returncode, " | ".join(err)))
            raise Failed(f"`{argv[0]} {' '.join(argv[1:3])}` failed")
        return p.stdout

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

    @staticmethod
    def as_variables(listing) -> dict:
        """name -> value (None when sealed) of `railway variable list --json`."""
        if isinstance(listing, dict):
            if all(isinstance(v, (str, type(None))) for v in listing.values()):
                return dict(listing)
            for key in ("variables", "data"):
                if key in listing:
                    return Run.as_variables(listing[key])
        if isinstance(listing, list):
            return {e.get("name") or e.get("key"): e.get("value") for e in listing if isinstance(e, dict) and (e.get("name") or e.get("key"))}
        return {}

    def variables_of(self, service: str) -> dict:
        if self.dry:
            return {}
        return self.as_variables(self.rw("variable", "list", "--service", service, *self.env(), "--json", quiet=True))

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
            v = re.search(r"(\d+)\.", subprocess.run(["psql", "--version"], capture_output=True, text=True).stdout or "")
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
        p = subprocess.run([sys.executable, "-I", "scripts/check_deploy.py"], cwd=ROOT, capture_output=True, text=True)
        if p.returncode != 0:
            problems.append("scripts/check_deploy.py failed: " + p.stderr.strip()[-300:])
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
            have = self.variables_of(s)
            for name, e in self.spec["services"][s]["variables"].items():
                src = e.get("source")
                if src == "owner":
                    continue  # the owner's: never set or read
                if src == "generated":
                    if name in have:
                        value = self.secret(have[name]) if have[name] else None  # None: sealed, in use
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

    def proxy_open(self) -> None:
        self.say("4. database logins (a TCP proxy on PostgreSQL for this run only)")
        listing = self.rw("tcp-proxy", "list", "--service", self.spec["database"], *self.env(), "--json")
        if not any(d.get("id") for d in self.walk(listing)):
            made = self.rw("tcp-proxy", "create", "--port", "5432", "--service", self.spec["database"], *self.env(), "--json")
            self.proxy_created = next((d["id"] for d in self.walk(made) if d.get("id")), None) if not self.dry else "<proxy id>"

    def proxy_close(self) -> None:
        if self.proxy_created:
            self.say("   removing this run's TCP proxy")
            self.rw("tcp-proxy", "delete", self.proxy_created, "--yes", "--service", self.spec["database"], *self.env(), "--json", check=False)

    def admin(self) -> dict:
        """psql's environment for the database's administrator, through the proxy."""
        if self.dry:
            self.say("  (reads Postgres's DATABASE_PUBLIC_URL from `railway variable list --service Postgres --json`; never printed)")
            return {}
        deadline = time.time() + 300
        while True:
            url = self.as_variables(self.rw("variable", "list", "--service", self.spec["database"], *self.env(), "--json", quiet=True)).get("DATABASE_PUBLIC_URL")
            if url:
                break
            if time.time() > deadline:
                raise Failed("Postgres has no DATABASE_PUBLIC_URL: the TCP proxy did not become active (redeploy Postgres, then run again)")
            time.sleep(10)
        self.secret(url)
        u = urllib.parse.urlparse(url)
        password = urllib.parse.unquote(u.password or "")
        self.secret(password)
        env = {"PGHOST": u.hostname or "", "PGPORT": str(u.port or 5432), "PGUSER": urllib.parse.unquote(u.username or ""),
               "PGPASSWORD": password, "PGDATABASE": u.path.lstrip("/"), "PGSSLMODE": "require", "PGCONNECT_TIMEOUT": "10"}
        for _ in range(18):  # the proxy may need a moment after the variable appears
            if subprocess.run(["psql", "-X", "-Atc", "SELECT 1"], env=dict(os.environ, **env), capture_output=True, text=True).returncode == 0:
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
                         "the worker's deployment still SUCCESS", "the owner's required variables exist (names only)"):
                self.say("   would check " + line)
            return
        problems = []
        for s in SERVICES:
            names = set(self.variables_of(s))
            for name, e in self.spec["services"][s]["variables"].items():
                if e.get("source") == "owner" and e.get("required") and name not in names:
                    problems.append(f"the owner has not set {name} on {s} (Railway → {s} → Variables)")
        api = self.domain_of(self.rw("domain", "list", "--service", "api", *self.env(), "--json", quiet=True))
        web = self.domain_of(self.rw("domain", "list", "--service", "web", *self.env(), "--json", quiet=True))
        if not api or not web:
            raise Failed("the public domains of api and web were not found")
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
        self.say(f"   Auth0 Application: Allowed Callback URL https://{web}/auth/callback, Allowed Logout URL https://{web}/")
        if problems:
            raise Failed("verification:\n  - " + "\n  - ".join(problems))

    # -- the whole run -------------------------------------------------------------

    def main(self) -> None:
        self.preflight()
        sha = self.run(["git", "rev-parse", "--short", "HEAD"], quiet=True, local=True).strip() or "<commit>"
        if self.dry:
            self.say("dry run: nothing remote is called; the steps and commands of --apply follow")
        self.project()
        self.resources()
        passwords = self.variables(self.bucket_credentials())
        self.proxy_open()
        try:
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
        self.say(("dry run: done, nothing was called" if self.dry else "deploy: done") +
                 ". Variables that are the owner's: docs/runbooks/deploy.md §7.3.")


def main() -> int:
    ap = argparse.ArgumentParser(prog="deploy/railway/deploy.sh", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    mode = ap.add_mutually_exclusive_group(required=True)
    mode.add_argument("--dry-run", action="store_true", help="check the inputs, tools and files and print the plan; call nothing remote")
    mode.add_argument("--apply", action="store_true", help="create or update the deployment (needs a Railway token)")
    args = ap.parse_args()
    run = Run(dry=args.dry_run)
    try:
        run.main()
    except Failed as e:
        run.say(f"deploy: FAIL: {e}")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
