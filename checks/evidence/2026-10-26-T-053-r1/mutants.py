#!/usr/bin/env python3
"""T-053 review round 1: the mutation pass after the fixes.

The author's harness (checks/evidence/2026-10-26-T-053/mutants.py) with its
mutants brought to the fixed code and the round's new ones: the ledger's
organization filter (F, M1), the database's clock (C, L3), a settlement
above its reservation (O, L1), the inference geo (G, M2), failover and the
unanswered attempts (R, M3, I1), the spend record and the run's cap (K, H1,
L6), and the keys (W, L2).

Each mutant replaces text that occurs exactly once in one source file, is
built with `go vet` on the packages it names (a mutant that does not build
is INVALID, never killed), then those packages' tests run with -count=1. A
mutant is KILLED when the tests fail, SURVIVED when they pass. A mutant
marked equivalent is expected to survive (the reason is in its text); it
is reported EQUIVALENT if it does and KILLED otherwise, and is not a
failure. DB mutants need GEMINUS_TEST_DATABASE_URL (a dt_test_* database
prepared by make db-test) and GEMINUS_REQUIRE_DB_TESTS=1; without it the
harness refuses to run them. Every file is restored in a finally block and
its hash compared with the one before.

Run it in a `git archive HEAD` copy, never the worktree, with Go 1.27.3:

    python3 mutants.py <copy> [--only U1,R1] > log

No network call; no environment value is read besides PATH, HOME, GO* and
the two DB-test variables (passed through to go).
"""
import hashlib
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

SP = "internal/ai/spend/spend.go"
MT = "internal/ai/provider/meter.go"
PR = "internal/ai/provider/provider.go"
AN = "internal/ai/provider/anthropic/anthropic.go"
AA = "internal/config/aiapproval/aiapproval.go"
CA = "internal/config/ai.go"
MA = "internal/aievals/main.go"
LV = "internal/aievals/live.go"
RC = "internal/aievals/record.go"
LS = "internal/storage/aispend/aispend.go"

