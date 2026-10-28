// What the scenario page needs (T-032): the scenario and the immutable
// version it is a patch on (its name, number and time unit), the first
// failure winning and nothing half-shown; and, loaded on their own so the
// run form never waits for them (T-057 review r1 M4), the scenario's
// earlier runs beside their baselines, so their results can be opened again.
import type { ApiClient, Result } from "../../lib/api/client";
import type { ModelVersion, Run, Scenario } from "../../lib/contracts";
import { earlierPairs, type RunPair } from "./run-pair";

// ponytail: the list is oldest first, so the newest runs need every page; past this many the page says the list is cut. A newest-first list on the API lifts it.
export const MAX_RUN_PAGES = 10;

/** The results page of a scenario run and its baseline. */
export const resultsHref = (scenarioId: string, baselineRunId: string, scenarioRunId: string) =>
  `/scenarios/${scenarioId}/results?baseline=${baselineRunId}&scenario=${scenarioRunId}`;

export type ScenarioPage = { scenario: Scenario; version: ModelVersion };

export type EarlierRuns = { pairs: RunPair[]; cut: boolean };

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
  return { ok: true, data: { scenario: scenario.data, version: version.data }, requestId: scenario.requestId, etag: null };
}

/** The scenario's earlier runs, newest first, over the model's runs (every page up to MAX_RUN_PAGES). */
export async function loadEarlierRuns(api: ApiClient, scenario: Scenario, signal?: AbortSignal): Promise<Result<EarlierRuns>> {
  const runs: Run[] = [];
  let cursor: string | undefined;
  let requestId: string | null = null;
  for (let n = 0; n < MAX_RUN_PAGES; n++) {
    const page = await api.listSimulations(scenario.model_id, { cursor, signal });
    if (!page.ok) return page;
    requestId ??= page.requestId;
    runs.push(...page.data.items);
    cursor = page.data.next_cursor ?? undefined;
    if (cursor === undefined) break;
  }
  return { ok: true, data: { pairs: earlierPairs(runs, scenario), cut: cursor !== undefined }, requestId, etag: null };
}
