"""Targeted mutations of the T-024 code: each must make the named tests fail.

Usage (from the T-024 worktree root): python3 -I t024_mutate.py <out-file>
Each mutation edits one file, runs `go test` (with a test timeout, so a
mutation that hangs a test counts as caught), and puts the file back
exactly as it was; the script checks every file afterwards.
"""
import hashlib
import subprocess
import sys
from pathlib import Path

MUTATIONS = [
    ("M1 block content not HTML-escaped (forged tags)", "internal/ai/provider/prompt.go",
     'json.Deterministic(true), jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))',
     'json.Deterministic(true))', "./internal/ai/provider/", "TestBlocksCannotBeForged"),
    ("M2 no server-ID redaction", "internal/ai/aicontext/sanitize.go",
     "{RedactServerID, regexp.MustCompile(", "{RedactServerID + \"-off\", regexp.MustCompile(`$^`), false, nil}, //", "./internal/ai/aicontext/",
     "TestRedaction|TestInjectionStaysData|TestAnalysisInjection"),
    ("M3 no sk-ant credential redaction", "internal/ai/aicontext/sanitize.go",
     r"{RedactCredential, regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{8,}`), false, nil},", "", "./internal/ai/aicontext/",
     "TestRedaction|TestMappingContext|TestSanitizerRedacts"),
    ("M4 pool of another organization accepted", "internal/ai/aicontext/proposal.go",
     "if in.Pool.OrganizationID != in.Scope.OrganizationID {", "if false {", "./internal/ai/aicontext/", "TestCrossTenantInputsAreRefused"),
    ("M5 data summary of another organization accepted", "internal/ai/aicontext/proposal.go",
     "if s.OrganizationID != in.Scope.OrganizationID {", "if false && s.OrganizationID != in.Scope.OrganizationID {", "./internal/ai/aicontext/",
     "TestCrossTenantInputsAreRefused"),
    ("M6 intent copied into the system rules", "internal/ai/aicontext/proposal.go",
     "system := text + serverRules(role, modeText, loc)", "system := text + \"\\nIntent: \" + in.Intent + serverRules(role, modeText, loc)",
     "./internal/ai/aicontext/", "TestProposalContext|TestInjectionStaysData"),
    ("M7 answer not validated against the schema", "internal/ai/provider/provider.go",
     "issues, err := validate.Document(resp.Text, role.OutputSchema(), OutputInvalid)",
     "issues, err := validate.Document(resp.Text, role.OutputSchema(), OutputInvalid)\n\tissues, err = nil, nil", "./internal/ai/provider/", "TestAdversarialAnswersAreRefused"),
    ("M8 prompt integrity not checked", "internal/ai/provider/provider.go",
     "p.Role != role || p.Verify() != nil", "p.Role != role", "./internal/ai/provider/", "TestRequestsTheClientRefuses"),
    ("M9 failover on 429 (merges rate limits, against D-018)", "internal/ai/provider/anthropic/anthropic.go",
     "case status == http.StatusUnauthorized, status == http.StatusPaymentRequired, status == http.StatusForbidden:",
     "case status == http.StatusUnauthorized, status == http.StatusPaymentRequired, status == http.StatusForbidden, status == http.StatusTooManyRequests:",
     "./internal/ai/provider/anthropic/", "TestSameCredentialRetries"),
    ("M10 no failover on 402", "internal/ai/provider/anthropic/anthropic.go",
     "status == http.StatusUnauthorized, status == http.StatusPaymentRequired, status == http.StatusForbidden:",
     "status == http.StatusUnauthorized, status == http.StatusForbidden:", "./internal/ai/provider/anthropic/", "TestFailover"),
    ("M11 the SDK's environment defaults (NewClient)", "internal/ai/provider/anthropic/anthropic.go",
     "msg, err := svc.New(ctx, params, option.WithResponseInto(&raw))",
     "cl := sdk.NewClient(svc.Options...)\n\t\tmsg, err := cl.Messages.New(ctx, params, option.WithResponseInto(&raw))",
     "./internal/ai/provider/anthropic/", "TestRequestShape"),
    ("M12 redirects followed", "internal/ai/provider/anthropic/anthropic.go",
     "client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }", "",
     "./internal/ai/provider/anthropic/", "TestNoRedirects"),
    ("M13 tool_use stop reason accepted", "internal/ai/provider/anthropic/anthropic.go",
     "case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence:", "case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence, sdk.StopReasonToolUse:",
     "./internal/ai/provider/anthropic/", "TestStopReasons"),
    ("M14 an unlisted approval enables live AI", "internal/config/aiapproval/aiapproval.go",
     'if id == "" || !recorded[id] {', 'if id == "" {', "./internal/config/...", "TestLoadAIRejects|TestNothingIsRecorded"),
    ("M15 keys read before the approval", "internal/config/ai.go",
     "if ai.LiveEnabled() {", "if ai.Provider == AIProviderAnthropic {", "./internal/config/", "TestLiveAIStaysOff"),
    ("M16 fake allowed in production", "internal/config/ai.go",
     'if env != "development" && env != "test" {', 'if env == "never" {', "./internal/config/", "TestLoadAIRejects"),
    ("M17 Chinese numeral runs not flagged", "internal/ai/locale/locale.go",
     "case n >= 2:", "case n >= 99:", "./internal/ai/locale/", "TestUnverifiableWritings|TestCheckProse"),
    ("M18 wrong-kind pool ID accepted", "internal/ai/idpool/idpool.go",
     "case slices.Contains(p.IDs(k), id):", "case slices.Contains(p.Node, id) || slices.Contains(p.Flow, id) || slices.Contains(p.Resource, id):",
     "./internal/ai/idpool/", "TestCheck"),
    ("M19 projection opens objects", "internal/ai/provider/anthropic/projection.go",
     "\t\t\t\tout[k] = false\n", "\t\t\t\tout[k] = true\n", "./internal/ai/provider/anthropic/", "TestProjection"),
    ("M20 unavailable metrics citable", "internal/ai/aicontext/analysis.go",
     "if s.Status == simulation.Available && s.Mean != nil {", "if true {", "./internal/ai/aicontext/", "TestAnalysisContext"),
    ("M21 concurrency limit ignored", "internal/ai/provider/provider.go",
     "if c.active[org] >= c.budget.MaxConcurrentPerOrganization {", "if false {", "./internal/ai/provider/", "TestConcurrencyPerOrganization"),
    ("M22 budget timeout not applied", "internal/ai/provider/provider.go",
     "callCtx, cancel := context.WithTimeout(ctx, c.budget.Timeout)", "callCtx, cancel := context.WithCancel(ctx)",
     "./internal/ai/provider/", "TestCancellationAndTimeout"),
    ("M23 array indices counted as supplied numbers", "internal/ai/aicontext/analysis.go",
     'for _, k := range []string{"capacity", "probability"} {', 'for _, k := range []string{"capacity", "probability", "index"} {',
     "./internal/ai/aicontext/", "TestAnalysisContext"),
    ("M24 Secret prints its value", "internal/secret/secret.go",
     "func (Secret) Format(f fmt.State, _ rune)   { _, _ = io.WriteString(f, redacted) }",
     "func (s Secret) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, string(s)) }", "./internal/secret/", "TestSecretNeverPrints"),
    # T-024 review findings
    ("M25 Han characters continue identifiers (review H1)", "internal/ai/locale/locale.go",
     "|| asciiLetter(r) || asciiDigit(r)", "|| unicode.IsLetter(r) || unicode.IsDigit(r)", "./internal/ai/locale/", "TestNumbers|TestCheckProse"),
    ("M26 no redaction by member name (review M2)", "internal/ai/aicontext/sanitize.go",
     "if credentialName(name) {", "if false && credentialName(name) {", "./internal/ai/aicontext/",
     "TestSanitizerRedactsByMemberName|TestRedaction|TestMappingContext"),
    ("M27 keyword only at a word start (review M2)", "internal/ai/aicontext/sanitize.go",
     r"(\b(?:(?:[A-Za-z0-9.]+[_.-])*?(?:password", r"(\b(?:(?:password", "./internal/ai/aicontext/", "TestSanitizerRedacts"),
    ("M28 look-alike delimiters kept (review L3)", "internal/ai/aicontext/sanitize.go",
     "z.counts[RedactDelimiter]++\n\t\t\t\treturn a", "z.counts[RedactDelimiter]++\n\t\t\t\t_ = a\n\t\t\t\treturn r", "./internal/ai/aicontext/",
     "TestSanitizerFoldsLookAlikeDelimiters|TestRedaction"),
    ("M29 collisions not counted (review L4)", "internal/ai/aicontext/sanitize.go",
     "z.counts[collided]++\n", "\n", "./internal/ai/aicontext/", "TestSanitizerCountsCollisions|TestRedaction|TestMappingContext"),
    ("M30 prompt organization not compared (review L6)", "internal/ai/provider/provider.go",
     "p.OrganizationID != org || ", "", "./internal/ai/provider/", "TestRequestsTheClientRefuses"),
    ("M31 organization not in the hash (review L6)", "internal/ai/provider/prompt.go",
     "}{p.OrganizationID, p.Role,", "}{\"\", p.Role,", "./internal/ai/provider/", "TestSealBindsTheOrganization|TestVerify"),
    ("M32 adapter takes any approval (review L5)", "internal/ai/provider/anthropic/anthropic.go",
     "if !cfg.Approval.Valid() {", "if false {", "./internal/ai/provider/anthropic/", "TestNewRefuses"),
    ("M33 fake reports usage (review L8)", "internal/ai/provider/fake/fake.go",
     'resp := provider.Response{Model: "fake"}', 'resp := provider.Response{Model: "fake", Usage: provider.Usage{InputTokens: 1}}',
     "./internal/ai/provider/fake/", "TestDefaultAnswers"),
    ("M34 skipped credential not named (review INFO)", "internal/ai/provider/anthropic/anthropic.go",
     "use the primary.\n\t\t\tfailed = append(failed, c.label)\n", "use the primary.\n", "./internal/ai/provider/anthropic/", "TestFailover"),
    ("M35 402 disables for good (review INFO)", "internal/ai/provider/anthropic/anthropic.go",
     "c.resting = now.Add(billingCooldown)\n\t\treturn", "c.disabled = true\n\t\treturn", "./internal/ai/provider/anthropic/", "TestBillingCooldown"),
    ("M36 every long base64-like run redacted (review M2)", "internal/ai/aicontext/sanitize.go",
     "false, longBase64},", "false, nil},", "./internal/ai/aicontext/", "TestSanitizerKeeps"),
    # T-024 re-review L-new
    ("M37 model IDs not exempt (re-review L-new)", "internal/ai/aicontext/sanitize.go",
     "return !z.withinExemptID(s, loc[0], loc[1])", "return true", "./internal/ai/aicontext/",
     "TestSanitizerKeepsModelIDs|TestLongModelIDsReachTheProvider"),
    ("M38 short base64 runs redacted (re-review L-new)", "internal/ai/aicontext/sanitize.go",
     'if !strings.ContainsAny(m, "+/=") && len(m) < 64 {', "if false {", "./internal/ai/aicontext/", "TestSanitizerKeeps"),
    ("M39 any word before token counts (re-review L-new)", "internal/ai/aicontext/sanitize.go",
     "return len(words) == 1 || tokenQualifiers[words[len(words)-2]]", "return true", "./internal/ai/aicontext/",
     "TestSanitizerRedactsByMemberName"),
]


