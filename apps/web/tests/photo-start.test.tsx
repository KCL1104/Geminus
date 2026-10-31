// @vitest-environment happy-dom
// H-062: a new model can start from a photo or floor plan. On the project
// page's new-model form, the electronics line (the one domain whose pack
// reads photos, PHOTO_DOMAINS) also offers "Start from a photo or floor
// plan": the person names the model and picks a PNG or JPEG (the model
// page's limit and notice; nothing is sent without one, or with one larger
// than 5 MB); Create model sends createModel with the pack's photo template,
// then the photo to createImageProposal on the new model's version 1 with a
// key of its own, and opens the proposal. When the photo is not sent the
// model exists: the page opens it, and the model page says the photo was not
// sent, with the refusal, above its own photo button, which sends it again;
// once. A refused create sends no photo and its retry keeps its key. A
// viewer is offered no form. Both languages, axe on every state.
import { act, Suspense } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { createProposalStandIn } from "../app/ui-gallery/proposal/stand-in";
import { MAX_PHOTO_BYTES, PHOTO_DOMAINS } from "../features/ai-proposals/photo-form";
import { ModelView } from "../features/model-editor/model-view";
import { notePhotoNotSent, photoNotSent } from "../features/models/photo-start";
import { createApiClient } from "../lib/api/client";
import type { Problem } from "../lib/api/problem";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider } from "../lib/auth/session";
import type { Model } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { mount, press, settle, unmountAll, violations } from "./ui-harness";

const nav = vi.hoisted(() => ({ pushed: [] as string[] }));
vi.mock("next/navigation", async (orig) => ({
  ...(await orig<typeof import("next/navigation")>()),
  useRouter: () => ({ push: (href: string) => nav.pushed.push(href), refresh: () => {} }),
}));

vi.setConfig({ testTimeout: 120_000 });

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const PRJ = "prj_01M4YDQK00TA00000000000001";
const NEW = "mdl_01M4YDQK00TA00000000000077";
const NEW_VERSION = "mv_01M4YDQK00TA00000000000077";
const AIP = "aip_01M4YDQK00TA00000000000099";
const auth = (role: Auth["role"]): Auth => ({ session: { organizationId: ORG }, role });
const catalogs = { en, "zh-Hant": zhHant } as const;

const model = (id: string, name: string, domain: Model["domain"], version = "mv_01M4YDQK00TA00000000000001"): Model => ({
  id, organization_id: ORG, project_id: PRJ, domain, name,
  draft: { revision: 1, base_version_id: version, out_of_date: false },
  latest_version: { id: version, version: 1 }, created_at: "2026-10-31T08:00:00Z",
});

type Parts = { parts: string[]; base_version_id: string | null; output_locale: string | null; model: string | null; file: { name: string; type: string; size: number } | null };
type Call = { method: string; path: string; key: string | null; body: unknown };
type Answer = () => Response | Promise<Response>;

function parts(form: FormData): Parts {
  const field = (n: string) => (typeof form.get(n) === "string" ? (form.get(n) as string) : null);
  const file = form.get("file");
  return {
    parts: [...form.keys()], base_version_id: field("base_version_id"), output_locale: field("output_locale"), model: field("model"),
    file: file instanceof Blob ? { name: (file as File).name, type: file.type, size: file.size } : null,
  };
}

/** The API behind the app's own client: the project's models, createModel (from `creates` in turn, then 201) and createImageProposal on the new model (from `photos` in turn, then 202). */
function fakeApi(opts: { creates?: Answer[]; photos?: Answer[] } = {}) {
  const calls: Call[] = [];
  const creates = [...(opts.creates ?? [])];
  const photos = [...(opts.photos ?? [])];
  const client = createApiClient({
    session: () => ({ organizationId: ORG }),
    fetch: (async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://app.test").pathname;
      const method = init?.method ?? "GET";
      const key = new Headers(init?.headers).get("Idempotency-Key");
      const body = init?.body instanceof FormData ? parts(init.body) : init?.body ? JSON.parse(String(init.body)) : null;
      calls.push({ method, path, key, body });
      if (path === `/api/v1/projects/${PRJ}/models` && method === "GET") {
        return new Response(JSON.stringify({ items: [model("mdl_01M4YDQK00TA00000000000001", "Line 1", "electronics_line")], next_cursor: null }), { status: 200 });
      }
      if (path === `/api/v1/projects/${PRJ}/models` && method === "POST") {
        const next = creates.shift();
        if (next) return next();
        const b = body as { name: string; domain: Model["domain"] };
        return new Response(JSON.stringify(model(NEW, b.name, b.domain, NEW_VERSION)), { status: 201 });
      }
      if (path === `/api/v1/models/${NEW}/ai-proposals/image` && method === "POST") {
        const next = photos.shift();
        if (next) return next();
        return new Response(JSON.stringify({ proposal_id: AIP, status: "generating" }), { status: 202 });
      }
      throw new Error(`no fake answer for ${method} ${path}`);
    }) as typeof fetch,
  });
  const posts = () => calls.filter((c) => c.method === "POST");
  return { client, calls, posts, modelCreates: () => posts().filter((c) => c.path.endsWith("/models")), photoSends: () => posts().filter((c) => c.path.endsWith("/ai-proposals/image")) };
}

