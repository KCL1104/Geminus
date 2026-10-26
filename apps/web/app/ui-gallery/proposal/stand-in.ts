// A stand-in for the model and AI proposal API, in the page, for the
// proposal review's review page (/ui-gallery/proposal) and its tests
// (T-031). The real API client talks to it through its fetch, so what the
// client and the page check (IDs, keys, status codes and error codes) is
// exercised as against the Go API. It keeps what the server keeps for these
// flows: versions, the model's draft, proposals and scenarios. It answers
// createProposal with 202 generating and the sample's answer a few reads
// later; createImageProposal (T-056) likewise, with the photo sample's
// answer, after the checks a client can meet (a multipart form whose last
// part is the file, a PNG or JPEG by its first bytes, at most 5 MiB);
// approveProposal as T-026 does (decided: PROPOSAL_ALREADY_DECIDED;
// not ready: LIFECYCLE_CONFLICT; a base that is no longer the latest makes
// the proposal conflicted and answers 409 MODEL_VERSION_CONFLICT; an edit
// makes one version and moves a clean draft or keeps a changed one out of
// date; a scenario makes one scenario and no version); rejectProposal for
// ready and needs_input only; a key used again with the same body gets the
// first answer (D-011). What it does NOT do is the server's work: it does
// not call any AI, review a patch, validate a model or compute a risk; each
// sample's validation and risk are written in samples.ts.
import { draftETag, parseDraftETag } from "../../../lib/api/etag";
import type { Approval, Draft, JsonPatch, Model, ModelIR, ModelVersion, Proposal, ProposalRequest, Scenario } from "../../../lib/contracts";
import { applyPatch } from "../../../features/model-editor/patch";
import { SAMPLE_MODEL_ID, sampleLine } from "../editor/sample-line";
import { ORG, otherChange, proposalId, readyEdit, readyFromPhoto, readyScenario, REQUEST, type SampleState, samplePhoto, sampleFor, scenarioId, USER, versionId } from "./samples";

const PROJECT = "prj_01M4YDQK00E1ECTR0N1C500000";
const CLOCK = "2026-10-26T09:30:00Z";

export type Refusal = { status: number; code: string; message: string; details?: { path: string; reason: string; code?: string; severity?: "error" | "warning" }[] };

export type ProposalStandInOptions = {
  /** The sample proposal there is when the page opens; null: none yet (the model page asks for one). */
  sample: SampleState | null;
  /** Reads of a generating proposal before its answer is there (default 2). */
  generatingPolls?: number;
  /** The model's draft has saved changes that are not published (D-003: an approval keeps it, out of date). */
  dirtyDraft?: boolean;
  /** The next createProposal, approve or reject is refused with this. */
  refuseNext?: Refusal;
  /** Holds every approve and reject until release() is called (to see "Approving…"). */
  holdDecisions?: boolean;
  /** The proposal does not exist (or is another organization's: 404 either way). */
  missing?: boolean;
};

export type Call = { method: string; path: string; key: string | undefined; body: unknown; contentType: string | null };

/** What a multipart request carried, as a call records it: its parts in order, the fields, and the file's name, type and size. */
export type FormCall = { parts: string[]; base_version_id: string | null; output_locale: string | null; file: { name: string; type: string; size: number } | null };

function formCall(form: FormData): FormCall {
  const file = form.get("file");
  const field = (name: string) => {
    const v = form.get(name);
    return typeof v === "string" ? v : null;
  };
  return {
    parts: [...form.keys()],
    base_version_id: field("base_version_id"),
    output_locale: field("output_locale"),
    file: file instanceof Blob ? { name: file instanceof File ? file.name : "", type: file.type, size: file.size } : null,
  };
}

const MAX_PHOTO_BYTES = 5_242_880;

