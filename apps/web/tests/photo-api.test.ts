// T-056 (H-033): the client for createImageProposal, at the contract's
// method and path (planning/api-operations.json, openapi createImageProposal)
// with the tenant selector and an Idempotency-Key the caller keeps for
// retries (D-011): a multipart form whose parts are base_version_id, the
// optional output_locale and the file, in that order, the file under a
// fixed name (never the person's), and no Content-Type of its own (the
// browser writes the boundary). It refuses what the contract would refuse
// before sending anything, and hands the server's refusals over as given.
// H-052: getProposalImage, the stored photo back as a PNG or JPEG Blob.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { createApiClient } from "../lib/api/client";
import { MAX_PHOTO_BYTES, PHOTO_TYPES } from "../features/ai-proposals/photo-form";

const read = (path: string): unknown => JSON.parse(readFileSync(new URL(path, import.meta.url), "utf8"));
const operations = (read("../../../planning/api-operations.json") as { operations: { operation_id: string; method: string; path: string; roles: string[] }[] }).operations;
const proposalSchema = read("../../../schemas/proposal.schema.json") as {
  properties: { source_image: { properties: { size_bytes: { maximum: number }; media_type: { enum: string[] } } } };
};

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const MODEL = "mdl_01M4YDQK004AGVNEGJQJ1NMM0N";
const VERSION = "mv_01M4YDQK00FRYWV7GS7X1QCXZR";

type Call = { url: URL; init: RequestInit };
function fakeFetch(...responses: Response[]) {
  const calls: Call[] = [];
  const fn = async (input: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: new URL(String(input), "https://app.test"), init: init ?? {} });
    const next = responses.shift();
    if (next === undefined) throw new Error("unexpected request");
    return next;
  };
  return { fetch: fn as typeof fetch, calls };
}
const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });
const api = (f: ReturnType<typeof fakeFetch>) => createApiClient({ session: () => ({ organizationId: ORG }), fetch: f.fetch });
const headers = (c: Call) => c.init.headers as Record<string, string>;
const png = () => new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3])], "Line 2 – GPS 25.03N.png", { type: "image/png" });

