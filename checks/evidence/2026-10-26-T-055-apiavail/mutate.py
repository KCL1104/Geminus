#!/usr/bin/env python3
"""Mutation run for the key-free availability (T-053 I5 follow-up).

Usage: mutate.py <pristine tree (git archive of the commit)> <log>
Each mutant replaces exactly one occurrence of a string in one file of a
copy of the tree, runs the tests of the packages it touches, and is killed
when they fail (or do not build). The tree given is never changed.
"""
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

TREE = Path(sys.argv[1]).resolve()
LOG = Path(sys.argv[2]).resolve()
WORK = TREE.parent / "work"
GOBIN = "/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin"
ENV = dict(os.environ, PATH=GOBIN + ":" + os.environ["PATH"], GOTOOLCHAIN="local", GOFLAGS="-count=1")
for k in [k for k in ENV if k.startswith(("GEMINUS_", "AI_", "ANTHROPIC", "MODEL_API", "DATABASE_URL"))]:
    ENV.pop(k)  # no test database, no AI variable of this shell

CFG = ["./internal/config/..."]
API = ["./cmd/api/"]
WRK = ["./cmd/worker/"]
ALL = CFG + API + WRK

A = "internal/config/apiai.go"
G = "internal/config/ai.go"
M = "cmd/api/main.go"