# (id, what it breaks, file, [(old, new)], packages, db, equivalent)
MUTANTS = [
    # --- the author's mutants (U1-U46, D1-D9), brought to the fixed code ---
    ("U1", "Cost ignores output tokens", SP, [("c = add(c, mul(t.Output, p.Output))", "_ = t.Output")], [S, M], False, False),
    ("U2", "AttemptBound ignores max_tokens", SP,
     [("return add(mul(inputTokens, p.Input), mul(outputTokens, p.Output))", "return mul(inputTokens, p.Input)")], [S, M], False, False),
    ("U3", "cache writes at the cheaper 5m rate", SP, [("max(p.CacheWrite1h, p.CacheWrite5m)", "p.CacheWrite5m")], [S, M], False, False),
    ("U4", "the reservation ignores retries", MT,
     [("most := mulSat(per, int64(1+max(0, req.MaxRetries)))", "most := per")], [M, A], False, False),
    ("U5", "a quota refusal becomes spend_unavailable", MT,
     [("if errors.As(err, &pe) && pe.Code == CodeQuotaExceeded {", "if false && errors.As(err, &pe) && pe.Code == CodeQuotaExceeded {")], [M, A], False, False),
    ("U6", "a reservation without an ID is accepted", MT, [('if res.ID == "" {', "if false {")], [M], False, False),
    ("U7", "attempts without an answer are not charged", MT, [("case resp.UnknownAttempts > 0:", "case false:")], [M, A], False, False),
    # U8 (the clamp removed) is retired: review round 1 M3 removed the clamp
    # itself; R5 below puts it back and must be killed.
    ("U9", "settle runs on the caller's (maybe cancelled) context", MT,
     [("sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)", "sctx, cancel := context.WithTimeout(ctx, settleTimeout)")],
     [M], False, False),
    ("U10", "a call is never settled (reservation never released)", PR,
     [("if m != nil {\n\t\tc.settle(", "if false && m != nil {\n\t\tc.settle(")], [M, A], False, False),
    ("U11", "New accepts a billable backend (no meter)", PR,
     [("if _, ok := b.(Billable); ok {", "if _, ok := b.(Billable); ok && false {")], [M, A], False, False),
    ("U12", "MemoryMeter refuses a charge that exactly fits", MT,
     [("if c.NanoUSD > m.limit-m.committed {", "if c.NanoUSD >= m.limit-m.committed {")], [M, E], False, False),
    ("U13", "MemoryMeter settle keeps the whole reservation", MT, [("m.committed += s.NanoUSD - reserved", "_ = reserved")], [M, E], False, False),
    ("U14", "a metered call without attribution is accepted", PR, [("if c.meter != nil && !attr.complete() {", "if false {")], [M], False, False),
    ("U15", "a refused reservation still calls the provider", PR,
     [("if m, err = c.reserve(ctx, org, role, attr, req); err != nil {\n\t\t\treturn nil, meta, err\n\t\t}",
       "if m, err = c.reserve(ctx, org, role, attr, req); err != nil {\n\t\t\tm, err = &metering{}, nil\n\t\t}")], [M, A], False, False),
    ("U16", "the quota limit is not passed on", MT,
     [("return nil, &Error{Code: CodeQuotaExceeded, Limit: pe.Limit}", "return nil, &Error{Code: CodeQuotaExceeded}")], [M], False, False),
    ("U17", "spend_unavailable is not retryable", "internal/ai/provider/errors.go",
     [("CodeSpendUnavailable: true, // no call", "CodeSpendUnavailable: false, // no call")], [M], False, False),
    ("U18", "the adapter calls without a reservation", AN, [('if req.Reservation == "" {', "if false {")], [A], False, False),
    ("U19", "the adapter serves any prompt role", AN,
     [("if b.allows == nil || !b.allows(string(req.Prompt.Role)) {", "if b.allows == nil {")], [A], False, False),
    ("U20", "a transport failure is not counted", AN, [("if err != nil {\n\t\tt.unknown.Add(1)", "if err != nil {\n\t\t_ = t")], [A], False, False),
    ("U21", "a cut body is not counted", AN, [("b.tally.unknown.Add(1)", "_ = b.tally")], [A], False, False),
    ("U22", "New accepts another model", AN, [("if cfg.Model != cfg.Approval.Model() {", "if false {")], [A, AP], False, False),
    ("U23", "New accepts other credentials", AN,
     [("if !slices.Equal(labels, cfg.Approval.Credentials()) {", "if !slices.Equal(labels, labels) {")], [A, AP], False, False),
    ("U24", "New accepts no approval", AN, [("if !cfg.Approval.Valid() {", "if false {")], [A, AP], False, False),
    ("U25", "the record needs no retention confirmation", AA,
     [("case !r.Retention.Given() || (r.Retention.Arrangement != RetentionStandard && r.Retention.Arrangement != RetentionZero):", "case false:")],
     [AP, C, E], False, False),
    ("U26", "the record needs no spend-limit confirmation", AA, [("case !r.SpendLimits.Given():", "case false:")], [AP, C], False, False),
    ("U27", "the record may name any role", AA,
     [("slices.ContainsFunc(r.DataPolicy.Roles, func(role string) bool { return !slices.Contains(liveRoles, role) })", "false")], [AP], False, False),
    ("U28", "the record may keep raw text in logs", AA, [("r.DataPolicy.LogsKeepRawText ||", "false ||")], [AP], False, False),
    ("U29", "Valid does not recheck the record", AA,
     [('return ok && r.Gap() == "" && reflect.DeepEqual(r, *a.rec)', 'return ok && r.Gap() == "" && reflect.DeepEqual(r, r) && a.rec != nil')],
     [AP], False, False),
    ("U30", "AllowsRole ignores the data policy", AA,
     [('return a.Valid() && role != "" && slices.Contains(a.rec.DataPolicy.Roles, role)', 'return a.Valid() && role != ""')], [AP, A], False, False),
    ("U31", "the switch is not needed", CA, [("case !on:\n\t\t\twhy = LiveOffSwitch", "case false:\n\t\t\twhy = LiveOffSwitch")], [C, AP, E], False, False),
    ("U32", "any AI_MODEL is accepted", CA, [("case model != rec.Model:", "case false:")], [C, AP], False, False),
    ("U33", "the price check is skipped", CA, [("case !priced:", "case !priced && false:")], [C, AP], False, False),
    ("U34", "the record's gap is ignored", CA, [('case rec.Gap() != "":', "case false:")], [C, AP, E], False, False),
    ("U35", "one key is enough", CA, [('case v == "":', 'case v == "" && len(creds) == 0:')], [C, AP], False, False),
    ("U36", "the secondary key may be the primary key", CA, [("case len(creds) > 0 && creds[0].Key.Reveal() == v:", "case false:")], [C, AP], False, False),
    ("U37", "the monthly limit may exceed the budget", CA, [("if l.Month > budget {", "if false {")], [C, AP], False, False),
    ("U38", "the live eval runs while live AI is off", MA, [("if !ai.LiveEnabled() {", "if false {")], [E, AP], False, False),
    ("U39", "a live report may take any name", LV, [("if !liveName.MatchString(filepath.Base(path)) {", "if false {")], [E, A], False, False),
    ("U40", "a ready safe-failure case breaks no gate", LV, [("r.Gates[GateSafeFailureReady]++", "r.Gates[GateSafeFailureReady] += 0")], [E], False, False),
    ("U41", "the run goes on after its cap refuses", LV, [('if o.Code == "AI_QUOTA_EXCEEDED" {', "if false {")], [E], False, False),
    # U42 (the cap above the monthly limit) is retired with its check: the
    # cap is now held to the daily limit (K1), which config keeps within the
    # month, and to what the record leaves of the month (K4).
    ("U43", "a spend limit is not RATE_LIMITED in the API", "internal/proposals/public.go",
     [('"AI_QUOTA_EXCEEDED":    "RATE_LIMITED",\n', "")], [P], False, False),
    ("U44", "the generator has no message for a spend limit", "internal/proposals/generator/generator.go",
     [('provider.CodeQuotaExceeded:    "a live AI spend limit was reached; no provider call was made",\n', "")], [G], False, False),
    ("U45", "the refusal's limit may be any label", "internal/audit/audit.go",
     [('"limit": oneOf("organization_day", "day", "month")', '"limit": {shape: label}')], [AU], False, False),
    ("U46", "an ID-shaped model name reaches a metric", "internal/observability/telemetry/telemetry.go",
     [("if observability.Fits(observability.KindID, m) {", "if false {")], [T], False, False),
    ("D1", "the month limit is not checked", LS,
     [("WHERE period = 'month' AND starts_on = $2 AND committed_nano_usd + $1 <= $3`, []any{c.NanoUSD, month, l.Month}}",
       "WHERE period = 'month' AND starts_on = $2`, []any{c.NanoUSD, month}}")], [L], True, False),
    ("D2", "the deployment day limit is not checked", LS,
     [("WHERE period = 'day' AND starts_on = $2 AND committed_nano_usd + $1 <= $3`, []any{c.NanoUSD, day, l.Day}}",
       "WHERE period = 'day' AND starts_on = $2`, []any{c.NanoUSD, day}}")], [L], True, False),
    ("D3", "the organization day limit is not checked", LS,
     [("WHERE organization_id = $4 AND day = $2 AND committed_nano_usd + $1 <= $3`, []any{c.NanoUSD, day, l.OrganizationDay, c.OrganizationID}}",
       "WHERE organization_id = $3 AND day = $2`, []any{c.NanoUSD, day, c.OrganizationID}}")], [L], True, False),
    ("D4", "settle moves the totals the wrong way", LS, [("delta := s.NanoUSD - reserved", "delta := reserved - s.NanoUSD")], [L], True, False),
    ("D5", "settle does not check the state", LS, [('if state != "reserved" {', 'if false && state != "reserved" {')], [L], True, False),
    ("D6", "settle charges today instead of the reservation's day", LS,
     [("[]any{delta, day}},", "[]any{delta, spend.Day(time.Now())}},")], [L], True, False),
    ("D7", "a reservation is not audited", LS,
     [('return m.event(ctx, tx, c.OrganizationID, "ai_spend.reserve", c.Attribution, map[string]any{',
       'return func(...any) error { return nil }(ctx, tx, c.OrganizationID, "ai_spend.reserve", c.Attribution, map[string]any{')], [L], True, False),
    ("D8", "the generator charges no target", "internal/proposals/generator/generator.go", [("pc.Attribution = attr", "_ = attr")], [G], True, False),
    ("D9", "settle reads without the organization filter (RLS still filters)", LS,
     [("FROM twin.ai_spend_reservations WHERE organization_id = $1 AND id = $2 FOR UPDATE`, org, r.ID)",
       "FROM twin.ai_spend_reservations WHERE id = $1 FOR UPDATE`, r.ID)")], [L], True, False),

    # --- M1: the ledger's organization filter (the reviewer's F1-F3, and two more) ---
    ("F1", "reserve's organization-day UPDATE without the organization filter", LS,
     [("WHERE organization_id = $4 AND day = $2 AND committed_nano_usd + $1 <= $3`",
       "WHERE $4::text IS NOT NULL AND day = $2 AND committed_nano_usd + $1 <= $3`")], [L], True, False),
    ("F2", "TotalsAt reads the day without the organization filter", LS,
     [("coalesce((SELECT committed_nano_usd FROM twin.ai_spend_days WHERE organization_id = $3 AND day = $2), 0)`",
       "coalesce((SELECT committed_nano_usd FROM twin.ai_spend_days WHERE $3::text IS NOT NULL AND day = $2), 0)`")], [L], True, False),
    ("F3", "settle's reservation UPDATE without the organization filter (equivalent: the filtered SELECT ... FOR UPDATE of the same "
     "transaction has found the reservation in this organization, and IDs are unique)", LS,
     [("settled_at = now() WHERE organization_id = $1 AND id = $2`", "settled_at = now() WHERE $1::text IS NOT NULL AND id = $2`")],
     [L], True, True),
    ("F4", "reserve's audit reads the organization day without the organization filter", LS,
     [("`SELECT committed_nano_usd FROM twin.ai_spend_days WHERE organization_id = $1 AND day = $2`,\n\t\t\tc.OrganizationID, day)",
       "`SELECT committed_nano_usd FROM twin.ai_spend_days WHERE $1::text IS NOT NULL AND day = $2`,\n\t\t\tc.OrganizationID, day)")],
     [L], True, False),
    ("F5", "settle's organization-day UPDATE without the organization filter", LS,
     [("WHERE organization_id = $3 AND day = $2`,\n\t\t\t\t[]any{delta, day, org}}", "WHERE $3::text IS NOT NULL AND day = $2`,\n\t\t\t\t[]any{delta, day, org}}")],
     [L], True, False),

    # --- L3: the database's clock ---
    ("C1", "the day is the session time zone's date, not the UTC date", LS,
     [("`SELECT (now() AT TIME ZONE 'UTC')::date`", "`SELECT now()::date`")], [L], True, False),
    ("C2", "an unpinned meter takes the zero day", LS, [("if !m.day.IsZero() {", "if true {")], [L, G], True, False),

    # --- L1: a settlement above its reservation ---
    ("O1", "a settlement above its reservation is not flagged in the event", LS,
     [('if over = s.NanoUSD > reserved; over {', 'if over = s.NanoUSD > reserved; false {')], [L], True, False),
    ("O2", "a settlement above its reservation is not counted", LS,
     [("\t\ttel.SpendWarning(ctx, model, role, telemetry.SpendOverReservation)\n", "")], [L], True, False),
    ("O3", "a settlement above its reservation is refused (under-records what was billed)", LS,
     [("delta := s.NanoUSD - reserved", "if s.NanoUSD > reserved {\n\t\t\treturn errors.New(\"over\")\n\t\t}\n\t\tdelta := s.NanoUSD - reserved")],
     [L], True, False),
    ("O4", "an unknown geo is not counted", LS, [("\t\ttel.SpendWarning(ctx, model, role, telemetry.SpendGeoUnknown)\n", "")], [L], True, False),
    ("O5", "the ledger's own check of the geo is skipped (equivalent: the audit catalogue's oneOf(global, us, unknown) refuses the "
     "same settlement's ai_spend.settle event inside the transaction, which rolls back; the settlement fails and nothing is written "
     "either way, mutation run 1)", LS,
     [('case s.InferenceGeo != "" && spend.Geo(s.InferenceGeo) != s.InferenceGeo:', "case false:")], [L], True, True),

    # --- M2: the inference geo ---
    ("G1", "the reservation is at the list price (global)", MT,
     [("per := spend.AtDearestGeo(price.AttemptBound(q.MaxInputTokens, req.MaxOutputTokens))",
       "per := price.AttemptBound(q.MaxInputTokens, req.MaxOutputTokens)")], [M, A, E], False, False),
    ("G2", "the answer is priced at the list price whatever its geo", MT,
     [("CacheRead: u.CacheReadInputTokens}), s.InferenceGeo)", "CacheRead: u.CacheReadInputTokens}), spend.GeoGlobal)")], [M, A], False, False),
    ("G3", "an unknown geo is priced at the list price", SP, [("tenths = dearestTenths", "tenths = 10")], [S, M, A], False, False),
    ("G4", "spend.Geo passes the provider's text on", SP, [("\treturn GeoUnknown\n}", "\treturn reported\n}")], [S, M, A], False, False),
    ("G5", "the adapter drops usage.inference_geo", AN,
     [("resp.InferenceGeo = msg.Usage.InferenceGeo // untrusted: priced through spend.Geo only", "_ = msg.Usage.InferenceGeo")], [A], False, False),
    ("G6", "the request pins inference_geo to global", AN,
     [("OutputConfig: sdk.OutputConfigParam{Format: sdk.JSONOutputFormatParam{Schema: proj.Schema}},",
       "OutputConfig: sdk.OutputConfigParam{Format: sdk.JSONOutputFormatParam{Schema: proj.Schema}},\n\t\tInferenceGeo: sdk.String(\"global\"),")],
     [A], False, False),
    ("G7", "a geo's price rounds down", SP, [("return add(whole, (r*tenths+9)/10)", "return add(whole, (r*tenths)/10)")], [S, M], False, False),
    ("G8", "an unknown geo is not warned of (generator)", "internal/proposals/generator/generator.go",
     [("if m.InferenceGeo == spend.GeoUnknown {", "if m.InferenceGeo == spend.GeoUnknown && false {")], [G], False, False),

    # --- M3, I1: failover and unanswered attempts ---
    ("R1", "failover after an attempt that may have been billed", AN,
     [("if t.unknown.Load() > unknownBefore {", "if t.unknown.Load() > unknownBefore && false {")], [A], False, False),
    ("R2", "an undecodable 2xx answer is not counted", AN,
     [("t.unknown.Add(1)\n\t\t\t\tresp.Undecoded = true", "resp.Undecoded = true")], [A], False, False),
    ("R3", "an undecodable answer settles at its attempts, not the reservation", MT, [("if resp.Undecoded {", "if false {")], [M, A], False, False),
    ("R4", "a cut body on the last attempt counts twice", AN, [("t.body.Load() != bodyCut", "true")], [A], False, False),
    ("R5", "the settlement is clamped to the reservation again", MT,
     [("s.NanoUSD = addSat(cost, mulSat(int64(resp.UnknownAttempts), m.perAttempt))",
       "s.NanoUSD = min(addSat(cost, mulSat(int64(resp.UnknownAttempts), m.perAttempt)), m.res.Charge.NanoUSD)")], [M, A], False, False),
    ("R6", "the metadata does not say the call cost more than its reservation", MT,
     [("meta.InferenceGeo, meta.SpendOverReservation = s.InferenceGeo, s.NanoUSD > m.res.Charge.NanoUSD",
       "meta.InferenceGeo, meta.SpendOverReservation = s.InferenceGeo, false")], [M, A, E], False, False),
    ("R7", "a call above its reservation is not warned of (generator)", "internal/proposals/generator/generator.go",
     [("if m.SpendOverReservation {", "if false {")], [G], False, False),

    # --- H1, L6: the run's cap and the spend record ---
    ("K1", "the run's cap may exceed the daily limit", MA, [("if capNano > ai.Limits.Day {", "if false {")], [E, AP], False, False),
    ("K2", "what the day has left is not checked", MA, [("case capNano > ai.Limits.Day-day:", "case false:")], [E, AP], False, False),
    ("K3", "what the month has left is not checked", MA, [("case capNano > ai.Limits.Month-month:", "case false:")], [E, AP], False, False),
    ("K4", "an open run without its close counts nothing", RC,
     [("amount, days, months := run.open.CapNanoUSD, []string{run.open.Day}, []string{run.open.Month}",
       "amount, days, months := int64(0), []string{run.open.Day}, []string{run.open.Month}")], [E, AP], False, False),
    ("K5", "a closed run counts only on the day and month it opened", RC,
     [("amount, days, months = c.CommittedNanoUSD, append(days, c.Day), append(months, c.Month)",
       "amount = c.CommittedNanoUSD")], [E, AP], False, False),
    ("K6", "a closed run counts what was settled, not what its meter held", RC,
     [("amount, days, months = c.CommittedNanoUSD, append(days, c.Day), append(months, c.Month)",
       "amount, days, months = c.SettledNanoUSD, append(days, c.Day), append(months, c.Month)")], [E, AP], False, False),
    ("K7", "a line that does not parse is skipped", RC,
     [("if err := r.parse(bytes.TrimSuffix(line, []byte(\"\\n\"))); err != nil {", "if err := r.parse(bytes.TrimSuffix(line, []byte(\"\\n\"))); false {")],
     [E, AP], False, False),
    ("K8", "a missing record counts as empty", RC,
     [("\tinfo, err := os.Stat(r.path)\n\tif err != nil {\n\t\treturn fmt.Errorf(\"aievals: the spend record: %w\", err)\n\t}",
       "\tinfo, err := os.Stat(r.path)\n\tif err != nil {\n\t\treturn nil\n\t}")], [E, AP], False, False),
    ("K9", "no open line before the calls", MA,
     [("if err := rec.Open(runID, kind, start, ai.Model, ai.Approval.ID(), capNano); err != nil {",
       "if err := error(nil); err != nil {")], [E, AP], False, False),
    ("K10", "an error after paid calls leaves the run open", MA,
     [("if err := closeRun(ResultError); err != nil {", "if err := error(nil); err != nil {")], [E, AP], False, False),
    ("K11", "an error after paid calls prints no summary or cost", MA,
     [("\t\tif report != nil {\n\t\t\tfmt.Fprintln(stdout, summary())\n\t\t} else {",
       "\t\tif report != nil {\n\t\t\t_ = summary\n\t\t} else {")], [E, AP], False, False),
    ("K12", "a report replaces an earlier one", LV,
     [("f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)", "f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)")],
     [E, AP], False, False),
    ("K13", "the record's lock is not exclusive", RC,
     [("f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)", "f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE, 0o644)")],
     [E, AP], False, False),
    ("K14", "a close line may come without its open", RC, [("\t\tcase !ok:\n\t\t\treturn errors.New(\"a close line before its run's open line\")\n",
                                                           "\t\tcase !ok:\n\t\t\trun = &recordedRun{}\n")], [E, AP], False, False),
    ("K15", "a stopped run is recorded as complete", MA, [("\t\tresult = ResultStopped\n", "\t\tresult = ResultComplete\n")], [E, AP], False, False),

    # --- L2: the keys ---
    ("W1", "a key with white space or control characters is taken", CA, [("case !cleanKey(v):", "case false:")], [C, AP], False, False),
    ("W2", "only trailing white space is refused", CA,
     [("strings.TrimFunc(v, unicode.IsSpace) != v", "strings.TrimRightFunc(v, unicode.IsSpace) != v")], [C], False, False),
    ("W3", "a control character inside a key is taken", CA, [("return !strings.ContainsFunc(v, unicode.IsControl)", "return true")], [C], False, False),
]


