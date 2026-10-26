#!/usr/bin/env python3
"""T-053 mutation pass.

Each mutant replaces text that occurs exactly once in one source file, is
built with `go vet` on the packages it names (a mutant that does not build
is reported INVALID, never as killed), then those packages' tests run with
-count=1. A unit mutant is KILLED when the tests fail and SURVIVED when they
pass. A DB mutant is only built here: its tests need PostgreSQL
(GEMINUS_TEST_DATABASE_URL), so without it its result is NOT RUN, never
killed. Every file is restored in a finally block; run it from the
repository root with Go 1.27.3 first on PATH:

    python3 checks/evidence/2026-10-26-T-053/mutants.py [--only U1,U2] > log

It makes no network call and reads no environment value besides PATH,
HOME, GOPATH/GOCACHE/GOFLAGS (passed through to go) and
GEMINUS_TEST_DATABASE_URL's presence.
"""
import os
import subprocess
import sys

M = "./internal/ai/provider/"
A = "./internal/ai/provider/anthropic/"
S = "./internal/ai/spend/"
AP = "./internal/config/aiapproval/"
C = "./internal/config/"
E = "./internal/aievals/"
P = "./internal/proposals/"
G = "./internal/proposals/generator/"
AU = "./internal/audit/"
T = "./internal/observability/telemetry/"
L = "./internal/storage/aispend/"

