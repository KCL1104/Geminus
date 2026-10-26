#!/usr/bin/env python3
"""T-039: static checks of the container and deployment files, which run
without Docker (make spec-deploy, part of make spec-check):

- Dockerfile.api, Dockerfile.worker, apps/web/Dockerfile: every base image
  pinned by digest (no floating tag), the final stage's USER is not root,
  no ARG (a platform could pass a secret into the build) and no ENV or LABEL
  naming a credential, a STOPSIGNAL, a HEALTHCHECK where the image serves
  HTTP;
- .dockerignore keeps git, env files (in any directory: `**/.env`,
  `**/.env.*`, with `!**/.env.example` the only exception; checked against
  sample paths with Docker's matching rules), installed packages and build
  output out of the build context;
- deploy/local/compose.yaml is fenced as local-only, its images are pinned
  or the local build tags, and every credential in it is a ${...} variable
  of the smoke run, except the test provider's published test client;
- deploy/railway/*.railway.json are Railway config-as-code files for a
  Dockerfile that exists, and nothing under deploy/railway/ mentions the
  test provider, loopback or development settings; only api and web have a
  public domain (on their PORT), api's healthcheck is /readyz, web's /, and
  worker and migrate have none;
- deploy/railway/variables.json names only variables the services read,
  gives every variable each service requires, holds no secret value, keeps
  every AI key variable the API refuses to start with off the API (the list
  in internal/config/apiai.go) and the live adapters' keys off the web, gives
  the API the worker's seven AI settings, declared alike (the proposals'
  four, T-053 I5, and the photo path's three, D-124), and no other, keeps
  the database off the web, and never selects the fake provider or the
  filesystem adapter;
- the Auth0 CLI of the deployer's Auth0 step (T-057, H-034) is pinned alike
  in deploy.sh and railway_deploy.py (version, the binary's SHA-256) and
  --install-auth0-cli checks the archive's and the binary's SHA-256 before it
  installs; only the web's BFF_OIDC_CLIENT_SECRET comes from that step;
- docs/runbooks/deploy.md names every variable the code reads.

It checks the files, not a running deployment (NOT a container scan).
"""
import fnmatch
import json
import posixpath
import re
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
SECRETISH = re.compile(r"PASSWORD|SECRET|SESSION_KEYS|API_KEY|ACCESS_KEY|DATABASE_URL", re.I)
DIGEST = re.compile(r"@sha256:[0-9a-f]{64}$")


def fail(msg: str) -> None:
    raise AssertionError(msg)


