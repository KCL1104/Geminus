#!/usr/bin/env python3
"""T-055 review round 1: mutants of the count-based reservation (H-037) and of
the L1-L5 fixes, on a `git archive HEAD` copy (tree/). Each mutant is one exact
replacement (the old text must occur exactly once); the packages are built
first (`go vet`, so test files compile too): a build failure is INVALID, never
KILLED. Then `go test -count=1` on the packages: a failure is KILLED, a pass
SURVIVED. The file is restored after each mutant. No AI variable reaches the
commands (removed by name); GOPROXY=off; the tests use local servers only."""
import os
import subprocess
import sys

TREE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "tree")
GO = "/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin"
UNSET = ["GEMINUS_MUSE_API_KEY", "GEMINUS_META_API_KEY", "MODEL_API_KEY", "GEMINUS_ANTHROPIC_API_KEY_A",
         "GEMINUS_ANTHROPIC_API_KEY_B", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "AI_API_KEY", "GEMINUS_AI_LIVE",
         "AI_PROVIDER", "AI_POLICY_APPROVAL", "AI_MODEL", "AI_VISION_PROVIDER", "AI_VISION_POLICY_APPROVAL",
         "AI_VISION_MODEL", "RAILWAY_TOKEN", "RAILWAY_API_TOKEN", "GOFLAGS"]

META = "internal/ai/provider/meta/meta.go"
CONN = "internal/ai/provider/meta/conn.go"
METER = "internal/ai/provider/meter.go"
RULES = "scripts/check-architecture/rules.go"
APPROVAL = "internal/config/aiapproval/aiapproval.go"
P_META = ["./internal/ai/provider/meta/"]
P_PROV = ["./internal/ai/provider/", "./internal/ai/provider/meta/"]
P_ARCH = ["-run", "TestSyntheticGraph|TestEveryRuleEntryIsEnforced|TestProbesAreCaught/the-anthropic-adapter-links-meta",
          "./scripts/check-architecture/"]
P_APPR = ["./internal/config/aiapproval/"]

