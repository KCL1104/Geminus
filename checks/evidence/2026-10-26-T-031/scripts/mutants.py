#!/usr/bin/env python3
"""T-031 mutation check: each mutant changes one place of the slice's code,
runs the tests that should catch it, and restores the file. Killed: the
tests fail; survived: they pass. Run from apps/web:

    python3 ../../checks/evidence/2026-10-26-T-031/scripts/mutants.py > ../../checks/evidence/2026-10-26-T-031/mutants.txt
"""
import pathlib
import subprocess
import sys

V = "tests/proposal-view.test.tsx"
A = "tests/proposal-ask.test.tsx"
C = "tests/proposal-changes.test.ts"
S = "tests/proposal-simplicity.test.tsx"
VIEW = "features/ai-proposals/proposal-view.tsx"
ASK = "features/ai-proposals/ask-form.tsx"
CHG = "features/ai-proposals/changes.ts"
LIST = "features/ai-proposals/change-list.tsx"

MUTANTS = [
    ("M1 Approve decides without the in-place confirmation", VIEW,
     '''                      setProblem(null);
                      setDecision("confirmApprove");
                      setFocus("confirm");''',
     '''                      setProblem(null);
                      void approve();''', [V]),
    ("M2 a retried approval takes a new key", VIEW,
     "const key = (approveKey.current ??= newIdempotencyKey());", "const key = (approveKey.current = newIdempotencyKey());", [V]),
    ("M3 the server's cell shows the AI's claim", VIEW,
     "{p.server_risk_level && <p className={styles.level}>{t(`proposal.risk.level.${p.server_risk_level}`)}</p>}",
     "{p.server_risk_level && <p className={styles.level}>{t(`proposal.risk.level.${p.provider_risk_claim!}`)}</p>}", [V]),
    ("M4 the counted risk is the server's alone, not the higher of the two", VIEW,
     "{p.risk_level && <p>{t(`proposal.risk.counted.${p.risk_level}`)}</p>}",
     "{p.risk_level && <p>{t(`proposal.risk.counted.${p.server_risk_level!}`)}</p>}", [V]),
    ("M5 the AI's summary is rendered as HTML", VIEW,
     "<p className={styles.words}>{p.summary}</p>",
     "<p className={styles.words} dangerouslySetInnerHTML={{ __html: p.summary }} />", [V]),
    ("M6 a 409 on approval is shown as a plain problem, not the kept page with what changed", VIEW,
     '''    } else if (r.problem.code === "MODEL_VERSION_CONFLICT") {
      setOutcome({ kind: "conflict" });''',
     '''    } else if (r.problem.code === "MODEL_VERSION_CONFLICT") {
      setProblem(r.problem);''', [V]),
    ("M7 Approve offered on a stale base", VIEW,
     '{p.status === "ready" && !stale && (\n                  <Button', '{p.status === "ready" && (\n                  <Button', [V, S]),
    ("M8 an empty reason is sent as a comment", VIEW,
     'comment === "" ? {} : { comment }', "{ comment }", [V]),
    ("M9 a generating proposal is not read again", VIEW,
     'if (p.status !== "generating") return;', "return;", [V]),
    ("M10 a viewer may decide", VIEW,
     'const decidable = canEdit(role) && outcome === null', "const decidable = outcome === null", [V]),
    ("M11 the scenario link names the proposal, not the scenario the approval made", VIEW,
     "<Link href={hrefs.scenario(outcome.approval.scenario_id)}>", "<Link href={hrefs.scenario(outcome.approval.proposal_id)}>", [V]),
    ("M12 the page is not read again after a decision", VIEW,
     "    if (r.ok) setPage(r.data);\n    else setProblem(r.problem);", "    void r;", [V]),
    ("M13 the AI's prose is always asked in English", ASK,
     "output_locale: locale", 'output_locale: "en"', [A]),
    ("M14 sending ignores unsaved edits", ASK,
     "<Button blocked={blockedReason !== null}", "<Button blocked={false}", [A]),
    ("M15 a retried request takes a new key", ASK,
     "if (sent.current?.request !== request) sent.current", "if (true) sent.current", [A]),
    ("M16 an empty request is sent", ASK,
     'if (words === "") {', 'if (words === "" && false) {', [A]),
    ("M17 every op is read against the base, not the model right before it", CHG,
     "    doc = next.doc;\n", "", [C]),
    ("M18 a prototype member is read", CHG,
     "tokens.some((t) => UNSAFE.has(t))", "false", [C]),
    ("M19 a time reads without its unit", LIST,
     'if (v.format === "time") return duration(v.value, base.time_unit);', 'if (v.format === "time") return number(v.value);', [V]),
    ("M20 a queue's capacity removed reads 'not set', not 'no limit'", LIST,
     '(s.kind === "step" && f.key === "capacity") ||', "", [V]),
    ("M21 a test op is listed as a change", CHG,
     'if (op.op !== "test") describeOp(', "if (true) describeOp(op as Exclude<Op, { op: \"test\" }>, tokens, doc, names, out, added, moved, () => {}); if (false) describeOp(", [C]),
]


def run(tests):
    r = subprocess.run(["npx", "vitest", "run", *tests], capture_output=True, text=True, timeout=900)
    return r.returncode


def main():
    killed = survived = 0
    only = [f"{a} " for a in sys.argv[1:]]  # e.g. M6: run only those mutants
    for name, path, old, new, tests in MUTANTS:
        if only and not any(name.startswith(o) for o in only):
            continue
        p = pathlib.Path(path)
        src = p.read_text()
        if src.count(old) != 1:
            print(f"{name}: SKIPPED (the text to change occurs {src.count(old)} times)")
            continue
        p.write_text(src.replace(old, new))
        try:
            code = run(tests)
        finally:
            p.write_text(src)
        if code != 0:
            killed += 1
            print(f"{name}: killed ({' '.join(tests)} exit {code})")
        else:
            survived += 1
            print(f"{name}: SURVIVED ({' '.join(tests)} exit 0)")
        sys.stdout.flush()
    print(f"run {killed + survived} of {len(MUTANTS)}: {killed} killed, {survived} survived")


if __name__ == "__main__":
    main()
