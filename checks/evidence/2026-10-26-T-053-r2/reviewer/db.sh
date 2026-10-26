#!/usr/bin/env bash
# Reviewer (T-053 r2): create or drop a disposable dt_test_* database on the
# shared PostgreSQL 16 cluster (never started or stopped here).
# Usage: db.sh create|drop <dt_test_name>
set -u
op="$1"; db="$2"
case "$db" in dt_test_*) ;; *) echo "refusing: $db is not dt_test_*" >&2; exit 2;; esac
sock=/var/lib/postgresql/geminus-t009-fresh
case "$op" in
  create) su postgres -c "createdb -h $sock -p 55440 -E UTF8 -T template0 --locale=C.UTF-8 $db" ;;
  drop) su postgres -c "dropdb -h $sock -p 55440 --if-exists $db" ;;
  *) echo "usage: db.sh create|drop <dt_test_name>" >&2; exit 2 ;;
esac