const refusal = (status: number, code: string, details?: { path: string; reason: string; code: string }[]) => () =>
  new Response(JSON.stringify({ error: { code, message: "refused", request_id: "req_01M4YDQK00V6H8S0ST4F7510KR", ...(details ? { details } : {}) } }), { status });

async function projectPage(opts: { role?: Auth["role"]; locale?: Locale; creates?: Answer[]; photos?: Answer[] } = {}) {
  const api = fakeApi(opts);
  const { default: ProjectPage } = await import("../app/projects/[projectId]/page");
  const el = await mount(
    <AuthProvider value={auth(opts.role === undefined ? "editor" : opts.role)}>
      <ApiProvider client={api.client}>
        <Suspense fallback={null}>
          <ProjectPage params={Promise.resolve({ projectId: PRJ })} />
        </Suspense>
      </ApiProvider>
    </AuthProvider>,
    opts.locale ?? "en",
  );
  await settle();
  await settle();
  return { el, api };
}

const text = (el: Element | null | undefined) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();
const button = (el: ParentNode, name: string) => [...el.querySelectorAll<HTMLButtonElement>("button")].find((b) => text(b) === name);
const form = (el: Element) => el.querySelector<HTMLElement>("[data-new-model]");
const photoField = (el: Element) => el.querySelector<HTMLElement>("[data-photo-start]");
const fileInput = (el: Element) => el.querySelector<HTMLInputElement>("[data-photo-start] input[type=file]");
const radio = (el: ParentNode, template: string) => el.querySelector<HTMLInputElement>(`[data-template="${template}"] input[type="radio"]`);
const nameField = (el: Element, label: string) => {
  const l = [...el.querySelectorAll("label")].find((x) => text(x) === label);
  if (!l) throw new Error(`no field ${label}`);
  return document.getElementById(l.htmlFor) as HTMLInputElement;
};
const described = (el: Element) => (el.getAttribute("aria-describedby") ?? "").split(" ").filter(Boolean).map((id) => text(document.getElementById(id)));

const PNG = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
const png = (name: string, size = 11) => new File([new Uint8Array(size).map((_, i) => PNG[i] ?? i % 251)], name, { type: "image/png", lastModified: 1_700_000_000_000 });

