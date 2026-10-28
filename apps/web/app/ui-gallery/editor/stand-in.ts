// A stand-in for the model API, in the page, for the editor's review
// preview (/ui-gallery/editor) and its tests. The real API client talks to
// it through its fetch, so everything the client checks (If-Match and ETag,
// the error codes and their statuses) is exercised as against the Go API.
// What it does NOT do is the server's work: it applies the patch (with the
// editor's own JSON Patch code), counts revisions and versions, answers a
// stale If-Match with 409; it does not validate the model. The issues it
// returns are given to it (the sample's were computed by the server's
// validator for that IR, see sample-states.ts) and follow their step as
// indices shift; they never appear or go away by themselves.
import { draftETag, parseDraftETag } from "../../../lib/api/etag";
import type { Draft, JsonPatch, Model, ModelIR, ModelVersion, ValidationIssue } from "../../../lib/contracts";
import { applyPatch, parsePointer, pointer } from "../../../features/model-editor/patch";
import { SAMPLE_PROPOSAL_ID } from "../proposal/samples";

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const PROJECT = "prj_01M4YDQK00E1ECTR0N1C500000";
const USER = "usr_01M4YDQK00E1ECTR0N1C500000";
const REQUEST = "req_01M4YDQK00V6H8S0ST4F7510KR";
const versionId = (n: number) => `mv_01M4YDQK00E1ECTR0N1C5000${String(n).padStart(2, "0")}`;

/** An issue anchored on a node (by ID, with the rest of the path), or on a path of its own. */
export type StandInIssue = Omit<ValidationIssue, "path"> & ({ node: string; suffix: string } | { path: string });

export type StandInOptions = {
  modelId: string;
  ir: ModelIR;
  issues?: StandInIssue[];
  /** The draft differs from its version (something to publish). */
  changed?: boolean;
  /** Someone else saves this patch just before the first save arrives, so that save gets a 409. */
  otherSave?: JsonPatch;
  /** Holds every PATCH until release() is called (to see "Saving…"). */
  holdSaves?: boolean;
  /** The next save is refused as the server refuses a candidate that is not schema-valid (422, details into the candidate IR). */
  rejectNextSave?: { path: string; reason: string; code: string }[];
};

export type Call = { method: string; path: string; ifMatch: string | null; body: unknown };

