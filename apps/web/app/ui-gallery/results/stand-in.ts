// A stand-in for the two reads the results view makes for itself, in the
// page, for the results' review page (/ui-gallery/results) and its tests
// (H-073). The real API client talks to it through its fetch, so what the
// client checks (IDs, status codes) is exercised as against the Go API. It
// answers listSimulations for the board line's model with the page's own
// two runs, the model's only runs here: the reproducibility card then says,
// truly, that no other run with the same inputs was found (it never counts
// the page's own pair). It answers getSimulationResults for those two runs
// with their Results. The review page mounts no AI explanation, so nothing
// reads the pair's analyses. What it does NOT do is the server's work: no
// run, no comparison; anything else is 404.
import type { Result as SimulationResult, Run } from "../../../lib/contracts";

const REQUEST = "req_01M4YDQK00V6H8S0ST4F7510KR";

export type ResultsStandIn = { fetch: typeof globalThis.fetch; organizationId: string };

export function createResultsStandIn(pair: { baseline: { run: Run; result: SimulationResult }; scenario: { run: Run; result: SimulationResult } }): ResultsStandIn {
  const runs = [pair.baseline, pair.scenario];
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQUEST } });
  const notFound = () => json(404, { error: { code: "NOT_FOUND", message: "not found", request_id: REQUEST } });

  function respond(method: string, path: string): Response {
    if (method !== "GET") return notFound();
    if (path === `/api/v1/models/${pair.scenario.run.model_id}/simulations`) return json(200, { items: runs.map((r) => r.run), next_cursor: null });
    const results = path.match(/^\/api\/v1\/simulations\/([^/]+)\/results$/);
    const found = results ? runs.find((r) => r.run.id === results[1]) : undefined;
    if (found) return json(200, found.result);
    return notFound();
  }

  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://stand-in.invalid");
    return respond(init?.method ?? "GET", url.pathname);
  }) as typeof globalThis.fetch;

  return { fetch, organizationId: pair.scenario.run.organization_id };
}
