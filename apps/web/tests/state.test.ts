// T-014: remote state, in-flight saves and unsaved edits stay separate; edits
// are never lost or rebased; selection holds only IR IDs (TEST-025, partial).
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { Problem } from "../lib/api/problem";
import type { Draft, JsonPatch, ModelIR } from "../lib/contracts";
import { canPublish, type DraftEvent, type DraftSession, nextSave, openDraft, pendingOps, reduceDraft } from "../lib/state/draft";
import { createSelectionStore, entityIds } from "../lib/state/selection";

const fixture = <T>(name: string): T =>
  JSON.parse(readFileSync(new URL(`../../../testdata/contracts/${name}`, import.meta.url), "utf8")) as T;

const base = fixture<Draft>("valid-draft-with-issues.json");
const at = (revision: number, extra: Partial<Draft> = {}): Draft => ({ ...base, revision, issues: [], out_of_date: false, ...extra });
const op = (n: number): JsonPatch[number] => ({ op: "replace", path: "/name", value: `edit ${n}` });
const problem = (kind: Problem["kind"]): Problem => ({
  kind, code: kind === "conflict" ? "MODEL_VERSION_CONFLICT" : null, status: null, message: kind, requestId: null, details: [], retryAfterSeconds: null,
});
const run = (s: DraftSession, ...events: DraftEvent[]) => events.reduce(reduceDraft, s);