def dockerfiles() -> dict:
    out = {}
    for rel, http in (("Dockerfile.api", True), ("Dockerfile.worker", False), ("apps/web/Dockerfile", True)):
        text = (ROOT / rel).read_text(encoding="utf-8")
        # Join continuation lines; drop comments.
        lines = [l for l in re.sub(r"\\\n", " ", text).splitlines() if l.strip() and not l.lstrip().startswith("#")]
        instr = [(l.split(None, 1)[0].upper(), l.split(None, 1)[1] if len(l.split(None, 1)) > 1 else "") for l in lines]
        froms = [a for i, a in instr if i == "FROM"]
        if not froms:
            fail(f"{rel}: no FROM")
        stages = set()
        for a in froms:
            parts = a.split()
            image = parts[0]
            if image not in stages and not DIGEST.search(image):
                fail(f"{rel}: base image {image} is not pinned by digest")
            if ":latest" in image:
                fail(f"{rel}: floating tag in {image}")
            if len(parts) == 3 and parts[1].upper() == "AS":
                stages.add(parts[2])
        last = max(i for i, (k, _) in enumerate(instr) if k == "FROM")
        users = [a for k, a in instr[last:] if k == "USER"]
        if not users or users[-1].split(":")[0] in ("root", "0"):
            fail(f"{rel}: the final stage must run as a non-root USER")
        for k, a in instr:
            if k == "ARG":
                fail(f"{rel}: ARG {a}: no build argument (a platform could pass a secret into the build)")
            if k in ("ENV", "LABEL") and SECRETISH.search(a):
                fail(f"{rel}: {k} {a} names a credential")
            if k == "ADD":
                fail(f"{rel}: ADD (use COPY)")
            if k == "COPY" and re.search(r"(^|\s)\.env", a):
                fail(f"{rel}: copies an env file")
        kinds = {k for k, _ in instr[last:]}
        if "STOPSIGNAL" not in kinds:
            fail(f"{rel}: no STOPSIGNAL")
        if http and "HEALTHCHECK" not in kinds:
            fail(f"{rel}: no HEALTHCHECK")
        out[rel] = {"stages": len(froms), "user": users[-1], "healthcheck": "HEALTHCHECK" in kinds}
    lines = [l.strip() for l in (ROOT / ".dockerignore").read_text(encoding="utf-8").splitlines()]
    patterns = [l for l in lines if l and not l.startswith("#")]
    for must in (".git", ".env", ".env.*", "**/.env", "**/.env.*", "!**/.env.example", "**/node_modules", "**/.next",
                 "checks", "sources", "bin"):
        if must not in patterns:
            fail(f".dockerignore: {must} is missing" if must.startswith("!") else f".dockerignore: {must} is not excluded")
    for p in patterns:
        if p.startswith("!") and ".env" in p and p != "!**/.env.example":
            fail(f".dockerignore: {p} brings an env file back into the build context")
    # Docker matches each path against every pattern and the last match decides
    # (review round 1, L3: an env file in apps/web/ reached the web image).
    for path, excluded in ((".env", True), (".env.local", True), ("apps/web/.env", True), ("apps/web/.env.local", True),
                           ("apps/web/.env.production", True), ("cmd/api/.env", True), ("apps/web/.env.example", False),
                           (".env.example", False), ("apps/web/app/page.tsx", False), ("cmd/api/main.go", False)):
        if dockerignored(patterns, path) != excluded:
            fail(f".dockerignore: {path} would be {'in' if excluded else 'out of'} the build context")
    return out


def docker_match(pattern: str, path: str) -> bool:
    """A .dockerignore pattern against a path relative to the context, as
    Docker matches them: segments as Go's filepath.Match, `**` any number of
    directories (none too), and a match of a parent directory excludes
    everything under it."""
    pat = [s for s in posixpath.normpath(pattern.lstrip("/")).split("/") if s]
    parts = path.split("/")

    def m(i: int, j: int) -> bool:
        if i == len(pat):
            return 0 < j <= len(parts)
        if pat[i] == "**":
            return any(m(i + 1, k) for k in range(j, len(parts) + 1))
        return j < len(parts) and fnmatch.fnmatchcase(parts[j], pat[i]) and m(i + 1, j + 1)
    return m(0, 0)


def dockerignored(patterns: list, path: str) -> bool:
    excluded = False
    for p in patterns:
        negated = p.startswith("!")
        if docker_match(p[1:] if negated else p, path):
            excluded = not negated
    return excluded


def compose() -> dict:
    path = ROOT / "deploy/local/compose.yaml"
    text = path.read_text(encoding="utf-8")
    if not text.startswith("# LOCAL STAGING SMOKE ONLY (T-039). NEVER A PUBLIC DEPLOYMENT."):
        fail("deploy/local/compose.yaml: the local-only fence is not its first line")
    doc = yaml.safe_load(text)
    fake = (ROOT / "apps/web-e2e/fake/servers.mjs").read_text(encoding="utf-8")
    test_values = set(re.findall(r'const (?:CLIENT_SECRET|CLIENT_ID|AUDIENCE) = "([^"]+)"', fake))
    if len(test_values) != 3:
        fail("apps/web-e2e/fake/servers.mjs: the test client's constants were not found")
    for name, svc in doc["services"].items():
        image = svc.get("image", "")
        if not (DIGEST.search(image) or re.fullmatch(r"geminus-(api|worker|web):local", image)):
            fail(f"compose: {name}: image {image} is neither pinned nor a local build")
        for var, value in (svc.get("environment") or {}).items():
            value = str(value)
            if SECRETISH.search(var) and "${" not in value and value not in test_values:
                fail(f"compose: {name}: {var} has a literal value")
            if re.search(r"://[^/:@]+:[^@${]+@", value):
                fail(f"compose: {name}: {var} has a literal password in a URL")
        # A long-running service that replaces its image's command would inherit
        # the image's HEALTHCHECK, which probes the replaced server (the web
        # image's probes 127.0.0.1:$PORT), so `up --wait` never sees it healthy.
        if "command" in svc and "steps" not in (svc.get("profiles") or []) and "healthcheck" not in svc:
            fail(f"compose: {name}: replaces its image's command without a healthcheck of its own")
    if "oidc-test" not in doc["services"]:
        fail("compose: the test provider service is expected only here")
    # Every file the test provider reads next to itself must be mounted where
    # it resolves in the container (servers.mjs runs as /fake/servers.mjs).
    mounts = [str(v).split(":")[1] for v in doc["services"]["oidc-test"].get("volumes") or []]
    for rel in re.findall(r'new URL\("([^"]+)",\s*import\.meta\.url\)', fake):
        target = posixpath.normpath(posixpath.join("/fake", rel))
        if not any(target == m or target.startswith(m.rstrip("/") + "/") for m in mounts):
            fail(f"compose: oidc-test: servers.mjs reads {target}, which no volume mounts")
    return {"services": sorted(doc["services"])}


