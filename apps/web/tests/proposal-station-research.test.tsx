// @vitest-environment happy-dom
// H-073 (5): after a photo proposal is approved, its page offers to look up
// typical times for the stations the photo added (a photo shows no times, so
// the server let them take only numbers the model already had, review.Photo).
// The offer names those stations, from the approved proposal's changes, by
// their names; it shows the research note the ask form shows beside its
// opt-in; and it opens the model's Ask AI with the request filled in, web
// research ticked and a change to the model chosen. Nothing is sent: the
// person reads, edits and sends it, and approves what comes back. No AI is
// called: the stand-in (app/ui-gallery/proposal/stand-in.ts) answers.
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { SAMPLE_MODEL_ID, sampleLine } from "../app/ui-gallery/editor/sample-line";
import { readyEdit, readyFromPhoto, readyScenario, SAMPLE_PROPOSAL_ID, USER, versionId } from "../app/ui-gallery/proposal/samples";
import { createProposalStandIn, type ProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { askPrefill, clearAskPrefill, newStations, noteAskPrefill, photoStations, stationTimesRequest } from "../features/ai-proposals/station-research";
import { ModelView } from "../features/model-editor/model-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider, type Role } from "../lib/auth/session";
import type { JsonPatch, Proposal } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { mount, press, settle, unmountAll, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });

