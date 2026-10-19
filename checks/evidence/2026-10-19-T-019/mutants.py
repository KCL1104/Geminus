#!/usr/bin/env python3
"""T-019 mutation check: each mutant weakens one rule of the job queue
(internal/storage/jobs), the worker scope (internal/tenant/workerscope),
the architecture rule that confines it, the job loop (internal/worker), its
configuration (internal/config) or its composition root (cmd/worker), and
must make that package's tests fail. Mutants are applied with `go test -overlay`; the repository is never
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
LOOP = "internal/worker/worker.go"
CONFIG = "internal/config/worker.go"
CMD = "cmd/worker/main.go"
TESTS = {JOBS: "./internal/storage/jobs/", SCOPE: "./internal/tenant/workerscope/", RULES: "./scripts/check-architecture/",
         LOOP: "./internal/worker/", CONFIG: "./internal/config/", CMD: "./cmd/worker/"}

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
    ("end-keeps-the-lock", JOBS, "SET status=$5, locked_at=NULL, locked_by=NULL,", "SET status=$5, locked_at=NULL,"),
    # Cancellation (TEST-021)
    ("cancel-queued-any-status", JOBS, "WHERE organization_id=$1 AND id=$2 AND status='queued'`, org, id)", "WHERE organization_id=$1 AND id=$2`, org, id)"),
    ("cancel-queued-any-org", JOBS, "SET status='cancelled' WHERE organization_id=$1 AND id=$2", "SET status='cancelled' WHERE $1::text<>'' AND id=$2"),
    ("cancel-queued-always-reports", JOBS, "return tag.RowsAffected() == 1, nil", "return tag.RowsAffected() >= 0, nil"),
    ("cancel-completes", JOBS, "return end(ctx, tx, l, Cancelled)", "return end(ctx, tx, l, Completed)"),
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
    # Lease length, retry backoff and the reaper's limit (review L4-L6)
    ("rv-claim-lease-x10", JOBS, "lease_expires_at=now()+($3::bigint*interval '1 millisecond'), heartbeat_at=now()", "lease_expires_at=now()+($3::bigint*interval '10 millisecond'), heartbeat_at=now()"),
    ("rv-heartbeat-lease-x10", JOBS, "lease_expires_at=now()+($5::bigint*interval '1 millisecond')", "lease_expires_at=now()+($5::bigint*interval '10 millisecond')"),
    ("rv-fail-backoff-first-only", JOBS, "now()+(($6::bigint[])[attempt]*interval '1 millisecond')", "now()+(($6::bigint[])[1]*interval '1 millisecond')"),
    ("rv-reap-ignores-limit", JOBS, "FOR UPDATE SKIP LOCKED LIMIT $2", "FOR UPDATE SKIP LOCKED LIMIT $2*0+1000"),
    ("nil-failure-crashes", JOBS, "\tif f == nil {\n\t\treturn \"jobs: a nil failure\"\n\t}\n", ""),
    # Enqueue
    ("enqueue-any-resource", JOBS, "|| !j.Resource.of(j.Type) ||", "||"),
    ("enqueue-any-attempts", JOBS, "\tif j.MaxAttempts < 1 || j.MaxAttempts > 10 {\n\t\treturn Job{}, ErrInvalidJob\n\t}\n", ""),
    # The worker scope
    ("scope-any-organization", SCOPE, "\tif !slices.Contains(w.assigned, org) {\n\t\treturn ErrNotAssigned\n\t}\n", ""),
    ("scope-not-set", SCOPE, "\t\tif err := scope.Set(ctx, tx, org, \"\"); err != nil {\n\t\t\treturn err\n\t\t}\n", ""),
    ("scope-duplicates-ok", SCOPE, "!scope.OrgID.MatchString(o) || slices.Contains(assigned[:i], o)", "!scope.OrgID.MatchString(o) || slices.Contains(assigned[:i], \"\")"),
    ("scope-any-id", SCOPE, "!scope.OrgID.MatchString(o) || slices.Contains(assigned[:i], o)", "o == \"\" || slices.Contains(assigned[:i], o)"),
    # The job loop
    ("loop-no-reaping", LOOP, "requeued, failed, err := jobs.Reap(ctx, tx, org, w.cfg.ReapLimit, w.cfg.Policy)", "requeued, failed, err := 0, 0, error(nil)"),
    ("loop-one-organization-a-pass", LOOP, "\t\t\tworked = true\n\t\t}\n", "\t\t\treturn true\n\t\t}\n"),
    ("loop-claims-unhandled-types", LOOP, "jobs.Claim(ctx, tx, org, w.cfg.ID, w.types, w.cfg.Policy)", "jobs.Claim(ctx, tx, org, w.cfg.ID, append(w.types, jobs.ImportValidate), w.cfg.Policy)"),
    ("loop-ignores-stop", LOOP, "\tfor ctx.Err() == nil {\n\t\tif w.Pass(ctx) {", "\tfor {\n\t\tif w.Pass(ctx) {"),
    ("rv-no-idle-pause", LOOP, "case <-time.After(w.cfg.Idle):", "case <-time.After(0):"),
    # The heartbeat and the deadline (review M1, M2)
    ("no-heartbeat", LOOP, "beats.Go(func() { w.heartbeat(hctx, lease, claimed, setLost, log) })", "beats.Go(func() {})"),
    ("heartbeat-ignores-a-lost-lease", LOOP, "\t\t\tcase errors.Is(r.err, jobs.ErrLeaseLost):\n\t\t\t\tsetLost()\n\t\t\t\treturn\n", ""),
    ("deadline-ignored", LOOP, "\t\t\tlog.Warn(\"the lease could not be renewed in time; the attempt stops\")\n\t\t\tsetLost()\n\t\t\treturn\n", "\t\t\tlog.Warn(\"the lease could not be renewed in time; the attempt stops\")\n"),
    ("deadline-at-once", LOOP, "keep := w.cfg.Policy.Lease - w.cfg.Heartbeat/2", "keep := time.Duration(0)"),
    ("rv-give-up-after-two-leases", LOOP, "keep := w.cfg.Policy.Lease - w.cfg.Heartbeat/2", "keep := 2*w.cfg.Policy.Lease - w.cfg.Heartbeat/2"),
    ("deadline-without-margin", LOOP, "keep := w.cfg.Policy.Lease - w.cfg.Heartbeat/2", "keep := w.cfg.Policy.Lease"),
    ("deadline-margin-a-heartbeat", LOOP, "keep := w.cfg.Policy.Lease - w.cfg.Heartbeat/2", "keep := w.cfg.Policy.Lease - w.cfg.Heartbeat"),
    ("deadline-counts-from-later", LOOP, "\t\tclaimed := time.Now()\n", "\t\tclaimed := time.Now().Add(time.Second)\n"),
    ("rv-renewed-never-updated", LOOP, "\t\t\t\tdeadline = r.start.Add(keep)\n", ""),
    ("deadline-timer-not-reset", LOOP, "\t\t\t\texpire.Reset(time.Until(deadline))\n", ""),
    ("deadline-from-the-renewal-end", LOOP, "deadline = r.start.Add(keep)", "deadline = time.Now().Add(keep)"),
    ("renewals-overlap", LOOP, "\t\t\tif inFlight {\n\t\t\t\tcontinue\n\t\t\t}\n", ""),
    ("renewal-unbounded", LOOP, "bctx, cancel := context.WithDeadline(ctx, until)", "bctx, cancel := context.WithCancel(ctx)"),
    # Checkpoint and Finish (review M3)
    ("locked-without-lock", LOOP, "\t\tif _, err := jobs.Lock(ctx, tx, a.lease); err != nil {\n\t\t\treturn err\n\t\t}\n", ""),
    ("locked-lease-loss-not-noted", LOOP, "\tif errors.Is(err, jobs.ErrLeaseLost) {\n\t\ta.lost()\n\t}\n", ""),
    ("finish-without-end", LOOP, "\t\treturn end(ctx, tx)\n", "\t\t_ = end\n\t\treturn nil\n"),
    ("finish-cancel-completes", LOOP, "\t\tends = jobs.Cancel\n", "\t\tends = jobs.Complete\n"),
    ("finish-any-end", LOOP, "\tdefault:\n\t\treturn handler.ErrInvalidEnd\n", "\tdefault:\n\t\tends = jobs.Complete\n"),
    ("finish-not-noted", LOOP, "a.finished, a.ended = true, end", "a.finished, a.ended = false, end"),
    ("finish-noted-on-error", LOOP, "\tif err == nil {\n\t\ta.mu.Lock()\n", "\tif true {\n\t\ta.mu.Lock()\n"),
    # The attempt's outcome (review M4, L1, L2, I2, I3)
    ("lost-lease-recorded", LOOP, "\tcase isLost || errors.Is(err, jobs.ErrLeaseLost):\n", "\tcase false && (isLost || errors.Is(err, jobs.ErrLeaseLost)):\n"),
    ("stop-overrides-the-verdict", LOOP, "\t// The handler's own verdict counts even when the worker is stopping:\n", "\tcase ctx.Err() != nil:\n\t\tfailure = jobs.Failure{Code: WorkerStopped, Message: \"the worker stopped during the attempt\", Retryable: true}\n"),
    ("stop-not-retried", LOOP, "Message: \"the worker stopped during the attempt\", Retryable: true}", "Message: \"the worker stopped during the attempt\"}"),
    ("stop-recorded-as-error", LOOP, "\tcase ctx.Err() != nil:\n\t\tfailure = jobs.Failure{Code: WorkerStopped,", "\tcase false:\n\t\tfailure = jobs.Failure{Code: WorkerStopped,"),
    ("stop-recorded-on-a-cancelled-context", LOOP, "context.WithTimeout(context.WithoutCancel(ctx), w.cfg.Heartbeat)", "context.WithTimeout(ctx, w.cfg.Heartbeat)"),
    ("rv-failure-record-unbounded", LOOP, "context.WithTimeout(context.WithoutCancel(ctx), w.cfg.Heartbeat)", "context.WithTimeout(context.WithoutCancel(ctx), 1000*w.cfg.Heartbeat)"),
    ("no-result-retried", LOOP, "Message: \"the handler returned without a result\"}", "Message: \"the handler returned without a result\", Retryable: true}"),
    ("failure-not-unwrapped", LOOP, "\tcase errors.As(err, &f) && f != nil:\n", "\tcase f != nil:\n"),
    ("typed-nil-failure-used", LOOP, "\tcase errors.As(err, &f) && f != nil:\n", "\tcase errors.As(err, &f):\n"),
    ("failure-not-checked", LOOP, "\t\tfailure = checked(*f, log)\n", "\t\tfailure = *f\n"),
    ("checked-any-code", LOOP, "if !failureCode.MatchString(f.Code) {", "if f.Code == \"\" {"),
    ("checked-drops-the-retry-decision", LOOP, "Message: \"the job's handler failed\", Retryable: f.Retryable}", "Message: \"the job's handler failed\", Retryable: true}"),
    ("checked-empty-message", LOOP, "n == 0 || n > 1000", "n > 1000"),
    ("checked-overlong-message", LOOP, "n == 0 || n > 1000", "n == 0"),
    ("panic-retried", LOOP, "failure = jobs.Failure{Code: Internal, Message: \"the job's handler failed\"}", "failure = jobs.Failure{Code: Internal, Message: \"the job's handler failed\", Retryable: true}"),
    ("error-not-retried", LOOP, "Message: \"the job's handler failed\", Retryable: true}", "Message: \"the job's handler failed\"}"),
    ("error-text-stored", LOOP, "Message: \"the job's handler failed\", Retryable: true}", "Message: err.Error(), Retryable: true}"),
    ("panic-not-recovered", LOOP, "\t\tif v := recover(); v != nil {\n", "\t\tif v := any(nil); v != nil {\n"),
    ("panic-value-logged", LOOP, "return fmt.Sprintf(\"handler panicked with a %T\", p.value)", "return fmt.Sprintf(\"handler panicked with %v\", p.value)"),
    ("runtime-panic-message-dropped", LOOP, "return \"handler panicked: \" + re.Error()", "return \"handler panicked: \" + fmt.Sprintf(\"%T\", re)"),
    ("panic-stack-dropped", LOOP, "\"panic\", p.Error(), \"stack\", p.stack)", "\"panic\", p.Error(), \"stack\", \"\")"),
    ("error-logged-raw", LOOP, "\"error\", logText(err))", "\"error\", err.Error())"),
    ("log-text-keeps-controls", LOOP, "\t\tif unicode.IsControl(r) {\n", "\t\tif false && unicode.IsControl(r) {\n"),
    ("log-text-unbounded", LOOP, "\tif len(s) > 500 {\n", "\tif len(s) > 500000 {\n"),
    ("new-heartbeat-unbounded", LOOP, "cfg.Heartbeat*3 > cfg.Policy.Lease", "cfg.Heartbeat > cfg.Policy.Lease"),
    ("new-nil-handler", LOOP, "\t\tif h == nil {\n\t\t\treturn nil, fmt.Errorf(\"worker: no handler for %s\", t)\n\t\t}\n", "\t\t_ = fmt.Errorf\n\t\t_ = h\n"),
    # Configuration and the composition root
    ("config-worker-id-optional", CONFIG, "\tcase w.ID == \"\":\n", "\tcase false:\n"),
    ("config-worker-id-any", CONFIG, "r < 0x21 || r > 0x7e", "r < 0x20"),
    ("config-blank-allowlist", CONFIG, "case strings.TrimSpace(raw) == \"\":", "case raw == \"\":"),
    ("cmd-starts-without-handlers", CMD, "\tif len(handlers) == 0 {\n", "\tif len(handlers) < 0 {\n"),
    ("cmd-allowlist-checked-late", CMD, "\tif _, err := workerscope.New(nil, wcfg.Organizations); err != nil {\n\t\tlog.Error(\"invalid configuration\", \"error\", \"WORKER_ORGANIZATION_IDS must list organization IDs, each once\")\n\t\treturn exitConfig\n\t}\n", ""),
    ("cmd-no-role-check", CMD, "\terr = tenant.CheckRuntimeRole(checkCtx, pool, tenant.WorkerRole)\n", "\t_ = checkCtx\n\terr = nil\n"),
    ("rv-cmd-no-pool-guard", CMD, "\tcfg.AfterRelease = tenant.Guard\n", "\t_ = tenant.Guard\n"),
    ("cmd-second-signal-swallowed", CMD, "\t\tsignal.Stop(signals)\n\t\tcancel()\n", "\t\tcancel()\n"),
    ("cmd-stop-before-start-fails", CMD, "\tif ctx.Err() != nil {\n\t\t// A signal before the loop began", "\tif false {\n\t\t// A signal before the loop began"),
    # The architecture rule
    ("rule-exempts-everything", RULES, 'except:  []string{"./cmd/worker/...", "./internal/worker/...", "./internal/tenant/workerscope/..."},', 'except:  []string{"./..."},'),
]
EQUIVALENT = {
    "claim-no-org-filter": "RLS confines the worker's transaction to the organization its scope sets, so the filter is defence in depth; no test can see it go (the application filter is required by AGENTS.md, and kept)",
    "claim-ignores-attempts": "migration 0007 refuses a queued job without an attempt left, for every role (review L3), so no row the claim can see has its attempts spent; the condition is defence in depth",
    "cancel-queued-any-org": "as claim-no-org-filter: RLS confines the API's transaction to the organization it selected, so another organization's job is not there to cancel",
    "fence-any-status": "every write that ends a running job also clears locked_by, which the fence still requires, so a finished job never matches",
    "renewal-unbounded": "the renewal's bound is the deadline, which the expire timer enforces as well: at the deadline it stops the handler and so cancels the renewal's parent context; the bound on the renewal itself is defence in depth (only one renewal is in flight, so the deadline cannot move while it runs)",
}


def run(overlay: str | None, pkg: str) -> str:
    env = dict(os.environ, GEMINUS_REQUIRE_DB_TESTS="1")
    cmd = ["go", "test", "-count=1", "-timeout=300s", pkg]
    if overlay:
        cmd.insert(2, "-overlay=" + overlay)
    r = subprocess.run(cmd, cwd=REPO, env=env, capture_output=True, text=True, timeout=900)
    if r.returncode:
        if "--- FAIL" in r.stdout:
            return "KILLED\t" + next(l.strip() for l in r.stdout.splitlines() if l.startswith("--- FAIL"))
        if "panic: test timed out" in r.stdout:
            return "KILLED\ttest timed out (a hang the tests do not bound)"
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
