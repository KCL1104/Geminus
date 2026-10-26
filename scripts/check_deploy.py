#!/usr/bin/env python3
"""T-039: static checks of the container and deployment files, which run
without Docker (make spec-deploy, part of make spec-check):

- Dockerfile.api, Dockerfile.worker, apps/web/Dockerfile: every base image
  pinned by digest (no floating tag), the final stage's USER is not root,
  no ARG (a platform could pass a secret into the build) and no ENV or LABEL
  naming a credential, a STOPSIGNAL, a HEALTHCHECK where the image serves
  HTTP;
- .dockerignore keeps git, env files, installed packages and build output
  out of the build context;
- deploy/local/compose.yaml is fenced as local-only, its images are pinned
  or the local build tags, and every credential in it is a ${...} variable
  of the smoke run, except the test provider's published test client;
- deploy/railway/*.railway.json are Railway config-as-code files for a
  Dockerfile that exists, and nothing under deploy/railway/ mentions the
  test provider, loopback or development settings;
- deploy/railway/variables.json names only variables the services read,
  gives every variable each service requires, holds no secret value, keeps
  the AI keys off the API (T-053 I5) and the database off the web, and never
  selects the fake provider or the filesystem adapter;
- docs/runbooks/deploy.md names every variable the code reads.

It checks the files, not a running deployment (NOT a container scan).
"""
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
    ignore = set((ROOT / ".dockerignore").read_text(encoding="utf-8").split())
    for must in (".git", ".env", ".env.*", "**/node_modules", "**/.next", "checks", "sources", "bin"):
        if must not in ignore:
            fail(f".dockerignore: {must} is not excluded")
    return out


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
PENDING = {"GEMINUS_META_API_KEY"}  # T-055 adds it (worker only)
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
    for p in configs:
        c = json.loads(p.read_text(encoding="utf-8"))
        if c.get("$schema") != "https://railway.com/railway.schema.json" or c.get("build", {}).get("builder") != "DOCKERFILE":
            fail(f"{p.name}: not a Dockerfile config-as-code file")
        if not (ROOT / c["build"]["dockerfilePath"]).is_file():
            fail(f"{p.name}: {c['build']['dockerfilePath']} does not exist")
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
            if source is not None and not (source in ("generated", "owner") or re.fullmatch(r"bucket:(accessKeyId|secretAccessKey)", source)):
                fail(f"variables.json: {svc}: {name}: unknown source {source!r}")
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
    ai_keys = {"GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "GEMINUS_META_API_KEY", "GEMINUS_AI_LIVE"}
    if ai_keys - set(v["services"]["api"]["forbidden"]):
        fail("variables.json: the API must forbid every AI key and the live switch (T-053 I5)")
    return {"configs": [p.name for p in configs], "services": summary}


def runbook(known: set) -> dict:
    text = (ROOT / "docs/runbooks/deploy.md").read_text(encoding="utf-8")
    missing = sorted(n for n in known | PENDING if not re.search(r"(?<![A-Z0-9_])" + re.escape(n) + r"(?![A-Z0-9_])", text))
    if missing:
        fail(f"docs/runbooks/deploy.md does not name {missing}")
    return {"variables_named": len(known | PENDING)}


def main() -> dict:
    known = code_variables()
    report = {"status": "PASS_STATIC_DEPLOY_CHECKS", "dockerfiles": dockerfiles(), "compose": compose(),
              "railway": railway(known), "runbook": runbook(known), "code_variables": sorted(known),
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
