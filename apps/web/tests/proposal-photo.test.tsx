// @vitest-environment happy-dom
// T-056 (H-033): on the model page of an electronics line, one way to ask
// for a change from a photo: one button, beside the ask form's send button,
// that opens the file chooser for a PNG or JPEG; the chosen file goes to createImageProposal on the model's
// latest version with the reader's language, and the API's answer is
// followed to the proposal page, which says the proposal was asked for with
// a photo. A viewer, a domain whose pack reads no photos, and an editor
// with unsaved edits (blocked, its reason beside it, nothing opens) get no
// file chooser. Then the loop against one stand-in: the photo proposal is
// reviewed and approved into a new version.
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { createProposalStandIn, type FormCall, type ProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { MAX_PHOTO_BYTES, PHOTO_DOMAINS, PhotoForm } from "../features/ai-proposals/photo-form";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { ModelView } from "../features/model-editor/model-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider, type Role } from "../lib/auth/session";
import type { Model } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { key, mount, press, settle, unmountAll, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });

const en = createTranslator("en");
const zh = createTranslator("zh-Hant");

type Opts = Omit<ProposalStandInOptions, "sample"> & { role?: Role | null; locale?: Locale; api?: ProposalStandIn };

async function modelPage(opts: Opts = {}) {
  const api = opts.api ?? createProposalStandIn({ sample: null, ...opts });
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
  await settle();
  await waitFor(() => el.querySelector("h1, [role=alert]") !== null, "the model page");
  await settle();
  return { el, api, created };
}

