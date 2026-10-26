#!/usr/bin/env bash
# make staging-smoke (T-039, TEST-030): the local staging composition
# (deploy/local/compose.yaml, LOCAL ONLY, with the repository's test OIDC
# provider) on the images of make build-containers. It checks what the
# deployment depends on and ends with everything removed:
#
#   1. the migration step as the migration login applies every migration,
#      and a second run applies nothing; the API's runtime login and the
#      superuser cannot migrate (exit 4);
#   2. the seed step provisions an organization (D-042);
#   3. the API refuses the migration login (exit 3: runtime roles only);
#   4. API, worker, web and the test provider start (healthchecks green);
#      /healthz and /readyz are 200, /v1/me without a token is 401;
#      the worker runs for the seeded organization;
#   5. the BFF serves / and redirects /auth/login to the provider (its
#      configuration is valid), /api/v1/me without a session is 401;
#   6. the images run as non-root users and carry no credential variable;
#   7. SIGTERM stops the API and the worker cleanly (exit 0).
#
# Without Docker (no CLI, no reachable daemon, no compose) it fails with
# NOT RUN and exit 2: never a fake success. It never touches any database
# but its own container's.
set -euo pipefail
cd "$(dirname "$0")/../.."

notrun() { echo "staging-smoke: NOT RUN: $*" >&2; exit 2; }
command -v docker >/dev/null 2>&1 || notrun "docker is not installed"
docker info >/dev/null 2>&1 || notrun "no Docker daemon is reachable (docker info failed)"
docker compose version >/dev/null 2>&1 || notrun "the docker compose plugin is not available"
command -v curl >/dev/null 2>&1 || notrun "curl is not installed"
command -v openssl >/dev/null 2>&1 || notrun "openssl is not installed"
for image in geminus-api:local geminus-worker:local geminus-web:local; do
  docker image inspect "$image" >/dev/null 2>&1 || { echo "staging-smoke: $image is missing: run make build-containers first" >&2; exit 2; }
done

project="geminus-smoke-$$"
compose=(docker compose -p "$project" -f deploy/local/compose.yaml)
web_port=${GEMINUS_SMOKE_WEB_PORT:-3100}
api_port=${GEMINUS_SMOKE_API_PORT:-18080}
export GEMINUS_SMOKE_WEB_PORT=$web_port GEMINUS_SMOKE_API_PORT=$api_port

# Throwaway credentials of this run only (never printed).
pw() { openssl rand -hex 32; }
GEMINUS_LOCAL_ADMIN_PASSWORD=$(pw)
GEMINUS_MIGRATOR_DB_PASSWORD=$(pw)
GEMINUS_API_DB_PASSWORD=$(pw)
GEMINUS_WORKER_DB_PASSWORD=$(pw)
GEMINUS_PROVISIONER_DB_PASSWORD=$(pw)
GEMINUS_LOCAL_SESSION_KEYS="local:$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')"
GEMINUS_LOCAL_ORG=""
export GEMINUS_LOCAL_ADMIN_PASSWORD GEMINUS_MIGRATOR_DB_PASSWORD GEMINUS_API_DB_PASSWORD GEMINUS_WORKER_DB_PASSWORD \
  GEMINUS_PROVISIONER_DB_PASSWORD GEMINUS_LOCAL_SESSION_KEYS GEMINUS_LOCAL_ORG

