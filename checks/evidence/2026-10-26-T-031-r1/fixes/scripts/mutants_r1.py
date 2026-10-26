"""T-031 review round 1: hand-written mutants against the fixes (one change
each), run against the test files that should kill them, then restored.
The reviewer's MA, MB and MC are here (MA at the two places it now maps to:
the shared rendering of data parts, and the added/removed line's subject,
where the reviewer put it), with the M1, L1, L4 and I3 mutants. A mutant is
killed when the tests fail with it; for the markup mutants, lint is also
run on the mutated file.

    python -I mutants_r1.py <repo root> [ids...]

Prints one line per mutant and a total of the mutants it ran.
"""
import json
import re
import subprocess
import sys
from pathlib import Path

root = Path(sys.argv[1])
only = set(sys.argv[2:])
web = root / "apps/web"
F = "apps/web/features/ai-proposals/"
names = "tests/proposal-names.test.tsx"
view = "tests/proposal-view.test.tsx"
changes = "tests/proposal-changes.test.ts"
shown = "tests/proposal-shown.test.ts"
simplicity = "tests/proposal-simplicity.test.tsx"
preview = "tests/proposal-preview.test.tsx"
ask = "tests/proposal-ask.test.tsx"

mutants = [
    ("MA", "data parts rendered as HTML (the shared <bdi>)", F + "words.tsx",
     ": <bdi key={i}>{p.data}</bdi>))}", ": <bdi key={i} dangerouslySetInnerHTML={{ __html: p.data }} />))}",
     [names, view, changes, simplicity, preview], True),
    ("MA-subject", "the reviewer's MA: an added/removed line's subject as HTML", F + "change-list.tsx",
     "            <Words parts={element(c.subject)} />", "            <span dangerouslySetInnerHTML={{ __html: flat(element(c.subject)) }} />",
     [names, view, changes, simplicity, preview], True),
    ("MA-assumptions", "assumptions as HTML", F + "proposal-view.tsx",
     '<li key={i}>{visible(a, "prose")}</li>', '<li key={i} dangerouslySetInnerHTML={{ __html: visible(a, "prose") }} />',
     [names, view], True),
    ("MA-questions", "questions as HTML", F + "proposal-view.tsx",
     '<li key={i}>{visible(q, "prose")}</li>', '<li key={i} dangerouslySetInnerHTML={{ __html: visible(q, "prose") }} />',
     [names, view], True),
    ("MB", "after a 409, approve again on the new base with a new key", F + "proposal-view.tsx",
     '      setOutcome({ kind: "conflict" });\n',
     '      setOutcome({ kind: "conflict" });\n      void api.approveProposal(id, { expected_base_version_id: page.model.latest_version.id }, newIdempotencyKey());\n',
     [view, simplicity, ask], False),
    ("MC", "a new key for every rejection", F + "proposal-view.tsx",
     "    if (rejectKey.current?.comment !== comment) rejectKey.current = { comment, key: newIdempotencyKey() };",
     "    rejectKey.current = { comment, key: newIdempotencyKey() };",
     [view, simplicity, ask], False),
    ("M1-no-ids", "never an ID beside a name", F + "change-list.tsx",
     "    return r.showId && r.name !== r.id ? rich(", "    return false && r.showId && r.name !== r.id ? rich(",
     [names, view, simplicity, preview], False),
    ("M1-new-not-shown", "a new entity without its ID", F + "changes.ts",
     "        if (!before.has(id)) shown.add(`${catalog} ${id}`);\n", "",
     [changes, names, view], False),
    ("M1-exact-names", "shared only when the names are equal strings", F + "changes.ts",
     "add(byName, nameKey(nameOf(e as Record<string, unknown>, id)), id);", "add(byName, nameOf(e as Record<string, unknown>, id), id);",
     [changes, names, view], False),
    ("M1-flow-ids", "parallel connections without their IDs", F + "changes.ts",
     '  shared("flows", byEnds);\n', "",
     [changes, names, view], False),
    ("M1-raw", "names and values without their hidden characters shown", F + "words.tsx",
     "export const data = (s: string): Rich => [{ data: visible(s) }];", "export const data = (s: string): Rich => [{ data: s }];",
     [names, view], False),
    ("M1-no-bdi", "data parts not isolated", F + "words.tsx",
     ": <bdi key={i}>{p.data}</bdi>))}", ": <Fragment key={i}>{p.data}</Fragment>))}",
     [names, view], False),
    ("M1-no-same", "X → X when the two sides read the same", F + "change-list.tsx",
     "    if (!readsAlike(flat(before), flat(after))) return rich(key, { [slot]: label, before, after });",
     "    return rich(key, { [slot]: label, before, after });",
     [names, view], False),
    ("M1-no-ascii", "exact values never in ASCII", F + "change-list.tsx",
     '    return readsAlike(shown(v), shown(other)) ? data(asciiJson(rawOf(v))) : data(JSON.stringify(rawOf(v)) ?? "");',
     '    return data(JSON.stringify(rawOf(v)) ?? "");',
     [names, view], False),
    ("M1-patch-raw", "the exact JSON Patch with its hidden characters raw", F + "change-list.tsx",
     "<pre>{patchJson(changes)}</pre>", "<pre>{JSON.stringify(changes, null, 2)}</pre>",
     [names, view], False),
    ("M1-prose-raw", "assumptions with their hidden characters raw", F + "proposal-view.tsx",
     '<li key={i}>{visible(a, "prose")}</li>', "<li key={i}>{a}</li>",
     [names, view], False),
    ("M1-key-case", "the name key keeps letter case", F + "shown.ts",
     "    .trim()\n    .toLowerCase();", "    .trim();",
     [shown, changes], False),
    ("L4-never", "a refused approval offers Approve again (no refusal noted)", F + "proposal-view.tsx",
     "      const why = refusedBy(r.problem);", "      const why = null as Refused;",
     [view, simplicity], False),
    ("L4-no-risk", "a risk-changed 409 is not a refusal", F + "proposal-view.tsx",
     '  if (problem.code === "LIFECYCLE_CONFLICT" && problem.details.some((d) => d.path === "/risk_level" || d.path === "/server_risk_level")) return "risk";\n',
     "", [view, simplicity], False),
    ("L4-approve-shown", "Approve offered beside the refusal note", F + "proposal-view.tsx",
     '  const approvable = p.status === "ready" && !stale && refused === null;', '  const approvable = p.status === "ready" && !stale;',
     [view, simplicity], False),
    ("L4-focus", "focus not moved to the refusal note", F + "proposal-view.tsx",
     '      setFocus(why === null ? "approve" : "refused");', '      setFocus("approve");',
     [view], False),
    ("I3", "an error code looked up with `in`", F + "proposal-view.tsx",
     "!Object.hasOwn(errorCodeStatus, code)", "!(code in errorCodeStatus)",
     [view], False),
]


