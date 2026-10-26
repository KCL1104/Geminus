// T-015 on the T-014 draft session: after a 409 the edits stay (conflict);
// the person reloads the current draft and may then choose to put their
// edits on top of it. That choice is theirs: nothing reapplies by itself
// (REQ-006, AGENTS §2), and the next save goes with the new revision's
// If-Match, so the server still refuses it if the draft moved again.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import type { Problem } from "../lib/api/problem";
import type { Draft, JsonPatch } from "../lib/contracts";
import { canPublish, nextSave, openDraft, pendingOps, reduceDraft } from "../lib/state/draft";

const draft = JSON.parse(readFileSync(join(process.cwd(), "../../testdata/contracts/valid-draft-with-issues.json"), "utf8")) as Draft;
const at = (revision: number): Draft => ({ ...draft, revision, issues: [] });
const op = (n: number): JsonPatch[number] => ({ op: "replace", path: "/name", value: `edit ${n}` });
const conflict: Problem = { kind: "conflict", code: "MODEL_VERSION_CONFLICT", status: 409, message: "", requestId: null, details: [], retryAfterSeconds: null };

function inConflict() {
  let s = reduceDraft(openDraft(at(3)), { type: "edit", ops: [op(1), op(2)] });
  s = reduceDraft(s, { type: "saveStarted", revision: 3, ops: [op(1), op(2)] });
  return reduceDraft(s, { type: "saveFailed", problem: conflict });
}

describe("reapplying edits after a conflict (only when the person asks)", () => {
  it("keeps the edits on the reloaded draft, unsaved, and saves them with its revision", () => {
    const reloaded = reduceDraft(inConflict(), { type: "remoteLoaded", draft: at(5) });
    expect([reloaded.status, nextSave(reloaded)]).toEqual(["conflict", null]);
    const s = reduceDraft(reloaded, { type: "reapplyLocal" });
    expect([s.status, s.remote.revision, pendingOps(s), s.problem]).toEqual(["dirty", 5, [op(1), op(2)], null]);
    expect(nextSave(s)).toEqual({ revision: 5, ops: [op(1), op(2)] });
    expect(canPublish(s)).toBe(false);
  });

  it("does nothing outside a conflict, or with a save in flight", () => {
    const dirty = reduceDraft(openDraft(at(3)), { type: "edit", ops: [op(1)] });
    expect(reduceDraft(dirty, { type: "reapplyLocal" })).toBe(dirty);
    const clean = openDraft(at(3));
    expect(reduceDraft(clean, { type: "reapplyLocal" })).toBe(clean);
    const saving = reduceDraft(dirty, { type: "saveStarted", revision: 3, ops: [op(1)] });
    expect(reduceDraft(saving, { type: "reapplyLocal" })).toBe(saving);
    const error = reduceDraft(saving, { type: "saveFailed", problem: { ...conflict, kind: "network", code: null, status: null } });
    expect(error.status).toBe("error");
    expect(reduceDraft(error, { type: "reapplyLocal" })).toBe(error);
  });

  it("is never what a reload does on its own", () => {
    const s = reduceDraft(inConflict(), { type: "remoteLoaded", draft: at(6) });
    expect(s.status).toBe("conflict");
    expect(pendingOps(s)).toEqual([op(1), op(2)]);
  });
});