def digest(p):
    return hashlib.sha256(Path(p).read_bytes()).hexdigest()


def main():
    out = Path(sys.argv[1])
    before = {m[1]: digest(m[1]) for m in MUTATIONS}
    lines, killed = [], 0
    for name, file, old, new, pkg, tests in MUTATIONS:
        path = Path(file)
        orig = path.read_bytes()
        text = orig.decode()
        if text.count(old) != 1:
            lines.append(f"{name}: SETUP ERROR: the original text occurs {text.count(old)} times in {file}")
            continue
        path.write_text(text.replace(old, new))
        try:
            r = subprocess.run(["go", "test", "-count=1", "-timeout", "90s", "-run", tests, pkg],
                               capture_output=True, text=True, timeout=300)
            code, output = r.returncode, r.stdout + r.stderr
        except subprocess.TimeoutExpired:
            code, output = -1, "TIMEOUT"
        finally:
            path.write_bytes(orig)
        broken = "[build failed]" in output or "[setup failed]" in output
        caught = code != 0 and not broken
        killed += caught
        verdict = "INVALID (the mutant does not compile)" if broken else ("KILLED" if caught else "SURVIVED")
        lines.append(f"{name}: {verdict} (exit {code}; {file}; -run '{tests}')")
        shown = [l for l in output.splitlines() if l.startswith("--- FAIL") or "panic: test timed out" in l or "build failed" in l]
        lines.extend("    " + l for l in shown[:5])
    after = {f: digest(f) for f in before}
    restored = all(before[f] == after[f] for f in before)
    lines.append(f"{killed}/{len(MUTATIONS)} mutations killed; every mutated file restored byte for byte: {restored}")
    out.write_text("\n".join(lines) + "\n")
    print("\n".join(lines))
    sys.exit(0 if restored else 1)


if __name__ == "__main__":
    main()
