// Typed client for the Go REST API (openapi/openapi.yaml), T-014. It calls
// the API through the same-origin BFF (ADR-018, T-043): the browser holds no
// token, the session cookie goes along and the BFF adds the bearer token. It
// fails closed: without a session it sends nothing and returns a Problem, so
// the UI can never show data the server did not return.
import type { CreateModel, Draft, JsonPatch, Model, Models, ModelVersion, OrganizationID, Projects, Publish, ResetDraft, Versions } from "../contracts";
import { draftETag, parseDraftETag } from "./etag";
import { isID } from "./ids";
import {
  invalidRequestProblem,
  networkProblem,
  type Problem,
  problemFromResponse,
  protocolProblem,
  requestIdFrom,
  unauthenticatedProblem,
} from "./problem";

/** What the auth boundary hands the client for one request: who is signed in, for which organization. */
export type Session = {
  /** Tenant selector only; the server authorizes it against the token's membership (D-008). */
  organizationId: OrganizationID;
};

/** The BFF's same-origin path to the API's /v1 (ADR-018). */
export const API_PREFIX = "/api/v1";

export type ClientConfig = {
  session: () => Session | null;
  fetch?: typeof fetch;
};

export type Ok<T> = { ok: true; data: T; requestId: string | null; etag: string | null };
export type Failed = { ok: false; problem: Problem };
export type Result<T> = Ok<T> | Failed;

type Request = {
  method: "GET" | "PATCH" | "POST";
  path: string;
  query?: Record<string, string | undefined>;
  body?: string;
  contentType?: string;
  ifMatch?: string;
  idempotencyKey?: string;
  signal?: AbortSignal;
};

// openapi IdempotencyKey: 8–128 characters; the server takes visible ASCII.
const IDEMPOTENCY_KEY = /^[\x21-\x7e]{8,128}$/;

/**
 * A new Idempotency-Key for one write (D-011). Keep it for retries of the
 * same request: the server answers a retry with the first response.
 */
export function newIdempotencyKey(): string {
  return `key-${crypto.randomUUID()}`;
}

export type ApiClient = ReturnType<typeof createApiClient>;

const badKey = (): Failed => ({ ok: false, problem: invalidRequestProblem("An Idempotency-Key is 8 to 128 visible ASCII characters.") });

/** A draft revision the server can have issued (a positive safe integer); anything else is refused locally, never thrown. */
const validRevision = (n: number): boolean => Number.isSafeInteger(n) && n >= 1;
const badRevision = (): Failed => ({ ok: false, problem: invalidRequestProblem("A draft revision is a positive integer.") });

const notFound = (what: string): Failed => ({
  ok: false,
  problem: { kind: "not_found", code: null, status: null, message: `${what} does not exist.`, requestId: null, details: [], retryAfterSeconds: null },
});

