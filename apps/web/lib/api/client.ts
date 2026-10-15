// Typed client for the Go REST API (openapi/openapi.yaml), T-014. It fails
// closed: without a configured API URL or a session it sends nothing and
// returns a Problem, so the UI can never show data the server did not return.
import type { Draft, JsonPatch, Model, Models, OrganizationID, Projects } from "../contracts";
import { draftETag, parseDraftETag } from "./etag";
import { isID } from "./ids";
import {
  invalidRequestProblem,
  networkProblem,
  notConfiguredProblem,
  type Problem,
  problemFromResponse,
  protocolProblem,
  requestIdFrom,
  unauthenticatedProblem,
} from "./problem";

/** What the auth boundary hands the client for one request (T-008 supplies a real one). */
export type Session = {
  accessToken: string;
  /** Tenant selector only; the server authorizes it against the token's membership (D-008). */
  organizationId: OrganizationID;
};

export type ClientConfig = {
  /**
   * Absolute https URL of the API (plain http only on loopback, for local
   * development; SECURITY §3), or null when this deployment has none.
   */
  baseUrl: string | null;
  session: () => Session | null;
  fetch?: typeof fetch;
};

export type Ok<T> = { ok: true; data: T; requestId: string | null; etag: string | null };
export type Failed = { ok: false; problem: Problem };
export type Result<T> = Ok<T> | Failed;

type Request = {
  method: "GET" | "PATCH";
  path: string;
  query?: Record<string, string | undefined>;
  body?: string;
  contentType?: string;
  ifMatch?: string;
  signal?: AbortSignal;
};

export type ApiClient = ReturnType<typeof createApiClient>;

const loopback = new Set(["localhost", "127.0.0.1", "[::1]"]);

/**
 * The API base, or null when it is not one this client will send a token to:
 * https (http only on loopback), no credentials, query or fragment, and a
 * path without empty segments ("//" would let a path become another host).
 */
function apiBase(baseUrl: string): { origin: string; path: string } | null {
  let url: URL;
  try {
    url = new URL(baseUrl);
  } catch {
    return null;
  }
  const secure = url.protocol === "https:" || (url.protocol === "http:" && loopback.has(url.hostname));
  if (!secure || url.username !== "" || url.password !== "" || url.search !== "" || url.hash !== "" || baseUrl.includes("?") || baseUrl.includes("#")) return null;
  const path = url.pathname.replace(/\/$/, "");
  if (path.includes("//")) return null;
  return { origin: url.origin, path };
}

const notFound = (what: string): Failed => ({
  ok: false,
  problem: { kind: "not_found", code: null, status: null, message: `${what} does not exist.`, requestId: null, details: [], retryAfterSeconds: null },
});

export function createApiClient(cfg: ClientConfig) {
  const doFetch = cfg.fetch ?? globalThis.fetch.bind(globalThis);

  async function call<T>(req: Request): Promise<Result<T>> {
    const base = cfg.baseUrl === null ? null : apiBase(cfg.baseUrl);
    if (base === null) return { ok: false, problem: notConfiguredProblem("The API") };
    const session = cfg.session();
    if (session === null) return { ok: false, problem: unauthenticatedProblem() };

    // Joined as a string on the fixed origin: req.path is a fixed template
    // with validated IDs, so the request can only go to the configured host.
    const url = new URL(base.origin + base.path + req.path);
    for (const [k, v] of Object.entries(req.query ?? {})) if (v !== undefined) url.searchParams.set(k, v);
    const headers: Record<string, string> = {
      Accept: "application/json",
      Authorization: `Bearer ${session.accessToken}`,
      "X-Organization-ID": session.organizationId,
    };
    if (req.contentType) headers["Content-Type"] = req.contentType;
    if (req.ifMatch) headers["If-Match"] = req.ifMatch;

    let res: Response;
    try {
      res = await doFetch(url, { method: req.method, headers, body: req.body, signal: req.signal, credentials: "omit", cache: "no-store" });
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
  };
}
