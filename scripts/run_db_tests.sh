#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ "${DT_ALLOW_DB_TESTS:-}" != "yes" ]]; then
  echo 'Refusing DB writes. Set DT_ALLOW_DB_TESTS=yes only for a disposable test cluster/database.' >&2
  exit 2
fi
: "${DATABASE_URL:?Set DATABASE_URL for a disposable PostgreSQL database named dt_test_*}"
command -v psql >/dev/null || { echo 'NOT RUN: psql is not installed.' >&2; exit 2; }
name=$(psql -X "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atc 'SELECT current_database()')
[[ "$name" == dt_test_* ]] || { echo 'Refusing non-dt_test_* database.' >&2; exit 2; }
exists=$(psql -X "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM pg_namespace WHERE nspname='twin'")
[[ "$exists" == 0 ]] || { echo 'Refusing an existing twin schema. Provide a fresh disposable DB.' >&2; exit 2; }
run() { psql -X "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$1"; }
run db/bootstrap_roles.sql
run migrations/0001_core.up.sql
run migrations/0002_rls.up.sql
run db/tests/invariants.sql
run db/tests/constraints.sql
run db/tests/roles.sql
run migrations/0002_rls.down.sql
run migrations/0001_core.down.sql
run migrations/0001_core.up.sql
run migrations/0002_rls.up.sql
run db/tests/invariants.sql
run db/tests/constraints.sql
run db/tests/roles.sql
echo 'DB migration smoke up/down/up completed. This does NOT replace real-login/API/concurrency tests.'
