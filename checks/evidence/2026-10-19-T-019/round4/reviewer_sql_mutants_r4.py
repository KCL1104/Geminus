#!/usr/bin/env python3
"""Round-4 reviewer SQL mutants for migration 0007 at 750b729 (the round-3
reviewer's list, re-run, and a few new ones). Loads the
author's harness (checks/evidence/2026-10-19-T-019/sql-mutants.py) from the
export and runs it with this list. Every mutant is expected to be killed;
the harness reports a survivor as UNEXPECTED.

Usage: ADMIN_URL=... DB_NAME=dt_test_rv019d_sqlmut2 python3 -I reviewer_sql_mutants_r4.py <export-root>
"""
import runpy
import sys

root = sys.argv[1]
harness = runpy.run_path(f"{root}/checks/evidence/2026-10-19-T-019/sql-mutants.py", run_name="reviewer")
harness["MUTANTS"][:] = [
    ("rv2-api-lease-columns",
     "ROW(NEW.attempt,NEW.lease_token,NEW.locked_at,NEW.locked_by,NEW.lease_expires_at,NEW.heartbeat_at,NEW.error)\n                    IS NOT DISTINCT FROM\n                    ROW(OLD.attempt,OLD.lease_token,OLD.locked_at,OLD.locked_by,OLD.lease_expires_at,OLD.heartbeat_at,OLD.error)",
     "ROW(NEW.attempt,NEW.error)\n                    IS NOT DISTINCT FROM\n                    ROW(OLD.attempt,OLD.error)"),
    ("rv2-worker-reschedules", "IF worker AND OLD.status='queued' AND NEW.status<>'running' THEN", "IF worker AND OLD.status='queued' AND NEW.status='cancelled' THEN"),
    ("rv2-queued-at-max", "NEW.status='queued' AND NEW.attempt>=NEW.max_attempts", "NEW.status='queued' AND NEW.attempt>NEW.max_attempts"),
    ("rv3-roles-by-session-user",
     "  owner boolean := pg_catalog.pg_has_role(current_user,'twin_owner','USAGE');\n  api boolean := pg_catalog.pg_has_role(current_user,'twin_api','USAGE');\n  worker boolean := pg_catalog.pg_has_role(current_user,'twin_worker','USAGE');",
     "  owner boolean := pg_catalog.pg_has_role(session_user,'twin_owner','USAGE');\n  api boolean := pg_catalog.pg_has_role(session_user,'twin_api','USAGE');\n  worker boolean := pg_catalog.pg_has_role(session_user,'twin_worker','USAGE');"),
    ("rv3-worker-by-membership", "worker boolean := pg_catalog.pg_has_role(current_user,'twin_worker','USAGE');", "worker boolean := pg_catalog.pg_has_role(current_user,'twin_worker','MEMBER');"),
    ("rv3-insert-with-a-heartbeat", " OR NEW.heartbeat_at IS NOT NULL OR NEW.error IS NOT NULL THEN", " OR NEW.error IS NOT NULL THEN"),
    ("rv3-insert-locked", "OR NEW.locked_at IS NOT NULL OR NEW.locked_by IS NOT NULL\n", "OR NEW.locked_by IS NOT NULL\n"),
    ("rv3-insert-with-an-expiry", "\n       OR NEW.lease_expires_at IS NOT NULL OR NEW.heartbeat_at", "\n       OR NEW.heartbeat_at"),
    ("rv3-worker-moves-locked-at", "ROW(NEW.locked_by,NEW.locked_at,NEW.error) IS DISTINCT FROM ROW(OLD.locked_by,OLD.locked_at,OLD.error)", "ROW(NEW.locked_by,NEW.error) IS DISTINCT FROM ROW(OLD.locked_by,OLD.error)"),
    ("rv3-worker-sets-an-error-while-running", "ROW(NEW.locked_by,NEW.locked_at,NEW.error) IS DISTINCT FROM ROW(OLD.locked_by,OLD.locked_at,OLD.error)", "ROW(NEW.locked_by,NEW.locked_at) IS DISTINCT FROM ROW(OLD.locked_by,OLD.locked_at)"),
    ("rv3-worker-leaves-locked-at", "(NEW.locked_by IS NOT NULL OR NEW.locked_at IS NOT NULL OR NEW.lease_expires_at IS NOT NULL)", "(NEW.locked_by IS NOT NULL OR NEW.lease_expires_at IS NOT NULL)"),
    ("rv3-worker-leaves-the-expiry", "(NEW.locked_by IS NOT NULL OR NEW.locked_at IS NOT NULL OR NEW.lease_expires_at IS NOT NULL)", "(NEW.locked_by IS NOT NULL OR NEW.locked_at IS NOT NULL)"),
    ("rv3-worker-leaves-the-holder", "(NEW.locked_by IS NOT NULL OR NEW.locked_at IS NOT NULL OR NEW.lease_expires_at IS NOT NULL)", "(NEW.locked_at IS NOT NULL OR NEW.lease_expires_at IS NOT NULL)"),
    ("rv3-worker-token-only", "ROW(NEW.attempt,NEW.lease_token) IS DISTINCT FROM ROW(OLD.attempt,OLD.lease_token)", "NEW.lease_token IS DISTINCT FROM OLD.lease_token"),
    ("rv3-worker-attempt-only", "ROW(NEW.attempt,NEW.lease_token) IS DISTINCT FROM ROW(OLD.attempt,OLD.lease_token)", "NEW.attempt IS DISTINCT FROM OLD.attempt"),
    ("rv3-worker-enqueues", "    IF NOT (owner OR api) THEN\n      RAISE EXCEPTION 'only the API enqueues a job'", "    IF NOT (owner OR api OR worker) THEN\n      RAISE EXCEPTION 'only the API enqueues a job'"),
    ("rv4-cancelled-not-final", "  IF OLD.status IN ('completed','failed','cancelled') THEN", "  IF OLD.status IN ('completed','failed') THEN"),
    ("rv4-failed-not-final", "  IF OLD.status IN ('completed','failed','cancelled') THEN", "  IF OLD.status IN ('completed','cancelled') THEN"),
    ("rv4-claim-same-token", "(NEW.attempt<>OLD.attempt+1 OR NEW.lease_token<=OLD.lease_token)", "(NEW.attempt<>OLD.attempt+1 OR NEW.lease_token<OLD.lease_token)"),
    ("rv4-claim-skips-attempts", "(NEW.attempt<>OLD.attempt+1 OR NEW.lease_token<=OLD.lease_token)", "(NEW.attempt<=OLD.attempt OR NEW.lease_token<=OLD.lease_token)"),
    ("rv4-created-at-mutable", "NEW.ai_analysis_id,NEW.max_attempts,NEW.created_at)", "NEW.ai_analysis_id,NEW.max_attempts,OLD.created_at)"),
    ("rv4-max-attempts-mutable", "NEW.ai_analysis_id,NEW.max_attempts,NEW.created_at)", "NEW.ai_analysis_id,OLD.max_attempts,NEW.created_at)"),
    ("rv4-tokens-go-back", "IF NEW.attempt<OLD.attempt OR NEW.lease_token<OLD.lease_token THEN", "IF NEW.attempt<OLD.attempt THEN"),
]
harness["EQUIVALENT"].clear()
sys.argv = [sys.argv[0], root]
sys.exit(harness["main"]())