describe("draft session", () => {
  it("saves with the remote revision and becomes clean on success", () => {
    let s = run(openDraft(at(3)), { type: "edit", ops: [op(1), op(2)] });
    expect([s.status, nextSave(s)]).toEqual(["dirty", { revision: 3, ops: [op(1), op(2)] }]);
    s = reduceDraft(s, { type: "saveStarted" });
    expect([s.status, s.inFlight, s.unsaved, nextSave(s)]).toEqual(["saving", [op(1), op(2)], [], null]);
    s = reduceDraft(s, { type: "saveSucceeded", draft: at(4) });
    expect([s.status, s.remote.revision, pendingOps(s)]).toEqual(["clean", 4, []]);
  });

  it("keeps edits made while saving for the next save", () => {
    const s = run(openDraft(at(3)), { type: "edit", ops: [op(1)] }, { type: "saveStarted" }, { type: "edit", ops: [op(2)] },
      { type: "saveSucceeded", draft: at(4) });
    expect([s.status, nextSave(s)]).toEqual(["dirty", { revision: 4, ops: [op(2)] }]);
  });

  it("turns a 409 into a conflict that keeps every edit in order", () => {
    const s = run(openDraft(at(3)), { type: "edit", ops: [op(1)] }, { type: "saveStarted" }, { type: "edit", ops: [op(2)] },
      { type: "saveFailed", problem: problem("conflict") });
    expect([s.status, pendingOps(s), s.remote.revision, nextSave(s)]).toEqual(["conflict", [op(1), op(2)], 3, null]);
    expect(s.problem?.code).toBe("MODEL_VERSION_CONFLICT");
  });

  it("never rebases local edits onto a newer remote revision", () => {
    const s = run(openDraft(at(3)), { type: "edit", ops: [op(1)] }, { type: "remoteLoaded", draft: at(5) });
    expect([s.status, s.remote.revision, pendingOps(s), nextSave(s)]).toEqual(["conflict", 5, [op(1)], null]);
    const discarded = reduceDraft(s, { type: "discardLocal" });
    expect([discarded.status, discarded.remote.revision, pendingOps(discarded)]).toEqual(["clean", 5, []]);
  });

  it("ignores stale or foreign reads and refreshes when there are no edits", () => {
    const s = openDraft(at(5));
    expect(reduceDraft(s, { type: "remoteLoaded", draft: at(4) })).toBe(s);
    expect(reduceDraft(s, { type: "remoteLoaded", draft: { ...at(9), model_id: "mdl_01M4YDQK00TA00000000000002" } })).toBe(s);
    expect(reduceDraft(s, { type: "remoteLoaded", draft: at(6) }).remote.revision).toBe(6);
  });

  it("lets the save response, not a refresh, decide an in-flight save", () => {
    const saving = run(openDraft(at(3)), { type: "edit", ops: [op(1)] }, { type: "saveStarted" });
    expect(reduceDraft(saving, { type: "remoteLoaded", draft: at(4) })).toBe(saving); // may be our own save
    expect(reduceDraft(saving, { type: "discardLocal" })).toBe(saving);
    const odd = reduceDraft(saving, { type: "saveSucceeded", draft: at(3) });
    expect([odd.status, pendingOps(odd), odd.problem?.kind]).toEqual(["error", [op(1)], "protocol"]);
  });

  it("allows publishing only a clean, current draft without errors", () => {
    expect(canPublish(openDraft(at(3)))).toBe(true);
    expect(canPublish(run(openDraft(at(3)), { type: "edit", ops: [op(1)] }))).toBe(false);
    expect(canPublish(openDraft(at(3, { out_of_date: true })))).toBe(false);
    expect(canPublish(openDraft(at(3, { issues: [{ code: "NO_REACHABLE_SINK", path: "/nodes/1", message: "x", severity: "error" }] })))).toBe(false);
    expect(canPublish(openDraft(at(3, { issues: [{ code: "UNREACHABLE_NODE", path: "/nodes/1", message: "x", severity: "warning" }] })))).toBe(true);
  });

  // Property: under any event sequence, the user's edits that the server has
  // not acknowledged are exactly pendingOps, in order; saves always target the
  // current remote revision.
  it("never loses, duplicates or reorders edits (random sequences)", () => {
    let seed = 42;
    const rand = (n: number) => ((seed = (seed * 1103515245 + 12345) % 2 ** 31), seed % n);
    for (let trial = 0; trial < 2000; trial++) {
      let s = openDraft(at(1));
      let next = 1;
      let expected: JsonPatch = [];
      let sent: JsonPatch | null = null;
      for (let step = 0; step < 30; step++) {
        const choice = rand(7);
        let e: DraftEvent;
        if (choice <= 1) {
          const ops = Array.from({ length: rand(3) + 1 }, () => op(next++));
          expected = [...expected, ...ops];
          e = { type: "edit", ops };
        } else if (choice === 2) {
          const save = nextSave(s);
          if (save !== null) expect(save.revision).toBe(s.remote.revision);
          e = { type: "saveStarted" };
        } else if (choice === 3) {
          e = { type: "saveSucceeded", draft: at(s.remote.revision + 1) };
        } else if (choice === 4) {
          e = { type: "saveFailed", problem: problem(rand(2) === 0 ? "conflict" : "network") };
        } else if (choice === 5) {
          e = { type: "remoteLoaded", draft: at(s.remote.revision + rand(3) - 1) };
        } else {
          e = { type: "discardLocal" };
        }
        const before = s;
        s = reduceDraft(s, e);
        if (e.type === "saveStarted" && before.inFlight === null && s.inFlight !== null) sent = s.inFlight;
        if (e.type === "saveSucceeded" && before.inFlight !== null && s.status !== "error") {
          expected = expected.slice(sent!.length); // the server took what we sent
        }
        if (e.type === "discardLocal" && before.inFlight === null) expected = [];
        if (e.type === "remoteLoaded" && pendingOps(before).length === 0 && s !== before) expected = [];
        expect(pendingOps(s)).toEqual(expected);
        if (s.status === "clean") expect(pendingOps(s)).toEqual([]);
      }
    }
  });
});

describe("selection", () => {
  const ir = fixture<ModelIR>("valid-minimal-ir.json");

  it("selects only entities of the current model and notifies once per change", () => {
    const store = createSelectionStore(ir);
    let calls = 0;
    const off = store.subscribe(() => calls++);
    expect(store.select("work")).toBe(true);
    expect(store.select("work")).toBe(true); // no change, no notification
    expect(store.select("worker")).toBe(true);
    expect(store.select("work_end")).toBe(true);
    expect(store.select("not_in_model")).toBe(false);
    expect([store.get(), calls]).toEqual(["work_end", 3]);
    off();
    store.select(null);
    expect(calls).toBe(3);
  });

  it("drops a selection whose entity disappears and keeps one that survives", () => {
    const store = createSelectionStore(ir);
    store.select("work");
    store.setModel({ ...ir, name: "renamed" });
    expect(store.get()).toBe("work");
    store.setModel({ ...ir, nodes: ir.nodes.filter((n) => n.id !== "work"), flows: [] });
    expect(store.get()).toBeNull();
    expect(entityIds(ir)).toEqual(new Set(["start", "work", "end", "worker", "start_work", "work_end"]));
  });
});