# (id, what it breaks, file, [(old, new)], packages, db)
MUTANTS = [
    # spend: prices and bounds
    ("U1", "Cost ignores output tokens", "internal/ai/spend/spend.go",
     [("c = add(c, mul(t.Output, p.Output))", "_ = t.Output")], [S, M], False),
    ("U2", "AttemptBound ignores max_tokens", "internal/ai/spend/spend.go",
     [("return add(mul(inputTokens, p.Input), mul(outputTokens, p.Output))", "return mul(inputTokens, p.Input)")], [S, M], False),
    ("U3", "cache writes at the cheaper 5m rate", "internal/ai/spend/spend.go",
     [("max(p.CacheWrite1h, p.CacheWrite5m)", "p.CacheWrite5m")], [S, M], False),
    # provider: reserve / settle / release
    ("U4", "the reservation ignores retries", "internal/ai/provider/meter.go",
     [("most := mulSat(per, int64(1+max(0, req.MaxRetries)))", "most := per")], [M, A], False),
    ("U5", "a quota refusal becomes spend_unavailable", "internal/ai/provider/meter.go",
     [("if errors.As(err, &pe) && pe.Code == CodeQuotaExceeded {", "if false && errors.As(err, &pe) && pe.Code == CodeQuotaExceeded {")], [M, A], False),
    ("U6", "a reservation without an ID is accepted", "internal/ai/provider/meter.go",
     [('if res.ID == "" {', "if false {")], [M], False),
    ("U7", "attempts without an answer are not charged", "internal/ai/provider/meter.go",
     [("case resp.UnknownAttempts > 0:", "case false:")], [M, A], False),
    ("U8", "the settlement may exceed the reservation", "internal/ai/provider/meter.go",
     [("if s.NanoUSD > m.res.Charge.NanoUSD {", "if false {")], [M, A], False),
    ("U9", "settle runs on the caller's (maybe cancelled) context", "internal/ai/provider/meter.go",
     [("sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)", "sctx, cancel := context.WithTimeout(ctx, settleTimeout)")], [M], False),
    ("U10", "a call is never settled (reservation never released)", "internal/ai/provider/provider.go",
     [("if m != nil {\n\t\tc.settle(", "if false && m != nil {\n\t\tc.settle(")], [M, A], False),
    ("U11", "New accepts a billable backend (no meter)", "internal/ai/provider/provider.go",
     [("if _, ok := b.(Billable); ok {", "if _, ok := b.(Billable); ok && false {")], [M, A], False),
    ("U12", "MemoryMeter refuses a charge that exactly fits", "internal/ai/provider/meter.go",
     [("if c.NanoUSD > m.limit-m.committed {", "if c.NanoUSD >= m.limit-m.committed {")], [M, E], False),
    ("U13", "MemoryMeter settle keeps the whole reservation", "internal/ai/provider/meter.go",
     [("m.committed += s.NanoUSD - reserved", "_ = reserved")], [M, E], False),
    ("U14", "a metered call without attribution is accepted", "internal/ai/provider/provider.go",
     [("if c.meter != nil && !attr.complete() {", "if false {")], [M], False),
    ("U15", "a refused reservation still calls the provider", "internal/ai/provider/provider.go",
     [("if m, err = c.reserve(ctx, org, role, attr, req); err != nil {\n\t\t\treturn nil, meta, err\n\t\t}",
       "if m, err = c.reserve(ctx, org, role, attr, req); err != nil {\n\t\t\tm, err = &metering{}, nil\n\t\t}")], [M, A], False),
    ("U16", "the quota limit is not passed on", "internal/ai/provider/meter.go",
     [("return nil, &Error{Code: CodeQuotaExceeded, Limit: pe.Limit}", "return nil, &Error{Code: CodeQuotaExceeded}")], [M], False),
    ("U17", "spend_unavailable is not retryable", "internal/ai/provider/errors.go",
     [("CodeSpendUnavailable: true, // no call", "CodeSpendUnavailable: false, // no call")], [M], False),
    # anthropic adapter
    ("U18", "the adapter calls without a reservation", "internal/ai/provider/anthropic/anthropic.go",
     [('if req.Reservation == "" {', "if false {")], [A], False),
    ("U19", "the adapter serves any prompt role", "internal/ai/provider/anthropic/anthropic.go",
     [("if b.allows == nil || !b.allows(string(req.Prompt.Role)) {", "if b.allows == nil {")], [A], False),
    ("U20", "a transport failure is not counted", "internal/ai/provider/anthropic/anthropic.go",
     [("if err != nil {\n\t\tt.unknown.Add(1)", "if err != nil {\n\t\t_ = t")], [A], False),
    ("U21", "a cut body is not counted", "internal/ai/provider/anthropic/anthropic.go",
     [("b.tally.unknown.Add(1)", "_ = b.tally")], [A], False),
    ("U22", "New accepts another model", "internal/ai/provider/anthropic/anthropic.go",
     [("if cfg.Model != cfg.Approval.Model() {", "if false {")], [A, AP], False),
    ("U23", "New accepts other credentials", "internal/ai/provider/anthropic/anthropic.go",
     [("if !slices.Equal(labels, cfg.Approval.Credentials()) {", "if !slices.Equal(labels, labels) {")], [A, AP], False),
    ("U24", "New accepts no approval", "internal/ai/provider/anthropic/anthropic.go",
     [("if !cfg.Approval.Valid() {", "if false {")], [A, AP], False),
    # approval record
    ("U25", "the record needs no retention confirmation", "internal/config/aiapproval/aiapproval.go",
     [("case !r.Retention.Given() || (r.Retention.Arrangement != RetentionStandard && r.Retention.Arrangement != RetentionZero):", "case false:")], [AP, C, E], False),
    ("U26", "the record needs no spend-limit confirmation", "internal/config/aiapproval/aiapproval.go",
     [("case !r.SpendLimits.Given():", "case false:")], [AP, C], False),
    ("U27", "the record may name any role", "internal/config/aiapproval/aiapproval.go",
     [("slices.ContainsFunc(r.DataPolicy.Roles, func(role string) bool { return !slices.Contains(liveRoles, role) })", "false")], [AP], False),
    ("U28", "the record may keep raw text in logs", "internal/config/aiapproval/aiapproval.go",
     [("r.DataPolicy.LogsKeepRawText ||", "false ||")], [AP], False),
    ("U29", "Valid does not recheck the record", "internal/config/aiapproval/aiapproval.go",
     [('return ok && r.Gap() == "" && reflect.DeepEqual(r, *a.rec)', 'return ok && r.Gap() == "" && reflect.DeepEqual(r, r) && a.rec != nil')], [AP], False),
    ("U30", "AllowsRole ignores the data policy", "internal/config/aiapproval/aiapproval.go",
     [('return a.Valid() && role != "" && slices.Contains(a.rec.DataPolicy.Roles, role)', 'return a.Valid() && role != ""')], [AP, A], False),
    # configuration gate
    ("U31", "the switch is not needed", "internal/config/ai.go",
     [("case !on:\n\t\t\twhy = LiveOffSwitch", "case false:\n\t\t\twhy = LiveOffSwitch")], [C, AP, E], False),
    ("U32", "any AI_MODEL is accepted", "internal/config/ai.go",
     [("case model != rec.Model:", "case false:")], [C, AP], False),
    ("U33", "the price check is skipped", "internal/config/ai.go",
     [("case !priced:", "case !priced && false:")], [C, AP], False),
    ("U34", "the record's gap is ignored", "internal/config/ai.go",
     [('case rec.Gap() != "":', "case false:")], [C, AP, E], False),
    ("U35", "one key is enough", "internal/config/ai.go",
     [('case v == "":', 'case v == "" && len(creds) == 0:')], [C, AP], False),
    ("U36", "the secondary key may be the primary key", "internal/config/ai.go",
     [("case len(creds) > 0 && creds[0].Key.Reveal() == v:", "case false:")], [C, AP], False),
    ("U37", "the monthly limit may exceed the budget", "internal/config/ai.go",
     [("if l.Month > budget {", "if false {")], [C, AP], False),
    # live eval command
    ("U38", "the live eval runs while live AI is off", "internal/aievals/main.go",
     [("if !ai.LiveEnabled() {", "if false {")], [E, AP], False),
    ("U39", "a live report may take any name", "internal/aievals/live.go",
     [("if !liveName.MatchString(filepath.Base(path)) {", "if false {")], [E, A], False),
    ("U40", "a ready safe-failure case breaks no gate", "internal/aievals/live.go",
     [("r.Gates[GateSafeFailureReady]++", "r.Gates[GateSafeFailureReady] += 0")], [E], False),
    ("U41", "the run goes on after its cap refuses", "internal/aievals/live.go",
     [('if o.Code == "AI_QUOTA_EXCEEDED" {', "if false {")], [E], False),
    ("U42", "the run's cap may exceed the monthly limit", "internal/aievals/main.go",
     [("if capNano > ai.Limits.Month {", "if false {")], [E, AP], False),
    # public errors, logs, audit, telemetry
    ("U43", "a spend limit is not RATE_LIMITED in the API", "internal/proposals/public.go",
     [('"AI_QUOTA_EXCEEDED":    "RATE_LIMITED",\n', "")], [P], False),
    ("U44", "the generator has no message for a spend limit", "internal/proposals/generator/generator.go",
     [('provider.CodeQuotaExceeded:    "a live AI spend limit was reached; no provider call was made",\n', "")], [G], False),
    ("U45", "the refusal's limit may be any label", "internal/audit/audit.go",
     [('"limit": oneOf("organization_day", "day", "month")', '"limit": {shape: label}')], [AU], False),
    ("U46", "an ID-shaped model name reaches a metric", "internal/observability/telemetry/telemetry.go",
     [("if observability.Fits(observability.KindID, m) {", "if false {")], [T], False),
    # DB ledger (built here; killed only by the PostgreSQL tests)
    ("D1", "the month limit is not checked", "internal/storage/aispend/aispend.go",
     [("WHERE period = 'month' AND starts_on = $2 AND committed_nano_usd + $1 <= $3`, []any{c.NanoUSD, month, l.Month}}",
       "WHERE period = 'month' AND starts_on = $2`, []any{c.NanoUSD, month}}")], [L], True),
    ("D2", "the deployment day limit is not checked", "internal/storage/aispend/aispend.go",
     [("WHERE period = 'day' AND starts_on = $2 AND committed_nano_usd + $1 <= $3`, []any{c.NanoUSD, day, l.Day}}",
       "WHERE period = 'day' AND starts_on = $2`, []any{c.NanoUSD, day}}")], [L], True),
    ("D3", "the organization day limit is not checked", "internal/storage/aispend/aispend.go",
     [("WHERE organization_id = $4 AND day = $2 AND committed_nano_usd + $1 <= $3`, []any{c.NanoUSD, day, l.OrganizationDay, c.OrganizationID}}",
       "WHERE organization_id = $3 AND day = $2`, []any{c.NanoUSD, day, c.OrganizationID}}")], [L], True),
    ("D4", "settle moves the totals the wrong way", "internal/storage/aispend/aispend.go",
     [("delta := s.NanoUSD - reserved", "delta := reserved - s.NanoUSD")], [L], True),
    ("D5", "settle does not check the state", "internal/storage/aispend/aispend.go",
     [('if state != "reserved" {', 'if false && state != "reserved" {')], [L], True),
    ("D6", "settle charges today instead of the reservation's day", "internal/storage/aispend/aispend.go",
     [("[]any{delta, day}},", "[]any{delta, spend.Day(m.cfg.Now())}},")], [L], True),
    ("D7", "a reservation is not audited", "internal/storage/aispend/aispend.go",
     [('return m.event(ctx, tx, c.OrganizationID, "ai_spend.reserve", c.Attribution, map[string]any{',
       'return func(...any) error { return nil }(ctx, tx, c.OrganizationID, "ai_spend.reserve", c.Attribution, map[string]any{')], [L], True),
    ("D8", "the generator charges no target", "internal/proposals/generator/generator.go",
     [("pc.Attribution = attr", "_ = attr")], [G], True),
    ("D9", "settle reads without the organization filter (RLS still filters)", "internal/storage/aispend/aispend.go",
     [("FROM twin.ai_spend_reservations WHERE organization_id = $1 AND id = $2 FOR UPDATE`, org, r.ID)",
       "FROM twin.ai_spend_reservations WHERE id = $1 FOR UPDATE`, r.ID)")], [L], True),
]