def summary(out: str) -> str:
    m = re.findall(r"Tests\s+(.*)", out)
    return m[-1].strip() if m else out.strip().splitlines()[-1] if out.strip() else ""


results = []
for mid, what, rel, old, new, tests, lint in mutants:
    if only and mid not in only:
        continue
    path = root / rel
    text = path.read_text(encoding="utf-8")
    if text.count(old) != 1:
        print(f"{mid}: NOT APPLIED (the original text occurs {text.count(old)} times)")
        results.append((mid, "not applied"))
        continue
    path.write_text(text.replace(old, new), encoding="utf-8")
    try:
        r = subprocess.run(["npx", "vitest", "run", *tests], cwd=web, capture_output=True, text=True)
        verdict = "killed" if r.returncode != 0 else "SURVIVED"
        line = f"{mid}: {verdict} ({what}; {rel}; vitest exit {r.returncode}: {summary(r.stdout + r.stderr)}; {' '.join(tests)})"
        if lint:
            l = subprocess.run(["npx", "eslint", "--max-warnings=0", str(path.relative_to(web))], cwd=web, capture_output=True, text=True)
            rules = sorted(set(re.findall(r"react/no-danger", l.stdout)))
            line += f"; eslint exit {l.returncode} {rules}"
    finally:
        path.write_text(text, encoding="utf-8")
    diff = subprocess.run(["git", "-C", str(root), "diff", "--quiet", "--", rel])
    if diff.returncode != 0:
        raise SystemExit(f"{rel} was not restored")
    print(line, flush=True)
    results.append((mid, verdict))

killed = sum(1 for _, v in results if v == "killed")
survived = sum(1 for _, v in results if v == "SURVIVED")
print(f"ran {len(results)}: {killed} killed, {survived} survived, {len(results) - killed - survived} not applied")