/** Chooses files in the hidden file input, as the browser's chooser does. */
async function pick(input: HTMLInputElement | null, ...files: File[]) {
  if (!input) throw new Error("no file chooser");
  await act(async () => {
    Object.defineProperty(input, "files", { configurable: true, value: files });
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await settle();
}
async function open(el: Element, t = createTranslator("en")) {
  await press(button(el, t("newModel.open")));
  await settle();
  const f = form(el);
  if (!f) throw new Error("the form did not open");
  return f;
}
async function choosePhotoStart(f: HTMLElement) {
  await press(radio(f, "photo_start")?.closest("label"));
  await settle();
}
async function submit(f: HTMLElement, t = createTranslator("en")) {
  await press(button(f, t("newModel.create")));
  for (let i = 0; i < 4; i++) await settle();
}

beforeEach(() => {
  nav.pushed = [];
});

describe("a new model from a photo or floor plan on the project page (H-062)", () => {
  it.each(["en", "zh-Hant"] as const)("is offered beside the electronics line's template only, in %s, and asks for a photo with the model page's limit and notice", async (locale) => {
    const t = createTranslator(locale);
    const { el } = await projectPage({ locale });
    const f = await open(el, t);
    // Only where the pack reads photos: one option, in the electronics line's group, after its template.
    expect(PHOTO_DOMAINS).toEqual(["electronics_line"]);
    const options = [...f.querySelectorAll("[data-template=photo_start]")];
    expect(options).toHaveLength(1);
    const group = options[0]!.closest("[role=group]")!;
    expect(text(group.querySelector("h3"))).toBe(t("domain.electronics_line.name"));
    expect([...group.querySelectorAll("[data-template]")].map((o) => o.getAttribute("data-template"))).toEqual(["electronics_assembly_test_line", "photo_start"]);
    const r = radio(f, "photo_start")!;
    expect(text(document.getElementById(r.getAttribute("aria-labelledby")!))).toBe(t("template.photo_start.name"));
    expect(text(document.getElementById(r.getAttribute("aria-describedby")!))).toBe(t("template.photo_start.description"));
    expect(photoField(f), "no photo field until the option is chosen").toBeNull();

    await choosePhotoStart(f);
    expect(r.checked).toBe(true);
    const field = photoField(f)!;
    expect(field.getAttribute("role")).toBe("group");
    expect(text(document.getElementById(field.getAttribute("aria-labelledby")!))).toBe(t("newModel.photo.label"));
    const choose = button(field, t("newModel.photo.choose"))!;
    expect(choose.getAttribute("data-variant")).toBe("secondary");
    // The model page's limit and the notice that what is sent goes to an AI service.
    expect(text(field)).toContain(t("photo.hint"));
    expect(text(field)).toContain(t("ask.dataNotice"));
    expect(described(choose).join(" ")).toContain(t("photo.hint"));
    const input = fileInput(f)!;
    expect([input.accept, input.multiple]).toEqual(["image/png,image/jpeg", false]);
    // The person names the model; it starts as a line from a photo.
    expect(nameField(f, t("newModel.name")).value).toBe(t("newModel.photo.defaultName"));
    // Create model stays the page's one primary action.
    expect(el.querySelectorAll('[data-variant="primary"]')).toHaveLength(1);
    expect(text(f.querySelector('[data-variant="primary"]'))).toBe(t("newModel.create"));
    expect(await violations()).toEqual([]);

    await pick(input, png("Line 2.png"));
    expect(text(f.querySelector("[data-photo-file]"))).toBe("Line 2.png");
    const again = button(field, t("newModel.photo.again"))!;
    expect(described(again)[0], "the button names the chosen file").toBe("Line 2.png");
    expect(await violations()).toEqual([]);
  });

  it.each(["en", "zh-Hant"] as const)("creates the model, then sends the photo on its version 1 with a key of its own, and opens the proposal, in %s", async (locale) => {
    const t = createTranslator(locale);
    const { el, api } = await projectPage({ locale });
    const f = await open(el, t);
    await choosePhotoStart(f);
    await pick(fileInput(f), png("floor plan.png"));
    await submit(f, t);
    const [create, send] = api.posts();
    expect(api.posts()).toHaveLength(2);
    expect(create!.path).toBe(`/api/v1/projects/${PRJ}/models`);
    expect(create!.body).toEqual({ name: catalogs[locale]["newModel.photo.defaultName"], domain: "electronics_line", template: "photo_start", locale });
    expect(send!.path).toBe(`/api/v1/models/${NEW}/ai-proposals/image`);
    expect(send!.body).toEqual({ parts: ["base_version_id", "output_locale", "file"], base_version_id: NEW_VERSION, output_locale: locale, model: null, file: { name: "photo", type: "image/png", size: 11 } } satisfies Parts);
    for (const c of [create!, send!]) expect(c.key).toMatch(/^[\x21-\x7e]{8,128}$/);
    expect(create!.key).not.toBe(send!.key);
    expect(nav.pushed).toEqual([`/proposals/${AIP}`]);
    expect(photoNotSent(NEW)).toBeNull();
  });

  it("keeps a name the person typed, and sends nothing without a name or a photo, or with a photo larger than 5 MB", async () => {
    const { el, api } = await projectPage();
    const f = await open(el);
    await choosePhotoStart(f);
    await submit(f);
    expect(api.posts(), "no request without a photo").toHaveLength(0);
    const field = photoField(f)!;
    expect(text(field)).toContain(en["newModel.photo.required"]);
    expect(described(button(field, en["newModel.photo.choose"])!)).toContain(en["newModel.photo.required"]);

    await pick(fileInput(f), new File([new Uint8Array(MAX_PHOTO_BYTES + 1)], "huge.png", { type: "image/png" }));
    expect(f.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("invalid");
    expect(text(f)).toContain(en["photo.problem.tooLarge"]);
    expect(f.querySelector("[data-photo-file]"), "a refused file is not kept").toBeNull();
    await submit(f);
    expect(api.posts()).toHaveLength(0);

    await pick(fileInput(f), png("line.png"));
    expect(f.querySelector('[role="alert"]'), "the refusal goes with a file that fits").toBeNull();
    expect(text(field)).not.toContain(en["newModel.photo.required"]);
    const name = nameField(f, en["newModel.name"]);
    await act(async () => {
      name.focus();
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(name, "  ");
      name.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await settle();
    await submit(f);
    expect(api.posts(), "no request without a name").toHaveLength(0);
    expect(text(f)).toContain(en["newModel.nameRequired"]);
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(name, " Plant 3 line ");
      name.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await settle();
    await submit(f);
    expect(api.modelCreates().map((c) => c.body)).toEqual([{ name: "Plant 3 line", domain: "electronics_line", template: "photo_start", locale: "en" }]);
    expect(api.photoSends()).toHaveLength(1);
  });

  it("a refused create sends no photo and keeps the form; its retry reuses the key, then the photo goes", async () => {
    const { el, api } = await projectPage({ creates: [refusal(503, "SERVICE_UNAVAILABLE")] });
    const f = await open(el);
    await choosePhotoStart(f);
    await pick(fileInput(f), png("line.png"));
    await submit(f);
    expect(f.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("unavailable");
    expect(api.photoSends()).toHaveLength(0);
    expect(nav.pushed).toEqual([]);
    await submit(f);
    const keys = api.modelCreates().map((c) => c.key);
    expect(keys).toHaveLength(2);
    expect(new Set(keys).size, "a retry of the same request reuses its key").toBe(1);
    expect(api.photoSends()).toHaveLength(1);
    expect(nav.pushed).toEqual([`/proposals/${AIP}`]);
  });

  it("when the photo is not sent, opens the new model and leaves it the refusal with the photo form's advice", async () => {
    const format = [{ path: "/file", reason: "not a PNG or JPEG", code: "IMAGE_FORMAT_REJECTED" }];
    const { el, api } = await projectPage({ photos: [refusal(415, "UNSUPPORTED_MEDIA_TYPE", format)] });
    const f = await open(el);
    await choosePhotoStart(f);
    await pick(fileInput(f), png("line.png"));
    await submit(f);
    expect(api.modelCreates()).toHaveLength(1);
    expect(api.photoSends()).toHaveLength(1);
    expect(nav.pushed).toEqual([`/models/${NEW}`]);
    const left = photoNotSent(NEW);
    expect([left?.problem.kind, left?.problem.code, left?.problem.requestId, left?.guidance]).toEqual(["invalid", "UNSUPPORTED_MEDIA_TYPE", "req_01M4YDQK00V6H8S0ST4F7510KR", "photo.problem.format"]);

    nav.pushed = [];
    unmountAll();
    const again = await projectPage({ photos: [refusal(503, "AI_PROVIDER_NOT_CONFIGURED")] });
    const g = await open(again.el);
    await choosePhotoStart(g);
    await pick(fileInput(g), png("line.png"));
    await submit(g);
    expect(nav.pushed).toEqual([`/models/${NEW}`]);
    expect([photoNotSent(NEW)?.problem.kind, photoNotSent(NEW)?.guidance]).toEqual(["unavailable", undefined]);
  });

  it("holds Cancel and the chooser while the model is created and the photo sent", async () => {
    let answer!: (r: Response) => void;
    const { el, api } = await projectPage({ photos: [() => new Promise<Response>((r) => (answer = r))] });
    const f = await open(el);
    await choosePhotoStart(f);
    await pick(fileInput(f), png("line.png"));
    await submit(f);
    expect(api.photoSends()).toHaveLength(1);
    expect(text(el.querySelector('[role="status"]'))).toBe(en["photo.sending"]);
    expect(button(f, en["newModel.cancel"])!.getAttribute("aria-disabled")).toBe("true");
    expect(button(photoField(f)!, en["newModel.photo.again"])!.getAttribute("aria-disabled")).toBe("true");
    expect(fileInput(f), "no file chooser behind the held button").toBeNull();
    await press(button(f, en["newModel.cancel"]));
    await settle();
    expect(form(el)).not.toBeNull();
    await act(async () => answer(new Response(JSON.stringify({ proposal_id: AIP, status: "generating" }), { status: 202 })));
    await settle();
    expect(api.modelCreates()).toHaveLength(1);
    expect(nav.pushed).toEqual([`/proposals/${AIP}`]);
  });

  it("is not offered to a viewer", async () => {
    for (const role of ["viewer", null] as const) {
      const { el } = await projectPage({ role });
      expect(button(el, en["newModel.open"]), `${role}`).toBeUndefined();
      expect(el.querySelector("[data-template=photo_start]")).toBeNull();
      unmountAll();
    }
  });
});

// The model page, for a model whose photo the new-model page could not send.
async function modelPage(opts: { role?: Auth["role"]; locale?: Locale } = {}) {
  const api = createProposalStandIn({ sample: null });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const created: string[] = [];
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: opts.role === undefined ? "editor" : opts.role }}>
      <ApiProvider client={client}>
        <ModelView modelId={SAMPLE_MODEL_ID} onProposal={(id) => created.push(id)} />
      </ApiProvider>
    </AuthProvider>,
    opts.locale ?? "en",
  );
  for (let i = 0; i < 400 && el.querySelector("h1") === null; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
  await settle();
  return { el, api, created };
}

const formatRefusal = (): { problem: Problem; guidance: "photo.problem.format" } => ({
  problem: { kind: "invalid", code: "UNSUPPORTED_MEDIA_TYPE", status: 415, message: "refused", requestId: "req_01M4YDQK00V6H8S0ST4F7510KR",
    details: [{ path: "/file", reason: "not a PNG or JPEG", code: "IMAGE_FORMAT_REJECTED" }], retryAfterSeconds: null },
  guidance: "photo.problem.format",
});

describe("the model page of a model whose photo was not sent (H-062)", () => {
  it.each(["en", "zh-Hant"] as const)("says so above its photo button, with the refusal, in %s; the button sends the photo again", async (locale) => {
    const t = createTranslator(locale);
    notePhotoNotSent(SAMPLE_MODEL_ID, formatRefusal());
    const { el, api, created } = await modelPage({ locale });
    const notice = el.querySelector<HTMLElement>("[data-photo-not-sent]")!;
    expect(notice).not.toBeNull();
    const warning = notice.querySelector('[data-tone="warning"]')!;
    expect(warning.getAttribute("role")).toBe("alert");
    expect(text(warning.querySelector("h2"))).toBe(t("modelPhoto.notSent.title"));
    expect(text(warning.querySelector("p"))).toBe(t("modelPhoto.notSent.body", { button: t("photo.choose") }));
    const problem = notice.querySelector('[data-problem="invalid"]')!;
    expect(text(problem)).toContain(t("photo.problem.format"));
    expect(problem.querySelector("h3"), "the refusal is under the notice's heading").not.toBeNull();
    // Above the request whose photo button sends it again.
    const retry = button(el.querySelector("[data-photo]")!, t("photo.choose"))!;
    expect(notice.compareDocumentPosition(retry) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // Publish stays the page's one primary action.
    expect(el.querySelectorAll('[data-variant="primary"]')).toHaveLength(1);
    expect(await violations()).toEqual([]);
    expect(photoNotSent(SAMPLE_MODEL_ID), "read once").toBeNull();

    await pick(el.querySelector<HTMLInputElement>("[data-photo] input[type=file]"), png("line.png"));
    for (let i = 0; i < 200 && created.length === 0; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
    expect(created).toHaveLength(1);
    expect(api.calls.filter((c) => c.method === "POST" && c.path.endsWith("/ai-proposals/image"))).toHaveLength(1);
  });

  it("shows it once: opened again, the page is the model as it is", async () => {
    notePhotoNotSent(SAMPLE_MODEL_ID, formatRefusal());
    const first = await modelPage();
    expect(first.el.querySelector("[data-photo-not-sent]")).not.toBeNull();
    unmountAll();
    const second = await modelPage();
    expect(second.el.querySelector("h1")).not.toBeNull();
    expect(second.el.querySelector("[data-photo-not-sent]")).toBeNull();
  });

  it("is not shown to a viewer, who gets no request", async () => {
    notePhotoNotSent(SAMPLE_MODEL_ID, formatRefusal());
    const { el } = await modelPage({ role: "viewer" });
    expect(el.querySelector("[data-photo-not-sent]")).toBeNull();
    expect(el.querySelector("[data-photo]")).toBeNull();
    expect(photoNotSent(SAMPLE_MODEL_ID)).toBeNull();
  });
});