const en = createTranslator("en");

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
// By its accessible name: an icon key (the composer's send, 2026-11-02) is named by its aria-label.
const buttons = (el: ParentNode, name: string) => [...el.querySelectorAll<HTMLButtonElement>("button")].filter((x) => (x.getAttribute("aria-label") ?? text(x)) === name);
const button = (el: ParentNode, name: string) => {
  const b = buttons(el, name)[0];
  if (!b) throw new Error(`no button "${name}"`);
  return b;
};
const offer = (el: Element) => el.querySelector<HTMLElement>("[data-station-research]");
const described = (el: Element) => (el.getAttribute("aria-describedby") ?? "").split(/\s+/).filter(Boolean).map((id) => text(document.getElementById(id)));
const ask = (el: Element) => el.querySelector<HTMLElement>("[data-ask]")!;
const fieldOf = (el: Element, label: string) => {
  const l = [...el.querySelectorAll("label")].find((x) => text(x) === label);
  if (!l) throw new Error(`no field ${label}`);
  return document.getElementById(l.htmlFor) as HTMLTextAreaElement;
};
const optIn = (el: Element, label: string) => {
  // By its name: the composer's chip (2026-11-02) shows the start of it and is named by aria-label.
  const input = [...el.querySelectorAll<HTMLInputElement>("input[type=checkbox]")].find((x) => (x.getAttribute("aria-label") ?? text(x.closest("label"))) === label);
  if (!input) throw new Error(`no checkbox ${label}`);
  return input;
};
async function type(input: HTMLTextAreaElement, value: string) {
  await act(async () => {
    input.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
}
const creates = (api: ProposalStandIn) => api.calls.filter((c) => c.method === "POST" && /\/ai-proposals(\/image)?$/.test(c.path));

/** The stations' names as the request and the offer quote them, in the reader's language. */
const quoted = (t: typeof en, names: string[], locale: Locale) =>
  new Intl.ListFormat(locale, { type: "conjunction" }).format(names.map((n) => t("proposal.value.text", { text: n })));

const decided = { status: "approved" as const, new_version_id: versionId(2), decided_by: USER, decided_at: "2026-10-26T09:20:00Z" };

// IDs from the proposal's allocation pool (D-046), as the samples have them.
const node = (id: string, name: string, type: "process" | "queue" = "process") =>
  ({ op: "add", path: "/nodes/-", value: type === "process" ? { id, type, name, service_time: { distribution: { type: "constant", value: 0.9 } }, requires: [] } : { id, type, name, capacity: 20 } }) as JsonPatch[number];
const A = "node_01m4ydqk00e1ectr0n1c5x0011";
const B = "node_01m4ydqk00e1ectr0n1c5x0012";
const C = "node_01m4ydqk00e1ectr0n1c5x0013";
const D = "node_01m4ydqk00e1ectr0n1c5x0014";
/** A photo answer that adds a buffer and changes a capacity, but no station. */
const noStation: JsonPatch = [node(D, "Before X-ray", "queue")];
/** Three new stations, two of them named alike. */
const sharedNames: JsonPatch = [node(A, "Reflow oven"), node(C, "reflow  OVEN"), node(D, "X-ray inspection")];

const photo = (changes?: JsonPatch, extra: Partial<Proposal> = {}): Proposal => ({ ...readyFromPhoto(SAMPLE_PROPOSAL_ID), ...(changes ? { changes } : {}), ...extra });

describe("the stations a photo proposal added", () => {
  it("are its new process steps by name: not its buffer, its machine or a step it was not asked for with a photo", () => {
    // The photo sample adds X-ray inspection, the buffer before it and its X-ray machine.
    expect(photoStations(photo(), sampleLine)).toEqual(["X-ray inspection"]);
    expect(photoStations(photo(noStation), sampleLine)).toEqual([]);
    // Not asked for with a photo, or not a change to the model: none.
    expect(photoStations({ ...readyEdit(SAMPLE_PROPOSAL_ID), changes: photo().changes }, sampleLine)).toEqual([]);
    expect(photoStations({ ...readyScenario(SAMPLE_PROPOSAL_ID), source_image: photo().source_image, changes: photo().changes }, sampleLine)).toEqual([]);
    expect(photoStations(photo(undefined, { changes: undefined }), sampleLine)).toEqual([]);
  });

  it("keep the patch's order, name a station once, leave out one the patch removes again, and show what a name hides", () => {
    const n = sampleLine.nodes.length;
    const changes: JsonPatch = [
      node(A, "Reflow oven"),
      node(B, "Selective solder"),
      node(C, "reflow  OVEN"),
      { op: "test", path: `/nodes/${n + 1}/id`, value: B },
      { op: "remove", path: `/nodes/${n + 1}` },
      node(D, "X-ray‮inspection"),
    ];
    expect(photoStations(photo(changes), sampleLine)).toEqual(["Reflow oven", "X-ray⟨U+202E⟩inspection"]);
  });

  it("are none when the changes do not apply to the base", () => {
    expect(photoStations(photo([{ op: "remove", path: "/nodes/999" }]), sampleLine)).toEqual([]);
    expect(newStations(photo([{ op: "remove", path: "/nodes/999" }]), sampleLine)).toEqual({ names: [], count: 0 });
  });

  // H-073 review (Low): the count is of the process steps the proposal adds; only the names are listed once.
  it("are counted as the process steps the proposal adds, though names they share are listed once", () => {
    expect(newStations(photo(), sampleLine)).toEqual({ names: ["X-ray inspection"], count: 1 });
    expect(newStations(photo(sharedNames), sampleLine)).toEqual({ names: ["Reflow oven", "X-ray inspection"], count: 3 });
    expect(newStations(photo(noStation), sampleLine)).toEqual({ names: [], count: 0 });
  });

  it.each(["en", "zh-Hant"] as const)("are named in the %s request, quoted and listed the reader's way", (locale) => {
    const t = createTranslator(locale);
    expect(stationTimesRequest(t, locale, ["X-ray inspection"])).toBe(t("ask.prefill.stationTimes.intent", { count: 1, stations: t("proposal.value.text", { text: "X-ray inspection" }) }));
    const two = stationTimesRequest(t, locale, ["Reflow oven", "X-ray inspection"]);
    expect(two).toBe(t("ask.prefill.stationTimes.intent", { count: 2, stations: quoted(t, ["Reflow oven", "X-ray inspection"], locale) }));
    expect(two).toContain(locale === "en" ? "“Reflow oven” and “X-ray inspection”" : "「Reflow oven」和「X-ray inspection」");
  });

  it.each(["en", "zh-Hant"] as const)("give the %s request the count of the new stations, and say that shared names are listed once", (locale) => {
    const t = createTranslator(locale);
    const names = ["Reflow oven", "X-ray inspection"];
    const shared = stationTimesRequest(t, locale, names, 3);
    expect(shared).toBe(t("ask.prefill.stationTimes.shared", { count: 3, stations: quoted(t, names, locale) }));
    expect(shared).toContain(
      locale === "en"
        ? "A photo added 3 stations; names they share are listed once: “Reflow oven” and “X-ray inspection”."
        : "照片新增了 3 個工作站，同名的只列一次：「Reflow oven」和「X-ray inspection」。",
    );
    // As many names as stations: the request as before.
    expect(stationTimesRequest(t, locale, names, 2)).toBe(stationTimesRequest(t, locale, names));
  });
});

type Opts = Omit<ProposalStandInOptions, "sample"> & { sample?: ProposalStandInOptions["sample"]; role?: Role | null; locale?: Locale; api?: ProposalStandIn; offered?: boolean };

async function proposalPage(opts: Opts = {}) {
  const api = opts.api ?? createProposalStandIn({ ...opts, sample: opts.sample ?? "photo" });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const asked: string[] = [];
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: opts.role === undefined ? "editor" : opts.role }}>
      <ApiProvider client={client}>
        <ProposalRoute proposalId={api.proposalId ?? SAMPLE_PROPOSAL_ID} pollMs={25} onAsk={opts.offered === false ? undefined : (id) => asked.push(id)} />
      </ApiProvider>
    </AuthProvider>,
    opts.locale ?? "en",
  );
  await settle();
  await waitFor(() => el.querySelector("[data-proposal-status], [role=alert]") !== null, "the proposal page");
  await settle();
  return { el, api, asked };
}