async function waitFor(check: () => boolean, what: string, ms = 10_000): Promise<void> {
  const until = Date.now() + ms;
  while (!check()) {
    if (Date.now() > until) throw new Error(`timed out waiting for ${what}`);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
}

const text = (el: Element | null | undefined) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();
const photo = (el: Element) => el.querySelector<HTMLElement>("[data-photo]");
const chooser = (el: Element) => el.querySelector<HTMLInputElement>("[data-photo] input[type=file]");
const button = (el: ParentNode, name: string) => {
  const b = [...el.querySelectorAll<HTMLButtonElement>("button")].find((x) => text(x) === name);
  if (!b) throw new Error(`no button "${name}"`);
  return b;
};
const uploads = (api: ProposalStandIn) => api.calls.filter((c) => c.method === "POST" && c.path.endsWith("/ai-proposals/image"));

const PNG = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
const file = (name: string, bytes: number[] = [...PNG, 1, 2, 3], type = "image/png") => new File([new Uint8Array(bytes)], name, { type, lastModified: 1_700_000_000_000 });

/** Chooses files in the hidden file input, as the browser's chooser does. */
async function pick(input: HTMLInputElement, ...files: File[]) {
  await act(async () => {
    Object.defineProperty(input, "files", { configurable: true, value: files });
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await settle();
}

async function unsavedEdit(el: Element) {
  const n = el.querySelector<HTMLElement>('.react-flow__node[data-id="smt"]')!;
  await act(async () => n.focus());
  await key(n, "Enter");
  await settle();
  const label = [...el.querySelectorAll("[data-panel=properties] label")].find((l) => text(l) === en("editor.field.name"))!;
  const name = document.getElementById((label as HTMLLabelElement).htmlFor) as HTMLInputElement;
  await act(async () => {
    name.focus();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(name, "SMT line 1");
    name.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
  await key(name, "Enter");
  await settle();
}

describe("asking for a change from a photo on the model page", () => {
  it("offers one button beside Send that opens the chooser for a PNG or JPEG, says what it does, and keeps Publish the one primary action", async () => {
    const { el } = await modelPage();
    const form = photo(el)!;
    // In the ask form: one AI section on the page, the editor where it was.
    expect(form.closest("section")?.hasAttribute("data-ask"), "inside the ask form's section").toBe(true);
    expect(form.querySelector("h2, section"), "no heading or section of its own").toBeNull();
    expect(text(form)).toContain(en("photo.hint"));
    const choose = button(form, en("photo.choose"));
    expect(choose.getAttribute("data-variant")).toBe("secondary");
    expect(form.querySelectorAll("button")).toHaveLength(1);
    // Its hint, then the ask form's notice that what is sent goes to an AI service (T-057, H-035).
    expect((choose.getAttribute("aria-describedby") ?? "").split(" ").map((id) => text(document.getElementById(id)))).toEqual([en("photo.hint"), en("ask.dataNotice")]);
    // The chooser: one file, PNG or JPEG, hidden behind the button.
    const input = chooser(el)!;
    expect([input.accept, input.multiple, input.style.display]).toEqual(["image/png,image/jpeg", false, "none"]);
    let opened = 0;
    input.addEventListener("click", () => opened++);
    await press(choose);
    expect(opened).toBe(1);
    expect(el.querySelectorAll('[data-variant="primary"]')).toHaveLength(1);
    expect(await violations()).toEqual([]);
  });

  it("sends the chosen file to createImageProposal on the latest version and follows the answer", async () => {
    const { el, api, created } = await modelPage();
    await pick(chooser(el)!, file("Line 2, Plant 3.png"));
    await waitFor(() => created.length === 1, "the proposal");
    const [c] = uploads(api);
    expect(c!.key).toMatch(/^key-/);
    // The parts in the contract's order, the file under the client's name, no Content-Type of the client's.
    expect([c!.body, c!.contentType]).toEqual([
      { parts: ["base_version_id", "output_locale", "file"], base_version_id: api.baseVersionId, output_locale: "en", file: { name: "photo", type: "image/png", size: 11 } } satisfies FormCall,
      null,
    ]);
    expect(api.proposals().find((p) => p.proposal_id === created[0])?.source_image?.media_type).toBe("image/png");
  });

  it("asks in the reader's language, and says so in zh-Hant", async () => {
    const { el, api, created } = await modelPage({ locale: "zh-Hant" });
    expect(text(photo(el))).toContain(zh("photo.hint"));
    button(photo(el)!, zh("photo.choose"));
    await pick(chooser(el)!, file("line.jpg", [0xff, 0xd8, 0xff, 0xe0, 0, 1], "image/jpeg"));
    await waitFor(() => created.length === 1, "the proposal");
    expect((uploads(api)[0]!.body as FormCall).output_locale).toBe("zh-Hant");
  });

  it("reuses the key for the same file after a failure, and takes a new one for another file", async () => {
    const { el, api, created } = await modelPage({ refuseNext: { status: 503, code: "SERVICE_UNAVAILABLE", message: "down" } });
    const first = file("line.png");
    await pick(chooser(el)!, first);
    await waitFor(() => photo(el)!.querySelector("[role=alert]") !== null, "the problem");
    expect(created).toEqual([]);
    await pick(chooser(el)!, first);
    await waitFor(() => created.length === 1, "the retry");
    await pick(chooser(el)!, file("another.png", [...PNG, 9, 9, 9, 9]));
    await waitFor(() => created.length === 2, "another photo");
    const [a, b, c] = uploads(api);
    expect(a!.key).toBe(b!.key);
    expect(c!.key).not.toBe(a!.key);
  });

  it("refuses a file larger than the contract takes before sending it, and says what to do", async () => {
    const { el, api, created } = await modelPage();
    const big = file("big.png");
    Object.defineProperty(big, "size", { value: MAX_PHOTO_BYTES + 1 });
    await pick(chooser(el)!, big);
    const alert = photo(el)!.querySelector("[role=alert]");
    expect(text(alert)).toContain(en("photo.problem.tooLarge"));
    expect([uploads(api), created]).toEqual([[], []]);
  });

  it("shows the server's refusal of a file that is no PNG or JPEG, with what to do", async () => {
    const { el, api, created } = await modelPage();
    await pick(chooser(el)!, file("line.gif", [0x47, 0x49, 0x46, 0x38, 0x39, 0x61], "image/gif"));
    await waitFor(() => photo(el)!.querySelector("[role=alert]") !== null, "the refusal");
    expect(text(photo(el)!.querySelector("[role=alert]"))).toContain(en("photo.problem.format"));
    expect(uploads(api)).toHaveLength(1);
    expect(created).toEqual([]);
    expect(await violations()).toEqual([]);
  });

  it("blocks the photo while the editor has unsaved edits, says why beside it, and opens nothing", async () => {
    const { el, api } = await modelPage();
    await unsavedEdit(el);
    const choose = button(photo(el)!, en("photo.choose"));
    expect(choose.getAttribute("aria-disabled")).toBe("true");
    const reasons = (choose.getAttribute("aria-describedby") ?? "").split(" ").map((id) => text(document.getElementById(id)));
    expect(reasons).toEqual([en("ask.saveFirst"), en("photo.hint"), en("ask.dataNotice")]);
    expect(chooser(el)).toBeNull();
    await press(choose);
    expect(uploads(api)).toEqual([]);
  });

  it("is not offered to a viewer, while the role is unknown, or for a domain whose pack reads no photos", async () => {
    for (const role of ["viewer", null] as const) {
      const { el } = await modelPage({ role });
      expect(photo(el)).toBeNull();
      unmountAll();
    }
    expect(PHOTO_DOMAINS).toEqual(["electronics_line"]);
    const model = { id: SAMPLE_MODEL_ID, domain: "software_delivery", latest_version: { id: "mv_01M4YDQK00E1ECTR0N1C500001", version: 1 } } as Model;
    expect(PHOTO_DOMAINS.includes(model.domain)).toBe(false);
    // The form itself, should a page render it, still sends only a chosen file.
    const api = createProposalStandIn({ sample: null });
    const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
    const el = await mount(
      <ApiProvider client={client}>
        <PhotoForm model={model} blockedReason={null} reasonId="reason" noticeId="notice" onCreated={() => {}} />
      </ApiProvider>,
    );
    await pick(el.querySelector<HTMLInputElement>("input[type=file]")!);
    expect(uploads(api)).toEqual([]);
  });
});

describe("the loop: a photo, the review, the approval", () => {
  it("says the proposal was asked for with a photo, and approves it into the next version", async () => {
    const api = createProposalStandIn({ sample: null, generatingPolls: 1 });
    const first = await modelPage({ api });
    await pick(chooser(first.el)!, file("line.png"));
    await waitFor(() => first.created.length === 1, "the proposal");
    unmountAll();
    const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
    const review = await mount(
      <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
        <ApiProvider client={client}>
          <ProposalRoute proposalId={first.created[0]!} pollMs={25} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => text(review.querySelector("[data-proposal-status]")) === en("proposal.status.ready"), "ready");
    expect(text(review.querySelector("[data-source=image]"))).toBe(en("proposal.fromPhoto", { type: "PNG", width: "1200", height: "520" }));
    const approve = en("proposal.approve.edit", { version: "2" });
    const confirm = en("proposal.approve.confirm.edit", { version: "2" });
    await press(button(review, approve));
    await press(button(review, confirm));
    await waitFor(() => text(review).includes(en("proposal.outcome.edit", { version: "2" })), "the outcome");
    expect(api.versions()).toHaveLength(2);
    expect(api.versions()[1]!.ir.nodes.some((n) => n.name === "X-ray inspection")).toBe(true);
    // No canvas positions came from the photo: the editor lays the step out.
    expect(Object.keys(api.versions()[1]!.ir.views?.["2d"]?.positions ?? {})).toEqual(Object.keys(api.versions()[0]!.ir.views?.["2d"]?.positions ?? {}));
  });
});
