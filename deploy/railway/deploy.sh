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
#   deploy/railway/deploy.sh --install-auth0-cli DIR
#                                                 install the pinned Auth0 CLI 1.37.0 into DIR (Linux x86_64),
#                                                 checking the archive's SHA-256 (the release's checksums.txt)
#                                                 and the binary's before anything is installed
#
# Token (exactly as the Railway CLI reads it; docs.railway.com/guides/cli,
# 2026-10-26; the CLI's source at v5.64.2, src/config.rs): RAILWAY_TOKEN, a
# project token (the project and environment are the token's and must exist),
# or RAILWAY_API_TOKEN, an account or workspace token (the project is found by
# name or created). The token is read by the CLI from the environment only:
# never echoed, never an argument, never in a log, redacted if the CLI quotes
# it, and removed from the environment of every other program (psql, go).
#
# Inputs (not secret): GEMINUS_AUTH0_ISSUER (the tenant's domain,
# https://<tenant>.<region>.auth0.com/), GEMINUS_AUTH0_AUDIENCE (the API's
# identifier), GEMINUS_SEED_OWNER_SUBJECT, GEMINUS_RAILWAY_BUCKET_REGION (sjc,
# iad, ams or sin; fixed once created); optional GEMINUS_RAILWAY_PROJECT
# (geminus-demo), GEMINUS_RAILWAY_ENVIRONMENT (production),
# GEMINUS_RAILWAY_WORKSPACE, GEMINUS_SEED_ORG_NAME;
# GEMINUS_RAILWAY_SEAL=off only for the recovery of the runbook's §7.7;
# GEMINUS_KEEP_DB_PROXY=1 accepts a TCP proxy still open on Postgres at the end
# (without it, any open proxy, also one from before the run, is named with its
# removal command and the run exits 1).
#
# Auth0 (T-057, H-034): GEMINUS_AUTH0_SETUP=cli (default) sets Auth0 up with
# the pinned Auth0 CLI (--install-auth0-cli; its SHA-256 is checked before it
# is run): the owner approves its device sign-in in their own browser (the URL
# and code are shown), then the API, the Regular Web Application, sign-up off
# and the judge's account (H-035 (2)) are found or created; the client secret
# goes on stdin to web's BFF_OIDC_CLIENT_SECRET and is sealed; the CLI is
# signed out at the end. Optional: GEMINUS_AUTH0_CONNECTION
# (Username-Password-Authentication), GEMINUS_JUDGE_EMAIL
# (judge@geminus-demo.invalid), GEMINUS_JUDGE_RESET_PASSWORD=1 (a new password
# for an existing judge's account). The judge's new password is shown once, on
# stderr, at the end of the run: do not keep the run's stderr in a file.
# GEMINUS_AUTH0_SETUP=manual skips the step (the owner sets Auth0 up in its
# dashboard, docs/runbooks/auth0.md §6): then GEMINUS_AUTH0_CLIENT_ID is an
# input, the owner fills BFF_OIDC_CLIENT_SECRET, and GEMINUS_JUDGE_SUBJECT (the
# judge's user_id) makes the judge an editor of the demo organization.
#
# The owner fills in Railway's UI, never this script, and seals what is secret
# (H-036): BFF_OIDC_CLIENT_SECRET (web) with GEMINUS_AUTH0_SETUP=manual, and only to turn live AI on, the same
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
# Railway CLI, the pinned Auth0 CLI (GEMINUS_AUTH0_SETUP=cli). Exit codes: 0 done, 1 a step failed or a TCP proxy is still open
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

# The Auth0 CLI (T-057, H-034): github.com/auth0/auth0-cli release v1.37.0 (2026-09-30; MIT, its LICENSE at the tag).
AUTH0_CLI_VERSION=1.37.0
# SHA-256 of auth0-cli_1.37.0_Linux_x86_64.tar.gz as the release's checksums.txt lists it (read 2026-10-26; the archive
# downloaded then has it).
AUTH0_CLI_ARCHIVE_SHA256=277e8fa32b9e4f4feb8e30717892139251821c0c66cd3ad5f326ba759ec6ac4f
# SHA-256 of the auth0 binary in that archive (computed 2026-10-26; railway_deploy.py checks it before every run of the CLI).
AUTH0_CLI_SHA256=2bc2ef7c1ab3c340a50bebb2c6bfd9692ea23dfee539bf7a2e1aac3debb3491c
AUTH0_CLI_URL="https://github.com/auth0/auth0-cli/releases/download/v${AUTH0_CLI_VERSION}/auth0-cli_${AUTH0_CLI_VERSION}_Linux_x86_64.tar.gz"

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
  --install-auth0-cli)
    [ "$#" = 2 ] || { usage >&2; exit 2; }
    dir=$2
    [ "$(uname -s)-$(uname -m)" = Linux-x86_64 ] || { echo "deploy.sh: --install-auth0-cli knows the Linux x86_64 archive only; install auth0 ${AUTH0_CLI_VERSION} yourself (the deploy checks its SHA-256)" >&2; exit 2; }
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    curl -fsSL -o "$tmp/auth0.tar.gz" "$AUTH0_CLI_URL"
    echo "$AUTH0_CLI_ARCHIVE_SHA256  $tmp/auth0.tar.gz" | sha256sum -c --quiet - || { echo "deploy.sh: the Auth0 CLI archive's SHA-256 is not the pinned one; nothing installed" >&2; exit 1; }
    tar -xzf "$tmp/auth0.tar.gz" -C "$tmp" auth0
    echo "$AUTH0_CLI_SHA256  $tmp/auth0" | sha256sum -c --quiet - || { echo "deploy.sh: the auth0 binary's SHA-256 is not the pinned one; nothing installed" >&2; exit 1; }
    mkdir -p "$dir"
    install -m 0755 "$tmp/auth0" "$dir/auth0"
    echo "deploy.sh: auth0 ${AUTH0_CLI_VERSION} installed in $dir (put it first on PATH; the deploy runs it only while its SHA-256 is the pinned one)"
    ;;
  -h|--help)
    usage
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
