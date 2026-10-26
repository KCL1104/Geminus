// What the scenario page needs (T-032): the scenario and the immutable
// version it is a patch on (its name, number and time unit). The first
// failure wins and nothing is half-shown.
import type { ApiClient, Result } from "../../lib/api/client";
import type { ModelVersion, Scenario } from "../../lib/contracts";

export type ScenarioPage = { scenario: Scenario; version: ModelVersion };

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
