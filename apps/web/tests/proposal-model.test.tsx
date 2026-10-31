// @vitest-environment happy-dom
// H-049: the person chooses the model of each proposal, Muse Spark 1.3 (the
// default: faster, costs less) or Muse Wonder 1.0 (Meta's most capable model,
// in preview: slower, costs more). On the model page one labelled choice,
// with a line that says what it means, for what is sent in words and for a
// photo alike; under an explanation's suggestions, one for "Propose as a
// scenario". Only Muse Wonder adds model to a request, so a request on the
// default is what it was; a deployment that does not offer the model is
// refused with its own advice; the proposal names the model that wrote it.
// The stand-in answers a request that names a model as written by it; no AI
// is called. H-051: Muse Wonder is off unless NEXT_PUBLIC_GEMINUS_OFFER_WONDER=1;
// the tests that choose it build as with the flag on (wonderOn), and without
// it there is no choice and every request is on the default.
import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { createProposalStandIn, type FormCall, type ProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { DEFAULT_MODEL, modelField, offeredModels, PROPOSAL_MODELS } from "../features/ai-proposals/model-choice";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { ModelView } from "../features/model-editor/model-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { ProposalModel } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { mount, press, settle, unmountAll, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });
afterEach(() => vi.unstubAllEnvs());

/** H-051: as a build with NEXT_PUBLIC_GEMINUS_OFFER_WONDER=1, which offers Muse Wonder 1.0. */
const wonderOn = () => vi.stubEnv("NEXT_PUBLIC_GEMINUS_OFFER_WONDER", "1");
/** H-051: as the production build, the flag unset. */
const wonderOff = () => vi.stubEnv("NEXT_PUBLIC_GEMINUS_OFFER_WONDER", undefined);

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
const button = (el: ParentNode, name: string) => {
  const b = [...el.querySelectorAll<HTMLButtonElement>("button")].find((x) => (x.getAttribute("aria-label") ?? text(x)) === name);
  if (!b) throw new Error(`no button "${name}"`);
  return b;
};
const ask = (el: Element) => el.querySelector<HTMLElement>("[data-ask]")!;
const fieldOf = (el: Element, label: string) => {
  const l = [...el.querySelectorAll("label")].find((x) => text(x) === label);
  if (!l) throw new Error(`no field ${label}`);
  return document.getElementById(l.htmlFor) as HTMLInputElement | HTMLTextAreaElement;
};
async function type(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
  await act(async () => {
    input.focus();
    const proto = input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
}
/** The Select's trigger whose label is label. */
const trigger = (el: Element, label: string) => {
  const b = [...el.querySelectorAll("button")].find((x) => x.getAttribute("aria-labelledby")?.split(" ").some((id) => text(document.getElementById(id)) === label));
  if (!b) throw new Error(`no select ${label}`);
  return b;
};
async function choose(el: Element, label: string, option: string) {
  await press(trigger(el, label));
  await settle();
  const o = [...document.querySelectorAll("[role=option]")].find((x) => text(x) === option);
  if (!o) throw new Error(`no option ${option}`);
  await press(o);
  await settle();
}
const creates = (api: ProposalStandIn) => api.calls.filter((c) => c.method === "POST" && c.path.endsWith("/ai-proposals"));
const uploads = (api: ProposalStandIn) => api.calls.filter((c) => c.method === "POST" && c.path.endsWith("/ai-proposals/image"));

async function modelPage(opts: Omit<ProposalStandInOptions, "sample"> & { locale?: Locale } = {}) {
  const api = createProposalStandIn({ sample: null, ...opts });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const created: string[] = [];
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
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

describe("the model choice", () => {
  it("is the contract's two models, the default first, and only Muse Wonder adds a model to a request", () => {
    expect(PROPOSAL_MODELS).toEqual(["muse-spark-1.3", "muse-wonder-1.0"] satisfies ProposalModel[]);
    expect(DEFAULT_MODEL).toBe("muse-spark-1.3");
    expect(modelField("muse-spark-1.3")).toEqual({});
    expect(modelField("muse-wonder-1.0")).toEqual({ model: "muse-wonder-1.0" });
  });

  it("offers Muse Spark 1.3 alone unless NEXT_PUBLIC_GEMINUS_OFFER_WONDER is 1 (H-051)", () => {
    wonderOff();
    expect(offeredModels()).toEqual(["muse-spark-1.3"]);
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_OFFER_WONDER", "0");
    expect(offeredModels()).toEqual(["muse-spark-1.3"]);
    wonderOn();
    expect(offeredModels()).toEqual(PROPOSAL_MODELS);
  });

  it.each(["en", "zh-Hant"] as const)("without Muse Wonder on offer, in %s: the model page shows no choice, and a request in words or with a photo carries no model (H-051)", async (locale) => {
    wonderOff();
    const t = createTranslator(locale);
    const { el, api, created } = await modelPage({ locale });
    expect(ask(el).querySelector("[data-model-choice]")).toBeNull();
    expect(text(ask(el))).not.toContain(t("ai.model.muse-wonder-1.0"));
    expect(text(ask(el))).not.toContain(t("ask.modelHint.muse-spark-1.3"));
    expect(await violations()).toEqual([]);
    await type(fieldOf(ask(el), t("ask.intent")), "Balance the line");
    await press(button(ask(el), t("ask.send")));
    await waitFor(() => created.length === 1, "the proposal");
    expect(creates(api)[0]!.body).toEqual({ base_version_id: api.baseVersionId, mode: "edit", intent: "Balance the line", output_locale: locale });

    const chooser = el.querySelector<HTMLInputElement>("[data-photo] input[type=file]")!;
    const png = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3])], "line.png", { type: "image/png", lastModified: 1_700_000_000_000 });
    await act(async () => {
      Object.defineProperty(chooser, "files", { configurable: true, value: [png] });
      chooser.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await settle();
    await waitFor(() => created.length === 2, "the photo proposal");
    const body = uploads(api)[0]!.body as FormCall;
    expect([body.parts, body.model]).toEqual([["base_version_id", "output_locale", "file"], null]);
  });

  it.each(["en", "zh-Hant"] as const)("on the model page, in %s: a labelled choice on Muse Spark 1.3 whose line says what it means", async (locale) => {
    wonderOn();
    const t = createTranslator(locale);
    const { el } = await modelPage({ locale });
    const b = trigger(ask(el), t("ask.model"));
    expect(text(b)).toBe(t("ai.model.muse-spark-1.3"));
    // Review round 1, L3: the line describes the Select's own trigger.
    const described = (b.getAttribute("aria-describedby") ?? "")
      .split(" ")
      .filter(Boolean)
      .map((id) => text(document.getElementById(id)));
    expect(described).toContain(t("ask.modelHint.muse-spark-1.3"));
    await choose(ask(el), t("ask.model"), t("ai.model.muse-wonder-1.0"));
    expect(text(trigger(ask(el), t("ask.model")))).toBe(t("ai.model.muse-wonder-1.0"));
    expect(text(ask(el).querySelector("[data-model-choice]"))).toContain(t("ask.modelHint.muse-wonder-1.0"));
    expect(await violations()).toEqual([]);
  });

  it("sends no model on the default, model: muse-wonder-1.0 when chosen, with a new key, and the proposal names Muse Wonder 1.0", async () => {
    wonderOn();
    const { el, api, created } = await modelPage();
    await type(fieldOf(ask(el), en("ask.intent")), "Balance the line");
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 1, "the first proposal");
    expect(creates(api)[0]!.body).toEqual({ base_version_id: api.baseVersionId, mode: "edit", intent: "Balance the line", output_locale: "en" });

    await choose(ask(el), en("ask.model"), en("ai.model.muse-wonder-1.0"));
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 2, "the Muse Wonder proposal");
    const [first, second] = creates(api);
    expect(second!.body).toEqual({ base_version_id: api.baseVersionId, mode: "edit", intent: "Balance the line", output_locale: "en", model: "muse-wonder-1.0" });
    expect(second!.key).not.toBe(first!.key);

    unmountAll();
    const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
    const page = await mount(
      <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
        <ApiProvider client={client}>
          <ProposalRoute proposalId={created[1]!} pollMs={25} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => page.querySelector('[data-proposal-status="ready"]') !== null, "the ready proposal");
    const writtenBy = [...page.querySelectorAll("header dl > div")].map((d) => text(d)).find((d) => d.startsWith(en("ai.writtenBy")));
    expect(writtenBy).toBe(`${en("ai.writtenBy")}${en("ai.byProvider", { model: en("ai.model.muse-wonder-1.0"), provider: en("ai.provider.meta") })}`);
  });

  it("sends the chosen model with a photo, as a field before the file", async () => {
    wonderOn();
    const { el, api, created } = await modelPage();
    await choose(ask(el), en("ask.model"), en("ai.model.muse-wonder-1.0"));
    const chooser = el.querySelector<HTMLInputElement>("[data-photo] input[type=file]")!;
    const png = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3])], "line.png", { type: "image/png", lastModified: 1_700_000_000_000 });
    await act(async () => {
      Object.defineProperty(chooser, "files", { configurable: true, value: [png] });
      chooser.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await settle();
    await waitFor(() => created.length === 1, "the photo proposal");
    const body = uploads(api)[0]!.body as FormCall;
    expect([body.parts, body.model]).toEqual([["base_version_id", "output_locale", "model", "file"], "muse-wonder-1.0"]);
  });

  it("says to choose Muse Spark 1.3 when this deployment does not offer the model, and keeps the words", async () => {
    wonderOn();
    const { el } = await modelPage({
      refuseNext: { status: 503, code: "AI_POLICY_NOT_APPROVED", message: "this model is not approved for proposals in this deployment", details: [{ path: "/model", reason: "this deployment's approval does not list the model for proposals", code: "AI_MODEL_NOT_APPROVED" }] },
    });
    await choose(ask(el), en("ask.model"), en("ai.model.muse-wonder-1.0"));
    await type(fieldOf(ask(el), en("ask.intent")), "Balance the line");
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => el.querySelector("[data-ask] [role=alert]") !== null, "the refusal");
    const alert = ask(el).querySelector("[role=alert]")!;
    expect(text(alert)).toContain(en("ask.model.unavailable"));
    expect(text(alert)).not.toContain(en("error.AI_POLICY_NOT_APPROVED"));
    expect(fieldOf(ask(el), en("ask.intent")).value).toBe("Balance the line");
    expect(await violations()).toEqual([]);
  });
});