cleanup() { "${compose[@]}" --profile steps down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT
fail() {
  echo "staging-smoke: FAIL: $*" >&2
  "${compose[@]}" logs --no-color --tail=40 >&2 2>&1 || true
  exit 1
}
ok() { echo "staging-smoke: ok: $*"; }
db_url() { echo "postgresql://$1:$2@postgres:5432/geminus?sslmode=disable"; }
network="${project}_default"

"${compose[@]}" up -d --wait postgres || fail "postgres did not start"
ok "postgres up; roles and logins created at first start"

# 1. Migrations: only the migration login.
set +e
"${compose[@]}" run --rm -T -e GEMINUS_MIGRATE_DATABASE_URL="$(db_url geminus_api "$GEMINUS_API_DB_PASSWORD")" migrate up >/dev/null 2>&1
code=$?
set -e
[ "$code" = 4 ] || fail "the API login migrating: exit $code, want 4"
set +e
"${compose[@]}" run --rm -T -e GEMINUS_MIGRATE_DATABASE_URL="$(db_url geminus_admin "$GEMINUS_LOCAL_ADMIN_PASSWORD")" migrate up >/dev/null 2>&1
code=$?
set -e
[ "$code" = 4 ] || fail "the superuser migrating: exit $code, want 4"
ok "the API login and the superuser cannot migrate (exit 4)"
out=$("${compose[@]}" run --rm -T migrate up) || fail "migrate up"
echo "$out" | grep -q '"pending":\[\]' || fail "migrate up left migrations pending: $out"
echo "$out" | grep -q '"applied":\[1,' || fail "migrate up applied nothing on an empty database: $out"
out=$("${compose[@]}" run --rm -T migrate up) || fail "migrate up again"
echo "$out" | grep -q '"applied":\[\]' || fail "a second migrate up applied something: $out"
ok "migrations applied as geminus_migrator; a second run applies nothing ($out)"

# 2. Seed.
out=$("${compose[@]}" run --rm -T seed) || fail "seed"
GEMINUS_LOCAL_ORG=$(echo "$out" | sed -n 's/.*"organization_id":"\(org_[0-9A-Z]*\)".*/\1/p')
[ -n "$GEMINUS_LOCAL_ORG" ] || fail "seed printed no organization: $out"
export GEMINUS_LOCAL_ORG
ok "seeded organization $GEMINUS_LOCAL_ORG"

# 3. The API takes only its runtime login.
set +e
docker run --rm --network "$network" -e APP_ENV=production \
  -e DATABASE_URL="$(db_url geminus_migrator "$GEMINUS_MIGRATOR_DB_PASSWORD")" \
  -e AUTH_OIDC_ISSUER=https://oidc.local.invalid/ -e AUTH_OIDC_AUDIENCE=https://api.e2e.invalid \
  -e AUTH_OIDC_JWKS_URL=https://oidc.local.invalid/.well-known/jwks.json geminus-api:local >/dev/null 2>&1
code=$?
set -e
[ "$code" = 3 ] || fail "the API with the migration login: exit $code, want 3"
ok "the API refuses the migration login (exit 3)"

# 4. The services.
"${compose[@]}" up -d --wait web oidc-test api worker || fail "the services did not become healthy"
api="http://127.0.0.1:$api_port"
web="http://127.0.0.1:$web_port"
[ "$(curl -s "$api/healthz")" = '{"status":"ok"}' ] || fail "GET /healthz"
[ "$(curl -s "$api/readyz")" = '{"status":"ready"}' ] || fail "GET /readyz"
body=$(mktemp)
status=$(curl -s -o "$body" -w '%{http_code}' "$api/v1/me")
grep -q '"UNAUTHENTICATED"' "$body" && [ "$status" = 401 ] || fail "GET /v1/me without a token: $status"
rm -f "$body"
ok "API: /healthz, /readyz 200; /v1/me 401 UNAUTHENTICATED"
running=""
for _ in $(seq 1 30); do
  if "${compose[@]}" logs --no-color worker 2>/dev/null | grep -q '"msg":"running"'; then running=yes; break; fi
  sleep 1
done
[ -n "$running" ] || fail "the worker did not start its loop"
ok "worker running for the seeded organization"

# 5. The BFF.
[ "$(curl -s -o /dev/null -w '%{http_code}' "$web/")" = 200 ] || fail "GET / on the web"
login=$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' "$web/auth/login")
case "$login" in
  30[23]\ http://127.0.0.1:3101/authorize\?*code_challenge_method=S256*) ;;
  *) fail "GET /auth/login: $login" ;;
esac
[ "$(curl -s -o /dev/null -w '%{http_code}' "$web/api/v1/me")" = 401 ] || fail "GET /api/v1/me without a session"
ok "web: / 200; /auth/login redirects to the provider with PKCE; /api/v1/me without a session 401"

# 6. The images.
for image in geminus-api:local geminus-worker:local geminus-web:local; do
  user=$(docker image inspect -f '{{.Config.User}}' "$image")
  case "$user" in ""|root|0|0:*|root:*) fail "$image runs as root ($user)" ;; esac
  envs=$(docker image inspect -f '{{json .Config.Env}}' "$image")
  if echo "$envs" | grep -Eq 'PASSWORD|SECRET|SESSION_KEYS|API_KEY|DATABASE_URL|ACCESS_KEY'; then fail "$image has a credential variable: $envs"; fi
  if docker history --no-trunc --format '{{.CreatedBy}}' "$image" | grep -Eq 'PASSWORD=|SECRET=|SESSION_KEYS=|API_KEY='; then fail "$image's history names a credential"; fi
  ok "$image runs as $user and carries no credential variable"
done

# 7. Graceful stop.
api_id=$("${compose[@]}" ps -q api)
worker_id=$("${compose[@]}" ps -q worker)
"${compose[@]}" stop -t 30 api worker >/dev/null 2>&1 || fail "stopping api and worker"
[ "$(docker inspect -f '{{.State.ExitCode}}' "$api_id")" = 0 ] || fail "the API did not exit 0 on SIGTERM"
[ "$(docker inspect -f '{{.State.ExitCode}}' "$worker_id")" = 0 ] || fail "the worker did not exit 0 on SIGTERM"
ok "API and worker stop on SIGTERM with exit 0"

echo "staging-smoke: PASS (local composition; the real Auth0 sign-in, the bucket and the public URL are checked on staging by a person: docs/runbooks/deploy.md §8)"
