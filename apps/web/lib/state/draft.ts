// The three kinds of model state the UI holds, kept apart (docs/ARCHITECTURE.md
// §7, T-014):
//   remote  — the server's saved draft at some revision (read-only here);
//   inFlight — ops sent with If-Match that the server has not answered yet;
//   unsaved — edits made locally since then.
// Edits are never dropped and never rebased automatically: a stale-revision
// 409 (MODEL_VERSION_CONFLICT) or a newer remote revision while edits are
// pending puts the session in "conflict", keeping every edit until the user
// explicitly discards them. Edits live in page state only: leaving or
// reloading the page loses unsaved ones.
import { type Problem, protocolProblem } from "../api/problem";
import type { Draft, JsonPatch } from "../contracts";

type Op = JsonPatch[number];

export type DraftStatus = "clean" | "dirty" | "saving" | "conflict" | "error";

export type DraftSession = {
  remote: Draft;
  inFlight: Op[] | null;
  unsaved: Op[];
  status: DraftStatus;
  /** The problem behind "conflict" or "error"; null otherwise. */
  problem: Problem | null;
};

export type DraftEvent =
  | { type: "edit"; ops: Op[] }
  /** The caller sent `ops` (from nextSave) with If-Match `revision`. */
  | { type: "saveStarted"; revision: number; ops: Op[] }
  | { type: "saveSucceeded"; draft: Draft }
  | { type: "saveFailed"; problem: Problem }
  | { type: "remoteLoaded"; draft: Draft }
  | { type: "discardLocal" };

export function openDraft(remote: Draft): DraftSession {
  return { remote, inFlight: null, unsaved: [], status: "clean", problem: null };
}

/**
 * A draft loaded for the page (first load, or a reload such as after a token
 * refresh). It merges into an open session of the same model like any other
 * read, so pending edits survive; only a first load or another model opens a
 * new session.
 */
export function loadedDraft(s: DraftSession | null, draft: Draft): DraftSession {
  return s === null || s.remote.model_id !== draft.model_id ? openDraft(draft) : reduceDraft(s, { type: "remoteLoaded", draft });
}

const sameOp = (a: Op, b: Op | undefined) => a === b || JSON.stringify(a) === JSON.stringify(b);

/** Everything the user has done locally and the server has not accepted yet, in order. */
export function pendingOps(s: DraftSession): Op[] {
  return [...(s.inFlight ?? []), ...s.unsaved];
}

/** What to send for the next save, and the revision it applies to; null when there is nothing to save or a save is running. */
export function nextSave(s: DraftSession): { revision: number; ops: Op[] } | null {
  if (s.inFlight !== null || s.unsaved.length === 0 || s.status === "conflict") return null;
  return { revision: s.remote.revision, ops: s.unsaved };
}

/** Whether the draft the user sees can be published: saved, current and free of errors (D-028). */
export function canPublish(s: DraftSession): boolean {
  return (
    s.status === "clean" &&
    !s.remote.out_of_date &&
    !s.remote.issues.some((i) => i.severity === "error")
  );
}

function settle(s: Omit<DraftSession, "status" | "problem">): DraftSession {
  return { ...s, status: s.inFlight !== null ? "saving" : s.unsaved.length > 0 ? "dirty" : "clean", problem: null };
}

export function reduceDraft(s: DraftSession, e: DraftEvent): DraftSession {
  switch (e.type) {
    case "edit":
      if (e.ops.length === 0) return s;
      // Edits made during a conflict or error are kept too; they wait for the user.
      return s.status === "conflict" || s.status === "error"
        ? { ...s, unsaved: [...s.unsaved, ...e.ops] }
        : settle({ ...s, unsaved: [...s.unsaved, ...e.ops] });
    case "saveStarted": {
      // Record exactly what was sent: the leading unsaved ops, on the current
      // revision. Edits reduced after the caller computed the save stay
      // unsaved. A start that does not match is ignored; its answer then
      // finds no save in flight, and the ops stay pending (the server rejects
      // a repeat with a stale If-Match).
      const next = nextSave(s);
      const n = e.ops.length;
      if (next === null || e.revision !== next.revision || n === 0 || !e.ops.every((o, i) => sameOp(o, s.unsaved[i]))) return s;
      return { ...s, inFlight: s.unsaved.slice(0, n), unsaved: s.unsaved.slice(n), status: "saving", problem: null };
    }
    case "saveSucceeded":
      if (s.inFlight === null) return s;
      if (e.draft.revision <= s.remote.revision) {
        // A success that does not advance the revision cannot be the save we sent.
        return {
          ...s,
          unsaved: pendingOps(s),
          inFlight: null,
          status: "error",
          problem: protocolProblem(200, "The save response did not advance the draft revision."),
        };
      }
      return settle({ remote: e.draft, inFlight: null, unsaved: s.unsaved });
    case "saveFailed":
      if (s.inFlight === null) return s;
      return {
        ...s,
        inFlight: null,
        unsaved: pendingOps(s),
        // Only a stale revision is a draft conflict; any other 409 (lifecycle,
        // idempotency...) is an error that keeps the edits.
        status: e.problem.code === "MODEL_VERSION_CONFLICT" ? "conflict" : "error",
        problem: e.problem,
      };
    case "remoteLoaded":
      if (e.draft.model_id !== s.remote.model_id) return s;
      // While a save is in flight a newer revision may be our own; its response decides.
      if (s.inFlight !== null) return s;
      if (e.draft.revision < s.remote.revision) return s; // stale read; keep the newer state
      if (pendingOps(s).length === 0) return openDraft(e.draft);
      if (e.draft.revision === s.remote.revision) return { ...s, remote: e.draft };
      // Someone else saved while we have edits: never rebase them silently.
      return { ...s, remote: e.draft, unsaved: pendingOps(s), inFlight: null, status: "conflict", problem: null };
    case "discardLocal":
      // A save in flight may still be applied by the server; wait for its answer.
      return s.inFlight !== null ? s : openDraft(s.remote);
  }
}