MUTANTS = [
    # H-037: the count and its margin (meta.go)
    ("K1", "images are not counted (text quote)", META, "\tif len(req.Prompt.Images) == 0 {\n\t\tn := int64(", "\tif true {\n\t\tn := int64(", P_META),
    ("K2", "no margin", META, "MaxInputTokens: min(n+(n+countMarginDivisor-1)/countMarginDivisor, contextWindowTokens)}",
     "MaxInputTokens: min(n, contextWindowTokens)}", P_META),
    ("K3", "the margin rounded down", META, "n+(n+countMarginDivisor-1)/countMarginDivisor", "n+n/countMarginDivisor", P_META),
    ("K4", "a 5% margin", META, "const countMarginDivisor = 10", "const countMarginDivisor = 20", P_META),
    ("K5", "no cap at the context window", META, "MaxInputTokens: min(n+(n+countMarginDivisor-1)/countMarginDivisor, contextWindowTokens)}",
     "MaxInputTokens: n + (n+countMarginDivisor-1)/countMarginDivisor}", P_META),
    ("K6", "a failed count falls back to the text quote (smaller)", META,
     "\tn, err := b.count(ctx, req, proj)\n\tif err != nil {\n\t\treturn provider.Quote{}, err\n\t}",
     "\tn, err := b.count(ctx, req, proj)\n\tif err != nil {\n\t\tn = int64(len(req.Prompt.System)+len(req.Prompt.User())+proj.size) + quoteOverheadTokens\n\t}", P_META),
    ("K7", "a failed count reserves the whole window (the other choice)", META,
     "\tn, err := b.count(ctx, req, proj)\n\tif err != nil {\n\t\treturn provider.Quote{}, err\n\t}",
     "\tn, err := b.count(ctx, req, proj)\n\tif err != nil {\n\t\treturn provider.Quote{Model: b.model, MaxInputTokens: contextWindowTokens}, nil\n\t}", P_META),
    ("K8", "a count of 0 is sane", META, "if err != nil || n < 1 || n > contextWindowTokens {", "if err != nil || n < 0 || n > contextWindowTokens {", P_META),
    ("K9", "a count above the window is sane", META, "if err != nil || n < 1 || n > contextWindowTokens {", "if err != nil || n < 1 {", P_META),
    ("K10", "a fraction or an exponent is a count", META, "\tn, err := strconv.ParseInt(string(c.InputTokens), 10, 64)\n",
     "\tf, err := strconv.ParseFloat(string(c.InputTokens), 64)\n\tn := int64(f)\n", P_META),
    ("K11", "a string is a count", META, "strconv.ParseInt(string(c.InputTokens), 10, 64)",
     "strconv.ParseInt(strings.Trim(string(c.InputTokens), `\"`), 10, 64)", P_META),
    ("K12", "member names case-insensitive", META, "\tif json.Unmarshal(body, &c) != nil {\n\t\treturn 0, false",
     "\tif json.Unmarshal(body, &c, json.MatchCaseInsensitiveNames(true)) != nil {\n\t\treturn 0, false", P_META),
    ("K13", "duplicate members allowed", META, "\tif json.Unmarshal(body, &c) != nil {\n\t\treturn 0, false",
     "\tif json.Unmarshal(body, &c, jsontext.AllowDuplicateNames(true)) != nil {\n\t\treturn 0, false", P_META),
    ("K14", "invalid UTF-8 allowed", META, "\tif json.Unmarshal(body, &c) != nil {\n\t\treturn 0, false",
     "\tif json.Unmarshal(body, &c, jsontext.AllowInvalidUTF8(true)) != nil {\n\t\treturn 0, false", P_META),
    ("K15", "the count's body carries max_tokens", META,
     "json.Marshal(countRequest{Model: m.Model, System: m.System, Messages: m.Messages, OutputConfig: m.OutputConfig}, json.Deterministic(true))",
     "json.Marshal(m, json.Deterministic(true))", P_META),
    ("K16", "the count skips the role check", META, "\tif b.allows == nil || !b.allows(string(req.Prompt.Role)) || !b.images {\n\t\treturn 0,",
     "\tif !b.images {\n\t\treturn 0,", P_META),
    ("K17", "the count skips the images check", META, "\tif b.allows == nil || !b.allows(string(req.Prompt.Role)) || !b.images {\n\t\treturn 0,",
     "\tif b.allows == nil || !b.allows(string(req.Prompt.Role)) {\n\t\treturn 0,", P_META),
    ("K18", "the count goes to /v1/messages", META, 'const countPath = "v1/messages/count_tokens"', 'const countPath = "v1/messages"', P_META),
    ("K19", "the count is never retried", META, "countPath, body, req.AttemptTimeout, req.MaxRetries)", "countPath, body, req.AttemptTimeout, 0)", P_META),
    ("K20", "an untrusted count is invalid_output (not retryable)", META, "\t\treturn 0, provider.NewError(provider.CodeUnavailable, 0, b.c.label)",
     "\t\treturn 0, provider.NewError(provider.CodeInvalidOutput, 0, b.c.label)", P_META),
    ("K21", "the count is posted without a deadline of its own attempts", META,
     "ex, _, err := b.c.post(ctx, &tally{}, countPath", "ex, _, err := b.c.post(context.WithoutCancel(ctx), &tally{}, countPath", P_META),
    # The client (meter.go)
    ("P1", "the quote does not get the call's deadline", METER, "q, err := c.billable.Quote(callCtx, req)", "q, err := c.billable.Quote(ctx, req)", P_PROV),
    ("P2", "a typed quote error becomes invalid_request", METER, "\t\tif errors.As(err, &pe) {\n\t\t\treturn nil, pe\n\t\t}",
     "\t\tif errors.As(err, &pe) {\n\t\t\treturn nil, invalidRequest()\n\t\t}", P_PROV),
    # L2 (conn.go)
    ("R1", "a Retry-After over 60 s is waited for", CONN, "\tif ra > maxRetryAfter {\n\t\treturn false\n\t}", "\tif false {\n\t\treturn false\n\t}", P_META),
    ("R2", "a Retry-After of exactly 60 s is not retried", CONN, "\tif ra > maxRetryAfter {", "\tif ra >= maxRetryAfter {", P_META),
    ("R3", "the old clamp: 60 s, then retried", CONN, "\tif ra > maxRetryAfter {\n\t\treturn false\n\t}\n\td := max(c.backoff(attempt), ra)",
     "\td := min(max(c.backoff(attempt), ra), maxRetryAfter)", P_META),
    ("R4", "Retry-After read against the wall clock (L1)", CONN, "\tra := retryAfter(h, c.now())", "\tra := retryAfter(h, time.Now())", P_META),
    # L3 (meta.go)
    ("U1", "a 200 without usage is a message", META, "m.Role != \"assistant\" || u == nil || u.InputTokens < 0", "m.Role != \"assistant\" || u.InputTokens < 0", P_META),
    # L4 (aiapproval.go)
    ("H1", "the retention source back to H-029 (1)", APPROVAL,
     '\t\tSource: "H-035 (1): the owner accepted the Meta Model API Terms of Service (2026-10-09 version, checked by the coordinator), " +\n',
     '\t\tSource: "H-029 (1): Standard mode, never the Contributor variant that trains on the content; H-033 keeps it; " +\n', P_APPR),
    # L5 (rules.go)
    ("A1", "the mirror rule forbids nothing of Meta's", RULES,
     '\t\tapplies: []string{"./internal/ai/provider/anthropic/..."},\n\t\tforbid:  []string{"./internal/ai/provider/meta/..."},',
     '\t\tapplies: []string{"./internal/ai/provider/anthropic/..."},\n\t\tforbid:  []string{"./internal/ai/provider/nothing/..."},', P_ARCH),
]


