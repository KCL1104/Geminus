#!/bin/bash
# make integration-test on the reviewer's fresh disposable database dt_test_t050rv1 (shared PostgreSQL 16, not started or stopped here).
export DATABASE_URL='postgres://postgres@/dt_test_t050rv1?host=/var/lib/postgresql/geminus-t009-fresh&port=55440'
export GEMINUS_TEST_DATABASE_URL="$DATABASE_URL"
export DT_ALLOW_DB_TESTS=yes
psql "$DATABASE_URL" -Atc "select current_database(), version(), (select count(*) from pg_tables where schemaname='public')" || exit 3
exec make integration-test PYTHON=/home/user/Geminus/.venv/bin/python