// A stable stand-in for sha256(JCS(ir)), as the editor preview's.
function fakeHash(v: unknown): string {
  const text = canonical(v);
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

function patched(ir: ModelIR, patch: JsonPatch): ModelIR {
  const r = applyPatch(ir, patch);
  if (!r.ok) throw new Error(`the sample patch does not apply: op ${r.index}, ${r.reason}`);
  return r.doc;
}

type Stored = { proposal: Proposal; answer?: Proposal; polls: number };

export function createProposalStandIn(opts: ProposalStandInOptions) {
  const calls: Call[] = [];
  const sample = opts.sample === null ? null : sampleFor(opts.sample);
  const versions: ModelVersion[] = [];
  const scenarios: Scenario[] = [];
  const proposals = new Map<string, Stored>();
  const answers = new Map<string, { fingerprint: string; status: number; body: unknown }>();
  let refusal = opts.refuseNext;
  let held: (() => void)[] = [];
  let newerOnApprove = sample?.newer === "onApprove";

  const latest = () => versions[versions.length - 1]!;
  const addVersion = (ir: ModelIR, comment: string | null, by: string) => {
    const n = versions.length + 1;
    const v: ModelVersion = {
      id: versionId(n), organization_id: ORG, model_id: SAMPLE_MODEL_ID, version: n, schema_version: "0.1",
      parent_version_id: n > 1 ? versionId(n - 1) : null, ir, hash: fakeHash(ir), dataset_bindings: [], comment,
      created_by: by, created_at: `2026-10-26T09:${String(10 + n).padStart(2, "0")}:00Z`,
    };
    versions.push(v);
    return v;
  };
  addVersion(sampleLine, null, USER);

  // The draft: the latest version's IR, or (dirty) one saved change on it.
  let draft = { ir: opts.dirtyDraft ? patched(sampleLine, [{ op: "replace", path: "/name", value: `${sampleLine.name}, draft` }]) : sampleLine, revision: 3, base: versionId(1), changed: !!opts.dirtyDraft };
  /** A new latest version: a clean draft moves to it, a changed one stays and is out of date (D-003). */
  const moveDraft = (v: ModelVersion) => {
    if (!draft.changed) draft = { ir: v.ir, revision: draft.revision + 1, base: v.id, changed: false };
  };
  /** Someone else publishes a version now (the stale and conflict cases). */
  const publishOther = () => {
    const v = addVersion(patched(latest().ir, otherChange.patch), otherChange.comment, otherChange.by);
    moveDraft(v);
    return v;
  };

  if (sample !== null && opts.sample !== null) {
    if (sample.proposal.status === "approved") moveDraft(addVersion(patched(sampleLine, sample.proposal.changes!), sample.proposal.decision_comment ?? null, USER));
    if (sample.newer === "loaded") publishOther();
    proposals.set(sample.proposal.proposal_id, { proposal: sample.proposal, answer: sample.answer, polls: opts.generatingPolls ?? 2 });
  }

  const model = (): Model => ({
    id: SAMPLE_MODEL_ID, organization_id: ORG, project_id: PROJECT, domain: sampleLine.domain, name: latest().ir.name,
    draft: { revision: draft.revision, base_version_id: draft.base, out_of_date: draft.base !== latest().id },
    latest_version: { id: latest().id, version: latest().version }, created_at: "2026-10-26T00:00:00Z",
  });
  const draftBody = (): Draft => ({ model_id: SAMPLE_MODEL_ID, base_version_id: draft.base, revision: draft.revision, ir: draft.ir, hash: fakeHash(draft.ir), out_of_date: draft.base !== latest().id, issues: [] });

  const json = (status: number, body: unknown, etag?: string) =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQUEST, ...(etag ? { ETag: etag } : {}) } });
  const error = (status: number, code: string, message: string, details?: Refusal["details"]) => json(status, { error: { code, message, request_id: REQUEST, ...(details ? { details } : {}) } });
  const refused = (): Response | null => {
    if (refusal === undefined) return null;
    const r = refusal;
    refusal = undefined;
    return error(r.status, r.code, r.message, r.details);
  };
  /** The first answer to a key, again for the same request; another request with it is a conflict (D-011). */
  const once = (key: string | undefined, fingerprint: string, run: () => { status: number; body: unknown; keep: boolean }): Response => {
    if (key === undefined) return error(400, "IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required");
    const seen = answers.get(key);
    if (seen !== undefined) return seen.fingerprint === fingerprint ? json(seen.status, seen.body) : error(409, "IDEMPOTENCY_KEY_CONFLICT", "this Idempotency-Key was used for a different request");
    const r = run();
    if (r.keep) answers.set(key, { fingerprint, status: r.status, body: r.body });
    return json(r.status, r.body);
  };
  const decide = (s: Stored, change: Partial<Proposal>) => {
    s.proposal = { ...s.proposal, ...change, decided_by: USER, decided_at: CLOCK };
  };

  async function respond(method: string, path: string, key: string | undefined, ifMatch: string | null, body: unknown, form?: FormData): Promise<Response> {
    const m = `/api/v1/models/${SAMPLE_MODEL_ID}`;
    if (method === "GET" && path === m) return json(200, model());
    if (method === "GET" && path === `${m}/draft`) return json(200, draftBody(), draftETag(draft.revision));
    if (method === "PATCH" && path === `${m}/draft`) {
      if (parseDraftETag(ifMatch ?? "") !== draft.revision) return error(409, "MODEL_VERSION_CONFLICT", "the draft changed since it was read; read it again (no change was made)");
      const r = applyPatch(draft.ir, body as JsonPatch);
      if (!r.ok) return error(422, "MODEL_VALIDATION_FAILED", "the model is not valid");
      draft = { ...draft, ir: r.doc, revision: draft.revision + 1, changed: true };
      return json(200, draftBody(), draftETag(draft.revision));
    }
    const version = path.match(/^\/api\/v1\/model-versions\/([^/]+)$/);
    if (method === "GET" && version) {
      const v = versions.find((x) => x.id === version[1]);
      return v ? json(200, v) : error(404, "NOT_FOUND", "not found");
    }
    if (method === "POST" && path === `${m}/ai-proposals`) {
      const no = refused();
      if (no) return no;
      const req = body as ProposalRequest;
      return once(key, `create:${canonical(req)}`, () => {
        if (req.base_version_id !== latest().id) return { status: 409, body: { error: { code: "MODEL_VERSION_CONFLICT", message: "base_version_id is not the model's latest version", details: [{ path: "/base_version_id", reason: "not the model's latest version" }], request_id: REQUEST } }, keep: false };
        const id = proposalId(proposals.size + 1);
        const answer = req.mode === "scenario" ? readyScenario(id) : readyEdit(id);
        const generating: Proposal = { proposal_id: id, organization_id: ORG, model_id: SAMPLE_MODEL_ID, base_version_id: req.base_version_id, mode: req.mode, status: "generating", created_by: USER, created_at: CLOCK, requires_confirmation: true };
        proposals.set(id, { proposal: generating, answer: { ...answer, base_version_id: req.base_version_id }, polls: opts.generatingPolls ?? 2 });
        return { status: 202, body: { proposal_id: id, status: "generating" }, keep: true };
      });
    }
    if (method === "POST" && path === `${m}/ai-proposals/image`) {
      const no = refused();
      if (no) return no;
      if (form === undefined) return error(415, "UNSUPPORTED_MEDIA_TYPE", "the request body must be multipart/form-data");
      const parts = [...form.keys()];
      const file = form.get("file");
      if (!(file instanceof Blob) || parts[parts.length - 1] !== "file" || parts.filter((n) => n === "file").length !== 1) {
        return error(422, "REQUEST_VALIDATION_FAILED", "the request is not acceptable", [{ path: "/file", reason: "one file, the last part, is required" }]);
      }
      if (file.size > MAX_PHOTO_BYTES) return error(413, "PAYLOAD_TOO_LARGE", "the photo is larger than a proposal may take");
      const bytes = new Uint8Array(await file.arrayBuffer());
      const png = bytes.length >= 8 && [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a].every((b, i) => bytes[i] === b);
      const jpeg = bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff;
      if (!png && !jpeg) {
        return error(415, "UNSUPPORTED_MEDIA_TYPE", "the photo is not a PNG or JPEG image", [{ path: "/file", reason: "only PNG and JPEG images are accepted", code: "IMAGE_FORMAT_REJECTED" }]);
      }
      const base = String(form.get("base_version_id") ?? "");
      const locale = form.get("output_locale");
      const digest = fakeHash(Array.from(bytes));
      return once(key, `image:${base}:${String(locale)}:${digest}`, () => {
        if (base !== latest().id) return { status: 409, body: { error: { code: "MODEL_VERSION_CONFLICT", message: "base_version_id is not the model's latest version", details: [{ path: "/base_version_id", reason: "not the model's latest version" }], request_id: REQUEST } }, keep: false };
        const id = proposalId(proposals.size + 1);
        // The stand-in reads no pixels: the size in pixels is the sample photo's.
        const photo = { ...samplePhoto, media_type: png ? ("image/png" as const) : ("image/jpeg" as const), size_bytes: file.size, content_hash: digest };
        const generating: Proposal = { proposal_id: id, organization_id: ORG, model_id: SAMPLE_MODEL_ID, base_version_id: base, mode: "edit", status: "generating", created_by: USER, created_at: CLOCK, requires_confirmation: true, source_image: photo };
        proposals.set(id, { proposal: generating, answer: { ...readyFromPhoto(id, photo), base_version_id: base }, polls: opts.generatingPolls ?? 2 });
        return { status: 202, body: { proposal_id: id, status: "generating" }, keep: true };
      });
    }
    const p = path.match(/^\/api\/v1\/ai-proposals\/([^/]+)(\/approve|\/reject)?$/);
    if (p) {
      const s = opts.missing ? undefined : proposals.get(p[1]!);
      if (s === undefined) return error(404, "NOT_FOUND", "not found");
      if (method === "GET" && p[2] === undefined) {
        if (s.proposal.status === "generating" && s.answer !== undefined && s.polls-- <= 0) s.proposal = s.answer;
        return json(200, s.proposal);
      }
      if (method !== "POST" || p[2] === undefined) return error(404, "NOT_FOUND", "not found");
      if (opts.holdDecisions) await new Promise<void>((resolve) => held.push(resolve));
      const no = refused();
      if (no) return no;
      const id = s.proposal.proposal_id;
      if (p[2] === "/reject") {
        const comment = (body as { comment?: string }).comment ?? "";
        return once(key, `reject:${id}:${comment}`, () => {
          if (["approved", "rejected", "conflicted"].includes(s.proposal.status)) return { status: 409, body: { error: { code: "PROPOSAL_ALREADY_DECIDED", message: `the proposal is already ${s.proposal.status}; nothing was changed`, request_id: REQUEST } }, keep: true };
          if (s.proposal.status !== "ready" && s.proposal.status !== "needs_input") return { status: 409, body: { error: { code: "LIFECYCLE_CONFLICT", message: "only a ready or needs_input proposal can be rejected; nothing was changed", request_id: REQUEST } }, keep: true };
          decide(s, { status: "rejected", ...(comment === "" ? {} : { decision_comment: comment }) });
          return { status: 200, body: s.proposal, keep: true };
        });
      }
      const expected = (body as { expected_base_version_id?: string }).expected_base_version_id;
      return once(key, `approve:${id}:${canonical(body)}`, () => {
        if (["approved", "rejected", "conflicted"].includes(s.proposal.status)) return { status: 409, body: { error: { code: "PROPOSAL_ALREADY_DECIDED", message: `the proposal is already ${s.proposal.status}; nothing was changed`, request_id: REQUEST } }, keep: true };
        if (s.proposal.status !== "ready") return { status: 409, body: { error: { code: "LIFECYCLE_CONFLICT", message: "only a ready proposal can be approved; nothing was changed", request_id: REQUEST } }, keep: true };
        if (newerOnApprove) {
          newerOnApprove = false;
          publishOther();
        }
        if (s.proposal.base_version_id !== latest().id) {
          decide(s, { status: "conflicted" });
          return {
            status: 409,
            body: { error: { code: "MODEL_VERSION_CONFLICT", message: "the model's latest version is no longer this proposal's base; the proposal is now conflicted and nothing was applied (ask for a new proposal on the latest version)", details: [{ path: "/expected_base_version_id", reason: "the proposal's base is not the model's latest version" }], request_id: REQUEST } },
            keep: true,
          };
        }
        if (expected !== s.proposal.base_version_id) return { status: 409, body: { error: { code: "MODEL_VERSION_CONFLICT", message: "expected_base_version_id is not this proposal's base; nothing was changed", request_id: REQUEST } }, keep: true };
        let approval: Approval;
        if (s.proposal.mode === "edit") {
          const v = addVersion(patched(latest().ir, s.proposal.changes!), null, USER);
          const outOfDate = draft.changed;
          moveDraft(v);
          decide(s, { status: "approved", new_version_id: v.id });
          approval = { proposal_id: id, status: "approved", mode: "edit", new_version_id: v.id, version: v.version, patch_hash: s.proposal.patch_hash!, draft_out_of_date: outOfDate };
        } else {
          const scn: Scenario = {
            id: scenarioId(scenarios.length + 1), organization_id: ORG, model_id: SAMPLE_MODEL_ID, base_version_id: s.proposal.base_version_id,
            name: s.proposal.summary!, patch: s.proposal.changes!, patch_hash: s.proposal.patch_hash!, source_proposal_id: id, created_by: USER, created_at: CLOCK,
          };
          scenarios.push(scn);
          decide(s, { status: "approved", scenario_id: scn.id });
          approval = { proposal_id: id, status: "approved", mode: "scenario", scenario_id: scn.id, patch_hash: s.proposal.patch_hash! };
        }
        return { status: 201, body: approval, keep: true };
      });
    }
    return error(404, "NOT_FOUND", "not found");
  }

  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://stand-in.invalid");
    const method = init?.method ?? "GET";
    const headers = new Headers(init?.headers);
    const form = init?.body instanceof FormData ? init.body : undefined;
    const body = typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : form !== undefined ? formCall(form) : undefined;
    const key = headers.get("Idempotency-Key") ?? undefined;
    calls.push({ method, path: url.pathname, key, body, contentType: headers.get("Content-Type") });
    return respond(method, url.pathname, key, headers.get("If-Match"), body, form);
  }) as typeof globalThis.fetch;

  return {
    fetch,
    calls,
    organizationId: ORG,
    modelId: SAMPLE_MODEL_ID,
    /** The sample proposal's ID; null when the page asks for one. */
    proposalId: sample?.proposal.proposal_id ?? null,
    /** The version the page opens on (version 1). */
    baseVersionId: versionId(1),
    versions: () => [...versions],
    scenarios: () => [...scenarios],
    proposals: () => [...proposals.values()].map((s) => s.proposal),
    publishOther,
    /** Lets every held decision through. */
    release() {
      const waiting = held;
      held = [];
      for (const r of waiting) r();
    },
  };
}

export type ProposalStandIn = ReturnType<typeof createProposalStandIn>;
