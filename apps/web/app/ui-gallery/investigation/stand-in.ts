// A stand-in for the investigation API, in the page, for the investigation
// page's review page (/ui-gallery/investigation) and its tests (H-061 A8).
// The real API client talks to it through its fetch, so what the client and
// the page check (IDs, keys, status codes and error codes) is exercised as
// against the Go API. It answers getInvestigation with the investigation it
// holds (and, while `next` has more, moves on to the next one after each
// read: the worker and the executor at work), stopInvestigation as
// openapi.yaml says (the requester or an owner; another editor 403; an
// investigation that ended 409 LIFECYCLE_CONFLICT; a key used again with the
// same body gets the first answer, D-011), and the model, its base version
// and the two runs the page reads. What it does NOT do is the server's
// work: no agent, no tool call, no comparison; every figure is written in
// samples.ts.
import type { Investigation, Model, ModelVersion, Run } from "../../../lib/contracts";
import { sampleLine } from "../editor/sample-line";
import { REQUEST, versionId } from "../proposal/samples";
import { sampleRuns } from "./samples";

export type InvestigationStandInOptions = {
  /** What getInvestigation answers first. */
  investigation: Investigation;
  /** What it answers after each later read, in order (the last one stays). */
  next?: Investigation[];
  /** Who calls: their user ID and role in the organization. */
  caller: { userId: string; role: "owner" | "editor" | "viewer" };
  /** The investigation does not exist, or is another organization's (404 either way). */
  missing?: boolean;
  /** The next stop is refused with this status and code (e.g. 503 SERVICE_UNAVAILABLE). */
  refuseStop?: { status: number; code: string; message: string };
  /** Runs the page may read, besides the sample's own pair. */
  runs?: Run[];
};

export type Call = { method: string; path: string; key: string | undefined; body: unknown };

const NOW = "2026-11-01T06:00:40Z";

export function createInvestigationStandIn(opts: InvestigationStandInOptions) {
  const calls: Call[] = [];
  let current = opts.investigation;
  const queue = [...(opts.next ?? [])];
  let refusal = opts.refuseStop;
  const answers = new Map<string, { fingerprint: string; status: number; body: unknown }>();

  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQUEST } });
  const error = (status: number, code: string, message: string) => json(status, { error: { code, message, request_id: REQUEST } });

  const model = (): Model => ({
    id: current.model_id, organization_id: current.organization_id, project_id: "prj_01M4YDQK00E1ECTR0N1C500000", domain: sampleLine.domain, name: sampleLine.name,
    draft: { revision: 3, base_version_id: current.base_version_id, out_of_date: false },
    latest_version: { id: current.base_version_id, version: 1 }, created_at: "2026-10-26T00:00:00Z",
  });
  const version = (): ModelVersion => ({
    id: current.base_version_id, organization_id: current.organization_id, model_id: current.model_id, version: 1, schema_version: "0.1",
    parent_version_id: null, ir: sampleLine, hash: `sha256:${"3".repeat(64)}`, dataset_bindings: [], comment: null, created_by: current.created_by, created_at: "2026-10-26T09:11:00Z",
  });

  function respond(method: string, path: string, key: string | undefined, body: unknown): Response {
    const inv = path.match(/^\/api\/v1\/investigations\/([^/]+)(\/stop)?$/);
    if (inv) {
      if (opts.missing || inv[1] !== current.id) return error(404, "NOT_FOUND", "not found");
      if (method === "GET" && inv[2] === undefined) {
        const shown = current;
        const moved = queue.shift();
        if (moved !== undefined) current = moved;
        return json(200, shown);
      }
      if (method !== "POST" || inv[2] === undefined) return error(404, "NOT_FOUND", "not found");
      if (opts.caller.role === "viewer") return error(403, "FORBIDDEN", "the caller's role does not allow this");
      if (opts.caller.role !== "owner" && opts.caller.userId !== current.created_by) return error(403, "FORBIDDEN", "only the requester or an owner may stop it");
      if (key === undefined) return error(400, "IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required");
      const fingerprint = `stop:${current.id}:${JSON.stringify(body)}`;
      const seen = answers.get(key);
      if (seen !== undefined) return seen.fingerprint === fingerprint ? json(seen.status, seen.body) : error(409, "IDEMPOTENCY_KEY_CONFLICT", "this Idempotency-Key was used for a different request");
      if (refusal !== undefined) {
        const r = refusal;
        refusal = undefined;
        return error(r.status, r.code, r.message);
      }
      if (current.status !== "running") {
        answers.set(key, { fingerprint, status: 409, body: { error: { code: "LIFECYCLE_CONFLICT", message: "the investigation has already ended", request_id: REQUEST } } });
        return error(409, "LIFECYCLE_CONFLICT", "the investigation has already ended");
      }
      current = { ...current, status: "stopped", reason: "stopped_by_person", activity: { state: "ended" }, stop: { by: opts.caller.userId, at: NOW }, finished_at: NOW, closing_line: current.closing_line ?? { key: "not_compared", candidate_index: null, metric_path: null, absolute_delta: null, relative_delta: null, compared: 0, draft_revision: null } };
      queue.length = 0;
      answers.set(key, { fingerprint, status: 200, body: current });
      return json(200, current);
    }
    if (method === "GET" && path === `/api/v1/models/${current.model_id}`) return json(200, model());
    if (method === "GET" && path === `/api/v1/model-versions/${current.base_version_id}`) return json(200, version());
    if (method === "GET" && path === `/api/v1/model-versions/${versionId(1)}`) return json(200, version());
    const sim = path.match(/^\/api\/v1\/simulations\/([^/]+)$/);
    if (method === "GET" && sim) {
      const r = [...(opts.runs ?? []), ...sampleRuns(current)].find((x) => x.id === sim[1]);
      return r ? json(200, r) : error(404, "NOT_FOUND", "not found");
    }
    return error(404, "NOT_FOUND", "not found");
  }

  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://stand-in.invalid");
    const method = init?.method ?? "GET";
    const headers = new Headers(init?.headers);
    const body = typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
    const key = headers.get("Idempotency-Key") ?? undefined;
    calls.push({ method, path: url.pathname, key, body });
    return respond(method, url.pathname, key, body);
  }) as typeof globalThis.fetch;

  return {
    fetch,
    calls,
    organizationId: opts.investigation.organization_id,
    investigationId: opts.investigation.id,
    /** What the stand-in holds now. */
    current: () => current,
  };
}

export type InvestigationStandIn = ReturnType<typeof createInvestigationStandIn>;
