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
run migrations/0003_draft_issues.up.sql
run migrations/0004_provisioner.up.sql
run migrations/0005_ir_text.up.sql
run migrations/0006_version_comment.up.sql
run migrations/0007_job_states.up.sql
run migrations/0008_me.up.sql
run migrations/0009_simulation_runs.up.sql
run migrations/0010_import_uploads.up.sql
run migrations/0011_ai_id_pool.up.sql
run migrations/0012_proposal_jobs.up.sql
run migrations/0013_proposal_decisions.up.sql
run migrations/0014_import_mapping.up.sql
run migrations/0015_scenarios.up.sql
run migrations/0016_ai_spend.up.sql
run migrations/0017_proposal_images.up.sql
run db/tests/invariants.sql
run db/tests/constraints.sql
run db/tests/roles.sql
run db/tests/self.sql
run db/tests/runs.sql
run db/tests/imports.sql
run db/tests/proposals.sql
run db/tests/proposal_jobs.sql
run db/tests/proposal_decisions.sql
run db/tests/import_mapping.sql
run db/tests/scenarios.sql
run db/tests/ai_spend.sql
run db/tests/proposal_images.sql
run migrations/0017_proposal_images.down.sql
run migrations/0016_ai_spend.down.sql
run migrations/0015_scenarios.down.sql
run migrations/0014_import_mapping.down.sql
run migrations/0013_proposal_decisions.down.sql
run migrations/0012_proposal_jobs.down.sql
run migrations/0011_ai_id_pool.down.sql
# D-046: 0011 stores the pool each proposal is created with, which cannot be
# made up afterwards. On a database with proposals it stops and changes nothing.
run db/tests/pool_rows.sql
if out=$(psql -X "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/0011_ai_id_pool.up.sql 2>&1); then
  echo 'FAILED: migration 0011 ran on a database with proposals' >&2; exit 1
fi
grep -q 'migration 0011 stores the ID allocation pool' <<<"$out" || { echo "FAILED: migration 0011 stopped for another reason: $out" >&2; exit 1; }
state=$(psql -X "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atc "SELECT count(c.column_name) || ' ' || bool_and(r.relforcerowsecurity)
  FROM pg_class r LEFT JOIN information_schema.columns c ON c.table_schema='twin' AND c.table_name='ai_proposals' AND c.column_name='id_pool'
  WHERE r.oid = 'twin.ai_proposals'::regclass")
[[ "$state" == '0 true' ]] || { echo "FAILED: migration 0011 changed ai_proposals: $state" >&2; exit 1; }
run migrations/0010_import_uploads.down.sql
run migrations/0009_simulation_runs.down.sql
run migrations/0008_me.down.sql
run migrations/0007_job_states.down.sql
run migrations/0006_version_comment.down.sql
run migrations/0005_ir_text.down.sql
# D-040: 0005 stores the canonical IR text, which SQL cannot make from
# jsonb. On a database with models it stops and changes nothing.
run db/tests/ir_rows.sql
if out=$(psql -X "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/0005_ir_text.up.sql 2>&1); then
  echo 'FAILED: migration 0005 ran on a database with models' >&2; exit 1
fi
grep -q 'migration 0005 stores the canonical IR text' <<<"$out" || { echo "FAILED: migration 0005 stopped for another reason: $out" >&2; exit 1; }
state=$(psql -X "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atc "SELECT string_agg(c.data_type, ',' ORDER BY c.table_name) || ' ' || bool_and(r.relforcerowsecurity)
  FROM information_schema.columns c JOIN pg_class r ON r.oid = ('twin.' || c.table_name)::regclass
  WHERE c.table_schema='twin' AND c.column_name='ir'")
[[ "$state" == 'jsonb,jsonb true' ]] || { echo "FAILED: migration 0005 changed the IR tables: $state" >&2; exit 1; }
run migrations/0004_provisioner.down.sql
run migrations/0003_draft_issues.down.sql
run migrations/0002_rls.down.sql
run migrations/0001_core.down.sql
run migrations/0001_core.up.sql
run migrations/0002_rls.up.sql
run migrations/0003_draft_issues.up.sql
run migrations/0004_provisioner.up.sql
run migrations/0005_ir_text.up.sql
run migrations/0006_version_comment.up.sql
run migrations/0007_job_states.up.sql
run migrations/0008_me.up.sql
run migrations/0009_simulation_runs.up.sql
run migrations/0010_import_uploads.up.sql
run migrations/0011_ai_id_pool.up.sql
run migrations/0012_proposal_jobs.up.sql
run migrations/0013_proposal_decisions.up.sql
run migrations/0014_import_mapping.up.sql
run migrations/0015_scenarios.up.sql
run migrations/0016_ai_spend.up.sql
run migrations/0017_proposal_images.up.sql
run db/tests/invariants.sql
run db/tests/constraints.sql
run db/tests/roles.sql
run db/tests/self.sql
run db/tests/runs.sql
run db/tests/imports.sql
run db/tests/proposals.sql
run db/tests/proposal_jobs.sql
run db/tests/proposal_decisions.sql
run db/tests/import_mapping.sql
run db/tests/scenarios.sql
run db/tests/ai_spend.sql
run db/tests/proposal_images.sql
echo 'DB migration smoke up/down/up completed. This does NOT replace real-login/API/concurrency tests.'
