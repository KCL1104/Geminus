// The model's earlier requests to the AI, above Ask AI's composer (the
// owner's decision of 2026-11-02, H-079): its latest few, read from the
// newest page of listProposals (order=newest, a small limit) and, where the
// page starts AI investigations, of listInvestigations (newest first), so
// the panel reads one page of each, never the whole list. What a row shows
// is the server's: the request's own words (the proposal's or the
// investigation's intent, as stored; a photo proposal has none), what it was
// asked to make, whether a photo was sent, the state (its status, in words)
// and the time. A proposal an AI investigation asked for is the AI's, not a
// person's request; an investigation started under an explanation ("Let
// the AI test these") is not this panel's. Every member of the organization
// reads these words (the owner accepted it, H-079). Nothing here is sent to
// the AI: each request starts from the model's latest version on its own,
// and Reuse only puts a request's words back in the composer (reused).
import type { ApiClient, Result } from "../../lib/api/client";
import type { Investigation, Model, Proposal, ProposalModel } from "../../lib/contracts";
import type { MessageKey } from "../../lib/i18n/translate";

/** How many requests the panel shows. */
export const SHOWN = 3;
/** How many of each list the panel reads: its one newest page (room for the AI's own proposals, which are not shown). */
export const READ = 10;

export type Entry = { kind: "proposal"; proposal: Proposal } | { kind: "investigation"; investigation: Investigation };

export type RequestLog = {
  /** The newest requests, at most SHOWN, oldest first: the newest is last, nearest the composer. */
  entries: Entry[];
  /** Older requests were left out of entries (more on the page, or a page after it). */
  older: boolean;
};

const at = (e: Entry) => (e.kind === "proposal" ? e.proposal.created_at : e.investigation.created_at);
const id = (e: Entry) => (e.kind === "proposal" ? e.proposal.proposal_id : e.investigation.id);
/** The server's order: created_at, then the ID. */
const byTime = (a: Entry, b: Entry) => (at(a) === at(b) ? (id(a) < id(b) ? -1 : 1) : at(a) < at(b) ? -1 : 1);

/** A deployment or organization without investigations answers 503 with this detail (openapi listInvestigations): there are none to list. */
const investigationsOff = (r: Result<unknown>) => !r.ok && r.problem.details[0]?.code === "INVESTIGATIONS_OFF";

/**
 * Reads the model's earlier requests: the newest page of its proposals
 * and, where the page starts them (H-061 A8), of its investigations (its
 * own, source "request"). The first failed read is the answer;
 * investigations being off is not a failure.
 */
export async function loadRequestLog(api: ApiClient, modelId: string, opts: { investigations: boolean; signal?: AbortSignal }): Promise<Result<RequestLog>> {
  const r = await api.listProposals(modelId, { order: "newest", limit: READ, signal: opts.signal });
  if (!r.ok) return r;
  const entries: Entry[] = r.data.items.filter((p) => p.investigation === undefined).map((proposal) => ({ kind: "proposal", proposal }));
  let more = r.data.next_cursor !== null;
  if (opts.investigations) {
    const i = await api.listInvestigations(modelId, { limit: READ, signal: opts.signal });
    if (!i.ok && !investigationsOff(i)) return i;
    if (i.ok) {
      entries.push(...i.data.items.filter((x) => x.source === "request").map((investigation) => ({ kind: "investigation" as const, investigation })));
      more ||= i.data.next_cursor !== null;
    }
  }
  entries.sort(byTime);
  const shown = entries.slice(-SHOWN);
  return { ok: true, data: { entries: shown, older: more || entries.length > shown.length }, requestId: r.requestId, etag: null };
}

/** A request's state in words: the message and, for a change that became the latest version, its number. */
export type State = { key: MessageKey; version?: string };

/**
 * The state of a request as the server gives it (its status), never
 * guessed. A ready proposal whose base is no longer the model's latest can
 * never be approved, so it says it is out of date, as its own page does
 * (H-079). An approved change says which version it became when the
 * server's new_version_id is the model's latest version, and otherwise only
 * that it became a new one (the list does not say which).
 */
export function requestState(e: Entry, latest: Model["latest_version"]): State {
  if (e.kind === "investigation") return { key: `ask.log.state.investigation.${e.investigation.status}` };
  const p = e.proposal;
  if (p.status === "ready" && p.base_version_id !== latest.id) return { key: "ask.log.state.stale" };
  if (p.status !== "approved") return { key: `ask.log.state.${p.status}` };
  if (p.mode === "scenario") return { key: "ask.log.state.approved.scenario" };
  return p.new_version_id !== undefined && p.new_version_id === latest.id ? { key: "ask.log.state.approved.version", version: String(latest.version) } : { key: "ask.log.state.approved.edit" };
}

/** What Reuse puts back in the composer (H-079): the request's words, its mode, and the model it named (null: none, so the deployment's default answered it). */
export type Reused = { intent: string; mode: Proposal["mode"]; model: ProposalModel | null };

/** A request's own words, or null when it has none (a photo, or a server before H-079). */
export function wordsOf(e: Entry): string | null {
  const w = e.kind === "proposal" ? e.proposal.intent : e.investigation.intent;
  return typeof w === "string" && w.trim() !== "" ? w : null;
}

/** What Reuse puts back for e; null when it has no words to reuse. */
export function reused(e: Entry): Reused | null {
  const intent = wordsOf(e);
  if (intent === null) return null;
  if (e.kind === "proposal") return { intent, mode: e.proposal.mode, model: e.proposal.requested_model ?? null };
  return { intent, mode: e.investigation.mode, model: e.investigation.proposal_model };
}