async function approve(el: HTMLElement, t = en) {
  await press(button(el, t("proposal.approve.edit", { version: "2" })));
  await press(button(el, t("proposal.approve.confirm.edit", { version: "2" })));
  await waitFor(() => text(el).includes(t("proposal.outcome.edit", { version: "2" })), "the outcome");
  await settle();
}

async function modelPage(api: ProposalStandIn, locale: Locale = "en") {
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const created: string[] = [];
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
      <ApiProvider client={client}>
        <ModelView modelId={SAMPLE_MODEL_ID} onProposal={(id) => created.push(id)} />
      </ApiProvider>
    </AuthProvider>,
    locale,
  );
  await settle();
  await waitFor(() => el.querySelector("[data-ask], [role=alert]") !== null, "the model page");
  await settle();
  return { el, created };
}

describe("the offer on an approved photo proposal", () => {
  it.each(["en", "zh-Hant"] as const)("after the approval in %s, names the new stations, shows the research note, and opens Ask AI with the request filled in, sending nothing", async (locale) => {
    const t = createTranslator(locale);
    clearAskPrefill(SAMPLE_MODEL_ID);
    const { el, api, asked } = await proposalPage({ locale });
    expect(offer(el), "not before the approval").toBeNull();
    await approve(el, t);
    const o = offer(el)!;
    expect(o.closest("[data-outcome=approved]"), "the approval's next step, in its outcome").not.toBeNull();
    const b = button(o, t("proposal.stationTimes.offer"));
    // A default button: Approve was the page's one primary, and opening a form asks nothing of the AI yet.
    expect(b.getAttribute("data-variant")).toBe("secondary");
    expect(o.querySelectorAll("button, a")).toHaveLength(1);
    // The station by its name, quoted, as data in its own <bdi>; then the research note the ask form shows beside its opt-in.
    expect([...o.querySelectorAll("bdi")].map((x) => x.textContent)).toEqual(["X-ray inspection"]);
    expect(text(o)).toContain(t("proposal.stationTimes.why", { stations: t("proposal.value.text", { text: "X-ray inspection" }) }));
    expect(text(o)).not.toContain("Before X-ray");
    expect(text(o)).not.toContain("X-ray machine");
    expect(described(b)).toEqual([
      text(o.querySelector("[data-station-research-why]")),
      t("ask.researchHint"),
    ]);
    expect(text(o.querySelector("[data-station-research-why]"))).toContain(t("proposal.stationTimes.opens", { form: t("ask.title") }));
    // DESIGN.md Simplicity: at most one primary, and no two buttons or links with one name or links to one place.
    expect(el.querySelectorAll('[data-variant="primary"]').length).toBeLessThanOrEqual(1);
    const names = [...el.querySelectorAll("button, a[href]")].map((c) => `${c.tagName} ${c.getAttribute("aria-label") ?? text(c)}`);
    const hrefs = [...el.querySelectorAll("a[href]")].map((a) => a.getAttribute("href"));
    expect(names.filter((n, i) => names.indexOf(n) !== i)).toEqual([]);
    expect(hrefs.filter((h, i) => hrefs.indexOf(h) !== i)).toEqual([]);
    expect(await violations()).toEqual([]);

    const before = creates(api).length;
    await press(b);
    expect(asked).toEqual([SAMPLE_MODEL_ID]);
    expect(askPrefill(SAMPLE_MODEL_ID)).toEqual({ from: "photoStations", intent: stationTimesRequest(t, locale, ["X-ray inspection"]), mode: "edit", research: true });
    expect(creates(api).length, "pressing it sends nothing").toBe(before);
    clearAskPrefill(SAMPLE_MODEL_ID);
  });

  it("is offered on the page of a photo proposal approved before it opened, in its decided section", async () => {
    const { el } = await proposalPage({ proposal: decided });
    const o = offer(el)!;
    expect(o.closest("[data-decided=approved]")).not.toBeNull();
    expect(text(o)).toContain(en("proposal.stationTimes.offer"));
    expect(text(o)).toContain("X-ray inspection");
    expect(await violations()).toEqual([]);
  });

  it("is not offered for a photo proposal that added no station, an approved edit without a photo, a rejected or ready photo proposal, a viewer, or a page that cannot open Ask AI", async () => {
    const cases: [string, Opts][] = [
      ["no station", { proposal: { ...decided, changes: noStation } }],
      ["no photo", { sample: "approved" }],
      ["rejected", { proposal: { status: "rejected", decided_by: USER, decided_at: "2026-10-26T09:20:00Z" } }],
      ["ready", {}],
      ["viewer", { proposal: decided, role: "viewer" }],
      ["unknown role", { proposal: decided, role: null }],
      ["no Ask AI", { proposal: decided, offered: false }],
    ];
    for (const [what, opts] of cases) {
      const { el } = await proposalPage(opts);
      expect(text(el).length, `${what}: the page rendered`).toBeGreaterThan(0);
      expect(offer(el), what).toBeNull();
      expect(buttons(el, en("proposal.stationTimes.offer")), what).toHaveLength(0);
      unmountAll();
    }
  });

  it("fills in the request with the number of new stations, a shared name once", async () => {
    clearAskPrefill(SAMPLE_MODEL_ID);
    const { el } = await proposalPage({ proposal: { ...decided, changes: sharedNames } });
    const o = offer(el)!;
    expect([...o.querySelectorAll("bdi")].map((x) => x.textContent)).toEqual(["Reflow oven", "X-ray inspection"]);
    await press(button(o, en("proposal.stationTimes.offer")));
    const intent = askPrefill(SAMPLE_MODEL_ID)?.intent;
    expect(intent).toBe(stationTimesRequest(en, "en", ["Reflow oven", "X-ray inspection"], 3));
    expect(intent).toContain("A photo added 3 stations");
    clearAskPrefill(SAMPLE_MODEL_ID);
  });

  it("is not offered after approving a photo proposal that added no station", async () => {
    const { el } = await proposalPage({ proposal: { changes: noStation } });
    await approve(el);
    expect(offer(el)).toBeNull();
  });
});

