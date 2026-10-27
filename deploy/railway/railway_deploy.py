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
  2b. Auth0 (T-057, H-034; GEMINUS_AUTH0_SETUP=cli, the default), with the
     pinned Auth0 CLI 1.37.0 (its SHA-256 checked before it is ever run), in
     agent mode, in a home of its own: the owner approves the CLI's device
     sign-in in their own browser (the URL and code it prints are shown,
     nothing else); then, each found and updated or created, never twice:
     the API (identifier GEMINUS_AUTH0_AUDIENCE, RS256, RFC 9068, 600 s,
     offline access), the Regular Web Application (Client Secret (Post),
     authorization code and refresh token only, rotation with a 30 s
     overlap, 43200 s idle and 604800 s absolute lifetimes, callback and
     logout URL only on the web's public domain), sign-up off on the
     database connection GEMINUS_AUTH0_CONNECTION and the application on
     that connection only, and the judge's account (H-035 (2)) with a
     generated password; the application's client secret goes from the
     CLI's JSON, inside this process, only on stdin to `railway variable set
     BFF_OIDC_CLIENT_SECRET --stdin` on web, and is sealed. The CLI is signed
     out and its home removed at the end, also on a failure, SIGTERM or
     SIGHUP. GEMINUS_AUTH0_SETUP=manual skips the step: the owner sets Auth0
     up in its dashboard (docs/runbooks/auth0.md §6) and fills
     BFF_OIDC_CLIENT_SECRET;
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
  6. seed: one synthetic organization with the owner as its first owner and
     the judge's account as an editor (cmd/provision-tenant as the
     provisioning login, whose password is new for each run and stored
     nowhere; -member-* adds the judge in the organization's own
     transaction), only if the worker has no WORKER_ORGANIZATION_IDS yet;
     then, as the database's administrator, the judge's memberships are
     listed: one, in the demo organization, as an editor, or the run fails
     at the end saying what to do; then this run's TCP proxy is removed;
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
The values to redact are kept as keyed digests only (T-057): the Railway
token, the client secret and the judge's password are in no attribute of
the deployer and no return value of its methods; only the CLI gets the token
in its environment. The judge's new password is shown once, on stderr, when
the run ends (whatever ends it), and nowhere else.
--dry-run calls nothing remote: it checks the inputs, the tools and the
repository's deployment files and prints the steps and commands.
"""
import argparse
import atexit
import base64
import enum
import functools
import hashlib
import hmac
import json
import os
import re
import secrets
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
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
    "GEMINUS_SEED_OWNER_SUBJECT": "the owner's Auth0 user_id (auth0|...), the demo organization's first owner",
    "GEMINUS_RAILWAY_BUCKET_REGION": "the bucket's region: sjc, iad, ams or sin (fixed once created)",
}
# With GEMINUS_AUTH0_SETUP=manual only: with the Auth0 step it is the step's output.
MANUAL_INPUTS = {"GEMINUS_AUTH0_CLIENT_ID": "the Auth0 Application's Client ID (GEMINUS_AUTH0_SETUP=manual)"}
DEFAULTS = {
    "GEMINUS_RAILWAY_PROJECT": "geminus-demo",
    "GEMINUS_RAILWAY_ENVIRONMENT": "production",
    "GEMINUS_SEED_ORG_NAME": "Demo line (synthetic)",
    "GEMINUS_AUTH0_SETUP": "cli",
    "GEMINUS_AUTH0_CONNECTION": "Username-Password-Authentication",
    "GEMINUS_JUDGE_EMAIL": "judge@geminus-demo.invalid",
}
WAIT_SECONDS = 900

# The Auth0 step (T-057, H-034). The pinned CLI: github.com/auth0/auth0-cli release v1.37.0 (2026-09-30, MIT).
# SHA-256 of the `auth0` binary in auth0-cli_1.37.0_Linux_x86_64.tar.gz, computed 2026-10-26 from the archive
# whose own SHA-256 the release's checksums.txt lists (deploy.sh --install-auth0-cli checks both); the CLI on
# PATH is hashed and refused before it is ever run when it is not this build.
AUTH0_CLI_VERSION = "1.37.0"
AUTH0_CLI_SHA256 = "2bc2ef7c1ab3c340a50bebb2c6bfd9692ea23dfee539bf7a2e1aac3debb3491c"
# Agent mode (internal/cli/root.go at v1.37.0): results as JSON on stdout, a JSON error envelope on
# stderr, no prompt, no color.
A0_FLAGS = ("--agent-mode", "--no-input", "--no-color")
A0_API_NAME = "Geminus demo API"
A0_APP_NAME = "Geminus demo web"
JUDGE_NAME = "Geminus demo judge"
# The least role that runs the demo flow: owner and editor may ask for AI proposals, approve them and run
# simulations (x-required-roles in openapi/openapi.yaml); a viewer may not; owner adds memberships and the
# audit log, which the demo does not need.
JUDGE_ROLE = "editor"
# The owner approves the sign-in within the device code's lifetime (Auth0's default is 900 s).
A0_LOGIN_SECONDS = 1200
# Fields of the CLI's JSON that hold a credential, dropped as the JSON is parsed (the deployer needs only
# the application's client secret, from one call that asks for it); the value fields only when they hold
# a value, not a settings object (an application's refresh_token, a database connection's
# options.authentication_methods.password: go-auth0 v1.50.0 management/connection.go:643). A settings
# object's own fields go through the same hook, so a credential inside one is still dropped.
A0_SECRET_FIELDS = frozenset({"client_secret", "signing_secret", "signing_keys", "encryption_key", "configuration",
                              "customScripts", "private_key", "device_code", "secret"})
A0_VALUE_FIELDS = frozenset({"access_token", "refresh_token", "id_token", "password"})
JUDGE_SQL = ("SELECT m.organization_id || ' ' || m.role FROM twin.memberships m JOIN twin.users u ON u.id = m.user_id\n"
             "WHERE u.oidc_issuer = :'issuer' AND u.oidc_subject = :'subject' ORDER BY 1;\n")
ORG_LIST = re.compile(r"org_[0-9A-Z]{26}(,org_[0-9A-Z]{26})*")


def judge_password() -> str:
    """A new password for the judge's account: 24 characters of four kinds from Python's secrets module, no
    three alike in a row (any Auth0 password policy up to "excellent")."""
    kinds = ("ABCDEFGHJKLMNPQRSTUVWXYZ", "abcdefghijkmnopqrstuvwxyz", "23456789", "-_.!")
    pool = "".join(kinds)
    while True:
        pw = "".join(secrets.choice(pool) for _ in range(24))
        if all(any(c in k for c in pw) for k in kinds) and not re.search(r"(.)\1\1", pw):
            return pw


class Hidden:
    """The values to redact, as keyed digests only (T-057): no secret is kept in the deployer's state. A
    text is searched by hashing each of its substrings of a registered length; the key is new for each run."""

    def __init__(self):
        self._key = secrets.token_bytes(32)
        self._by_len: dict = {}

    def _digest(self, text: str) -> bytes:
        return hmac.digest(self._key, text.encode("utf-8", "surrogatepass"), "sha256")

    def add(self, value) -> None:
        if isinstance(value, str) and value:
            self._by_len.setdefault(len(value), set()).add(self._digest(value))

    def __contains__(self, value) -> bool:
        return isinstance(value, str) and bool(value) and self._digest(value) in self._by_len.get(len(value), ())

    def __len__(self) -> int:
        return sum(len(d) for d in self._by_len.values())

    def spans(self, text: str) -> list:
        """Where registered values occur in text: [(start, end), ...]."""
        found = []
        for n, digests in self._by_len.items():
            for i in range(len(text) - n + 1):
                if self._digest(text[i:i + n]) in digests:
                    found.append((i, i + n))
        return sorted(found)

    def found(self, text: str) -> bool:
        return bool(self.spans(text))

    def redact(self, text: str) -> str:
        out, at = [], 0
        for start, end in self.spans(text):
            if start < at:  # overlaps the span before: one marker for both
                at = max(at, end)
                continue
            out.append(text[at:start] + "[redacted]")
            at = end
        return "".join(out) + text[at:]


class Failed(Exception):
    """A step failed; the message says which and what to do."""


class OutputLost(Exception):
    """The run's stdout cannot be written (a closed pipe, a hung-up terminal: EPIPE, EIO): the run stops at
    the next message outside a cleanup, the cleanups still run, and it exits EXIT_OUTPUT_LOST (review M2)."""


EXIT_OUTPUT_LOST = 74  # EX_IOERR of sysexits.h


def cleanup(method):
    """A cleanup (signing out, removing the TCP proxy, listing the proxies): a lost stdout never stops it."""
    @functools.wraps(method)
    def run(self, *args, **kwargs):
        self.cleaning += 1
        try:
            return method(self, *args, **kwargs)
        finally:
            self.cleaning -= 1
    return run


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
    try:
        print(f"deploy: interrupted ({signal.Signals(signum).name}): cleaning up, then exiting", flush=True)
    except OSError:  # a hung-up terminal (EIO) or a closed pipe: the cleanup runs regardless (M2)
        pass
    raise SystemExit(128 + signum)


class Run:
    def __init__(self, dry: bool):
        self.dry = dry
        self.out_lost = False  # stdout could not be written (M2)
        self.cleaning = 0  # inside a cleanup (the cleanup decorator)
        self.hidden = Hidden()  # secret values, as digests: never printed, never in argv
        for k in TOKENS:  # the CLI reads the token itself; an error that quotes it is redacted
            if os.environ.get(k):
                self.secret(os.environ[k])
        self.cfg = {k: os.environ.get(k, "") for k in list(INPUTS) + list(MANUAL_INPUTS) + list(DEFAULTS)}
        for k, v in DEFAULTS.items():
            self.cfg[k] = self.cfg[k] or v
        self.auth0_cli = self.cfg["GEMINUS_AUTH0_SETUP"] == "cli"
        self.project_token = bool(os.environ.get("RAILWAY_TOKEN"))
        self.spec = json.loads((ROOT / "deploy/railway/variables.json").read_text(encoding="utf-8"))
        # The variables whose values the deployer never keeps: the owner's, and the client secret the Auth0
        # step writes (or, with GEMINUS_AUTH0_SETUP=manual, the owner).
        self.owner = frozenset(n for s in self.spec["services"].values() for n, e in s["variables"].items()
                               if e.get("source") in ("owner", "auth0"))
        self.database_seen = False  # Postgres exists: its TCP proxies can be listed
        self.proxies_before: list[str] = []
        self.proxy_created = None
        self.proxies_listed = False
        self.proxy_problems: list[str] = []
        self.domains: dict = {}  # the public domains of api and web
        self.a0_bin = None  # the pinned Auth0 CLI, once its SHA-256 matched
        self.a0_home = None  # the CLI's home while it is signed in
        self.a0_domain = urllib.parse.urlparse(self.cfg["GEMINUS_AUTH0_ISSUER"]).hostname or ""
        # The judge's Auth0 user_id (H-035 (2)): from the Auth0 step, or GEMINUS_JUDGE_SUBJECT with the manual setup.
        self.judge_subject = "" if self.auth0_cli else os.environ.get("GEMINUS_JUDGE_SUBJECT", "")
        self.late: list[str] = []  # problems found before verification, which fail it
        self.code = 0

    # -- output and commands -------------------------------------------------

    def say(self, msg: str) -> None:
        """Prints msg; once stdout cannot be written, prints nothing more (stdout
        goes to /dev/null, so neither a child nor Python's exit flush meets the
        broken stream) and raises OutputLost, except inside a cleanup."""
        if not self.out_lost:
            try:
                print(self.hidden.redact(msg), flush=True)
            except OSError:
                self.out_lost = True
                null = os.open(os.devnull, os.O_WRONLY)
                os.dup2(null, sys.stdout.fileno())
                os.close(null)
        if self.out_lost and not self.cleaning:
            raise OutputLost

    @cleanup
    def tell(self, msg: str) -> None:
        """The run's last word: never raises."""
        self.say(msg)

    def secret(self, value: str) -> None:
        """Registers value for redaction (as a digest; nothing is returned or kept)."""
        self.hidden.add(value)

    @staticmethod
    def child_env(argv0: str, extra=None) -> dict:
        """A command's environment, never with the Railway token (L2: run() adds it
        for the Railway CLI alone, so no method returns it). The Auth0 CLI gets a
        small one: its home (where it keeps its session), the proxy and CA
        settings, no analytics (AUTH0_CLI_ANALYTICS=false, internal/analytics at
        v1.37.0: they would send the tenant's name), and no session bus, so it
        never reaches a desktop keyring (its session stays in the home that is
        removed at the end)."""
        if argv0 == "auth0":
            keep = ("PATH", "LANG", "LC_ALL", "TZ", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy",
                    "SSL_CERT_FILE", "SSL_CERT_DIR")
            env = {k: os.environ[k] for k in keep if k in os.environ}
            env.update(AUTH0_CLI_ANALYTICS="false", DBUS_SESSION_BUS_ADDRESS="disabled:")
        else:
            env = {k: v for k, v in os.environ.items() if k not in TOKENS}
            env.update(CI="true", RAILWAY_NO_AUTO_UPDATE="1")
        env.update(extra or {})
        return env

    def run(self, argv, *, stdin=None, env=None, check=True, quiet=False, local=False, parse=None, explain=None, executable=None):
        """Runs argv (local: a read-only local command, run in a dry run too).
        With parse (a json object_pairs_hook), returns the output parsed with
        it (None if it is not JSON), never the output itself. explain turns the
        command's stderr into what is shown of a failure (default: its last
        lines, redacted)."""
        for a in argv:
            if self.hidden.found(a):
                raise Failed("refusing to pass a secret as a command-line argument")
        if not quiet:
            on_stdin = {"railway": "   (value on stdin)", "auth0": "   (JSON on stdin)"}.get(argv[0], "") if stdin is not None else ""
            self.say("  $ " + " ".join(shlex.quote(a) for a in argv) + on_stdin)
        if self.dry and not local:
            self.code = 0
            return None if parse else ""
        child = dict(self.child_env(argv[0], env))  # a copy: child_env's own answer never gets the token
        if argv[0] == "railway":
            child.update({k: os.environ[k] for k in TOKENS if k in os.environ})
        feed = {"input": stdin} if stdin is not None else {"stdin": subprocess.DEVNULL} if argv[0] == "auth0" else {}
        p = subprocess.run(argv, capture_output=True, text=True, env=child, cwd=ROOT, executable=executable, **feed)
        self.code = p.returncode
        if check and p.returncode != 0:
            err = explain(p.stderr or "") if explain else " | ".join((p.stderr or "").strip().splitlines()[-5:])
            self.say("    failed (exit %d): %s" % (p.returncode, err))
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

    def variables_of(self, service: str, keep=frozenset(), plain=None) -> dict:
        """`railway variable list --json` of a service (src/commands/variable.rs
        at v5.64.2: a flat {name: value} map, null when sealed) as name -> the
        value for the names in keep (registered as a secret; None when sealed),
        the value for a name in plain ({name: pattern}: a setting that is no
        secret, kept only when it matches its pattern), and Seen.SET or
        Seen.SEALED for every other name ("" stays ""). The values not kept,
        the owner's among them, are dropped while the JSON is parsed (M2)."""
        plain = plain or {}
        if (keep | set(plain)) & self.owner:
            raise Failed("internal: the owner's variables are never kept")
        if self.dry:
            return {}

        def receive(pairs):
            out = {}
            for k, v in pairs:
                if k in keep and k not in self.owner and (v is None or isinstance(v, str)):
                    if v:
                        self.secret(v)
                    out[k] = v
                elif k in plain and isinstance(v, str) and plain[k].fullmatch(v):
                    out[k] = v
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
        mode = self.cfg["GEMINUS_AUTH0_SETUP"]
        inputs = dict(INPUTS, **({} if self.auth0_cli else MANUAL_INPUTS))
        problems, absent = [], [f"input {k}: {why}" for k, why in inputs.items() if not self.cfg[k]]
        if not (os.environ.get("RAILWAY_TOKEN") or os.environ.get("RAILWAY_API_TOKEN")):
            absent.append("RAILWAY_TOKEN (a project token) or RAILWAY_API_TOKEN (an account or workspace token)")
        if absent and not self.dry:
            problems += ["missing " + a for a in absent]
        elif absent:
            # A dry run shows the plan with placeholders and says what --apply will need.
            self.say("dry run: --apply will also need:\n  - " + "\n  - ".join(absent))
            for k in inputs:
                self.cfg[k] = self.cfg[k] or f"<{k}>"
        if self.auth0_cli:
            self.cfg["GEMINUS_AUTH0_CLIENT_ID"] = "<the Auth0 step's client ID>"
        if mode not in ("cli", "manual"):
            problems.append("GEMINUS_AUTH0_SETUP must be cli (the Auth0 step, H-034) or manual (the owner sets Auth0 up in its dashboard)")
        iss = self.cfg["GEMINUS_AUTH0_ISSUER"]
        if iss and not iss.startswith("<") and not re.fullmatch(r"https://[a-z0-9.-]+/", iss):
            problems.append("GEMINUS_AUTH0_ISSUER must be https://<host>/ with the final /")
        self.a0_domain = (urllib.parse.urlparse(iss).hostname or "") if not iss.startswith("<") else "<tenant domain>"
        aud = self.cfg["GEMINUS_AUTH0_AUDIENCE"]
        if aud and not aud.startswith("<") and (not re.fullmatch(r"[\x21-\x7e]{1,600}", aud) or aud == f"https://{self.a0_domain}/api/v2/"):
            problems.append("GEMINUS_AUTH0_AUDIENCE must be the deployment's own API identifier (visible ASCII), not the Management API's")
        if self.auth0_cli:
            problems += self.preflight_auth0()
        elif os.environ.get("GEMINUS_JUDGE_SUBJECT") and not re.fullmatch(r"[\x21-\x7e]{1,255}", self.judge_subject):
            problems.append("GEMINUS_JUDGE_SUBJECT must be the judge's Auth0 user_id (1-255 visible ASCII characters)")
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

    def preflight_auth0(self) -> list:
        """The Auth0 step's inputs, and the pinned CLI (H-034): hashed, and never
        run, not even for its version, unless its SHA-256 is the pinned one."""
        problems = []
        if os.environ.get("GEMINUS_AUTH0_CLIENT_ID"):
            problems.append("GEMINUS_AUTH0_CLIENT_ID is the Auth0 step's output with GEMINUS_AUTH0_SETUP=cli: unset it "
                            "(or use GEMINUS_AUTH0_SETUP=manual)")
        d = self.a0_domain
        if d and not d.startswith("<") and not d.endswith(".auth0.com"):
            problems.append("GEMINUS_AUTH0_ISSUER must be the tenant's own domain, https://<tenant>.<region>.auth0.com/: the CLI signs "
                            "in to it (internal/cli/login.go at v1.37.0 takes only an auth0.com domain)")
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]{0,127}", self.cfg["GEMINUS_AUTH0_CONNECTION"]):
            problems.append("GEMINUS_AUTH0_CONNECTION must be a connection's name (letters, digits and hyphens)")
        if not re.fullmatch(r"[A-Za-z0-9._%+-]{1,64}@[a-z0-9.-]+\.[a-z]{2,}", self.cfg["GEMINUS_JUDGE_EMAIL"]):
            problems.append("GEMINUS_JUDGE_EMAIL must be an e-mail address (the judge's sign-in name)")
        if self.dry:
            return problems
        path = shutil.which("auth0")
        if not path:
            problems.append(f"the Auth0 CLI is not installed (deploy/railway/deploy.sh --install-auth0-cli DIR installs {AUTH0_CLI_VERSION})")
            return problems
        real = os.path.realpath(path)
        with open(real, "rb") as f:
            image = f.read()
        if hashlib.sha256(image).hexdigest() != AUTH0_CLI_SHA256:
            problems.append(f"the auth0 on PATH ({real}) is not the pinned Auth0 CLI {AUTH0_CLI_VERSION} (its SHA-256 is not "
                            f"{AUTH0_CLI_SHA256}) and was not run: install the pinned one with deploy/railway/deploy.sh --install-auth0-cli DIR")
        else:
            # The bytes that were hashed, copied into a directory only this user can write, are what runs: the
            # file on PATH may change after the check (review L2). Removed when the deployer exits.
            private = tempfile.mkdtemp(prefix="geminus-auth0-cli-")
            atexit.register(shutil.rmtree, private, True)
            fd = os.open(os.path.join(private, "auth0"), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o700)
            with os.fdopen(fd, "wb") as f:
                f.write(image)
            self.a0_bin = os.path.join(private, "auth0")
        return problems

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
        missing = [s for s in (self.spec["database"], *SERVICES) if self.find_id(services, s) is None]
        if self.project_token and missing and not self.dry:
            # CLI 5.64.2: `railway add` creates the service, then records it in this directory's link
            # (src/commands/add.rs, Configs::link_service), which a project token has none of; `railway link`
            # lists workspaces, which a project token cannot. So it would fail after creating each one.
            raise Failed(f"a project token (RAILWAY_TOKEN) cannot create the missing {', '.join(missing)}: the Railway CLI creates "
                         "each one and then fails (`Project not found. Run railway link`). Set RAILWAY_API_TOKEN (an account or "
                         "workspace token) instead, with GEMINUS_RAILWAY_PROJECT=<the project's name>; nothing was created")
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
            if not dom:
                continue
            found = self.domain_of(self.rw("domain", "list", "--service", s, *self.env(), "--json"))
            if not found:
                found = self.domain_of(self.rw("domain", "--service", s, "--port", str(dom["port"]), *self.env(), "--json"))
            self.domains[s] = found or (f"<{s} domain>" if self.dry else None)

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

    # -- 2b. Auth0 (T-057, H-034, H-035 (2)) ------------------------------------------
    #
    # Each command and JSON shape is the Auth0 CLI's at v1.37.0 (internal/cli/*.go, internal/display/*.go)
    # and the Management API's (checks/evidence/2026-10-26-T-057-auth0/sources.txt). Writes go as JSON on
    # stdin (--data @-, internal/cli/data_json.go); the CLI's JSON is parsed with a hook that drops every
    # credential in it as it is parsed.

    def auth0(self, reveal: list) -> None:
        """The Auth0 step: signs the CLI in (the owner approves), finds and
        updates or creates the API, the application, sign-up off, the judge's
        account; puts the client secret on web (stdin, sealed); signs out."""
        self.say("2b. Auth0 (H-034): the API, the web application, sign-up off, the judge's account (H-035 (2)); "
                 "the CLI signed in by the owner's approval and signed out at the end")
        web = self.domains.get("web") or self.domain_of(self.rw("domain", "list", "--service", "web", *self.env(), "--json", quiet=True))
        if not web:
            raise Failed("the web service has no public domain, which the Auth0 application's callback needs")
        try:
            self.a0_open()
            self.a0_login()
            conn = self.cfg["GEMINUS_AUTH0_CONNECTION"]
            self.a0_owner_check(conn)
            self.a0_api()
            client_id, created = self.a0_app(web)
            self.a0_connection(conn, client_id)
            self.judge_subject = self.a0_judge(conn, reveal)
            self.cfg["GEMINUS_AUTH0_CLIENT_ID"] = client_id
            self.a0_secret_to_web(client_id, created)
        except BaseException:
            for p in self.a0_close():
                self.say(f"deploy: {p}")
            raise
        left = self.a0_close()
        if left:
            raise Failed("Auth0: " + "; ".join(left))

    def a0_env(self) -> dict:
        return {"HOME": str(self.a0_home or "<the CLI's home>")}

    def a0_receive(self, capture=None):
        """A json object_pairs_hook for the CLI's output: a credential field is
        replaced by Seen.SET (empty ones are kept as they are) as it is parsed;
        with capture ({field: [...]}), that field's value is appended to the
        caller's list instead, and registered as a secret."""
        def receive(pairs):
            out = {}
            for k, v in pairs:
                if capture is not None and k in capture and isinstance(v, str) and v:
                    self.secret(v)
                    capture[k].append(v)
                    out[k] = Seen.SET
                elif k in A0_SECRET_FIELDS or (k in A0_VALUE_FIELDS and not isinstance(v, dict)):
                    out[k] = v if v in (None, "", [], {}) else Seen.SET
                else:
                    out[k] = v
            return out
        return receive

    def a0_explain(self, sensitive: bool):
        """What is shown of a failed CLI call: the JSON error envelope's class
        (agent mode, internal/cli/exit.go at v1.37.0) and, unless the call
        carried a secret, its message (redacted, shortened)."""
        def explain(stderr: str) -> str:
            try:
                envelope = json.loads(stderr.strip().splitlines()[-1], object_pairs_hook=self.a0_receive())["error"]
                what = f"{envelope.get('code')}/{envelope.get('reason')} (status {envelope.get('status')})"
                message = str(envelope.get("message", ""))
            except (ValueError, IndexError, KeyError, TypeError, AttributeError):
                what, message = "no JSON error envelope", " | ".join(stderr.strip().splitlines()[-3:])
            if sensitive:
                return what + "; the CLI's message is not shown (the call carried a secret)"
            return f"{what}: {message[:300]}"
        return explain

    def a0(self, *args, stdin=None, capture=None, sensitive=False, tenant=True, quiet=False):
        """An Auth0 CLI call in agent mode on the signed-in tenant; returns its
        JSON parsed with a0_receive (None in a dry run or if it is not JSON)."""
        argv = ["auth0", *args, *(["--tenant", self.a0_domain] if tenant else []), *A0_FLAGS]
        return self.run(argv, stdin=stdin, env=self.a0_env(), parse=self.a0_receive(capture), quiet=quiet,
                        explain=self.a0_explain(sensitive or stdin is not None and '"password"' in stdin), executable=self.a0_bin)

    @staticmethod
    def a0_home_path() -> Path:
        state = os.environ.get("XDG_STATE_HOME") or str(Path.home() / ".local" / "state")
        return Path(state) / "geminus-deploy" / "auth0-cli"

    def a0_open(self) -> None:
        """The CLI's own home: a directory only this user can read, where the CLI
        keeps its session (no keyring: child_env). One left by an interrupted
        run (SIGKILL) is signed out and removed first."""
        home = self.a0_home_path()
        if self.dry:
            self.say(f"   the CLI's home: {home} (created for this run, removed at the end)")
            return
        if home.exists():
            self.say(f"   {home} was left by an interrupted run: signing its Auth0 CLI out and removing it first")
            self.a0_home = home
            self.a0_signout()
            shutil.rmtree(home, ignore_errors=True)
        home.mkdir(mode=0o700, parents=True)
        home.chmod(0o700)
        self.a0_home = home

    def a0_sessions(self):
        """The tenants the CLI is signed in to (`auth0 tenants list`), or None if it cannot tell."""
        listed = self.run(["auth0", "tenants", "list", *A0_FLAGS], env=self.a0_env(), check=False, quiet=True,
                          parse=self.a0_receive(), executable=self.a0_bin)
        if self.code == 0 and isinstance(listed, list):
            return [str(t.get("name")) for t in listed if isinstance(t, dict) and t.get("name")]
        if self.a0_home and not (self.a0_home / ".config" / "auth0" / "config.json").exists():
            return []
        return None

    def a0_signout(self) -> list:
        """Signs every session of the CLI's home out (`auth0 logout <tenant>`).
        Without a keyring the CLI removes the tenant from its config and then
        fails to delete its keyring entries (internal/cli/logout.go at v1.37.0),
        so what counts is the listing after it; returns what is still signed in."""
        if not self.a0_home or not self.a0_home.exists():
            return []
        self.run(["auth0", "logout", self.a0_domain, *A0_FLAGS], env=self.a0_env(), check=False, executable=self.a0_bin)
        left = self.a0_sessions()
        for t in left or []:
            self.run(["auth0", "logout", t, *A0_FLAGS], env=self.a0_env(), check=False, executable=self.a0_bin)
        return self.a0_sessions() if left else left

    @cleanup
    def a0_close(self) -> list:
        """Signs the CLI out and removes its home (also on a failure and on
        SIGTERM or SIGHUP: auth0() calls it from its handler); returns the
        problems."""
        if self.dry:
            self.say("   signing the Auth0 CLI out (`auth0 logout <tenant>`) and removing its home")
            self.run(["auth0", "logout", self.a0_domain, *A0_FLAGS], env=self.a0_env())
            return []
        if not self.a0_home:
            return []
        self.say("   signing the Auth0 CLI out and removing its home")
        problems = []
        left = self.a0_signout()
        if left is None or left:
            problems.append(f"the Auth0 CLI may still be signed in ({left if left else 'its tenants could not be listed'}); "
                            f"its home {self.a0_home} is removed, the session's token with it")
        shutil.rmtree(self.a0_home, ignore_errors=True)
        if self.a0_home.exists():
            problems.append(f"the Auth0 CLI's home {self.a0_home} could not be removed: remove it (it holds the CLI's session)")
        else:
            self.a0_home = None
        return problems

    def a0_login(self) -> None:
        """The device sign-in (internal/cli/login.go at v1.37.0, agent mode: a
        JSON line with verification_uri and user_code, then one with logged_in):
        only that URL and code are shown; the CLI waits until the owner approves
        or the code expires."""
        d = self.a0_domain
        argv = ["auth0", "login", "--domain", d, *A0_FLAGS]
        self.say("   the owner's approval: the CLI's device sign-in (the owner signs in to Auth0 in their own browser; "
                 "no password or token is handed over)")
        self.say("  $ " + " ".join(shlex.quote(a) for a in argv))
        if self.dry:
            self.say("   (shows the verification URL and the code the CLI prints, nothing else, and waits for the approval)")
            return
        p = subprocess.Popen(argv, executable=self.a0_bin, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                             text=True, env=self.child_env("auth0", self.a0_env()), cwd=ROOT)
        errors = []
        drain = threading.Thread(target=lambda: errors.append(p.stderr.read()), daemon=True)
        drain.start()
        watchdog = threading.Timer(A0_LOGIN_SECONDS, p.kill)
        watchdog.daemon = True
        watchdog.start()
        shown, result = False, None
        try:
            for line in p.stdout:
                try:
                    obj = json.loads(line, object_pairs_hook=self.a0_receive())
                except ValueError:
                    continue
                if not isinstance(obj, dict):
                    continue
                if not shown and isinstance(obj.get("verification_uri"), str) and isinstance(obj.get("user_code"), str):
                    uri, code = obj["verification_uri"], obj["user_code"]
                    if not (re.fullmatch(r"https://[a-z0-9.-]+\.auth0\.com/[\x21-\x7e]*", uri) and re.fullmatch(r"[A-Z0-9-]{4,20}", code)):
                        raise Failed("the Auth0 CLI printed a sign-in URL or code of an unexpected form (not shown)")
                    minutes = int(obj.get("expires_in") or 0) // 60 if isinstance(obj.get("expires_in"), int) else 0
                    self.say(f"   Auth0 sign-in: open {uri} in your browser, check that it shows the code {code}, and approve"
                             + (f" (within {minutes} minutes)" if minutes else ""))
                    shown = True
                elif obj.get("logged_in") is True:
                    result = obj
            code = p.wait()
        finally:
            watchdog.cancel()
            if p.poll() is None:
                p.kill()
                p.wait()
            drain.join(timeout=5)
        self.code = code
        if code != 0 or result is None:
            raise Failed("the Auth0 sign-in did not complete (" + self.a0_explain(False)(errors[0] if errors else "") + "); run again")
        if result.get("domain") != d:
            raise Failed(f"the Auth0 CLI signed in to {result.get('domain')!r}, not {d} (GEMINUS_AUTH0_ISSUER's tenant): "
                         "nothing was changed; approve the sign-in for that tenant")
        self.say(f"   signed in to {d}")

    def a0_owner_check(self, conn: str) -> None:
        """The owner's own user must be in the database connection: the
        application is enabled on that connection only."""
        subject = self.cfg["GEMINUS_SEED_OWNER_SUBJECT"]
        owner = self.a0("users", "show", subject)
        if self.dry:
            return
        conns = sorted({str(i.get("connection")) for i in (owner or {}).get("identities", []) if isinstance(i, dict)})
        if conn not in conns:
            raise Failed(f"the owner's Auth0 user {subject} is not in the database connection {conn} (its connections: {conns}): the "
                         "application will be enabled on that connection only, so the owner could not sign in; nothing was changed")

    @staticmethod
    def a0_drift(have, want, at="") -> list:
        """The fields of want that have differs in (dicts by want's keys; grant
        types in any order; a missing list or false counts as [] or false)."""
        if isinstance(want, dict):
            have = have if isinstance(have, dict) else {}
            out = []
            for k, v in want.items():
                out += Run.a0_drift(have.get(k, [] if v == [] else False if v is False else None), v, f"{at}.{k}" if at else k)
            return out
        if at.endswith("grant_types") and isinstance(have, list):
            return [] if sorted(have) == sorted(want) else [at]
        return [] if have == want and type(have) is type(want) else [at]

    def a0_api_settings(self) -> dict:
        return {"name": A0_API_NAME, "signing_alg": "RS256", "token_dialect": "rfc9068_profile", "token_lifetime": 600,
                "allow_offline_access": True, "skip_consent_for_verifiable_first_party_clients": True}

    def a0_app_settings(self, web: str) -> dict:
        return {"name": A0_APP_NAME, "app_type": "regular_web", "oidc_conformant": True, "token_endpoint_auth_method": "client_secret_post",
                "grant_types": ["authorization_code", "refresh_token"],
                # The BFF's own paths (apps/web/lib/session/oidc.ts: `${appOrigin}/auth/callback`, post-logout `${appOrigin}/`).
                "callbacks": [f"https://{web}/auth/callback"], "allowed_logout_urls": [f"https://{web}/"],
                "web_origins": [], "allowed_origins": [], "jwt_configuration": {"alg": "RS256"},
                "refresh_token": {"rotation_type": "rotating", "expiration_type": "expiring", "leeway": 30, "token_lifetime": 604800,
                                  "infinite_token_lifetime": False, "idle_token_lifetime": 43200, "infinite_idle_token_lifetime": False}}

    def a0_api(self) -> str:
        """The API (resource server) with the deployment's audience: found by its
        identifier, updated if it drifted, else created; read back."""
        aud, want = self.cfg["GEMINUS_AUTH0_AUDIENCE"], self.a0_api_settings()
        found = [x for x in self.a0("apis", "list", "--number", "1000") or [] if isinstance(x, dict) and x.get("identifier") == aud]
        if len(found) > 1:
            raise Failed(f"Auth0 has {len(found)} APIs with the identifier {aud}")
        if not found:
            made = self.a0("apis", "create", "--data", "@-", stdin=json.dumps(dict(want, identifier=aud)))
            api_id = made.get("id") if isinstance(made, dict) else "<API id>"
            self.say(f"   API {aud}: created")
        else:
            api_id, drift = found[0].get("id"), self.a0_drift(found[0], want)
            if drift:
                self.a0("apis", "update", str(api_id), "--data", "@-", stdin=json.dumps(want))
                self.say(f"   API {aud}: updated ({', '.join(drift)})")
            else:
                self.say(f"   API {aud}: as H-034 wants it, unchanged")
        if not self.dry:
            left = self.a0_drift(self.a0("apis", "show", str(api_id), quiet=True), dict(want, identifier=aud))
            if left:
                raise Failed(f"Auth0 did not keep the API's {', '.join(left)}")
        return str(api_id)

    def a0_app(self, web: str):
        """The Regular Web Application, found by name (exactly one), updated if
        it drifted, else created; read back. Returns (client ID, created)."""
        want = self.a0_app_settings(web)
        found = [x for x in self.a0("apps", "list", "--number", "1000") or [] if isinstance(x, dict) and x.get("name") == A0_APP_NAME]
        if len(found) > 1:
            raise Failed(f"Auth0 has {len(found)} applications named {A0_APP_NAME!r}: delete the extra ones (Applications → Applications)")
        created = not found
        if created:
            made = self.a0("apps", "create", "--data", "@-", stdin=json.dumps(want))
            client_id = made.get("client_id") if isinstance(made, dict) else "<client ID>"
            self.say(f"   application {A0_APP_NAME!r}: created")
        else:
            client_id, drift = found[0].get("client_id"), self.a0_drift(found[0], want)
            if drift:
                self.a0("apps", "update", str(client_id), "--data", "@-", stdin=json.dumps(want))
                self.say(f"   application {A0_APP_NAME!r}: updated ({', '.join(drift)})")
            else:
                self.say(f"   application {A0_APP_NAME!r}: as H-034 wants it, unchanged")
        if not self.dry and (not isinstance(client_id, str) or not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", client_id)):
            raise Failed("the Auth0 CLI printed no usable client ID")
        if not self.dry:
            left = self.a0_drift(self.a0("apps", "show", client_id, quiet=True), want)
            if left:
                raise Failed(f"Auth0 did not keep the application's {', '.join(left)}")
        self.say(f"   callback https://{web}/auth/callback and logout https://{web}/ only; client ID {client_id}")
        return client_id, created

    @staticmethod
    def a0_holds_dropped(obj) -> bool:
        return obj is Seen.SET or (isinstance(obj, dict) and any(Run.a0_holds_dropped(v) for v in obj.values())) or \
            (isinstance(obj, list) and any(Run.a0_holds_dropped(v) for v in obj))

    def a0_connection(self, name: str, client_id: str) -> None:
        """Sign-up off on the database connection, its other options kept (a
        PATCH replaces the whole options object, and a missing display_name
        becomes the name: the Management API's PATCH /connections/{id}); the
        application enabled on that connection only, so no other connection
        (Google, by default on a new tenant) signs anyone up into it."""
        conns = [c for c in self.a0("connections", "list") or [] if isinstance(c, dict)]
        db = [c for c in conns if c.get("name") == name]
        if not self.dry and (len(db) != 1 or db[0].get("strategy") != "auth0"):
            raise Failed(f"the database connection {name} was not found (or is no database connection): set GEMINUS_AUTH0_CONNECTION")
        conn_id = str(db[0].get("id")) if db else "<connection id>"
        full = self.a0("connections", "show", conn_id, quiet=True)
        if not self.dry:
            options = full.get("options") if isinstance(full, dict) else None
            if not isinstance(options, dict) or self.a0_holds_dropped(options):
                raise Failed(f"the options of {name} hold settings this script does not keep (custom database scripts or their "
                             "settings): turn sign-up off in the dashboard (docs/runbooks/auth0.md §6)")
            if options.get("disable_signup") is not True:
                body = {"options": dict(options, disable_signup=True)}
                if isinstance(full.get("display_name"), str):
                    body["display_name"] = full["display_name"]
                self.a0("connections", "update", conn_id, "--data", "@-", stdin=json.dumps(body))
                self.say(f"   sign-up on {name}: turned off")
            else:
                self.say(f"   sign-up on {name}: off already")
            again = self.a0("connections", "show", conn_id, quiet=True)
            if not (isinstance(again, dict) and isinstance(again.get("options"), dict) and again["options"].get("disable_signup") is True):
                raise Failed(f"sign-up on {name} is still on after the update: turn it off in the dashboard (docs/runbooks/auth0.md §6)")
        else:
            self.a0("connections", "update", conn_id, "--data", "@-", stdin="<its options, with disable_signup true>")
        for c in conns or [{"id": conn_id, "name": name}]:
            cid = str(c.get("id"))
            on = {str(x.get("client_id")) for x in self.a0("connections", "enabled-clients", "show", cid, quiet=True) or []
                  if isinstance(x, dict)}
            if (cid == conn_id) != (client_id in on):
                status = cid == conn_id
                self.a0("connections", "enabled-clients", "update", cid, "--data", "@-",
                        stdin=json.dumps([{"client_id": client_id, "status": status}]))
                self.say(f"   connection {c.get('name')}: the application {'enabled' if status else 'disabled'}")
        if not self.dry:
            for c in conns:
                cid = str(c.get("id"))
                on = {str(x.get("client_id")) for x in self.a0("connections", "enabled-clients", "show", cid, quiet=True) or []
                      if isinstance(x, dict)}
                if (cid == conn_id) != (client_id in on):
                    raise Failed(f"the application is {'not ' if cid == conn_id else ''}enabled on the connection {c.get('name')}")
            self.say(f"   the application signs users in through {name} only")

    def a0_judge(self, conn: str, reveal: list) -> str:
        """The judge's account (H-035 (2)) in the database connection, found by
        its e-mail or created with a new password (sent on stdin; shown once at
        the end of the run, by main); an existing account keeps its password
        unless GEMINUS_JUDGE_RESET_PASSWORD=1. Returns its user_id."""
        email = self.cfg["GEMINUS_JUDGE_EMAIL"]
        users = [u for u in self.a0("users", "search-by-email", email) or [] if isinstance(u, dict)]
        mine = [u for u in users if conn in {i.get("connection") for i in u.get("identities", []) if isinstance(i, dict)}]
        if len(mine) > 1:
            raise Failed(f"Auth0 has {len(mine)} users {email} in {conn}")
        reset = os.environ.get("GEMINUS_JUDGE_RESET_PASSWORD") == "1"
        if self.dry:
            self.a0("users", "create", "--data", "@-", stdin="<the judge's account, a new password>", sensitive=True)
            self.say("   (a found account keeps its password unless GEMINUS_JUDGE_RESET_PASSWORD=1)")
            return "<the judge's user_id>"
        if not mine or reset:
            pw = judge_password()
            self.secret(pw)
            if not mine:
                made = self.a0("users", "create", "--data", "@-", sensitive=True, stdin=json.dumps(
                    {"connection": conn, "email": email, "password": pw, "email_verified": True, "verify_email": False, "name": JUDGE_NAME}))
                uid = made.get("user_id") if isinstance(made, dict) else None
                self.say(f"   the judge's account {email}: created (its password is shown once, at the end of the run)")
            else:
                uid = mine[0].get("user_id")
                self.a0("users", "update", str(uid), "--data", "@-", sensitive=True, stdin=json.dumps({"password": pw, "connection": conn}))
                self.say(f"   the judge's account {email}: a new password (shown once, at the end of the run)")
            reveal.append(pw)
        else:
            uid = mine[0].get("user_id")
            self.say(f"   the judge's account {email}: exists; its password is unchanged and not shown "
                     "(GEMINUS_JUDGE_RESET_PASSWORD=1 sets and shows a new one)")
        if not isinstance(uid, str) or not re.fullmatch(r"[\x21-\x7e]{1,255}", uid):
            raise Failed("the Auth0 CLI printed no usable user_id for the judge's account")
        return uid

    def a0_secret_to_web(self, client_id: str, created: bool) -> None:
        """The client secret from `auth0 apps show --reveal-secrets` (only in this
        process: a0_receive puts it in a list of this call's own), only on stdin
        to `railway variable set BFF_OIDC_CLIENT_SECRET --stdin` on web, then
        sealed. Read only when web lacks it or has another application's; one
        there but not sealed (Railway refused before) is sealed now (review L1)."""
        have = self.variables_of("web", plain={"BFF_OIDC_CLIENT_ID": re.compile(r"[A-Za-z0-9_-]{1,128}")})
        if not created and "BFF_OIDC_CLIENT_SECRET" in have and have.get("BFF_OIDC_CLIENT_ID") == client_id:
            if have["BFF_OIDC_CLIENT_SECRET"] is Seen.SEALED:
                self.say("   web has this application's client secret already, sealed (not read again)")
            else:
                self.say("   web has this application's client secret already (not read again), not sealed: sealing it")
                self.a0_seal_secret()
            return
        box = {"client_secret": []}
        try:
            self.a0("apps", "show", client_id, "--reveal-secrets", capture=box, sensitive=True)
            got = box["client_secret"]
            if not self.dry and (len(got) != 1 or not re.fullmatch(r"[\x21-\x7e]{1,512}", got[0])):
                raise Failed("`auth0 apps show --reveal-secrets` printed no usable client secret (not shown)")
            self.set_from_stdin("web", "BFF_OIDC_CLIENT_SECRET", got[0] if got else "<the client secret>")
        finally:
            box["client_secret"].clear()
        self.say("   web's BFF_OIDC_CLIENT_SECRET: set from the Auth0 CLI on stdin (never printed, never in a file)")
        self.a0_seal_secret()

    def a0_seal_secret(self) -> None:
        """Seals web's client secret; a refusal fails the run at verification (L1), and a rerun seals it."""
        if self.seal_now([("web", "BFF_OIDC_CLIENT_SECRET")], "T-057 deploy.sh: seal the Auth0 client secret"):
            self.say("   web's BFF_OIDC_CLIENT_SECRET: sealed")
        else:
            self.late.append("web's BFF_OIDC_CLIENT_SECRET is set but Railway did not seal it: rerun deploy/railway/deploy.sh --apply "
                             "(it seals it without reading it again), or seal it in Railway's UI (web → Variables → "
                             "BFF_OIDC_CLIENT_SECRET's 3-dot menu → Seal)")

    def seal_now(self, pairs: list, message: str) -> bool:
        configs = []
        for s, name in pairs:
            configs += ["--service-config", s, f"variables.{name}.isSealed", "true"]
        self.run(["railway", "environment", "edit", *self.env(), *configs, "--message", message, "--json"], check=False)
        if self.code != 0:
            self.say(f"   note: the CLI did not seal {', '.join(n for _, n in pairs)}: seal it in Railway's UI (the service → Variables → "
                     "the variable's 3-dot menu → Seal)")
        return self.code == 0

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
            value = "k1:" + base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("=")
        else:
            value = secrets.token_hex(32)
        self.secret(value)
        return value

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
                if src in ("owner", "auth0"):
                    continue  # the owner's, or the Auth0 step's (2b): never set here; its value was dropped on receipt
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

    @cleanup
    def proxy_close(self) -> None:
        if not self.proxy_created:
            return
        self.say("   removing this run's TCP proxy")
        self.rw("tcp-proxy", "delete", self.proxy_created, "--yes", "--service", self.spec["database"], *self.env(), "--json", check=False)
        if self.code != 0:
            self.proxy_problems.append(f"could not remove this run's TCP proxy {self.proxy_created} (railway exit {self.code}): "
                                       f"remove it with `{self.proxy_delete_cmd(self.proxy_created)}`")

    @cleanup
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
                # The Auth0 step seals the client secret as soon as it sets it (2b), whatever GEMINUS_RAILWAY_SEAL
                # says: no other variable refers to it.
                if e.get("secret") and e.get("source") not in ("owner", "auth0"):
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
        self.say("6. seed (one synthetic organization" + (", the judge's account an editor of it)" if self.judge_subject else ")"))
        url = "postgresql://geminus_provisioner:%s@%s:%s/%s?sslmode=require" % (
            urllib.parse.quote(provisioner, safe=""), admin.get("PGHOST", "<host>"), admin.get("PGPORT", "<port>"), admin.get("PGDATABASE", "<db>"))
        self.secret(url)
        serving = self.variables_of("worker", plain={"WORKER_ORGANIZATION_IDS": ORG_LIST}).get("WORKER_ORGANIZATION_IDS")
        if serving:
            self.say("   the worker already serves an organization: none added")
            self.judge_check(admin, url, None, serving.split(",") if isinstance(serving, str) else [])
            return
        member = ["-member-issuer", self.cfg["GEMINUS_AUTH0_ISSUER"], "-member-subject", self.judge_subject, "-member-role", JUDGE_ROLE] \
            if self.judge_subject else []
        out = self.run(["go", "run", "./cmd/provision-tenant", "organization", "-name", self.cfg["GEMINUS_SEED_ORG_NAME"],
                        "-owner-issuer", self.cfg["GEMINUS_AUTH0_ISSUER"], "-owner-subject", self.cfg["GEMINUS_SEED_OWNER_SUBJECT"], *member],
                       env={"GEMINUS_PROVISION_DATABASE_URL": url})
        org = "<organization_id>" if self.dry else (re.search(r'"organization_id":"(org_[0-9A-Z]{26})"', out) or [None, None])[1]
        if not org:
            raise Failed("cmd/provision-tenant printed no organization")
        self.say(f"   organization {org}")
        self.rw("variable", "set", f"WORKER_ORGANIZATION_IDS={org}", "--service", "worker", *self.env(), "--skip-deploys", "--json")
        self.judge_check(admin, url, org, [org])

    def judge_check(self, admin: dict, url: str, seeded, demo: list) -> None:
        """The judge's memberships (H-035 (2)), listed by the database's
        administrator: exactly one, in the demo organization, as an editor. A
        demo organization seeded before the account existed cannot get it from
        provisioning (migration 0004 lets it add members only to an
        organization it creates): the account is made a user, and the end of the
        run says how an owner adds it. Anything else fails the run at the end."""
        if not self.judge_subject:
            if self.auth0_cli or self.dry:
                return
            self.say("   no judge's account (GEMINUS_JUDGE_SUBJECT is not set): none added")
            return
        self.say(f"   the judge's account: an editor of the demo organization, and a member of nothing else")
        rows = self.psql(admin, "-At", "-v", "issuer=" + self.cfg["GEMINUS_AUTH0_ISSUER"], "-v", "subject=" + self.judge_subject,
                         "-f", "-", stdin=JUDGE_SQL)
        if self.dry:
            return
        got = [line.split() for line in rows.splitlines() if line.strip()]
        if len(got) == 1 and len(got[0]) == 2 and got[0][0] in demo and got[0][1] == JUDGE_ROLE:
            self.say(f"   the judge's account {self.judge_subject}: {JUDGE_ROLE} of {got[0][0]}, nothing else")
            return
        if not got and seeded is None:
            out = self.run(["go", "run", "./cmd/provision-tenant", "user", "-issuer", self.cfg["GEMINUS_AUTH0_ISSUER"],
                            "-subject", self.judge_subject], env={"GEMINUS_PROVISION_DATABASE_URL": url})
            uid = (re.search(r'"user_id":"(usr_[0-9A-Z]{26})"', out) or [None, "<its user ID>"])[1]
            self.late.append(f"the judge's account {self.judge_subject} ({uid}) has no membership: the demo organization "
                             f"{', '.join(demo) or '(the worker serves)'} was seeded before it existed, and provisioning adds members only to an "
                             f"organization it creates (migration 0004); an owner of it adds the account as an editor (POST "
                             f"/v1/organizations/<organization>/memberships with {{\"user_id\": \"{uid}\", \"role\": \"{JUDGE_ROLE}\"}}, "
                             "ADR-023; docs/runbooks/deploy.md §7.8)")
            return
        self.late.append(f"the judge's account {self.judge_subject} has the memberships {[' '.join(r) for r in got]}, want only "
                         f"{JUDGE_ROLE} of {' or '.join(demo) or 'the demo organization'}: an owner removes the others (ADR-023)")

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
                src = e.get("source")
                if src not in ("owner", "auth0"):
                    continue
                if e.get("required") and name not in have:
                    problems.append(f"the Auth0 step did not set {name} on {s} (2b; or set it as GEMINUS_AUTH0_SETUP=manual says)"
                                    if src == "auth0" and self.auth0_cli else f"the owner has not set {name} on {s} (Railway → {s} → Variables)")
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
        problems += self.late
        if web:
            self.say(f"   Auth0 Application: Allowed Callback URL https://{web}/auth/callback, Allowed Logout URL https://{web}/"
                     + (" (set by the Auth0 step)" if self.auth0_cli else ""))
        if problems:
            raise Failed("verification:\n  - " + "\n  - ".join(problems))

    # -- the whole run -------------------------------------------------------------

    def steps(self, reveal: list) -> None:
        self.preflight()
        sha = self.run(["git", "rev-parse", "--short", "HEAD"], quiet=True, local=True).strip() or "<commit>"
        if self.dry:
            self.say("dry run: nothing remote is called; the steps and commands of --apply follow")
        self.project()
        self.resources()
        if self.auth0_cli:
            self.auth0(reveal)
        else:
            self.say("2b. Auth0: GEMINUS_AUTH0_SETUP=manual, the owner's (docs/runbooks/auth0.md §6)")
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
        exit), it ends by listing Postgres's TCP proxies (M1), unless verify did,
        and by showing the judge's new password, if this run made one (once, on
        stderr: H-035 (2)). The password is in no attribute and no return value:
        it lives in this call's own list until it is shown."""
        reveal: list = []
        try:
            self.run_steps(reveal)
        finally:
            self.reveal(reveal)

    def run_steps(self, reveal: list) -> None:
        try:
            self.steps(reveal)
        except Failed as e:
            extra = [p for p in self.check_proxies() + self.late if p not in str(e)]  # verify's message has its own
            if extra:
                raise Failed(f"{e}\n  - " + "\n  - ".join(extra)) from None
            raise
        except BaseException:
            for p in self.check_proxies() + self.late:
                self.say(f"deploy: {p}")
            raise
        left = self.check_proxies()
        if left:
            raise Failed("the deployment's steps ran, but:\n  - " + "\n  - ".join(left))
        self.say(("dry run: done, nothing was called" if self.dry else "deploy: done") +
                 ". Variables that are the owner's: docs/runbooks/deploy.md §7.3.")

    def reveal(self, shown: list) -> None:
        """Shows the judge's new password once, on stderr only (a log of the
        run's stdout never holds it), then forgets it."""
        if not shown:
            return
        try:
            sys.stderr.write("\ndeploy: the judge's demo account (H-035 (2)), shown this once and nowhere else (not in a log, not in a "
                             "file); put it in the submission text now:\n"
                             f"  email:    {self.cfg['GEMINUS_JUDGE_EMAIL']}\n  password: {shown[-1]}\n\n")
            sys.stderr.flush()
        except OSError:  # stderr is gone too: the password is lost (GEMINUS_JUDGE_RESET_PASSWORD=1 sets a new one)
            self.out_lost = True
        finally:
            shown.clear()


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
        run.tell(f"deploy: FAIL: {e}")
        return 1
    except OutputLost:  # the cleanups ran; nothing can be shown
        return EXIT_OUTPUT_LOST
    except SystemExit as e:  # exit_on_signal: the cleanup ran
        run.tell(f"deploy: INTERRUPTED (exit {e.code}): rerun deploy/railway/deploy.sh --apply")
        return int(e.code) if isinstance(e.code, int) else 1
    except KeyboardInterrupt:  # SIGINT: the cleanup ran too
        run.tell("deploy: INTERRUPTED (SIGINT): rerun deploy/railway/deploy.sh --apply")
        return 130
    return EXIT_OUTPUT_LOST if run.out_lost else 0


if __name__ == "__main__":
    sys.exit(main())
