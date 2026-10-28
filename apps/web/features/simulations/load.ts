// What the scenario page needs (T-032): the scenario, the immutable
// version it is a patch on (its name, number and time unit) and its earlier
// runs beside their baselines, so their results can be opened again. The
// first failure wins and nothing is half-shown.
import type { ApiClient, Result } from "../../lib/api/client";
import type { ModelVersion, Run, Scenario } from "../../lib/contracts";
import { earlierPairs, type RunPair } from "./run-pair";

// ponytail: the list is oldest first, so the newest runs need every page; past this many the page says the list is cut. A newest-first list on the API lifts it.
export const MAX_RUN_PAGES = 10;

/** The results page of a scenario run and its baseline. */
export const resultsHref = (scenarioId: string, baselineRunId: string, scenarioRunId: string) =>
  `/scenarios/${scenarioId}/results?baseline=${baselineRunId}&scenario=${scenarioRunId}`;

export type ScenarioPage = { scenario: Scenario; version: ModelVersion; earlier: RunPair[]; runsCut: boolean };

export async function loadScenarioPage(api: ApiClient, scenarioId: string, signal?: AbortSignal): Promise<Result<ScenarioPage>> {
  const scenario = await api.getScenario(scenarioId, { signal });
  if (!scenario.ok) return scenario;
  const version = await api.getModelVersion(scenario.data.base_version_id, { signal });
  if (!version.ok) return version;
  if (version.data.id !== scenario.data.base_version_id) {
    return {
      ok: false,
      problem: { kind: "protocol", code: null, status: 200, message: "The version is not the scenario's base.", requestId: version.requestId, details: [], retryAfterSeconds: null },
    };
  }
  const runs: Run[] = [];
  let cursor: string | undefined;
  for (let n = 0; n < MAX_RUN_PAGES; n++) {
    const page = await api.listSimulations(scenario.data.model_id, { cursor, signal });
    if (!page.ok) return page;
    runs.push(...page.data.items);
    cursor = page.data.next_cursor ?? undefined;
    if (cursor === undefined) break;
  }
  return {
    ok: true,
    data: { scenario: scenario.data, version: version.data, earlier: earlierPairs(runs, scenario.data), runsCut: cursor !== undefined },
    requestId: scenario.requestId,
    etag: null,
  };
}
