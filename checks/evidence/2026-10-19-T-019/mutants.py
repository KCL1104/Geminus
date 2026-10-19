#!/usr/bin/env python3
"""T-019 mutation check: each mutant weakens one rule of the job queue
(internal/storage/jobs), the worker scope (internal/tenant/workerscope) or
the architecture rule that confines it, and must make that package's tests
fail. Mutants are applied with `go test -overlay`; the repository is never
modified. A mutant that does not build is a harness error, never a kill.

Usage: GEMINUS_TEST_DATABASE_URL=... python3 checks/evidence/2026-10-19-T-019/mutants.py [--only=NAME ...] [repo-root]
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

ARGS = [a for a in sys.argv[1:] if not a.startswith("--only=")]
ONLY = {a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--only=")}
REPO = pathlib.Path(ARGS[0] if ARGS else pathlib.Path(__file__).resolve().parents[3]).resolve()
JOBS = "internal/storage/jobs/jobs.go"
SCOPE = "internal/tenant/workerscope/workerscope.go"
RULES = "scripts/check-architecture/rules.go"
TESTS = {JOBS: "./internal/storage/jobs/", SCOPE: "./internal/tenant/workerscope/", RULES: "./scripts/check-architecture/"}

MUTANTS = [
    # Claim
    ("claim-no-org-filter", JOBS, "WHERE organization_id=$1 AND status='queued' AND available_at<=now() AND attempt<max_attempts", "WHERE status='queued' AND available_at<=now() AND attempt<max_attempts"),
    ("claim-not-skip-locked", JOBS, "FOR UPDATE SKIP LOCKED LIMIT 1", "FOR UPDATE LIMIT 1"),
    ("claim-ignores-available-at", JOBS, "status='queued' AND available_at<=now() AND attempt<max_attempts", "status='queued' AND attempt<max_attempts"),
    ("claim-ignores-attempts", JOBS, "available_at<=now() AND attempt<max_attempts\n", "available_at<=now()\n"),
    ("claim-any-type", JOBS, "\n\t\t    AND type=ANY($4)\n", "\n\t\t    AND $4::text[] IS NOT NULL\n"),
    ("claim-newest-first", JOBS, "ORDER BY priority DESC, created_at, id", "ORDER BY priority DESC, created_at DESC, id"),
    ("claim-priority-ignored", JOBS, "ORDER BY priority DESC, created_at, id", "ORDER BY created_at, id"),
    ("claim-same-token", JOBS, "lease_token=lease_token+1,", "lease_token=lease_token,"),
    ("claim-no-attempt", JOBS, "SET status='running', attempt=attempt+1,", "SET status='running', attempt=attempt,"),
    # The fence
    ("fence-any-token", JOBS, "AND locked_by=$3 AND lease_token=$4 AND", "AND locked_by=$3 AND $4::bigint>=0 AND"),
    ("fence-any-worker", JOBS, "AND id=$2 AND locked_by=$3 AND", "AND id=$2 AND $3::text<>'' AND"),
    ("fence-expired-ok", JOBS, "AND status='running' AND lease_expires_at>now()`", "AND status='running'`"),
    ("fence-any-status", JOBS, "lease_token=$4 AND status='running' AND lease_expires_at>now()`", "lease_token=$4 AND lease_expires_at>now()`"),
    ("heartbeat-ignores-rows", JOBS, "\tif tag.RowsAffected() != 1 {\n\t\treturn ErrLeaseLost\n\t}\n\treturn nil\n}\n\n// Lock", "\t_ = tag\n\treturn nil\n}\n\n// Lock"),
    ("lock-not-for-update", JOBS, "+fence+` FOR UPDATE`", "+fence+``"),
    ("complete-keeps-the-lock", JOBS, "SET status='completed', locked_at=NULL, locked_by=NULL,", "SET status='completed', locked_at=NULL,"),
    # Fail and retry
    ("fail-retries-everything", JOBS, "status=CASE WHEN $5 AND attempt<max_attempts THEN 'queued'", "status=CASE WHEN $5::boolean IS NOT NULL AND attempt<max_attempts THEN 'queued'"),
    ("fail-retries-the-last-attempt", JOBS, "status=CASE WHEN $5 AND attempt<max_attempts THEN 'queued'", "status=CASE WHEN $5 AND attempt<=max_attempts THEN 'queued'"),
    ("fail-no-backoff", JOBS, "available_at=CASE WHEN $5 AND attempt<max_attempts THEN now()+(($6::bigint[])[attempt]*interval '1 millisecond') ELSE available_at END", "available_at=CASE WHEN $5 AND $6::bigint[] IS NOT NULL THEN now() ELSE available_at END"),
    ("backoff-not-doubling", JOBS, "\t\td *= 2\n", "\t\td += 0\n"),
    ("backoff-uncapped", JOBS, "return min(d, p.BackoffMax)", "return d"),
    # Reap
    ("reap-live-leases", JOBS, "status='running' AND lease_expires_at<=now()", "status='running'"),
    ("reap-requeues-spent", JOBS, "status=CASE WHEN j.attempt<j.max_attempts THEN 'queued'", "status=CASE WHEN j.attempt<=j.max_attempts THEN 'queued'"),
    ("reap-not-skip-locked", JOBS, "ORDER BY lease_expires_at, id\n\t\t  FOR UPDATE SKIP LOCKED LIMIT $2", "ORDER BY lease_expires_at, id\n\t\t  FOR UPDATE LIMIT $2"),
    ("reap-no-backoff", JOBS, "THEN now()+(($3::bigint[])[j.attempt]*interval '1 millisecond') ELSE j.available_at END", "THEN now()+($3::bigint[] IS NULL)::int*interval '1 second' ELSE j.available_at END"),
    # Enqueue
    ("enqueue-any-resource", JOBS, "|| !j.Resource.of(j.Type) ||", "||"),
    ("enqueue-any-attempts", JOBS, "\tif j.MaxAttempts < 1 || j.MaxAttempts > 10 {\n\t\treturn Job{}, ErrInvalidJob\n\t}\n", ""),
    # The worker scope
    ("scope-any-organization", SCOPE, "\tif !slices.Contains(w.assigned, org) {\n\t\treturn ErrNotAssigned\n\t}\n", ""),
    ("scope-not-set", SCOPE, "\t\tif err := scope.Set(ctx, tx, org, \"\"); err != nil {\n\t\t\treturn err\n\t\t}\n", ""),
    ("scope-duplicates-ok", SCOPE, "!scope.OrgID.MatchString(o) || slices.Contains(assigned[:i], o)", "!scope.OrgID.MatchString(o) || slices.Contains(assigned[:i], \"\")"),
    ("scope-any-id", SCOPE, "!scope.OrgID.MatchString(o) || slices.Contains(assigned[:i], o)", "o == \"\" || slices.Contains(assigned[:i], o)"),
    # The architecture rule
    ("rule-exempts-everything", RULES, 'except:  []string{"./cmd/worker/...", "./internal/worker/...", "./internal/tenant/workerscope/..."},', 'except:  []string{"./..."},'),
]
EQUIVALENT = {
    "claim-no-org-filter": "RLS confines the worker's transaction to the organization its scope sets, so the filter is defence in depth; no test can see it go (the application filter is required by AGENTS.md, and kept)",
    "claim-ignores-attempts": "no write leaves a queued job with its attempts spent: Fail and Reap fail a job whose last attempt ends; the condition guards against data changed by hand",
    "fence-any-status": "every write that ends a running job also clears locked_by, which the fence still requires, so a finished job never matches",
}


def run(overlay: str | None, pkg: str) -> str:
    env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1")
    cmd = ["go", "test", "-count=1", pkg]
    if overlay:
        cmd.insert(2, "-overlay=" + overlay)
    r = subprocess.run(cmd, cwd=REPO, env=env, capture_output=True, text=True, timeout=900)
    if r.returncode:
        if "--- FAIL" in r.stdout:
            return "KILLED\t" + next(l.strip() for l in r.stdout.splitlines() if l.startswith("--- FAIL"))
        return "BUILD-FAILED\t" + (r.stdout + r.stderr).strip().splitlines()[-1]
    return "SURVIVED"


def main() -> int:
    if not os.environ.get("GEMINUS_TEST_DATABASE_URL"):
        print("set GEMINUS_TEST_DATABASE_URL to a migrated dt_test_* database", file=sys.stderr)
        return 2
    sources = {f: (REPO / f).read_text() for f in TESTS}
    bad = [n for n, f, old, _ in MUTANTS if sources[f].count(old) != 1]
    if bad:
        print("patterns not found exactly once:", bad)
        return 2
    for f, pkg in TESTS.items():
        if run(None, pkg) != "SURVIVED":
            print("baseline fails:", pkg)
            return 2
    wrong = total = 0
    with tempfile.TemporaryDirectory() as tmp:
        for i, (name, f, old, new) in enumerate(MUTANTS):
            if ONLY and name not in ONLY:
                continue
            total += 1
            mutated = pathlib.Path(tmp) / f"m{i}.go"
            mutated.write_text(sources[f].replace(old, new))
            overlay = pathlib.Path(tmp) / f"o{i}.json"
            overlay.write_text(json.dumps({"Replace": {str(REPO / f): str(mutated)}}))
            v = run(str(overlay), TESTS[f])
            ok = not v.startswith("BUILD-FAILED") and (v == "SURVIVED") == (name in EQUIVALENT)
            wrong += not ok
            print(f"{name}\t{v}" + ("" if ok else "\tUNEXPECTED"), flush=True)
    print(f"total={total} unexpected={wrong}")
    return 1 if wrong else 0


if __name__ == "__main__":
    sys.exit(main())
