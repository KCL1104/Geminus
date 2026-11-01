// Which what-if wins (H-073, the owner's #2): the model's scenarios ranked
// by the server's comparison of each one's latest completed run with its
// baseline, on the median lead time. Nothing is computed here but the
// order: the figures, their deltas and the reasons are the comparison's as
// given (GET /simulation-comparisons), and a scenario the server did not
// compare, or compared without a percentage, has no rank. The rank is the
// relative change against the scenario's own baseline, so scenarios on
// different versions stand in one list; the largest cut comes first, ties
// share a rank. The reads go out together: the scenarios and the runs,
// then every comparison at once, at most MAX_COMPARISONS of them (the
// newest results), and the page says when a cap left some unread.
import type { ApiClient, Result } from "../../lib/api/client";
import type { Comparison, ComparisonMetric, Scenario } from "../../lib/contracts";
import { loadModelRuns, loadModelScenarios } from "./load";
import { earlierPairs, isActive, latestCompleted, type RunPair } from "./run-pair";

/** The most comparisons one view reads; the rest are listed without figures, and the page says so. */
export const MAX_COMPARISONS = 20;

/** What the ranking compares: the median lead time (as the results page's summary sentence). */
export const RANK_METRIC = "/metrics/lead_time_minutes/p50";

type Ranked = ComparisonMetric & { baseline: number; scenario: number; relative_delta: number };

/** Where a scenario stands, from its pairs (newest first) and the comparison of its latest completed one, if read. */
export type Standing =
  /** Compared, with both medians and the server's percentage. */
  | { kind: "ranked"; rank: number; pair: RunPair; metric: Ranked }
  /** Compared, but not comparable or without a percentage: the server's values and reason, no rank. */
  | { kind: "compared"; pair: RunPair; comparison: Comparison; metric: ComparisonMetric | null }
  /** Completed, but past MAX_COMPARISONS: not read on this view. */
  | { kind: "unread"; pair: RunPair }
  /** No completed pair; the newest is still queued or running. */
  | { kind: "running"; pair: RunPair }
  /** No completed pair; the newest failed or was cancelled. */
  | { kind: "stopped"; pair: RunPair }
  /** Never run beside a baseline. */
  | { kind: "none" };

export type RankedScenario = { scenario: Scenario; version: number; standing: Standing };

/** The ranked rows; more: the scenario list had more pages; runsCut: the runs were cut at MAX_RUN_PAGES; unread: how many results were not compared here. */
export type Ranking = { rows: RankedScenario[]; more: boolean; runsCut: boolean; unread: number };

const rankable = (comparison: Comparison, m: ComparisonMetric | null): m is Ranked =>
  comparison.comparable && m !== null && m.status === "available" && m.baseline !== null && m.scenario !== null && m.relative_delta !== null;

/** A scenario's standing; comparison: the server's comparison of latestCompleted(pairs), undefined when it was not read. The rank is set by rankScenarios. */
export function standingOf(pairs: readonly RunPair[], comparison: Comparison | undefined): Standing {
  const done = latestCompleted(pairs);
  if (done !== null) {
    if (comparison === undefined) return { kind: "unread", pair: done };
    const metric = comparison.metrics.find((m) => m.metric_path === RANK_METRIC) ?? null;
    return rankable(comparison, metric) ? { kind: "ranked", rank: 0, pair: done, metric } : { kind: "compared", pair: done, comparison, metric };
  }
  const newest = pairs[0];
  if (newest === undefined) return { kind: "none" };
  return isActive(newest.baseline) || isActive(newest.scenario) ? { kind: "running", pair: newest } : { kind: "stopped", pair: newest };
}

const place: Readonly<Record<Standing["kind"], number>> = { ranked: 0, compared: 1, unread: 2, running: 3, stopped: 4, none: 5 };
const change = (r: RankedScenario) => (r.standing.kind === "ranked" ? r.standing.metric.relative_delta : 0);

/** Best first: the ranked by the server's relative change (the largest cut first, ties one rank), then the rest by kind, each in the list's order. */
export function rankScenarios(rows: readonly RankedScenario[]): RankedScenario[] {
  const sorted = [...rows].sort((a, b) => place[a.standing.kind] - place[b.standing.kind] || change(a) - change(b));
  const ranked = sorted.filter((r) => r.standing.kind === "ranked").map(change);
  return sorted.map((r) => (r.standing.kind === "ranked" ? { ...r, standing: { ...r.standing, rank: 1 + ranked.filter((c) => c < change(r)).length } } : r));
}

/** The model's scenarios, ranked; the first failed read fails the whole (nothing half-ranked). */
export async function loadRanking(api: ApiClient, modelId: string, signal?: AbortSignal): Promise<Result<Ranking>> {
  // The runs are read beside the scenarios; with no scenario they are not needed, so not waited for.
  const reading = loadModelRuns(api, modelId, signal);
  const listed = await loadModelScenarios(api, modelId, signal);
  if (!listed.ok) return listed;
  if (listed.data.items.length === 0) return { ok: true, data: { rows: [], more: listed.data.more, runsCut: false, unread: 0 }, requestId: listed.requestId, etag: null };
  const read = await reading;
  if (!read.ok) return read;
  const found = listed.data.items.map((item) => ({ ...item, pairs: earlierPairs(read.data.runs, item.scenario) }));
  const done = found.flatMap((f) => {
    const pair = latestCompleted(f.pairs);
    return pair === null ? [] : [{ scenarioId: f.scenario.id, pair }];
  });
  // The newest results first, so a cap leaves out the oldest.
  const toRead = done.sort((a, b) => Date.parse(b.pair.scenario.created_at) - Date.parse(a.pair.scenario.created_at)).slice(0, MAX_COMPARISONS);
  const answers = await Promise.all(toRead.map(({ pair }) => api.compareSimulations(pair.baseline.id, pair.scenario.id, { signal })));
  const comparisons = new Map<string, Comparison>();
  for (const [i, answer] of answers.entries()) {
    if (!answer.ok) return answer;
    comparisons.set(toRead[i]!.scenarioId, answer.data);
  }
  const rows = rankScenarios(found.map(({ scenario, version, pairs }) => ({ scenario, version, standing: standingOf(pairs, comparisons.get(scenario.id)) })));
  return { ok: true, data: { rows, more: listed.data.more, runsCut: read.data.cut, unread: done.length - toRead.length }, requestId: listed.requestId, etag: null };
}
