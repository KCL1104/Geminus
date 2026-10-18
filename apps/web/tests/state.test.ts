// T-014: remote state, in-flight saves and unsaved edits stay separate; edits
// are never lost or rebased; selection holds only IR IDs (TEST-025, partial).
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { Problem } from "../lib/api/problem";
import type { Draft, JsonPatch, ModelIR } from "../lib/contracts";
import { canPublish, type DraftEvent, type DraftSession, loadedDraft, nextSave, openDraft, pendingOps, reduceDraft } from "../lib/state/draft";
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
/** saveStarted for exactly what was sent: `ops` on top of `revision`. */
const started = (revision: number, ...ops: JsonPatch): DraftEvent => ({ type: "saveStarted", revision, ops });

describe("draft session", () => {
  it("saves with the remote revision and becomes clean on success", () => {
    let s = run(openDraft(at(3)), { type: "edit", ops: [op(1), op(2)] });
    expect([s.status, nextSave(s)]).toEqual(["dirty", { revision: 3, ops: [op(1), op(2)] }]);
    s = reduceDraft(s, started(3, op(1), op(2)));
    expect([s.status, s.inFlight, s.unsaved, nextSave(s)]).toEqual(["saving", [op(1), op(2)], [], null]);
    s = reduceDraft(s, { type: "saveSucceeded", draft: at(4) });
    expect([s.status, s.remote.revision, pendingOps(s)]).toEqual(["clean", 4, []]);
  });

  it("keeps edits made while saving for the next save", () => {
    const s = run(openDraft(at(3)), { type: "edit", ops: [op(1)] }, started(3, op(1)), { type: "edit", ops: [op(2)] },
      { type: "saveSucceeded", draft: at(4) });
    expect([s.status, nextSave(s)]).toEqual(["dirty", { revision: 4, ops: [op(2)] }]);
  });

  it("turns a 409 into a conflict that keeps every edit in order", () => {
    const s = run(openDraft(at(3)), { type: "edit", ops: [op(1)] }, started(3, op(1)), { type: "edit", ops: [op(2)] },
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
    const saving = run(openDraft(at(3)), { type: "edit", ops: [op(1)] }, started(3, op(1)));
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

  // Review of T-014: the session records what was actually sent. An edit
  // reduced between computing the save and starting it stays unsaved.
  it("puts in flight exactly the ops that were sent", () => {
    let s = run(openDraft(at(3)), { type: "edit", ops: [op(1)] });
    const save = nextSave(s)!;
    s = reduceDraft(s, { type: "edit", ops: [op(2)] });
    s = reduceDraft(s, { type: "saveStarted", ...save });
    expect([s.inFlight, s.unsaved]).toEqual([[op(1)], [op(2)]]);
    s = reduceDraft(s, { type: "saveSucceeded", draft: at(4) });
    expect([s.status, pendingOps(s)]).toEqual(["dirty", [op(2)]]);
    // A start that does not match the session (wrong revision, not a prefix) is ignored.
    const dirty = run(openDraft(at(3)), { type: "edit", ops: [op(1)] });
    expect(reduceDraft(dirty, started(2, op(1)))).toBe(dirty);
    expect(reduceDraft(dirty, started(3, op(9)))).toBe(dirty);
    expect(reduceDraft(dirty, started(3))).toBe(dirty);
  });

  // Only a stale revision is a draft conflict; other 409s are errors that keep the edits.
  it("treats only MODEL_VERSION_CONFLICT as a draft conflict", () => {
    const saving = run(openDraft(at(3)), { type: "edit", ops: [op(1)] }, started(3, op(1)));
    const lifecycle: Problem = { ...problem("conflict"), code: "LIFECYCLE_CONFLICT" };
    const s = reduceDraft(saving, { type: "saveFailed", problem: lifecycle });
    expect([s.status, pendingOps(s), s.problem?.code]).toEqual(["error", [op(1)], "LIFECYCLE_CONFLICT"]);
    expect(reduceDraft(saving, { type: "saveFailed", problem: problem("conflict") }).status).toBe("conflict");
  });

  // Review of T-014: reloading the page data (e.g. after a token refresh)
  // merges into the open session instead of reopening it.
  it("merges a reloaded draft into the open session", () => {
    expect(loadedDraft(null, at(3))).toEqual(openDraft(at(3)));
    const dirty = run(openDraft(at(3)), { type: "edit", ops: [op(1)] });
    expect(pendingOps(loadedDraft(dirty, at(3)))).toEqual([op(1)]);
    expect(loadedDraft(dirty, at(2))).toBe(dirty);
    const conflict = loadedDraft(dirty, at(4));
    expect([conflict.status, pendingOps(conflict)]).toEqual(["conflict", [op(1)]]);
    const saving = reduceDraft(dirty, started(3, op(1)));
    expect(loadedDraft(saving, at(4))).toBe(saving);
    const other = { ...at(9), model_id: "mdl_01M4YDQK00TA00000000000002" };
    expect(loadedDraft(dirty, other)).toEqual(openDraft(other));
  });

  // Property with an independent server: the server applies a save only on
  // its current revision (If-Match) and others may save too. Whatever the
  // order of edits, saves, answers, reloads and discards, every edit the
  // user did not discard is either applied once on the server or still
  // pending locally, in order — never lost, duplicated or reordered.
  it("never loses, duplicates or reorders edits (random sequences, independent server)", () => {
    let seed = 42;
    const rand = (n: number) => ((seed = (seed * 1103515245 + 12345) % 2 ** 31), seed % n);
    const n = (o: JsonPatch[number]) => Number(String((o as { value: string }).value).slice(5));
    for (let trial = 0; trial < 3000; trial++) {
      let s = openDraft(at(1));
      const server = { revision: 1, ours: [] as number[] };
      let request: { revision: number; ops: JsonPatch } | null = null;
      const kept: number[] = []; // every edit the user did not discard, in order
      let next = 1;
      const edit = () => {
        const ops = Array.from({ length: rand(3) + 1 }, () => op(next++));
        kept.push(...ops.map(n));
        s = reduceDraft(s, { type: "edit", ops });
      };
      for (let step = 0; step < 40; step++) {
        const choice = rand(9);
        if (choice <= 1) edit();
        else if (choice === 2 && request === null) {
          const save = nextSave(s);
          if (save !== null) {
            if (rand(2) === 0) edit(); // an edit lands between computing and starting the save
            request = save;
            s = reduceDraft(s, { type: "saveStarted", ...save });
          }
        } else if (choice === 3 && request !== null) {
          if (request.revision === server.revision) {
            server.revision++;
            server.ours.push(...request.ops.map(n));
            s = reduceDraft(s, { type: "saveSucceeded", draft: at(server.revision) });
          } else {
            s = reduceDraft(s, { type: "saveFailed", problem: problem("conflict") });
          }
          request = null;
        } else if (choice === 4 && request !== null) {
          s = reduceDraft(s, { type: "saveFailed", problem: problem("network") }); // never reached the server
          request = null;
        } else if (choice === 5) server.revision++; // someone else saved
        else if (choice === 6) s = reduceDraft(s, { type: "remoteLoaded", draft: at(server.revision - rand(2)) });
        else if (choice === 7) {
          const before = s;
          s = reduceDraft(s, { type: "discardLocal" });
          if (s !== before) for (const o of pendingOps(before)) kept.splice(kept.indexOf(n(o)), 1);
        } else if (choice === 8) s = loadedDraft(s, at(server.revision));
        expect([...server.ours, ...pendingOps(s).map(n)]).toEqual(kept);
        if (s.status === "clean") expect(pendingOps(s)).toEqual([]);
        if (request === null) expect(s.inFlight).toBeNull();
      }
    }
    // About 3 s idle: the default 5 s fails it under load, and a timeout is
    // not a finding (T-044 review L2). The assertions are unchanged.
  }, 60_000);
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
