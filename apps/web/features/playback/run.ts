// The run a page plays (H-072; docs/design/H-072_TRACE_ANIMATION.md §5, §6):
// whose run it is, the model it ran (its version with its scenario's patch),
// the trace there is to play, and, for the model page, the latest completed
// run of the version shown, read through the API client as the results page
// reads its runs (features/results/load.ts). Nothing here makes a run up: no
// completed run is "none", and a trace that is missing or failed to load is
// said as such, so the page shows the line standing still.
import type { ApiClient, Result } from "../../lib/api/client";
import type { ModelIR, Result as SimulationResult, Run, TraceArtifact } from "../../lib/contracts";
import { applyPatch } from "../model-editor/patch";
import { loadTrace, type TraceState } from "../results/load";
import { MAX_RUN_PAGES } from "../simulations/load";

export type RunSide = "baseline" | "scenario";

/** A baseline runs the version as it is (no scenario, patch []); any other run is a scenario's. */
export const runSide = (run: Run): RunSide => (run.scenario_id === null && run.scenario_patch.length === 0 ? "baseline" : "scenario");

/**
 * The model a run ran: its version's IR with the run's scenario patch (a
 * fourth FCT tester is a fourth cabinet on the scenario's stage). The server
 * checked the patch when the run was made; one that no longer applies here
 * leaves the version's IR, whose steps the trace's events name.
 */
export function runModel(ir: ModelIR, run: Run): ModelIR {
  if (run.scenario_patch.length === 0) return ir;
  const patched = applyPatch(ir, run.scenario_patch);
  return patched.ok ? patched.doc : ir;
}

/** The trace to play, if there is one with events (a trace with no events is no trace). */
export const playableTrace = (s: TraceState): TraceArtifact | null => (s.state === "loaded" && s.trace.events.length > 0 ? s.trace : null);

/** Why a run does not play: it recorded no trace, or its trace could not be loaded. */
export const stillReason = (s: TraceState): "playback.noTrace" | "playback.traceFailed" => (s.state === "failed" ? "playback.traceFailed" : "playback.noTrace");

export type LatestRun = { run: Run; side: RunSide; model: ModelIR; result: SimulationResult; trace: TraceState };

const finished = (r: Run) => r.finished_at ?? r.created_at;

/**
 * The latest completed run of a model version (H-072 §5: the model page
 * plays it; a run still going or failed is not played), with its Result
 * (the horizon), the model it ran and its trace; null when the version has
 * no completed run. The model's runs are read oldest first, every page up
 * to MAX_RUN_PAGES, as the scenario page reads them.
 */
export async function loadLatestRun(api: ApiClient, modelId: string, versionId: string, signal?: AbortSignal): Promise<Result<LatestRun | null>> {
  let latest: Run | null = null;
  let cursor: string | undefined;
  let requestId: string | null = null;
  for (let n = 0; n < MAX_RUN_PAGES; n++) {
    const page = await api.listSimulations(modelId, { cursor, signal });
    if (!page.ok) return page;
    requestId ??= page.requestId;
    for (const r of page.data.items) {
      if (r.status !== "completed" || r.model_version_id !== versionId) continue;
      if (latest === null || finished(r) >= finished(latest)) latest = r;
    }
    cursor = page.data.next_cursor ?? undefined;
    if (cursor === undefined) break;
  }
  if (latest === null) return { ok: true, data: null, requestId, etag: null };
  const run = latest;
  const [result, version] = await Promise.all([api.getSimulationResults(run.id, { signal }), api.getModelVersion(run.model_version_id, { signal })]);
  if (!result.ok) return result;
  if (!version.ok) return version;
  const trace = await loadTrace(api, result.data, run.id, signal);
  return { ok: true, data: { run, side: runSide(run), model: runModel(version.data.ir, run), result: result.data, trace }, requestId, etag: null };
}
