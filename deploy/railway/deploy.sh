#!/usr/bin/env bash
# The hackathon deployment on Railway (T-039; H-031 Railway only; H-032 the
# coordinator applies it from a session that has the Railway token). The steps
# are deploy/railway/railway_deploy.py's (its docstring, docs/runbooks/deploy.md
# §7.7); this wrapper refuses a tracing shell, pins the CLI and runs it.
#
#   deploy/railway/deploy.sh --dry-run            check inputs, tools and files; print the plan; call nothing remote
#   deploy/railway/deploy.sh --apply              create or update the deployment
#   deploy/railway/deploy.sh --install-cli DIR    install the pinned Railway CLI 5.64.2 into DIR (Linux x86_64),
#                                                 checking the release archive's SHA-256
#
# Token (exactly as the Railway CLI reads it; docs.railway.com/guides/cli,
# 2026-10-26; the CLI's source at v5.64.2, src/config.rs): RAILWAY_TOKEN, a
# project token (the project and environment are the token's and must exist),
# or RAILWAY_API_TOKEN, an account or workspace token (the project is found by
# name or created). The token is read by the CLI from the environment only:
# never echoed, never an argument, never in a log, redacted if the CLI quotes
# it, and removed from the environment of every other program (psql, go).
#
# Inputs (not secret): GEMINUS_AUTH0_ISSUER, GEMINUS_AUTH0_AUDIENCE,
# GEMINUS_AUTH0_CLIENT_ID, GEMINUS_SEED_OWNER_SUBJECT,
# GEMINUS_RAILWAY_BUCKET_REGION (sjc, iad, ams or sin; fixed once created);
# optional GEMINUS_RAILWAY_PROJECT (geminus-demo), GEMINUS_RAILWAY_ENVIRONMENT
# (production), GEMINUS_RAILWAY_WORKSPACE, GEMINUS_SEED_ORG_NAME;
# GEMINUS_RAILWAY_SEAL=off only for the recovery of the runbook's §7.7;
# GEMINUS_KEEP_DB_PROXY=1 accepts a TCP proxy still open on Postgres at the end
# (without it, any open proxy, also one from before the run, is named with its
# removal command and the run exits 1).
#
# The owner fills in Railway's UI, never this script, and seals what is secret
# (H-036): BFF_OIDC_CLIENT_SECRET (web), and only to turn live AI on, the same
# AI_PROVIDER, AI_POLICY_APPROVAL, AI_MODEL and GEMINUS_AI_LIVE on the API and
# the worker, and for the photo path the same AI_VISION_PROVIDER,
# AI_VISION_POLICY_APPROVAL and AI_VISION_MODEL on both (the API holds no
# key: T-053 I5, D-124) and the worker's keys (GEMINUS_ANTHROPIC_API_KEY_A
# and _B, or the Meta key GEMINUS_MUSE_API_KEY).
# The CLI's variable listing returns unsealed values: the script drops the
# owner's on receipt and names the unsealed secrets. Live AI stays off until
# then (AI_PROVIDER=disabled).
#
# Needs: python3, git, psql 15 or later, go (cmd/provision-tenant), the pinned
# Railway CLI. Exit codes: 0 done, 1 a step failed or a TCP proxy is still open
# (the message says which), 2 usage or xtrace on, 128+N interrupted by signal N
# (SIGTERM, SIGHUP: this run's TCP proxy is removed first).
case "$-" in *x*) set +x; echo "deploy.sh: refusing to run with xtrace on (bash -x, set -x or SHELLOPTS)" >&2; exit 2 ;; esac
set -euo pipefail
umask 077
cd "$(dirname "$0")/../.."

CLI_VERSION=5.64.2
# SHA-256 of https://github.com/railwayapp/cli/releases/download/v5.64.2/railway-v5.64.2-x86_64-unknown-linux-musl.tar.gz,
# computed 2026-10-26 (the release publishes no checksum file; the npm package @railway/cli@5.64.2 downloads the
# same archive in its postinstall without checking it).
CLI_SHA256=a154c3ff5ac3e2ece99bdca9d8d5505fc7f85a949689346bd934fb425246e229
CLI_URL="https://github.com/railwayapp/cli/releases/download/v${CLI_VERSION}/railway-v${CLI_VERSION}-x86_64-unknown-linux-musl.tar.gz"

usage() { awk 'NR > 1 && !/^#/ { exit } NR > 1 { sub(/^# ?/, ""); print }' "$0"; }

case "${1:-}" in
  --dry-run|--apply)
    [ "$#" = 1 ] || { usage >&2; exit 2; }
    exec python3 -I deploy/railway/railway_deploy.py "$1"
    ;;
  --install-cli)
    [ "$#" = 2 ] || { usage >&2; exit 2; }
    dir=$2
    [ "$(uname -s)-$(uname -m)" = Linux-x86_64 ] || { echo "deploy.sh: --install-cli knows the Linux x86_64 archive only; install railway ${CLI_VERSION} yourself" >&2; exit 2; }
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    curl -fsSL -o "$tmp/railway.tar.gz" "$CLI_URL"
    echo "$CLI_SHA256  $tmp/railway.tar.gz" | sha256sum -c --quiet - || { echo "deploy.sh: the archive's SHA-256 is not the pinned one; nothing installed" >&2; exit 1; }
    tar -xzf "$tmp/railway.tar.gz" -C "$tmp" railway
    mkdir -p "$dir"
    install -m 0755 "$tmp/railway" "$dir/railway"
    echo "deploy.sh: railway ${CLI_VERSION} installed in $dir (put it first on PATH; the deploy sets RAILWAY_NO_AUTO_UPDATE=1 and CI=true so it stays at this version)"
    ;;
  -h|--help)
    usage
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