def env():
    e = {k: v for k, v in os.environ.items() if k not in UNSET}
    e["PATH"] = GO + ":" + e.get("PATH", "")
    e["GOTOOLCHAIN"] = "local"
    e["GOPROXY"] = "off"
    return e


def run(args, timeout=1800):
    p = subprocess.run(args, cwd=TREE, env=env(), capture_output=True, text=True, timeout=timeout)
    return p.returncode, p.stdout + p.stderr


def main():
    only = set(sys.argv[1:])
    results = []
    for mid, what, path, old, new, pkgs in MUTANTS:
        if only and mid not in only:
            continue
        full = os.path.join(TREE, path)
        src = open(full, encoding="utf-8").read()
        n = src.count(old)
        if n != 1:
            print(f"{mid} BADSPEC (old text occurs {n} times)", flush=True)
            results.append((mid, "BADSPEC"))
            continue
        open(full, "w", encoding="utf-8").write(src.replace(old, new, 1))
        try:
            vet_pkgs = [p for p in pkgs if p.startswith("./")]
            code, out = run(["go", "vet"] + vet_pkgs)
            if code != 0:
                verdict, detail = "INVALID", out.strip().splitlines()[:3]
            else:
                code, out = run(["go", "test", "-count=1", "-timeout", "900s"] + pkgs)
                fails = [l.strip() for l in out.splitlines() if l.startswith("--- FAIL") or l.startswith("    --- FAIL") or l.startswith("panic:")]
                verdict = "KILLED" if code != 0 else "SURVIVED"
                detail = fails[:6]
        finally:
            open(full, "w", encoding="utf-8").write(src)
        print(f"{mid} {verdict}: {what} [{path}] {detail}", flush=True)
        results.append((mid, verdict))
    counts = {}
    for _, v in results:
        counts[v] = counts.get(v, 0) + 1
    print("summary:", ", ".join(f"{k} {v}" for k, v in sorted(counts.items())), f"of {len(results)} mutants", flush=True)


if __name__ == "__main__":
    main()
