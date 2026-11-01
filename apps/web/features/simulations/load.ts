// What the scenario page needs (T-032): the scenario and the immutable
// version it is a patch on (its name, number and time unit), the first
// failure winning and nothing half-shown; and, loaded on their own so the
// run form never waits for them (T-057 review r1 M4), the scenario's
// earlier runs beside their baselines, so their results can be opened again.
// Also the model's scenarios and runs, which the Scenarios page ranks
// (ranking.ts, H-073).
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

/** The model's runs, oldest first, read page by page up to MAX_RUN_PAGES; cut when more remain. */
export type ModelRuns = { runs: Run[]; cut: boolean };

export async function loadModelRuns(api: ApiClient, modelId: string, signal?: AbortSignal): Promise<Result<ModelRuns>> {
  const runs: Run[] = [];
  let cursor: string | undefined;
  let requestId: string | null = null;
  for (let n = 0; n < MAX_RUN_PAGES; n++) {
    const page = await api.listSimulations(modelId, { cursor, signal });
    if (!page.ok) return page;
    requestId ??= page.requestId;
    runs.push(...page.data.items);
    cursor = page.data.next_cursor ?? undefined;
    if (cursor === undefined) break;
  }
  return { ok: true, data: { runs, cut: cursor !== undefined }, requestId, etag: null };
}

/** The scenario's earlier runs, newest first, over the model's runs (every page up to MAX_RUN_PAGES). */
export async function loadEarlierRuns(api: ApiClient, scenario: Scenario, signal?: AbortSignal): Promise<Result<EarlierRuns>> {
  const read = await loadModelRuns(api, scenario.model_id, signal);
  if (!read.ok) return read;
  return { ok: true, data: { pairs: earlierPairs(read.data.runs, scenario), cut: read.data.cut }, requestId: read.requestId, etag: null };
}

/** A model's scenarios with their versions' numbers, newest version first; more when a page was left unread. */
export type ModelScenarios = { items: { scenario: Scenario; version: number }[]; more: boolean };

// ponytail: one request for the versions' first page and one per version, sent together; a model-level scenario list on the API makes it one when models have many versions.
export async function loadModelScenarios(api: ApiClient, modelId: string, signal?: AbortSignal): Promise<Result<ModelScenarios>> {
  const versions = await api.listVersions(modelId, { signal });
  if (!versions.ok) return versions;
  const pages = await Promise.all(versions.data.items.map((v) => api.listScenarios(v.id, { signal })));
  const items: ModelScenarios["items"] = [];
  let more = versions.data.next_cursor !== null;
  for (const [i, page] of pages.entries()) {
    if (!page.ok) return page;
    more ||= page.data.next_cursor !== null;
    items.push(...page.data.items.map((scenario) => ({ scenario, version: versions.data.items[i]!.version })));
  }
  return { ok: true, data: { items, more }, requestId: versions.requestId, etag: null };
}