describe("Ask AI on the model page, filled in from the offer", () => {
  it.each(["en", "zh-Hant"] as const)("in %s: holds the request, research ticked, a change to the model chosen, says where it came from, has the focus, and sends nothing", async (locale) => {
    const t = createTranslator(locale);
    const api = createProposalStandIn({ sample: null });
    const intent = stationTimesRequest(t, locale, ["X-ray inspection", "Reflow oven"]);
    noteAskPrefill(SAMPLE_MODEL_ID, { from: "photoStations", intent, mode: "edit", research: true });
    const { el, created } = await modelPage(api, locale);
    const form = ask(el);
    const field = fieldOf(form, t("ask.intent"));
    expect(field.value).toBe(intent);
    expect(field.value).toContain(quoted(t, ["X-ray inspection", "Reflow oven"], locale));
    expect(optIn(form, t("ask.research")).checked).toBe(true);
    expect(text(form)).toContain(t("ask.mode.edit"));
    expect(text(form)).toContain(t("ask.modeHint.edit", { version: "2" }));
    const note = form.querySelector("[data-prefilled]");
    expect(text(note)).toBe(t("ask.prefill.stationTimes.note"));
    expect(described(field)).toContain(t("ask.prefill.stationTimes.note"));
    expect(document.activeElement).toBe(field);
    await settle();
    expect([creates(api), created], "nothing is sent until the person sends it").toEqual([[], []]);
    expect(await violations()).toEqual([]);
    // Taken once: the model page opened again later shows an empty request.
    expect(askPrefill(SAMPLE_MODEL_ID)).toBeNull();
    unmountAll();
    const again = await modelPage(api, locale);
    expect(fieldOf(ask(again.el), t("ask.intent")).value).toBe("");
    expect(optIn(ask(again.el), t("ask.research")).checked).toBe(false);
    expect(ask(again.el).querySelector("[data-prefilled]")).toBeNull();
  });

  it("sends what the person edited, as a researched change to the model, only when they send it", async () => {
    const api = createProposalStandIn({ sample: null });
    noteAskPrefill(SAMPLE_MODEL_ID, { from: "photoStations", intent: stationTimesRequest(en, "en", ["X-ray inspection"]), mode: "edit", research: true });
    const { el, created } = await modelPage(api);
    const edited = `${stationTimesRequest(en, "en", ["X-ray inspection"])} Keep AOI as it is.`;
    await type(fieldOf(ask(el), en("ask.intent")), edited);
    expect(creates(api)).toEqual([]);
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 1, "the proposal");
    expect(creates(api)[0]!.body).toEqual({ base_version_id: api.baseVersionId, mode: "edit", intent: edited, output_locale: "en", research: true });
  });

  it("the loop: approve the photo proposal, press the offer, and the model page holds the request for the new version", async () => {
    clearAskPrefill(SAMPLE_MODEL_ID);
    const { el, api, asked } = await proposalPage();
    await approve(el);
    await press(button(offer(el)!, en("proposal.stationTimes.offer")));
    expect(asked).toEqual([SAMPLE_MODEL_ID]);
    unmountAll();
    const page = await modelPage(api);
    const field = fieldOf(ask(page.el), en("ask.intent"));
    expect(field.value).toBe(stationTimesRequest(en, "en", ["X-ray inspection"]));
    expect(optIn(ask(page.el), en("ask.research")).checked).toBe(true);
    // The request goes to the version the approval made, which has the new station.
    expect(text(ask(page.el))).toContain(en("ask.intentHint", { version: "2" }));
    expect(api.versions()[1]!.ir.nodes.some((n) => n.name === "X-ray inspection")).toBe(true);
    expect(creates(api).filter((c) => c.path.endsWith("/ai-proposals"))).toEqual([]);
  });
});