def code_variables() -> set:
    names = set()
    for p in sorted((ROOT / "internal/config").glob("*.go")):
        if p.name.endswith("_test.go"):
            continue
        names |= set(re.findall(r'"([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)"', p.read_text(encoding="utf-8")))
    for p in sorted((ROOT / "cmd").glob("*/*.go")):
        if not p.name.endswith("_test.go"):
            names |= set(re.findall(r'getenv\("([A-Z][A-Z0-9_]+)"\)', p.read_text(encoding="utf-8")))
    for p in sorted((ROOT / "apps/web/lib").rglob("*.ts")):
        t = p.read_text(encoding="utf-8")
        names |= set(re.findall(r'"((?:BFF|SESSION)_[A-Z0-9_]+)"', t))
        names |= set(re.findall(r"(?:process\.env|env)\.([A-Z][A-Z0-9_]+)", t))
    # Prefixes and names that are not variables (a key's prefix, an algorithm).
    names -= {"GEMINUS_ANTHROPIC_API_KEY"}
    return {n for n in names if not re.fullmatch(r"(ES|PS|RS)\d+", n)}


PLATFORM = {"PORT", "HOSTNAME", "NODE_ENV", "NEXT_TELEMETRY_DISABLED"}
# The inputs of deploy/railway/deploy.sh that variables.json may name as {{INPUT}}.
INPUTS = {"GEMINUS_AUTH0_ISSUER", "GEMINUS_AUTH0_AUDIENCE", "GEMINUS_AUTH0_CLIENT_ID"}
PENDING: set = set()  # none: T-055 added GEMINUS_MUSE_API_KEY (worker only), which internal/config reads
REQUIRED = {
    "migrate": {"GEMINUS_MIGRATE_DATABASE_URL"},
    "api": {"APP_ENV", "DATABASE_URL", "AUTH_OIDC_ISSUER", "AUTH_OIDC_AUDIENCE", "AUTH_OIDC_JWKS_URL", "AUTH_OIDC_REQUIRE_AT_JWT",
            "PORT", "API_LISTEN_ADDR", "OBJECT_STORAGE_PROVIDER"},
    "worker": {"APP_ENV", "DATABASE_URL", "WORKER_ID", "WORKER_ORGANIZATION_IDS", "OBJECT_STORAGE_PROVIDER"},
    "web": {"BFF_APP_ORIGIN", "BFF_API_URL", "BFF_OIDC_ISSUER", "BFF_OIDC_AUTHORIZATION_URL", "BFF_OIDC_TOKEN_URL",
            "BFF_OIDC_REVOCATION_URL", "BFF_OIDC_END_SESSION_URL", "BFF_OIDC_JWKS_URL", "BFF_OIDC_CLIENT_ID",
            "BFF_OIDC_CLIENT_SECRET", "BFF_OIDC_AUDIENCE", "SESSION_KEYS", "PORT"},
}


