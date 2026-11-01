// H-073 (5): after a photo proposal is approved, a request for the typical
// times of the stations it added. A photo shows no service time, so the
// server let those stations take only numbers the model already had
// (internal/ai/review Photo); looking up published typical times is the
// next step a person may want. Nothing here asks the AI: the proposal page
// leaves the request here, in this tab's memory only (no URL, no storage,
// as photo-start.ts does), the model page's Ask AI takes it once when it
// opens, and the person reads, edits and sends it, and approves or rejects
// what comes back.
//
// The stations come from the approved proposal's changes, read against its
// base as the server applied them: the process steps the patch leaves in
// the model that the base did not have (the photo rules count only those
// as stations; buffers, decisions and sinks are the flow between them).
// They are named by their names, which the AI wrote: what a name hides or
// reorders becomes a visible ⟨U+XXXX⟩ (shown.ts), and a name that reads
// like another one is listed once. No ID goes into the request: it is
// used for a web search.
import type { ModelIR, Proposal } from "../../lib/contracts";
import type { Locale } from "../../lib/i18n/locale";
import type { Translator } from "../../lib/i18n/translate";
import { applyPatch } from "../model-editor/patch";
import { nameKey, visible } from "./shown";

/** What the model page's Ask AI opens with: the request, web research on, a change to the model. */
export type AskPrefill = { from: "photoStations"; intent: string; mode: "edit"; research: true };

/** The names of the stations a photo proposal's changes add, in the order the model then holds them (the patch's, for steps it appends); none for any other proposal or a patch that does not apply. */
export function photoStations(proposal: Proposal, base: ModelIR): string[] {
  if (proposal.source_image === undefined || proposal.mode !== "edit" || proposal.changes === undefined) return [];
  const after = applyPatch(base, proposal.changes);
  if (!after.ok) return [];
  const before = new Set(base.nodes.map((n) => n.id));
  const seen = new Set<string>();
  const names: string[] = [];
  for (const n of after.doc.nodes) {
    if (n.type !== "process" || before.has(n.id)) continue;
    const name = visible(typeof n.name === "string" && n.name !== "" ? n.name : n.id);
    const key = nameKey(name);
    if (seen.has(key)) continue;
    seen.add(key);
    names.push(name);
  }
  return names;
}

/** The request for the stations' typical times, in the reader's language: each name quoted, listed the locale's way. */
export function stationTimesRequest(t: Translator, locale: Locale, stations: readonly string[]): string {
  const list = new Intl.ListFormat(locale, { type: "conjunction" }).format(stations.map((s) => t("proposal.value.text", { text: s })));
  return t("ask.prefill.stationTimes.intent", { count: stations.length, stations: list });
}

const notes = new Map<string, AskPrefill>();

/** The model page of modelId opens its Ask AI with prefill, once. */
export function noteAskPrefill(modelId: string, prefill: AskPrefill): void {
  notes.set(modelId, prefill);
}

/** The request left for modelId, or null. Reading does not clear it (React may run a state initializer twice). */
export function askPrefill(modelId: string): AskPrefill | null {
  return notes.get(modelId) ?? null;
}

/** Forgets the request left for modelId. */
export function clearAskPrefill(modelId: string): void {
  notes.delete(modelId);
}