MUTANTS = [
    # LoadAPIAI: the keys
    ("K1 no key check", A, 'if getenv(name) != "" {', 'if false && getenv(name) != "" {', ALL),
    ("K2 MODEL_API_KEY not refused", A, ', "MODEL_API_KEY",', ",", ALL),
    ("K3 ANTHROPIC_AUTH_TOKEN not refused", A, '"ANTHROPIC_AUTH_TOKEN", ', "", ALL),
    ("K4 GEMINUS_META_API_KEY not refused", A, ', "GEMINUS_META_API_KEY",', ",", ALL),
    ("K5 AI_API_KEY not refused", A, '"AI_API_KEY", ', "", ALL),
    ("K6 GEMINUS_ANTHROPIC_API_KEY_B not refused", A, '"GEMINUS_ANTHROPIC_API_KEY_B", ', "", ALL),
    ("K7 GEMINUS_MUSE_API_KEY not refused", A, '"GEMINUS_MUSE_API_KEY", ', "", ALL),
    ("K8 ANTHROPIC_API_KEY not refused", A, '"ANTHROPIC_API_KEY", ', "", ALL),
    ("K9 the key's code invalid", A, "errs = append(errs, &Error{name, CodeKeyForbidden,", "errs = append(errs, &Error{name, CodeInvalid,", ALL),
    ("K10 a key refused only with a live provider", A, 'if getenv(name) != "" {', 'if getenv(name) != "" && getenv("AI_PROVIDER") != "" {', ALL),
    # LoadAPIAI: the selection and the gate
    ("S1 the fake everywhere", A, 'aiProvider(getenv, env == "development" || env == "test")', 'aiProvider(getenv, env == "development" || env == "test" || true)', ALL),
    ("S2 the gate always on", A, 'liveGate(getenv, p, approval, "AI_POLICY_APPROVAL", "AI_MODEL", liveSwitch, on, false)',
     'liveGate(getenv, p, approval, "AI_POLICY_APPROVAL", "AI_MODEL", liveSwitch, on || true, false)', ALL),
    ("S3 the gate never on", A, 'liveGate(getenv, p, approval, "AI_POLICY_APPROVAL", "AI_MODEL", liveSwitch, on, false)',
     'liveGate(getenv, p, approval, "AI_POLICY_APPROVAL", "AI_MODEL", liveSwitch, on && false, false)', ALL),
    ("S4 the gate's reason ignored", A, 'if why == "" && len(gerrs) == 0 {', "if len(gerrs) == 0 {", ALL),
    ("S5 the gate's errors ignored", A, "errs = append(errs, gerrs...)", "_ = gerrs", ALL),
    ("S6 LiveOff not set", A, "a.LiveOff = why", "_ = why", ALL),
    ("S7 no reason without an approval", A, "a.LiveOff = LiveOffNoApproval", "_ = LiveOffNoApproval", ALL),
    ("S8 an approval without a live provider", A, 'errs = append(errs, &Error{"AI_POLICY_APPROVAL", CodeInvalid, "must be empty unless AI_PROVIDER is anthropic or meta"})',
     "", ALL),
    ("S9 the errors not returned", A, "if len(errs) > 0 {\n\t\treturn APIAI{}, errors.Join(errs...)", "if false {\n\t\treturn APIAI{}, errors.Join(errs...)", ALL),
    ("S10 the model not kept", A, "a.Approval, a.Model = rec, model", "a.Approval, a.Model = rec, \"\"\n\t\t\t_ = model", ALL),
    ("S11 the switch's errors ignored", A, "errs = append(errs, serrs...)", "_ = serrs", ALL),
    ("S12 the provider's errors ignored", A, "errs = append(errs, perrs...)", "_ = perrs", ALL),
    ("S13 the photo path read", A, "env := getenv(\"APP_ENV\")", "env := getenv(\"APP_ENV\")\n\t_ = getenv(\"AI_VISION_PROVIDER\")", ALL),
    ("S14 the switch read as on", A, "on, serrs := switchOn(getenv, p, approval)", "_, serrs := switchOn(getenv, p, approval)\n\ton := true", ALL),
    # APIAI.LiveEnabled
    ("L1 any provider's approval", A, "return a.Provider.live() && a.Approval.Valid() && a.Approval.Provider() == string(a.Provider)",
     "return a.Provider.live() && a.Approval.Valid()", ALL),
    ("L2 no provider check", A, "return a.Provider.live() && a.Approval.Valid() && a.Approval.Provider() == string(a.Provider)",
     "return a.Approval.Valid()", ALL),
    ("L3 no validity check", A, "return a.Provider.live() && a.Approval.Valid() && a.Approval.Provider() == string(a.Provider)",
     "return a.Provider.live() && a.Approval.Provider() == string(a.Provider)", ALL),
    ("L4 the model instead of the approval", A, "return a.Provider.live() && a.Approval.Valid() && a.Approval.Provider() == string(a.Provider)",
     'return a.Provider.live() && a.Model != ""', ALL),
    # The shared gate (ai.go), which the worker's LoadAI uses too
    ("G1 no provider mismatch", G, "case rec.Provider != string(p):", "case false:", ALL),
    ("G2 no record gap", G, 'case rec.Gap() != "":', "case false:", ALL),
    ("G3 no model check", G, "case model != rec.Model:", "case false:", ALL),
    ("G4 no price check", G, "case !priced:", "case false:", ALL),
    ("G5 the switch without an approval", G, 'if !p.live() || approval == "" {', "if !p.live() {", ALL),
    ("G6 an unknown provider disabled silently", G,
     'return AIProviderDisabled, []error{&Error{"AI_PROVIDER", CodeInvalid, `unknown provider; allowed: "disabled", "fake", "anthropic", "meta"`}}',
     "return AIProviderDisabled, nil", ALL),
    ("G7 the switch's bad value accepted", G, 'return false, []error{&Error{liveSwitch, CodeInvalid, `allowed: "on", "off" or unset`}}', "return false, nil", ALL),
    ("G8 the unrecorded approval off, not an error", G,
     'return "", "", []error{&Error{approvalVar, CodeNotApproved,\n\t\t\t"this build records no such approval of live AI; the owner records one (model, budget, data policy) before any live call (H-001, H-017, H-018)"}}',
     'return "", LiveOffNoApproval, nil', ALL),
    ("G9 the worker reads the keys whatever the gate", G, "if why != \"\" || len(errs) > 0 {\n\t\treturn live{}, why, errs\n\t}",
     "if len(errs) > 0 {\n\t\treturn live{}, why, errs\n\t}", ALL),
    # cmd/api
    ("A1 available without the gate", M, "case ai.LiveEnabled():\n\t\treturn proposals.Available", 'case ai.LiveOff == "":\n\t\treturn proposals.Available', API),
    ("A2 no fake", M, "case ai.Provider == config.AIProviderFake:\n\t\treturn proposals.Available", "case false:\n\t\treturn proposals.Available", API),
    ("A3 live off is not configured", M, "\t\treturn proposals.PolicyNotApproved\n", "\t\treturn proposals.NotConfigured\n", API),
    ("A4 LoadAPIAI's errors dropped", M, "if err == nil {\n\t\terr = aerr\n\t}", "_ = aerr", API),
    ("A5 the worker's Load", M, "cfg, err := config.LoadAPI(getenv)", "cfg, err := config.Load(getenv)", API),
    ("A6 the reason not typed", M, '"code", strings.ToUpper(ai.LiveOff))', '"code", strings.ToLower(ai.LiveOff))', API),
    ("A7 no log of the AI", M, "\tlogAI(log, ai)\n", "\t_ = ai\n", API),
    ("A8 accepted logged without the model", M, '"model", ai.Model, "code", "LIVE_AI_ON"', '"code", "LIVE_AI_ON"', API),
    ("A9 always available", M, "\treturn proposals.NotConfigured\n}", "\treturn proposals.Available\n}", API),
]