export function createApiClient(cfg: ClientConfig) {
  const doFetch = cfg.fetch ?? globalThis.fetch.bind(globalThis);

  async function call<T>(req: Request): Promise<Result<T>> {
    const session = cfg.session();
    if (session === null) return { ok: false, problem: unauthenticatedProblem() };

    // A same-origin path: req.path is a fixed template with validated IDs,
    // so the request can only reach the BFF's API proxy.
    const query = new URLSearchParams();
    for (const [k, v] of Object.entries(req.query ?? {})) if (v !== undefined) query.set(k, v);
    const url = API_PREFIX + req.path + (query.size > 0 ? `?${query}` : "");
    const headers: Record<string, string> = {
      Accept: "application/json",
      "X-Organization-ID": session.organizationId,
      // The BFF refuses a state-changing request without it (CSRF, ADR-018).
      "X-Geminus-Request": "1",
    };
    if (req.contentType) headers["Content-Type"] = req.contentType;
    if (req.ifMatch) headers["If-Match"] = req.ifMatch;
    if (req.idempotencyKey) headers["Idempotency-Key"] = req.idempotencyKey;

    let res: Response;
    try {
      res = await doFetch(url, { method: req.method, headers, body: req.body, signal: req.signal, credentials: "same-origin", cache: "no-store", redirect: "error" });
    } catch {
      return { ok: false, problem: networkProblem(req.signal?.aborted ? "Request aborted." : "The API could not be reached.") };
    }
    let text: string;
    try {
      text = await res.text();
    } catch {
      return { ok: false, problem: networkProblem(req.signal?.aborted ? "Request aborted." : "The API response was cut off.") };
    }
    let body: unknown;
    try {
      body = text === "" ? undefined : JSON.parse(text);
    } catch {
      body = undefined;
    }
    const requestId = requestIdFrom(res.headers);
    if (!res.ok) return { ok: false, problem: problemFromResponse(res.status, res.headers, body) };
    if (typeof body !== "object" || body === null) {
      return { ok: false, problem: protocolProblem(res.status, "The API returned a success without a JSON object.", requestId) };
    }
    return { ok: true, data: body as T, requestId, etag: res.headers.get("ETag") };
  }

  /** Draft responses must carry an ETag that names the body's revision. */
  function checkDraft(r: Result<Draft>): Result<Draft> {
    if (!r.ok) return r;
    const revision = r.etag === null ? null : parseDraftETag(r.etag);
    if (revision === null || revision !== r.data.revision) {
      return { ok: false, problem: protocolProblem(200, "Draft response without a matching ETag.", r.requestId) };
    }
    return r;
  }

  return {
    listProjects(opts: { cursor?: string; signal?: AbortSignal } = {}) {
      return call<Projects>({ method: "GET", path: "/projects", query: { cursor: opts.cursor }, signal: opts.signal });
    },
    async listModels(projectId: string, opts: { cursor?: string; signal?: AbortSignal } = {}) {
      if (!isID("prj", projectId)) return notFound("The project");
      return call<Models>({ method: "GET", path: `/projects/${projectId}/models`, query: { cursor: opts.cursor }, signal: opts.signal });
    },
    async getModel(modelId: string, opts: { signal?: AbortSignal } = {}) {
      if (!isID("mdl", modelId)) return notFound("The model");
      return call<Model>({ method: "GET", path: `/models/${modelId}`, signal: opts.signal });
    },
    async getDraft(modelId: string, opts: { signal?: AbortSignal } = {}) {
      if (!isID("mdl", modelId)) return notFound("The model");
      return checkDraft(await call<Draft>({ method: "GET", path: `/models/${modelId}/draft`, signal: opts.signal }));
    },
    /** Saves ops on top of `revision`; a stale revision comes back as a 409 conflict, never a silent overwrite. */
    async patchDraft(modelId: string, revision: number, patch: JsonPatch, opts: { signal?: AbortSignal } = {}): Promise<Result<Draft>> {
      if (!isID("mdl", modelId)) return notFound("The model");
      if (!validRevision(revision)) return badRevision();
      if (patch.length === 0) return { ok: false, problem: invalidRequestProblem("A draft change needs at least one operation.") };
      return checkDraft(
        await call<Draft>({
          method: "PATCH",
          path: `/models/${modelId}/draft`,
          body: JSON.stringify(patch),
          contentType: "application/json-patch+json",
          ifMatch: draftETag(revision),
          signal: opts.signal,
        }),
      );
    },
    /** Creates a model, its first version and draft from the domain's template; reuse `key` to retry. */
    async createModel(projectId: string, body: CreateModel, key: string, opts: { signal?: AbortSignal } = {}): Promise<Result<Model>> {
      if (!isID("prj", projectId)) return notFound("The project");
      if (!IDEMPOTENCY_KEY.test(key)) return badKey();
      return call<Model>({
        method: "POST",
        path: `/projects/${projectId}/models`,
        body: JSON.stringify(body),
        contentType: "application/json",
        idempotencyKey: key,
        signal: opts.signal,
      });
    },
    /** Discards the draft's changes; `revision` and the latest version must still be current (D-003). */
    async resetDraft(modelId: string, revision: number, body: ResetDraft, opts: { signal?: AbortSignal } = {}): Promise<Result<Draft>> {
      if (!isID("mdl", modelId)) return notFound("The model");
      if (!validRevision(revision)) return badRevision();
      return checkDraft(
        await call<Draft>({
          method: "POST",
          path: `/models/${modelId}/draft/reset`,
          body: JSON.stringify(body),
          contentType: "application/json",
          ifMatch: draftETag(revision),
          signal: opts.signal,
        }),
      );
    },
    /** Publishes the draft at `revision` as the next immutable version; reuse `key` to retry. */
    async publishVersion(modelId: string, revision: number, body: Publish, key: string, opts: { signal?: AbortSignal } = {}): Promise<Result<ModelVersion>> {
      if (!isID("mdl", modelId)) return notFound("The model");
      if (!validRevision(revision)) return badRevision();
      if (!IDEMPOTENCY_KEY.test(key)) return badKey();
      return call<ModelVersion>({
        method: "POST",
        path: `/models/${modelId}/versions`,
        body: JSON.stringify(body),
        contentType: "application/json",
        ifMatch: draftETag(revision),
        idempotencyKey: key,
        signal: opts.signal,
      });
    },
    async listVersions(modelId: string, opts: { cursor?: string; signal?: AbortSignal } = {}) {
      if (!isID("mdl", modelId)) return notFound("The model");
      return call<Versions>({ method: "GET", path: `/models/${modelId}/versions`, query: { cursor: opts.cursor }, signal: opts.signal });
    },
    async getModelVersion(versionId: string, opts: { signal?: AbortSignal } = {}) {
      if (!isID("mv", versionId)) return notFound("The version");
      return call<ModelVersion>({ method: "GET", path: `/model-versions/${versionId}`, signal: opts.signal });
    },
  };
}