def railway(known: set) -> dict:
    d = ROOT / "deploy/railway"
    configs = sorted(d.glob("*.railway.json"))
    loaded = {}
    for p in configs:
        c = json.loads(p.read_text(encoding="utf-8"))
        if c.get("$schema") != "https://railway.com/railway.schema.json" or c.get("build", {}).get("builder") != "DOCKERFILE":
            fail(f"{p.name}: not a Dockerfile config-as-code file")
        if not (ROOT / c["build"]["dockerfilePath"]).is_file():
            fail(f"{p.name}: {c['build']['dockerfilePath']} does not exist")
        loaded[f"deploy/railway/{p.name}"] = c
    for p in sorted(x for x in d.iterdir() if x.is_file()):
        t = p.read_text(encoding="utf-8")
        for bad in ("web-e2e", "servers.mjs", "127.0.0.1", "localhost", "development", "fake", "filesystem", "e2e-client"):
            if bad in t:
                fail(f"deploy/railway/{p.name} mentions {bad!r}: the test setup never reaches the public deployment")
    v = json.loads((d / "variables.json").read_text(encoding="utf-8"))
    passwords = {f"GEMINUS_{x}_DB_PASSWORD" for x in ("MIGRATOR", "API", "WORKER", "PROVISIONER")}
    allowed = known | PLATFORM | PENDING | passwords
    summary = {}
    for svc, spec in v["services"].items():
        if not (ROOT / spec["config"]).is_file():
            fail(f"variables.json: {svc}: {spec['config']} does not exist")
        names = set(spec["variables"])
        unknown = names - allowed
        if unknown:
            fail(f"variables.json: {svc}: {sorted(unknown)} are read by no service")
        missing = REQUIRED[svc] - names
        if missing:
            fail(f"variables.json: {svc}: {sorted(missing)} are required")
        clash = names & set(spec.get("forbidden", []))
        if clash:
            fail(f"variables.json: {svc}: {sorted(clash)} must not be set on this service")
        for name, e in spec["variables"].items():
            value, source = e.get("value"), e.get("source")
            if value is None and source is None:
                fail(f"variables.json: {svc}: {name} has neither a value nor a source")
            if value is not None and source is not None:
                fail(f"variables.json: {svc}: {name} has both a value and a source")
            if e.get("secret") and value is not None:
                fail(f"variables.json: {svc}: {name} is secret and has a value")
            if source is not None and not (source in ("generated", "owner", "auth0") or re.fullmatch(r"bucket:(accessKeyId|secretAccessKey)", source)):
                fail(f"variables.json: {svc}: {name}: unknown source {source!r}")
            # T-057, H-034: the Auth0 step writes one variable, the web's client secret.
            if source == "auth0" and (svc, name) != ("web", "BFF_OIDC_CLIENT_SECRET"):
                fail(f"variables.json: {svc}: {name}: only the web's BFF_OIDC_CLIENT_SECRET comes from the Auth0 step")
            if source is not None and source != "owner" and not e.get("secret"):
                fail(f"variables.json: {svc}: {name}: a generated or copied credential must be secret")
            if value is not None:
                for ph in re.findall(r"(?<!\$)\{\{([^}]+)\}\}", value):
                    if ph not in INPUTS and not re.fullmatch(r"bucket:(endpoint|bucketName|region|urlStyle)|seed:organization_id", ph):
                        fail(f"variables.json: {svc}: {name}: unknown placeholder {{{{{ph}}}}}")
                if SECRETISH.search(name) and not re.fullmatch(r"(?:[^${]|\$\{\{[^}]+\}\}|\{\{(?:GEMINUS_[A-Z0-9_]+|bucket:[a-zA-Z]+)\}\})*", value):
                    fail(f"variables.json: {svc}: {name} must be built from references")
                if re.search(r"://[^/:@]+:(?!\$\{\{)[^@]+@", value):
                    fail(f"variables.json: {svc}: {name} has a literal password in a URL")
        settings = {n: e.get("value") for n, e in spec["variables"].items()}
        if settings.get("APP_ENV") not in (None, "production"):
            fail(f"variables.json: {svc}: APP_ENV must be production")
        if settings.get("AI_PROVIDER") == "fake" or settings.get("OBJECT_STORAGE_PROVIDER") == "filesystem":
            fail(f"variables.json: {svc}: the fake provider and the filesystem adapter are for development and tests only")
        summary[svc] = {"variables": len(names), "secrets": sorted(n for n, e in spec["variables"].items() if e.get("secret"))}
    # T-053 I5: the API never holds an AI key (it refuses to start with one)
    # and decides whether it accepts proposals, and photos (D-124), from the
    # worker's AI settings that are not secret, declared alike on both (the
    # owner sets the same values).
    api, worker = v["services"]["api"], v["services"]["worker"]
    keys = api_key_variables()
    if keys - set(api["forbidden"]):
        fail(f"variables.json: the API must forbid every AI key variable it refuses to start with (T-053 I5): {sorted(keys - set(api['forbidden']))}")
    if LIVE_KEYS - set(v["services"]["web"]["forbidden"]):
        fail(f"variables.json: the web must forbid the live adapters' keys: {sorted(LIVE_KEYS - set(v['services']['web']['forbidden']))}")
    for name in API_AI_SETTINGS:
        a, w = api["variables"].get(name), worker["variables"].get(name)
        if a is None or w is None:
            fail(f"variables.json: {name} must be declared on the API and on the worker (T-053 I5)")
        if a.get("secret") or w.get("secret"):
            fail(f"variables.json: {name} is a setting, not a secret (T-053 I5)")
        if {k: x for k, x in a.items() if k != "later"} != {k: x for k, x in w.items() if k != "later"}:
            fail(f"variables.json: the API's {name} must be declared as the worker's (T-053 I5)")
    other = sorted(n for n in api["variables"] if re.match(r"(GEMINUS_)?AI_", n) and n not in API_AI_SETTINGS)
    if other:
        fail(f"variables.json: the API reads no other AI setting (T-053 I5): {other}")
    # Exposure (review round 1, L5): a public domain only where a server answers on
    # PORT, Railway's healthcheck where the image serves HTTP, none elsewhere.
    public = sorted(s for s, spec in v["services"].items() if "public_domain" in spec)
    if public != ["api", "web"]:
        fail(f"variables.json: public domains on {public}: only api and web have one (worker and migrate never)")
    health = {"api": "/readyz", "web": "/", "worker": None, "migrate": None}
    if set(v["services"]) != set(health):
        fail(f"variables.json: the services are {sorted(v['services'])}, want {sorted(health)}")
    for svc, want in health.items():
        spec = v["services"][svc]
        got = loaded.get(spec["config"], {}).get("deploy", {}).get("healthcheckPath")
        if got != want:
            fail(f"{spec['config']}: healthcheckPath {got!r}, want {want!r}")
        if "public_domain" in spec and str(spec["public_domain"].get("port")) != spec["variables"].get("PORT", {}).get("value"):
            fail(f"variables.json: {svc}: the public domain's port is not its PORT")
    return {"configs": [p.name for p in configs], "services": summary, "public_domains": public,
            "api_ai": {"settings": list(API_AI_SETTINGS), "refused_keys": sorted(keys)}}