describe("createImageProposal (T-056) through the client", () => {
  it("sends the photo as the contract's multipart form, with its key and the tenant selector, and reads the 202", async () => {
    const accepted = read("../../../testdata/contracts/valid-proposal-accepted.json");
    const f = fakeFetch(json(202, accepted, { "X-Request-ID": REQ }));
    const file = png();
    const r = await api(f).createImageProposal(MODEL, { base_version_id: VERSION, output_locale: "zh-Hant" }, file, "key-photo-0001");
    expect(r.ok && [r.data, r.requestId]).toEqual([accepted, REQ]);
    const c = f.calls[0]!;
    const op = operations.find((o) => o.operation_id === "createImageProposal");
    const template = c.url.pathname.slice("/api".length).replace(/\/mdl_[0-9A-Z]{26}/, "/{id}");
    expect([c.init.method, template, op?.roles]).toEqual([op?.method, op?.path, ["owner", "editor"]]);
    expect([headers(c)["Idempotency-Key"], headers(c)["X-Organization-ID"], headers(c)["Content-Type"]]).toEqual(["key-photo-0001", ORG, undefined]);
    const form = c.init.body as FormData;
    expect(form).toBeInstanceOf(FormData);
    expect([...form.keys()]).toEqual(["base_version_id", "output_locale", "file"]);
    expect([form.get("base_version_id"), form.get("output_locale")]).toEqual([VERSION, "zh-Hant"]);
    const sent = form.get("file") as File;
    // The bytes as chosen; the name is the client's, never the person's file name.
    expect([sent.name, sent.size, sent.type]).toEqual(["photo", file.size, "image/png"]);
    expect(new Uint8Array(await sent.arrayBuffer())).toEqual(new Uint8Array(await file.arrayBuffer()));
  });

  it("leaves output_locale out when it is not given (the server's default)", async () => {
    const f = fakeFetch(json(202, { proposal_id: "aip_01M4YDQK00ADN261CME3WV5HZB", status: "generating" }));
    await api(f).createImageProposal(MODEL, { base_version_id: VERSION }, png(), "key-photo-0002");
    expect([...(f.calls[0]!.init.body as FormData).keys()]).toEqual(["base_version_id", "file"]);
  });

  it("refuses bad IDs and keys before sending anything", async () => {
    const f = fakeFetch();
    const client = api(f);
    const refused = [
      await client.createImageProposal(VERSION, { base_version_id: VERSION }, png(), "key-photo-0003"),
      await client.createImageProposal(`${MODEL}/../x`, { base_version_id: VERSION }, png(), "key-photo-0004"),
      await client.createImageProposal(MODEL, { base_version_id: MODEL }, png(), "key-photo-0005"),
      await client.createImageProposal(MODEL, { base_version_id: VERSION }, png(), "short"),
      await client.createImageProposal(MODEL, { base_version_id: VERSION }, png(), "key with spaces"),
    ];
    expect(refused.map((r) => !r.ok && r.problem.kind)).toEqual(["not_found", "not_found", "not_found", "invalid", "invalid"]);
    expect(f.calls).toHaveLength(0);
  });

  it("maps the server's photo refusals to the contract's problems, details as given", async () => {
    const err = (code: string, details?: unknown[]) => ({ error: { code, message: "x", request_id: REQ, ...(details ? { details } : {}) } });
    const f = fakeFetch(
      json(415, err("UNSUPPORTED_MEDIA_TYPE", [{ path: "/file", reason: "only PNG and JPEG images are accepted", code: "IMAGE_FORMAT_REJECTED" }])),
      json(413, err("PAYLOAD_TOO_LARGE")),
      json(422, err("REQUEST_VALIDATION_FAILED", [{ path: "/file", reason: "x", code: "IMAGE_PIXELS_EXCEEDED" }])),
      json(409, err("MODEL_VERSION_CONFLICT", [{ path: "/base_version_id", reason: "not the model's latest version" }])),
      json(503, err("AI_PROVIDER_NOT_CONFIGURED")),
    );
    const client = api(f);
    const out = [];
    for (let i = 0; i < 5; i++) out.push(await client.createImageProposal(MODEL, { base_version_id: VERSION }, png(), `key-photo-01${i}0`));
    expect(out.map((r) => !r.ok && [r.problem.code, r.problem.details[0]?.code ?? null, r.problem.requestId])).toEqual([
      ["UNSUPPORTED_MEDIA_TYPE", "IMAGE_FORMAT_REJECTED", REQ],
      ["PAYLOAD_TOO_LARGE", null, REQ],
      ["REQUEST_VALIDATION_FAILED", "IMAGE_PIXELS_EXCEEDED", REQ],
      ["MODEL_VERSION_CONFLICT", null, REQ],
      ["AI_PROVIDER_NOT_CONFIGURED", null, REQ],
    ]);
  });

  it("reads a proposal's photo (H-052) at the contract's method and path, with the tenant selector, as a PNG or JPEG Blob", async () => {
    const PROPOSAL = "aip_01M4YDQK00ADN261CME3WV5HZB";
    const bytes = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 7, 7]);
    const image = (type: string) => new Response(bytes, { status: 200, headers: { "Content-Type": type, "X-Request-ID": REQ } });
    const f = fakeFetch(image("image/png"), image("image/jpeg"));
    const client = api(f);
    const png = await client.getProposalImage(PROPOSAL);
    const jpeg = await client.getProposalImage(PROPOSAL);
    expect(png.ok && [png.data.type, png.data.size, png.requestId]).toEqual(["image/png", bytes.length, REQ]);
    expect(png.ok && new Uint8Array(await png.data.arrayBuffer())).toEqual(bytes);
    expect(jpeg.ok && jpeg.data.type).toBe("image/jpeg");
    const op = operations.find((o) => o.operation_id === "getProposalImage");
    const c = f.calls[0]!;
    const template = c.url.pathname.slice("/api".length).replace(/\/aip_[0-9A-Z]{26}/, "/{id}");
    expect([c.init.method, template, op?.roles]).toEqual([op?.method, op?.path, ["owner", "editor", "viewer"]]);
    expect([headers(c)["X-Organization-ID"], headers(c)["Accept"], c.init.cache, c.init.credentials]).toEqual([ORG, "image/png, image/jpeg", "no-store", "same-origin"]);
  });

  it("refuses a photo of another type, and hands the server's refusals over as given", async () => {
    const PROPOSAL = "aip_01M4YDQK00ADN261CME3WV5HZB";
    const err = (code: string) => ({ error: { code, message: "x", request_id: REQ } });
    const f = fakeFetch(
      new Response("<svg xmlns='http://www.w3.org/2000/svg'/>", { status: 200, headers: { "Content-Type": "image/svg+xml" } }),
      new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }),
      json(404, err("NOT_FOUND")),
      json(500, err("INTERNAL_ERROR")),
    );
    const client = api(f);
    const out = [];
    for (let i = 0; i < 4; i++) out.push(await client.getProposalImage(PROPOSAL));
    expect(out.map((r) => !r.ok && [r.problem.kind, r.problem.code])).toEqual([
      ["protocol", null],
      ["protocol", null],
      ["not_found", "NOT_FOUND"],
      ["internal", "INTERNAL_ERROR"],
    ]);
    // A bad ID, or no session: nothing is sent.
    const none = fakeFetch();
    expect((await api(none).getProposalImage("aip_x/../../models")).ok).toBe(false);
    expect((await createApiClient({ session: () => null, fetch: none.fetch }).getProposalImage(PROPOSAL)).ok).toBe(false);
    expect(none.calls).toHaveLength(0);
  });

  it("the form's bounds are the contract's", () => {
    const s = proposalSchema.properties.source_image.properties;
    expect(MAX_PHOTO_BYTES).toBe(s.size_bytes.maximum);
    expect([...PHOTO_TYPES]).toEqual(s.media_type.enum);
  });
});
