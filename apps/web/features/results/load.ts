// What the results view needs once both runs are completed (T-032), read
// through the API client only: the server's comparison (T-029), each run's
// Result (for the measurement window and each run's intervals), the model
// version (its steps' names and time unit) and each run's sampled trace
// (T-022/T-023). A trace that fails to load fails only the chart; any
// other failure fails the view, so nothing is half-shown.
import type { ApiClient, Result } from "../../lib/api/client";
import type { Problem } from "../../lib/api/problem";
import type { Comparison, ModelVersion, Result as SimulationResult, Run, TraceArtifact } from "../../lib/contracts";

export type TraceState = { state: "none" } | { state: "loaded"; trace: TraceArtifact } | { state: "failed"; problem: Problem };
export type RunData = { run: Run; result: SimulationResult; trace: TraceState };
export type ResultsData = { comparison: Comparison; version: ModelVersion; baseline: RunData; scenario: RunData };

async function trace(api: ApiClient, result: SimulationResult, runId: string, signal?: AbortSignal): Promise<TraceState> {
  if (!result.trace.available) return { state: "none" };
  const r = await api.downloadSimulationTrace(runId, { signal });
  return r.ok ? { state: "loaded", trace: r.data } : { state: "failed", problem: r.problem };
}

export async function loadResults(api: ApiClient, baseline: Run, scenario: Run, signal?: AbortSignal): Promise<Result<ResultsData>> {
  const [comparison, rb, rs, version] = await Promise.all([
    api.compareSimulations(baseline.id, scenario.id, { signal }),
    api.getSimulationResults(baseline.id, { signal }),
    api.getSimulationResults(scenario.id, { signal }),
    api.getModelVersion(scenario.model_version_id, { signal }),
  ]);
  if (!comparison.ok) return comparison;
  if (!rb.ok) return rb;
  if (!rs.ok) return rs;
  if (!version.ok) return version;
  const [tb, ts] = await Promise.all([trace(api, rb.data, baseline.id, signal), trace(api, rs.data, scenario.id, signal)]);
  return {
    ok: true,
    data: { comparison: comparison.data, version: version.data, baseline: { run: baseline, result: rb.data, trace: tb }, scenario: { run: scenario, result: rs.data, trace: ts } },
    requestId: comparison.requestId,
    etag: null,
  };
}