# The API's AI settings (internal/config.LoadAPIAI), the worker's too: the
# proposals' (T-053 I5) and the photo path's (the T-056 follow-up, D-124).
API_AI_SETTINGS = ("AI_PROVIDER", "AI_POLICY_APPROVAL", "AI_MODEL", "GEMINUS_AI_LIVE",
                   "AI_VISION_PROVIDER", "AI_VISION_POLICY_APPROVAL", "AI_VISION_MODEL")
# The live adapters' keys (internal/config/ai.go), which only the worker holds.
LIVE_KEYS = {"GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "GEMINUS_MUSE_API_KEY"}


def api_key_variables() -> set:
    """The AI key variables the API refuses to start with: apiKeyVars in
    internal/config/apiai.go (T-053 I5), the live adapters' keys among them."""
    text = (ROOT / "internal/config/apiai.go").read_text(encoding="utf-8")
    m = re.search(r"^var apiKeyVars = \[\]string\{(.*?)^\}", text, re.S | re.M)
    names = set(re.findall(r'"([A-Z][A-Z0-9_]+)"', m.group(1))) if m else set()
    if not names or LIVE_KEYS - names:
        fail("internal/config/apiai.go: apiKeyVars (the AI key variables the API refuses) was not found, or lacks a live adapter's key")
    return names


def auth0_pin() -> dict:
    """T-057, H-034: the Auth0 CLI is pinned to one release in deploy.sh (the
    archive's SHA-256, from the release's checksums file, and the binary's) and
    in railway_deploy.py (the binary's, checked before the CLI is ever run),
    alike; the archive comes from that release's exact URL; the install checks
    both hashes before it installs."""
    sh = (ROOT / "deploy/railway/deploy.sh").read_text(encoding="utf-8")
    py = (ROOT / "deploy/railway/railway_deploy.py").read_text(encoding="utf-8")

    def one(text, pattern, what):
        found = re.findall(pattern, text, re.M)
        if len(found) != 1:
            fail(f"{what}: {len(found)} definitions")
        return found[0]
    version = one(sh, r"^AUTH0_CLI_VERSION=(\d+\.\d+\.\d+)$", "deploy.sh AUTH0_CLI_VERSION")
    archive = one(sh, r"^AUTH0_CLI_ARCHIVE_SHA256=([0-9a-f]{64})$", "deploy.sh AUTH0_CLI_ARCHIVE_SHA256")
    binary = one(sh, r"^AUTH0_CLI_SHA256=([0-9a-f]{64})$", "deploy.sh AUTH0_CLI_SHA256")
    if one(py, r'^AUTH0_CLI_VERSION = "(\d+\.\d+\.\d+)"$', "railway_deploy.py AUTH0_CLI_VERSION") != version or \
            one(py, r'^AUTH0_CLI_SHA256 = "([0-9a-f]{64})"$', "railway_deploy.py AUTH0_CLI_SHA256") != binary:
        fail("deploy.sh and railway_deploy.py pin different Auth0 CLI builds")
    url = one(sh, r'^AUTH0_CLI_URL="([^"]+)"$', "deploy.sh AUTH0_CLI_URL")
    if url != "https://github.com/auth0/auth0-cli/releases/download/v${AUTH0_CLI_VERSION}/auth0-cli_${AUTH0_CLI_VERSION}_Linux_x86_64.tar.gz":
        fail(f"deploy.sh: AUTH0_CLI_URL {url} is not the pinned release's Linux x86_64 archive")
    if 'echo "$AUTH0_CLI_ARCHIVE_SHA256  $tmp/auth0.tar.gz" | sha256sum -c --quiet -' not in sh or \
            'echo "$AUTH0_CLI_SHA256  $tmp/auth0" | sha256sum -c --quiet -' not in sh:
        fail("deploy.sh --install-auth0-cli does not check the archive's and the binary's SHA-256")
    if sh.index("sha256sum -c --quiet - || {", sh.index("--install-auth0-cli)")) > sh.index('install -m 0755 "$tmp/auth0"'):
        fail("deploy.sh --install-auth0-cli installs before it checks")
    return {"version": version, "archive_sha256": archive, "binary_sha256": binary}


def runbook(known: set) -> dict:
    text = (ROOT / "docs/runbooks/deploy.md").read_text(encoding="utf-8")
    missing = sorted(n for n in known | PENDING if not re.search(r"(?<![A-Z0-9_])" + re.escape(n) + r"(?![A-Z0-9_])", text))
    if missing:
        fail(f"docs/runbooks/deploy.md does not name {missing}")
    return {"variables_named": len(known | PENDING)}


def main() -> dict:
    known = code_variables()
    report = {"status": "PASS_STATIC_DEPLOY_CHECKS", "dockerfiles": dockerfiles(), "compose": compose(),
              "railway": railway(known), "auth0_cli": auth0_pin(), "runbook": runbook(known), "code_variables": sorted(known),
              "container_build": "NOT_RUN_HERE (make build-containers)", "container_scan": "NOT_RUN (no scanner chosen)"}
    (ROOT / "checks/deploy-check.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return report


if __name__ == "__main__":
    try:
        main()
    except AssertionError as exc:
        print(f"DEPLOY CHECK FAILED: {exc}", file=sys.stderr)
        sys.exit(1)