def run(args, cwd, env):
    p = subprocess.run(args, capture_output=True, text=True, cwd=cwd, env=env)
    return p.returncode, p.stdout + p.stderr


def failing(out):
    return sorted({l.split()[2] for l in out.splitlines() if l.startswith("--- FAIL: ")})[:6]


def digest(path):
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


def main():
    if len(sys.argv) < 2 or not os.path.isdir(sys.argv[1]):
        sys.exit("usage: mutants.py <git-archive copy> [--only ID,ID]")
    root = os.path.abspath(sys.argv[1])
    if os.path.exists(os.path.join(root, ".git")):
        sys.exit("refusing: run it in a `git archive` copy, not a checkout")
    only = None
    if len(sys.argv) > 3 and sys.argv[2] == "--only":
        only = set(sys.argv[3].split(","))
    env = {k: v for k, v in os.environ.items()
           if k in ("PATH", "HOME", "GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "GOFLAGS", "GEMINUS_TEST_DATABASE_URL", "GEMINUS_REQUIRE_DB_TESTS")}
    url = env.get("GEMINUS_TEST_DATABASE_URL", "")
    has_db = "/dt_test_" in url and env.get("GEMINUS_REQUIRE_DB_TESTS") == "1"
    counts = {}
    for mid, what, rel, edits, pkgs, db, equivalent in MUTANTS:
        if only and mid not in only:
            continue
        path = os.path.join(root, rel)
        before = digest(path)
        with open(path, encoding="utf-8") as f:
            orig = f.read()
        mutated, bad = orig, None
        for old, new in edits:
            n = mutated.count(old)
            if n != 1:
                bad = f"pattern occurs {n} times"
                break
            mutated = mutated.replace(old, new)
        if bad:
            result, detail = "ERROR", bad
        elif db and not has_db:
            result, detail = "ERROR", "a DB mutant needs GEMINUS_TEST_DATABASE_URL (dt_test_*) and GEMINUS_REQUIRE_DB_TESTS=1"
        else:
            try:
                with open(path, "w", encoding="utf-8") as f:
                    f.write(mutated)
                code, out = run(["go", "vet", *pkgs], root, env)
                if code != 0:
                    result, detail = "INVALID", "does not build: " + out.strip().splitlines()[-1][:160]
                else:
                    code, out = run(["go", "test", "-count=1", "-p", "1", *pkgs], root, env)
                    if code != 0:
                        result, detail = "KILLED", ", ".join(failing(out)) or out.strip().splitlines()[-1][:160]
                    elif equivalent:
                        result, detail = "EQUIVALENT", "tests pass, as expected"
                    else:
                        result, detail = "SURVIVED", "tests pass"
            finally:
                with open(path, "w", encoding="utf-8") as f:
                    f.write(orig)
        if digest(path) != before:
            result, detail = "ERROR", "the file was not restored"
        counts[result] = counts.get(result, 0) + 1
        print(f"{mid}\t{result}\t{what}\t{rel}\t{' '.join(pkgs)}\t{detail}", flush=True)
    print("summary\t" + " ".join(f"{k}={v}" for k, v in sorted(counts.items())))
    bad = sum(counts.get(k, 0) for k in ("SURVIVED", "ERROR", "INVALID"))
    return 0 if bad == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