def run(name, rel, old, new, pkgs):
    if WORK.exists():
        shutil.rmtree(WORK)
    shutil.copytree(TREE, WORK, symlinks=True)
    f = WORK / rel
    text = f.read_text(encoding="utf-8")
    n = text.count(old)
    if n != 1:
        return "NOT APPLIED (%d occurrences)" % n, ""
    f.write_text(text.replace(old, new, 1), encoding="utf-8")
    start = time.time()
    try:
        p = subprocess.run(["go", "test", *pkgs], cwd=WORK, env=ENV, capture_output=True, text=True, timeout=900)
        out, code = p.stdout + p.stderr, p.returncode
    except subprocess.TimeoutExpired:
        return "KILLED (timeout)", ""
    failed = [l for l in out.splitlines() if l.startswith(("--- FAIL", "FAIL\t", "# "))]
    if "[build failed]" in out or "[setup failed]" in out:
        return "INVALID (does not build)", "\n".join(failed[:6])
    verdict = ("KILLED" if code != 0 else "SURVIVED") + " (%.0f s)" % (time.time() - start)
    return verdict, "\n".join(failed[:12])


def main():
    killed = survived = other = invalid = 0
    with LOG.open("w") as log:
        # The tree itself passes.
        verdict, detail = run("baseline", A, "package config", "package config", ALL)
        log.write("baseline (no change): %s\n%s\n" % (verdict, detail))
        log.flush()
        if not verdict.startswith("SURVIVED"):
            log.write("the baseline does not pass: stopping\n")
            return 1
        only = os.environ.get("ONLY")
        for name, rel, old, new, pkgs in MUTANTS:
            if only and not any(name.startswith(o + " ") for o in only.split(",")):
                continue
            verdict, detail = run(name, rel, old, new, pkgs)
            if verdict.startswith("KILLED"):
                killed += 1
            elif verdict.startswith("SURVIVED"):
                survived += 1
            elif verdict.startswith("INVALID"):
                invalid += 1
            else:
                other += 1
            log.write("%s | %s | %s\n" % (name, rel, verdict))
            if detail:
                log.write("    " + detail.replace("\n", "\n    ") + "\n")
            log.flush()
        log.write("\n%d mutants: %d killed, %d survived, %d invalid (do not build), %d not applied\n" % (len(MUTANTS), killed, survived, invalid, other))
    shutil.rmtree(WORK, ignore_errors=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