/** The proposal page of the stand-in's proposal id (pollMs: how often it reads a generating one again). */
async function proposalPage(api: ProposalStandIn, id: string, locale: Locale = "en", pollMs = 400) {
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const page = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
      <ApiProvider client={client}>
        <ProposalRoute proposalId={id} pollMs={pollMs} />
      </ApiProvider>
    </AuthProvider>,
    locale,
  );
  await waitFor(() => page.querySelector("[data-proposal-status]") !== null, "the proposal page");
  return page;
}
const slowNote = (el: Element) => el.querySelector("[data-slow-model]");

// H-065: Muse Wonder 1.0 is the choice for harder changes: it thinks longer
// (the server gives it 128,000 output tokens and 900 s), so its line under
// the choice says so, and while a proposal on it is generating the proposal
// page says it can take several minutes. The page knows from the proposal's
// requested_model (what the request named, as the server reads it from the
// job), never from the URL or the browser. The note is about the proposal
// itself, so it shows whether or not this build offers the choice; nothing
// else changes for Muse Spark 1.3 or a proposal that named no model.
describe("Muse Wonder for harder changes (H-065)", () => {
  it("says what Muse Wonder is for, in both languages", () => {
    const zh = createTranslator("zh-Hant");
    expect(en("ask.modelHint.muse-wonder-1.0")).toBe("For harder changes. It thinks longer: several minutes, costs more.");
    expect(zh("ask.modelHint.muse-wonder-1.0")).toBe("適合較難的修改。它想得比較久：要幾分鐘，也比較貴。");
    expect(en("proposal.generatingHint.muse-wonder-1.0")).toBe("Muse Wonder can take several minutes.");
    expect(zh("proposal.generatingHint.muse-wonder-1.0")).toBe("Muse Wonder 可能要幾分鐘。");
    // Muse Spark's line and the page's own hint are as they were.
    expect(en("ask.modelHint.muse-spark-1.3")).toBe("Faster, and costs less. The default.");
    expect(en("proposal.generatingHint")).toBe("You can leave this page and come back to it.");
  });

  for (const [build, flag] of [["with Muse Wonder on offer", wonderOn], ["as the production build", wonderOff]] as const) {
    it.each(["en", "zh-Hant"] as const)(`${build}, in %s: a generating Muse Wonder proposal says it can take several minutes, until it is ready`, async (locale) => {
      flag();
      const t = createTranslator(locale);
      const api = createProposalStandIn({ sample: "generating", proposal: { requested_model: "muse-wonder-1.0" } });
      const page = await proposalPage(api, api.proposalId!, locale);
      expect(page.querySelector("[data-proposal-status]")!.getAttribute("data-proposal-status")).toBe("generating");
      expect(text(slowNote(page))).toBe(t("proposal.generatingHint.muse-wonder-1.0"));
      expect(text(page)).toContain(t("proposal.generatingHint"));
      expect(await violations()).toEqual([]);
      await waitFor(() => page.querySelector('[data-proposal-status="ready"]') !== null, "the ready proposal");
      expect(slowNote(page)).toBeNull();
      expect(text(page)).not.toContain(t("proposal.generatingHint.muse-wonder-1.0"));
    });
  }

  it.each([["Muse Spark 1.3", { requested_model: "muse-spark-1.3" as const }], ["no model named", {}]] as const)("a generating proposal on %s has no such note", async (_, proposal) => {
    wonderOn();
    const api = createProposalStandIn({ sample: "generating", proposal });
    const page = await proposalPage(api, api.proposalId!);
    expect(page.querySelector("[data-proposal-status]")!.getAttribute("data-proposal-status")).toBe("generating");
    expect(slowNote(page)).toBeNull();
    expect(text(page)).not.toContain(en("proposal.generatingHint.muse-wonder-1.0"));
    expect(text(page)).toContain(en("proposal.generatingHint"));
  });

  it("from the canvas's Ask AI on Muse Wonder, in words and with a photo: the proposal page says it can take several minutes; on Muse Spark it does not", async () => {
    wonderOn();
    const { el, api, created } = await modelPage({ generatingPolls: 3 });
    await type(fieldOf(ask(el), en("ask.intent")), "Balance the line");
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 1, "the Muse Spark proposal");
    await choose(ask(el), en("ask.model"), en("ai.model.muse-wonder-1.0"));
    expect(text(ask(el).querySelector("[data-model-choice]"))).toContain("For harder changes. It thinks longer: several minutes, costs more.");
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 2, "the Muse Wonder proposal");
    const chooser = el.querySelector<HTMLInputElement>("[data-photo] input[type=file]")!;
    const png = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3])], "line.png", { type: "image/png", lastModified: 1_700_000_000_000 });
    await act(async () => {
      Object.defineProperty(chooser, "files", { configurable: true, value: [png] });
      chooser.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await settle();
    await waitFor(() => created.length === 3, "the Muse Wonder photo proposal");
    unmountAll();
    for (const [i, slow] of [[0, false], [1, true], [2, true]] as const) {
      const page = await proposalPage(api, created[i]!);
      expect(page.querySelector("[data-proposal-status]")!.getAttribute("data-proposal-status")).toBe("generating");
      expect(slowNote(page) !== null, `proposal ${i + 1}`).toBe(slow);
      unmountAll();
    }
  });
});