def run(args):
    p = subprocess.run(args, capture_output=True, text=True)
    return p.returncode, p.stdout + p.stderr


def failing(out):
    return sorted({l.split()[2] for l in out.splitlines() if l.startswith("--- FAIL: ")})[:6]


def main():
    only = None
    if len(sys.argv) > 2 and sys.argv[1] == "--only":
        only = set(sys.argv[2].split(","))
    has_db = bool(os.environ.get("GEMINUS_TEST_DATABASE_URL"))
    counts = {}
    for mid, what, path, edits, pkgs, db in MUTANTS:
        if only and mid not in only:
            continue
        with open(path, encoding="utf-8") as f:
            orig = f.read()
        mutated = orig
        bad = None
        for old, new in edits:
            n = mutated.count(old)
            if n != 1:
                bad = f"pattern occurs {n} times"
                break
            mutated = mutated.replace(old, new)
        if bad:
            result, detail = "ERROR", bad
        else:
            try:
                with open(path, "w", encoding="utf-8") as f:
                    f.write(mutated)
                code, out = run(["go", "vet", *pkgs])
                if code != 0:
                    result, detail = "INVALID", "does not build: " + out.strip().splitlines()[-1][:160]
                else:
                    code, out = run(["go", "test", "-count=1", *pkgs])
                    if db and not has_db:
                        if code != 0:
                            result, detail = "KILLED", "by the unit tests: " + ", ".join(failing(out))
                        else:
                            result, detail = "BUILDS", "NOT RUN: its tests need PostgreSQL (GEMINUS_TEST_DATABASE_URL unset)"
                    elif code != 0:
                        result, detail = "KILLED", ", ".join(failing(out)) or out.strip().splitlines()[-1][:160]
                    else:
                        result, detail = "SURVIVED", "tests pass"
            finally:
                with open(path, "w", encoding="utf-8") as f:
                    f.write(orig)
        counts[result] = counts.get(result, 0) + 1
        print(f"{mid}\t{result}\t{what}\t{path}\t{' '.join(pkgs)}\t{detail}", flush=True)
    code, out = run(["git", "status", "--porcelain", "--", "internal", "cmd"])
    print("summary\t" + " ".join(f"{k}={v}" for k, v in sorted(counts.items())))
    print("sources restored (git status of internal/ and cmd/ against the last commit): " + ("clean" if not out.strip() else "CHANGED:\n" + out))
    return 0 if counts.get("SURVIVED", 0) == 0 and counts.get("ERROR", 0) == 0 and counts.get("INVALID", 0) == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
