// The model's earlier requests to the AI, above Ask AI's composer (the
// owner's decision of 2026-11-02): its latest few, read from what the API
// already lists (listProposals, and where the page starts AI investigations
// listInvestigations), with no contract change. What a row shows is the
// server's: the state (its status, in words), the time, what the AI was
// asked to make, whether a photo was sent, and the AI's own summary.
// Neither list carries a request's own words (the server keeps them but
// does not return them), so a proposal is named by the AI's summary, or
// without one by what it was asked to make; nothing here can put a
// request's words back in the composer. A proposal an AI investigation
// asked for is the AI's, not a person's request; an investigation started
// under an explanation ("Let the AI test these") is not this panel's.
// listProposals lists the oldest first, so the pages are read to the end
// (at most MAX_PAGES) to reach the newest; with more than that, the newest
// cannot be reached and none is shown. Nothing here is sent to the AI: each
// request starts from the model's latest version on its own.
import type { ApiClient, Result } from "../../lib/api/client";
import type { Investigation, Model, Proposal } from "../../lib/contracts";
import type { MessageKey } from "../../lib/i18n/translate";

/** How many requests the panel shows. */
export const SHOWN = 3;
/** How many pages of listProposals (100 each) the panel reads at most. */
export const MAX_PAGES = 10;

export type Entry = { kind: "proposal"; proposal: Proposal } | { kind: "investigation"; investigation: Investigation };

export type RequestLog = {
  /** The newest requests, at most SHOWN, oldest first: the newest is last, nearest the composer. */
  entries: Entry[];
  /** Older requests were left out of entries. */
  older: boolean;
  /** listProposals has more pages than MAX_PAGES: its newest were not reached, so entries is empty. */
  tooMany: boolean;
};

const at = (e: Entry) => (e.kind === "proposal" ? e.proposal.created_at : e.investigation.created_at);
const id = (e: Entry) => (e.kind === "proposal" ? e.proposal.proposal_id : e.investigation.id);
/** The server's order: created_at, then the ID. */
const byTime = (a: Entry, b: Entry) => (at(a) === at(b) ? (id(a) < id(b) ? -1 : 1) : at(a) < at(b) ? -1 : 1);

/** A deployment or organization without investigations answers 503 with this detail (openapi listInvestigations): there are none to list. */
const investigationsOff = (r: Result<unknown>) => !r.ok && r.problem.details[0]?.code === "INVESTIGATIONS_OFF";

/**
 * Reads the model's earlier requests. investigations: the page starts them
 * (H-061 A8), so its own (source "request") are listed too. The first
 * failed read is the answer; investigations being off is not a failure.
 */
export async function loadRequestLog(api: ApiClient, modelId: string, opts: { investigations: boolean; signal?: AbortSignal }): Promise<Result<RequestLog>> {
  const people: Proposal[] = [];
  let cursor: string | undefined;
  let pages = 0;
  let requestId: string | null = null;
  for (;;) {
    const r = await api.listProposals(modelId, { cursor, signal: opts.signal });
    if (!r.ok) return r;
    requestId = r.requestId;
    // Only the newest SHOWN + 1 of each page are kept: enough to say whether older ones were left out.
    people.push(...r.data.items.filter((p) => p.investigation === undefined));
    people.splice(0, Math.max(0, people.length - (SHOWN + 1)));
    pages++;
    if (r.data.next_cursor === null) break;
    if (pages >= MAX_PAGES) return { ok: true, data: { entries: [], older: false, tooMany: true }, requestId, etag: null };
    cursor = r.data.next_cursor;
  }
  const entries: Entry[] = people.map((proposal) => ({ kind: "proposal", proposal }));
  let moreInvestigations = false;
  if (opts.investigations) {
    // Newest first: the first page holds the newest.
    const r = await api.listInvestigations(modelId, { signal: opts.signal });
    if (!r.ok && !investigationsOff(r)) return r;
    if (r.ok) {
      const own = r.data.items.filter((i) => i.source === "request");
      entries.push(...own.slice(0, SHOWN + 1).map((investigation) => ({ kind: "investigation" as const, investigation })));
      moreInvestigations = own.length > SHOWN + 1 || r.data.next_cursor !== null;
    }
  }
  entries.sort(byTime);
  const shown = entries.slice(-SHOWN);
  return { ok: true, data: { entries: shown, older: entries.length > shown.length || moreInvestigations, tooMany: false }, requestId, etag: null };
}

/** A request's state in words: the message and, for a change that became the latest version, its number. */
export type State = { key: MessageKey; version?: string };

/**
 * The state of a request as the server gives it (its status), never
 * guessed. An approved change says which version it became when the
 * server's new_version_id is the model's latest version, and otherwise only
 * that it became a new one (the list does not say which).
 */
export function requestState(e: Entry, latest: Model["latest_version"]): State {
  if (e.kind === "investigation") return { key: `ask.log.state.investigation.${e.investigation.status}` };
  const p = e.proposal;
  if (p.status !== "approved") return { key: `ask.log.state.${p.status}` };
  if (p.mode === "scenario") return { key: "ask.log.state.approved.scenario" };
  return p.new_version_id !== undefined && p.new_version_id === latest.id ? { key: "ask.log.state.approved.version", version: String(latest.version) } : { key: "ask.log.state.approved.edit" };
}