// A stable stand-in for sha256(JCS(ir)): the same IR gives the same value.
function fakeHash(ir: unknown): string {
  const text = canonical(ir);
  let h1 = 0x811c9dc5;
  let h2 = 0x01000193;
  for (let i = 0; i < text.length; i++) {
    h1 = Math.imul(h1 ^ text.charCodeAt(i), 0x01000193) >>> 0;
    h2 = Math.imul(h2 ^ text.charCodeAt(i), 0x811c9dc5) >>> 0;
  }
  return "sha256:" + (h1.toString(16).padStart(8, "0") + h2.toString(16).padStart(8, "0")).repeat(4);
}
function canonical(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`;
  if (v !== null && typeof v === "object") {
    const o = v as Record<string, unknown>;
    return `{${Object.keys(o).sort().map((k) => `${JSON.stringify(k)}:${canonical(o[k])}`).join(",")}}`;
  }
  return JSON.stringify(v);
}

const patchCodes = { invalid_path: "PATCH_PATH_INVALID", target_missing: "PATCH_TARGET_MISSING", test_failed: "PATCH_TEST_FAILED" } as const;

export function createStandIn(opts: StandInOptions) {
  const calls: Call[] = [];
  let ir = opts.ir;
  let revision = 3;
  let version = 1;
  let baseHash = opts.changed ? fakeHash({ ...opts.ir, name: `${opts.ir.name} (v1)` }) : fakeHash(opts.ir);
  let anchored = opts.issues ?? [];
  let otherSave = opts.otherSave;
  let reject = opts.rejectNextSave;
  let held: (() => void)[] = [];

  const issues = (): ValidationIssue[] =>
    anchored.flatMap((i) => {
      if ("path" in i) return [{ code: i.code, path: i.path, message: i.message, severity: i.severity }];
      const at = ir.nodes.findIndex((n) => n.id === i.node);
      return at < 0 ? [] : [{ code: i.code, path: pointer("nodes", at) + i.suffix, message: i.message, severity: i.severity }];
    });
  const draft = (): Draft => ({ model_id: opts.modelId, base_version_id: versionId(version), revision, ir, hash: fakeHash(ir), out_of_date: false, issues: issues() });
  const model = (): Model => ({
    id: opts.modelId, organization_id: ORG, project_id: PROJECT, domain: ir.domain, name: ir.name,
    draft: { revision, base_version_id: versionId(version), out_of_date: false },
    latest_version: { id: versionId(version), version }, created_at: "2026-10-26T00:00:00Z",
  });
  const modelVersion = (): ModelVersion => ({
    id: versionId(version), organization_id: ORG, model_id: opts.modelId, version, schema_version: "0.1",
    parent_version_id: version > 1 ? versionId(version - 1) : null, ir, hash: baseHash, dataset_bindings: [], comment: null,
    created_by: USER, created_at: "2026-10-26T00:00:00Z",
  });

  const json = (status: number, body: unknown, etag?: string) =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQUEST, ...(etag ? { ETag: etag } : {}) } });
  const error = (status: number, code: string, message: string, details?: unknown[]) => json(status, { error: { code, message, request_id: REQUEST, ...(details ? { details } : {}) } });
  const stale = (ifMatch: string | null) => parseDraftETag(ifMatch ?? "") !== revision;

  async function respond(method: string, path: string, ifMatch: string | null, body: unknown): Promise<Response> {
    const base = `/api/v1/models/${opts.modelId}`;
    if (method === "GET" && path === base) return json(200, model());
    if (method === "GET" && path === `${base}/draft`) return json(200, draft(), draftETag(revision));
    if (method === "GET" && path === `/api/v1/model-versions/${versionId(version)}`) return json(200, modelVersion());
    if (method === "GET" && path === `${base}/versions`) return json(200, { items: [modelVersion()], next_cursor: null });
    if (method === "GET" && path === `/api/v1/model-versions/${versionId(version)}/scenarios`) return json(200, { items: [], next_cursor: null });
    if (method === "PATCH" && path === `${base}/draft`) {
      if (otherSave !== undefined) {
        const theirs = applyPatch(ir, otherSave);
        if (theirs.ok) {
          ir = theirs.doc;
          revision++;
        }
        otherSave = undefined;
      }
      if (opts.holdSaves) await new Promise<void>((resolve) => held.push(resolve));
      if (stale(ifMatch)) return error(409, "MODEL_VERSION_CONFLICT", "the draft or the model changed since it was read; read it again (no change was made)");
      if (reject !== undefined) {
        const details = reject.map((d) => ({ ...d, severity: "error" }));
        reject = undefined;
        return error(422, "MODEL_VALIDATION_FAILED", "the model is not valid", details);
      }
      const r = applyPatch(ir, body as JsonPatch);
      if (!r.ok) return error(422, "MODEL_VALIDATION_FAILED", "the model is not valid", [{ path: `/${r.index}/path`, reason: r.reason, code: patchCodes[r.reason], severity: "error" }]);
      ir = r.doc;
      revision++;
      return json(200, draft(), draftETag(revision));
    }
    if (method === "POST" && path === `${base}/versions`) {
      const expected = (body as { expected_base_version_id?: string }).expected_base_version_id;
      if (stale(ifMatch) || expected !== versionId(version)) return error(409, "MODEL_VERSION_CONFLICT", "the draft or the model changed since it was read; read it again (no change was made)");
      const errors = issues().filter((i) => i.severity === "error");
      if (errors.length > 0) return error(422, "MODEL_VALIDATION_FAILED", "the model is not valid", errors.map((i) => ({ path: i.path, reason: i.message, code: i.code, severity: i.severity })));
      if (fakeHash(ir) === baseHash) return error(409, "LIFECYCLE_CONFLICT", "the draft has no changes to publish");
      version++;
      baseHash = fakeHash(ir);
      revision++;
      anchored = anchored.filter((i) => i.severity !== "warning" || !("node" in i) || ir.nodes.some((n) => n.id === i.node));
      return json(201, modelVersion());
    }
    // Asking the AI (T-031): stored as generating, as the API answers; the
    // proposal review page's sample being written is that proposal. No AI.
    if (method === "POST" && path === `${base}/ai-proposals`) {
      if ((body as { base_version_id?: string }).base_version_id !== versionId(version)) return error(409, "MODEL_VERSION_CONFLICT", "base_version_id is not the model's latest version");
      return json(202, { proposal_id: SAMPLE_PROPOSAL_ID, status: "generating" });
    }
    return error(404, "NOT_FOUND", "not found");
  }

  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://stand-in.invalid");
    const method = init?.method ?? "GET";
    const headers = new Headers(init?.headers);
    const body = typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
    calls.push({ method, path: url.pathname, ifMatch: headers.get("If-Match"), body });
    return respond(method, url.pathname, headers.get("If-Match"), body);
  }) as typeof globalThis.fetch;

  return {
    fetch,
    calls,
    organizationId: ORG,
    /** Lets every held save through. */
    release() {
      const waiting = held;
      held = [];
      for (const r of waiting) r();
    },
    get revision() {
      return revision;
    },
    get ir() {
      return ir;
    },
    /** The node a pointer into the current IR names (for tests). */
    nodeAt(path: string) {
      const t = parsePointer(path);
      return t && t[0] === "nodes" ? ir.nodes[Number(t[1])]?.id : undefined;
    },
  };
}

export type StandIn = ReturnType<typeof createStandIn>;
