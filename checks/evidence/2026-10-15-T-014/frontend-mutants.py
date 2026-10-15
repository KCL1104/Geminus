#!/usr/bin/env python3
"""Frontend mutation check for T-014: each mutant must fail `vitest run` or `tsc`."""
import os, pathlib, shutil, subprocess, sys
REPO = pathlib.Path("/home/user/Geminus/apps/web")
M = pathlib.Path(sys.argv[1]) / "apps/web"
MUTANTS = [
    ("client-ignores-missing-session", "lib/api/client.ts", "    if (session === null) return { ok: false, problem: unauthenticatedProblem() };\n", ""),
    ("client-sends-cookies", "lib/api/client.ts", 'credentials: "omit"', 'credentials: "include"'),
    ("client-no-tenant-header", "lib/api/client.ts", '      "X-Organization-ID": session.organizationId,\n', ""),
    ("client-no-if-match", "lib/api/client.ts", '    if (req.ifMatch) headers["If-Match"] = req.ifMatch;\n', ""),
    ("client-trusts-route-ids", "lib/api/client.ts", '      if (!isID("mdl", modelId)) return notFound("The model");\n      return call<Model>', "      return call<Model>"),
    ("draft-etag-unchecked", "lib/api/client.ts", "    if (revision === null || revision !== r.data.revision) {", "    if (revision === null) {"),
    ("problem-ignores-status-mismatch", "lib/api/problem.ts", "  if (errorCodeStatus[known] !== status) {", "  if (false) {"),
    ("problem-trusts-request-id", "lib/api/problem.ts", 'typeof err.request_id === "string" && REQUEST_ID.test(err.request_id)', 'typeof err.request_id === "string"'),
    ("etag-accepts-weak", "lib/api/etag.ts", 'const DRAFT_ETAG = /^"draft-revision-', 'const DRAFT_ETAG = /^(?:W\\/)?"draft-revision-'),
    ("ids-case-insensitive", "lib/api/ids.ts", 're = new RegExp(`^${prefix}_${ULID}$`);', 're = new RegExp(`^${prefix}_${ULID}$`, "i");'),
    ("draft-auto-rebase", "lib/state/draft.ts", '      return { ...s, remote: e.draft, unsaved: pendingOps(s), inFlight: null, status: "conflict", problem: null };',
     '      return settle({ remote: e.draft, unsaved: pendingOps(s), inFlight: null });'),
    ("draft-conflict-drops-edits", "lib/state/draft.ts", "        unsaved: pendingOps(s),\n        status: e.problem.kind === \"conflict\"", "        unsaved: [],\n        status: e.problem.kind === \"conflict\""),
    ("draft-save-during-conflict", "lib/state/draft.ts", ' || s.status === "conflict") return null;', ") return null;"),
    ("draft-publish-dirty", "lib/state/draft.ts", '    s.status === "clean" &&\n', ""),
    ("selection-accepts-unknown", "lib/state/selection.ts", "      if (id !== null && !ids.has(id)) return false;\n", ""),
    ("viewer-can-edit", "lib/auth/session.tsx", 'return role === "owner" || role === "editor";', 'return role !== null;'),
    ("not-found-echoes-server", "app/_components/problem-view.tsx", '{problem.message && problem.kind !== "not_found" && <p>{problem.message}</p>}', "{problem.message && <p>{problem.message}</p>}"),
    ("require-session-open", "app/_components/states.tsx", "  if (session === null) {\n    return (", "  if (session === undefined) {\n    return ("),
    ("loader-half-shown", "lib/pages.ts", "  if (!draft.ok) return draft;\n", ""),
]
def main():
    results = []
    for name, rel, old, new in MUTANTS:
        if M.exists(): shutil.rmtree(M)
        shutil.copytree(REPO, M, ignore=shutil.ignore_patterns("node_modules", ".next", "tsconfig.tsbuildinfo"))
        (M / "node_modules").symlink_to(REPO / "node_modules")
        src = (M / rel).read_text()
        if src.count(old) != 1:
            results.append((name, "BAD-MUTANT")); continue
        (M / rel).write_text(src.replace(old, new))
        env = dict(os.environ)
        t = subprocess.run(["npx", "vitest", "run"], cwd=M, capture_output=True, text=True, env=env)
        c = subprocess.run(["npx", "tsc", "--noEmit"], cwd=M, capture_output=True, text=True, env=env)
        results.append((name, "KILLED" if (t.returncode or c.returncode) else "SURVIVED"))
    for r in results: print("\t".join(r))
    killed = sum(1 for r in results if r[1] == "KILLED")
    print(f"{killed}/{len(results)} killed")
    sys.exit(0 if killed == len(results) else 1)
main()
