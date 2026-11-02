// @vitest-environment happy-dom
// The owner's decision of 2026-11-02: the editor side panel's "Ask AI for a
// change" is a mainstream prompt composer, with the model's earlier requests
// above it. UI only: the AI's behaviour, the contract and the requests sent
// are what they were (the other ask tests pin those).
//
// 1. One bordered box holds the request, a tool row of chips (what the AI
//    proposes, the model where there is a choice, Let the AI act on it where
//    it exists, Research on the web first) and the send icon button, the
//    panel's one AI action; an option's explanation shows under the box only
//    while it is on, as its description; the version line and the data
//    notice are always under it.
// 2. Enter sends; Shift+Enter is a new line; Enter while an input method
//    composes never sends; an empty or over-limit request never sends and
//    says why; a second Enter while sending does nothing. The composer's
//    description says so for screen readers.
// 3. Earlier requests: the model's latest few requests from this panel (the
//    people's proposals, and where the panel starts them its investigations),
//    newest nearest the composer, each a link to its page with its state in
//    the server's words and its time; a photo request says it came from a
//    photo. listProposals and listInvestigations carry no request's words, so
//    a proposal is named by the AI's summary (or what it was asked to make),
//    and the panel says so. No list when there are none; a failed read says
//    so with a retry and never blocks the composer.
import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { analysisId, investigationId, sampleInvestigation } from "../app/ui-gallery/investigation/samples";
import { createProposalStandIn, type ProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { earlierRequests, proposalId, readyEdit, readyScenario, versionId } from "../app/ui-gallery/proposal/samples";
import { requestState } from "../features/ai-proposals/request-log";
import { ModelView } from "../features/model-editor/model-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { Investigation, Proposal } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { key, mount, press, settle, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });
afterEach(() => vi.unstubAllEnvs());

const en = createTranslator("en");
const zh = createTranslator("zh-Hant");

type Opts = Omit<ProposalStandInOptions, "sample"> & { locale?: Locale; act?: boolean };

async function modelPage(opts: Opts = {}) {
  const api = createProposalStandIn({ sample: null, ...opts });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const created: string[] = [];
  const started: string[] = [];
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
      <ApiProvider client={client}>
        <ModelView modelId={SAMPLE_MODEL_ID} onProposal={(id) => created.push(id)} onInvestigation={opts.act ? (id) => started.push(id) : undefined} />
      </ApiProvider>
    </AuthProvider>,
    opts.locale ?? "en",
  );
  await settle();
  await waitFor(() => el.querySelector("h1, [role=alert]") !== null, "the model page");
  await settle();
  return { el, api, created, started };
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
const ask = (el: Element) => el.querySelector<HTMLElement>("[data-ask]")!;
const composer = (el: Element) => ask(el).querySelector<HTMLElement>("[data-composer]");
const button = (el: ParentNode, name: string) => {
  const b = [...el.querySelectorAll<HTMLButtonElement>("button")].find((x) => (x.getAttribute("aria-label") ?? text(x)) === name);
  if (!b) throw new Error(`no button "${name}"`);
  return b;
};
const fieldOf = (el: Element, label: string) => {
  const l = [...el.querySelectorAll("label")].find((x) => text(x) === label);
  if (!l) throw new Error(`no field ${label}`);
  return document.getElementById(l.htmlFor) as HTMLTextAreaElement;
};
/** A checkbox by its name: a chip's aria-label (it shows the start or the end of it), else its label's words. */
const checkbox = (el: Element, label: string) =>
  [...el.querySelectorAll<HTMLInputElement>("input[type=checkbox]")].find((x) => (x.getAttribute("aria-label") ?? text(x.closest("label"))) === label) ?? null;
const trigger = (el: Element, label: string) =>
  [...el.querySelectorAll("button")].find((b) => b.getAttribute("aria-labelledby")?.split(" ").some((id) => text(document.getElementById(id)) === label)) ?? null;
const described = (x: Element | null) => (x?.getAttribute("aria-describedby") ?? "").split(" ").filter(Boolean).map((id) => text(document.getElementById(id)));
const follows = (a: Node, b: Node) => (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;
async function type(input: HTMLTextAreaElement, value: string) {
  await act(async () => {
    input.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
}
async function tick(box: HTMLInputElement | null) {
  if (!box) throw new Error("no checkbox");
  await act(async () => box.click());
  await settle();
}
/** A keydown on the request as a browser sends it; answers whether the page took the key (preventDefault). */
async function keyDown(field: HTMLTextAreaElement, init: KeyboardEventInit & { keyCode?: number }): Promise<boolean> {
  let taken = false;
  await act(async () => {
    const e = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init });
    if (init.keyCode !== undefined) Object.defineProperty(e, "keyCode", { value: init.keyCode });
    field.dispatchEvent(e);
    taken = e.defaultPrevented;
  });
  await settle();
  return taken;
}
const creates = (api: ProposalStandIn) => api.calls.filter((c) => c.method === "POST" && c.path.endsWith("/ai-proposals"));
const lists = (api: ProposalStandIn) => api.calls.filter((c) => c.method === "GET" && c.path.endsWith("/ai-proposals"));
const earlier = (el: Element) => ask(el).querySelector<HTMLElement>("[data-earlier]");
const rows = (el: Element) => [...(earlier(el)?.querySelectorAll<HTMLElement>("[data-request]") ?? [])];

describe("the composer (2026-11-02)", () => {
  it.each(["en", "zh-Hant"] as const)("in %s, is one box holding the request, the chips and the send icon button; Publish stays the one primary action", async (locale) => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_OFFER_WONDER", "1");
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const t = createTranslator(locale);
    const { el } = await modelPage({ locale, act: true });
    const box = composer(el);
    expect(box, "the composer").not.toBeNull();
    const field = fieldOf(ask(el), t("ask.intent"));
    expect(field.tagName).toBe("TEXTAREA");
    expect(box!.contains(field)).toBe(true);
    expect(field.getAttribute("placeholder")).toBe(t("ask.placeholder"));
    // No silent cut at a length: the limit is said when sending (item 2).
    expect(field.hasAttribute("maxlength")).toBe(false);
    // The chips: real controls, each with its visible words and its own name, inside the box's tool row.
    const tools = box!.querySelector<HTMLElement>("[data-tools]")!;
    const mode = trigger(tools, t("ask.mode"));
    expect(text(mode)).toBe(t("ask.mode.edit"));
    expect(text(trigger(tools, t("ask.model")))).toBe(t("ai.model.muse-spark-1.3"));
    expect(tools.querySelector("[data-model-choice]")).not.toBeNull();
    // A toggle chip shows the start (or, for acting, the end) of its option's name, which stays its whole name (WCAG 2.5.3).
    for (const [label, words] of [[t("investigation.act"), t("investigation.act.chip")], [t("ask.research"), t("ask.research.chip")]] as const) {
      const c = checkbox(tools, label);
      expect(c, label).not.toBeNull();
      expect(c!.checked, label).toBe(false);
      expect(text(c!.closest("label")), label).toBe(words);
      expect(label.toLowerCase(), label).toContain(words.toLowerCase());
    }
    // The send button: an icon, named "Send to the AI", the AI's accent, the box's last control.
    const send = button(box!, t("ask.send"));
    expect(text(send)).toBe("");
    expect(send.querySelector("svg")).not.toBeNull();
    expect(send.getAttribute("data-variant")).toBe("ai");
    const focusable = [...box!.querySelectorAll("button, input, textarea")].filter((x) => !(x instanceof HTMLInputElement && x.type === "hidden"));
    expect(focusable.at(-1)).toBe(send);
    expect(el.querySelectorAll('[data-variant="primary"]')).toHaveLength(1);
    // The model's hint is under the box, as the chip's description.
    const hint = ask(el).querySelector("[data-model-hint]")!;
    expect(text(hint)).toBe(t("ask.modelHint.muse-spark-1.3"));
    expect(box!.contains(hint)).toBe(false);
    expect(described(trigger(tools, t("ask.model")))).toContain(t("ask.modelHint.muse-spark-1.3"));
    expect(await violations()).toEqual([]);
  });

  it("says the version line and the data notice under the box, whatever is chosen", async () => {
    const { el } = await modelPage();
    const box = composer(el)!;
    const line = ask(el).querySelector("[data-version-line]")!;
    expect(text(line)).toBe(`${en("ask.intentHint", { version: "1" })} ${en("ask.modeHint.edit", { version: "2" })}`);
    const notice = [...ask(el).querySelectorAll<HTMLElement>("*")].find((x) => x.children.length === 0 && text(x) === en("ask.dataNotice"))!;
    expect(notice).toBeDefined();
    expect([follows(box, line), follows(box, notice), box.contains(line), box.contains(notice)]).toEqual([true, true, false, false]);
    // The request names both, and the keys, as its description.
    const field = fieldOf(ask(el), en("ask.intent"));
    expect(described(field)).toEqual([en("ask.intentHint", { version: "1" }), en("ask.dataNotice"), en("ask.keys")]);
    // The keys are said, not drawn as key caps.
    expect(ask(el).querySelector("kbd")).toBeNull();
    // The mode chip's description is its line.
    expect(described(trigger(box, en("ask.mode")))).toEqual([en("ask.modeHint.edit", { version: "2" })]);
  });

  it("explains research and acting under the box only while each is on, as the chip's description", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const { el } = await modelPage({ act: true });
    const research = () => checkbox(composer(el)!, en("ask.research"))!;
    const acting = () => checkbox(composer(el)!, en("investigation.act"))!;
    expect(text(ask(el))).not.toContain(en("ask.researchHint"));
    expect(text(ask(el))).not.toContain(en("ask.researchHint.photo"));
    expect(text(ask(el))).not.toContain(en("investigation.actHint.edit"));
    expect(described(research())).toEqual([]);
    expect(described(acting())).toEqual([]);
    await tick(research());
    expect(described(research())).toEqual([en("ask.researchHint"), en("ask.researchHint.photo")]);
    const hint = document.getElementById(research().getAttribute("aria-describedby")!.split(" ")[0]!)!;
    expect([composer(el)!.contains(hint), follows(composer(el)!, hint)]).toEqual([false, true]);
    await tick(acting());
    expect(described(acting())).toEqual([en("investigation.actHint.edit")]);
    // Acting: no photo, so the photo's line goes; research is for the AI's one change.
    expect(described(research())).toEqual([`${en("ask.researchHint")} ${en("investigation.research.edit")}`]);
    await tick(research());
    await tick(acting());
    expect(described(research())).toEqual([]);
    expect(described(acting())).toEqual([]);
    expect(text(ask(el))).not.toContain(en("ask.researchHint"));
    expect(await violations()).toEqual([]);
  });
});

describe("Enter to send (2026-11-02)", () => {
  it("sends on Enter, as the send button does", async () => {
    const { el, api, created } = await modelPage();
    const field = fieldOf(ask(el), en("ask.intent"));
    await type(field, "Burn-in batches of 48 boards");
    expect(await keyDown(field, { key: "Enter" })).toBe(true);
    await waitFor(() => created.length === 1, "the proposal");
    expect(creates(api).map((c) => c.body)).toEqual([{ base_version_id: api.baseVersionId, mode: "edit", intent: "Burn-in batches of 48 boards", output_locale: "en" }]);
  });

  it("leaves Shift+Enter to the text area (a new line) and sends nothing", async () => {
    const { el, api } = await modelPage();
    const field = fieldOf(ask(el), en("ask.intent"));
    await type(field, "Burn-in batches of 48 boards");
    expect(await keyDown(field, { key: "Enter", shiftKey: true })).toBe(false);
    await settle();
    expect(creates(api)).toEqual([]);
  });

  it("never sends on Enter while an input method is composing (isComposing, or keyCode 229)", async () => {
    const { el, api } = await modelPage({ locale: "zh-Hant" });
    const field = fieldOf(ask(el), zh("ask.intent"));
    await type(field, "ICT 測試機");
    expect(await keyDown(field, { key: "Enter", isComposing: true })).toBe(false);
    expect(await keyDown(field, { key: "Process", keyCode: 229 })).toBe(false);
    expect(await keyDown(field, { key: "Enter", keyCode: 229 })).toBe(false);
    await settle();
    expect(creates(api)).toEqual([]);
  });

  it("says why and sends nothing for an empty request, and keeps the focus on it", async () => {
    const { el, api } = await modelPage();
    const field = fieldOf(ask(el), en("ask.intent"));
    await type(field, "   ");
    expect(await keyDown(field, { key: "Enter" })).toBe(true);
    expect(text(ask(el))).toContain(en("ask.intentRequired"));
    expect(field.getAttribute("aria-invalid")).toBe("true");
    expect(document.activeElement).toBe(field);
    // The send button says the same, and moves the focus to the request.
    await press(button(composer(el)!, en("ask.send")));
    expect(document.activeElement).toBe(field);
    expect(creates(api)).toEqual([]);
  });

  it("says why and sends nothing for a request over 20,000 characters; one of 20,000 goes", async () => {
    const { el, api, created } = await modelPage();
    const field = fieldOf(ask(el), en("ask.intent"));
    await type(field, "a".repeat(20_001));
    expect(await keyDown(field, { key: "Enter" })).toBe(true);
    const said = en("ask.intentTooLong", { count: "20,001", max: "20,000" });
    expect(text(ask(el))).toContain(said);
    expect(described(field)).toContain(said);
    expect(field.getAttribute("aria-invalid")).toBe("true");
    expect(creates(api)).toEqual([]);
    await type(field, "a".repeat(20_000));
    expect(text(ask(el))).not.toContain(said);
    expect(await keyDown(field, { key: "Enter" })).toBe(true);
    await waitFor(() => created.length === 1, "the proposal");
    expect(creates(api)).toHaveLength(1);
  });

  it("does nothing on a second Enter while the first is sending", async () => {
    const { el, api, created } = await modelPage();
    const field = fieldOf(ask(el), en("ask.intent"));
    await type(field, "Burn-in batches of 48 boards");
    await act(async () => {
      for (let i = 0; i < 2; i++) field.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    });
    await waitFor(() => created.length === 1, "the proposal");
    await settle();
    expect(creates(api)).toHaveLength(1);
  });

  it("does not send on Enter while the editor has unsaved edits, and names the reason as the request's description", async () => {
    const { el, api } = await modelPage();
    const n = el.querySelector<HTMLElement>('.react-flow__node[data-id="smt"]')!;
    await act(async () => n.focus());
    await key(n, "Enter");
    await settle();
    const name = [...el.querySelectorAll("[data-panel=properties] label")].find((l) => text(l) === en("editor.field.name"));
    const input = document.getElementById((name as HTMLLabelElement).htmlFor) as HTMLInputElement;
    await act(async () => {
      input.focus();
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "SMT line 1");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await settle();
    await key(input, "Enter");
    await settle();
    const field = fieldOf(ask(el), en("ask.intent"));
    await type(field, "Burn-in batches of 48 boards");
    expect(described(field)).toContain(en("ask.saveFirst"));
    expect(await keyDown(field, { key: "Enter" })).toBe(true);
    await settle();
    expect(creates(api)).toEqual([]);
  });
});

describe("the request's state in the server's words (2026-11-02)", () => {
  const latest = { id: versionId(2), version: 2 };
  const p = (over: Partial<Proposal>): Proposal => ({ ...readyEdit(proposalId(1)), ...over });
  it.each([
    [{ status: "generating" }, "ask.log.state.generating"],
    [{ status: "needs_input" }, "ask.log.state.needs_input"],
    [{ status: "ready" }, "ask.log.state.ready"],
    [{ status: "invalid" }, "ask.log.state.invalid"],
    [{ status: "failed" }, "ask.log.state.failed"],
    [{ status: "rejected" }, "ask.log.state.rejected"],
    [{ status: "conflicted" }, "ask.log.state.conflicted"],
    [{ status: "applied" }, "ask.log.state.applied"],
  ] as const)("%o says %s", (over, key) => {
    expect(requestState({ kind: "proposal", proposal: p(over) }, latest)).toEqual({ key });
  });

  it("says which version an approved change became when the server's ID is the latest, else only that it became one", () => {
    expect(requestState({ kind: "proposal", proposal: p({ status: "approved", new_version_id: versionId(2) }) }, latest)).toEqual({ key: "ask.log.state.approved.version", version: "2" });
    expect(requestState({ kind: "proposal", proposal: p({ status: "approved", new_version_id: versionId(1) }) }, latest)).toEqual({ key: "ask.log.state.approved.edit" });
    expect(requestState({ kind: "proposal", proposal: { ...readyScenario(proposalId(2)), status: "approved" } }, latest)).toEqual({ key: "ask.log.state.approved.scenario" });
  });

  it.each(["running", "finished", "stopped"] as const)("an investigation that is %s says so", (status) => {
    const inv: Investigation = { ...sampleInvestigation("running"), status };
    expect(requestState({ kind: "investigation", investigation: inv }, latest)).toEqual({ key: `ask.log.state.investigation.${status}` });
  });

  it("words every state in both languages", () => {
    for (const k of ["generating", "needs_input", "ready", "invalid", "failed", "rejected", "conflicted", "applied", "approved.edit", "approved.scenario", "investigation.running", "investigation.finished", "investigation.stopped"] as const) {
      expect(en(`ask.log.state.${k}`), k).not.toBe("");
      expect(zh(`ask.log.state.${k}`), k).not.toBe(en(`ask.log.state.${k}`));
    }
    expect(en("ask.log.state.approved.version", { version: "4" })).toBe("Approved as version 4");
    expect(en("ask.log.state.ready")).toBe("Ready to review");
    expect(en("ask.log.state.needs_input")).toBe("The AI needs your input");
  });
});

describe("earlier requests above the composer (2026-11-02)", () => {
  it("shows no list when the model has none", async () => {
    const { el, api } = await modelPage();
    expect(lists(api)).toHaveLength(1);
    expect(earlier(el)).toBeNull();
    expect(text(ask(el))).not.toContain(en("ask.log.title"));
  });

  it.each(["en", "zh-Hant"] as const)("in %s, lists the latest three people's requests, newest nearest the composer, each a link to its page with its state and time", async (locale) => {
    const t = createTranslator(locale);
    const { el } = await modelPage({ locale, earlier: earlierRequests() });
    const list = earlier(el)!;
    expect(list).not.toBeNull();
    expect(text(list.querySelector("h3"))).toBe(t("ask.log.title"));
    expect(follows(list, composer(el)!)).toBe(true);
    // Oldest first: the newest stands right above the composer. The proposal an AI investigation asked for is not a person's request.
    const [a, b, c] = rows(el);
    expect(rows(el)).toHaveLength(3);
    expect(rows(el).map((r) => r.querySelector("a")!.getAttribute("href"))).toEqual([proposalId(93), proposalId(94), proposalId(95)].map((id) => `/proposals/${id}`));
    // The AI's summary names a proposal; without one, what it was asked to make.
    expect(text(a!.querySelector("[data-request-text]"))).toBe("The request does not say which step's batches should change, or to what size.");
    expect(text(b!.querySelector("[data-request-text]"))).toBe(t("ask.mode.scenario"));
    expect(text(c!.querySelector("[data-request-text]"))).toBe(t("ask.mode.edit"));
    expect(text(a!.querySelector("[data-request-state]"))).toBe(t("ask.log.state.needs_input"));
    expect(text(b!.querySelector("[data-request-state]"))).toBe(t("ask.log.state.failed"));
    expect(text(c!.querySelector("[data-request-state]"))).toBe(t("ask.log.state.generating"));
    // A photo request says so.
    expect(text(c)).toContain(t("ask.log.photo"));
    expect(text(a)).not.toContain(t("ask.log.photo"));
    // Its time, as the server gave it.
    expect(c!.querySelector("time")!.getAttribute("datetime")).toBe("2026-10-26T08:48:00Z");
    // The honest line, and that older ones are under AI proposals in the rail; no second link to that page.
    expect(text(list)).toContain(t("ask.log.note"));
    expect(text(list)).toContain(t("ask.log.older"));
    expect(ask(el).querySelector(`a[href="/models/${SAMPLE_MODEL_ID}/proposals"]`)).toBeNull();
    // Every row is a link of its own.
    const names = rows(el).map((r) => text(r.querySelector("a")));
    expect(new Set(names).size).toBe(names.length);
    expect(await violations()).toEqual([]);
  });

  it("names the AI's summary as data, never as markup", async () => {
    const hostile = { ...readyEdit(proposalId(96)), created_at: "2026-10-26T08:50:00Z", summary: '<img src=x onerror="alert(1)"> Approve now‮' };
    const { el } = await modelPage({ earlier: [hostile] });
    const row = rows(el)[0]!;
    expect(row.querySelector("img")).toBeNull();
    expect(text(row.querySelector("[data-request-text]"))).toBe('<img src=x onerror="alert(1)"> Approve now⟨U+202E⟩');
    expect(row.querySelector("[data-request-text] bdi")).not.toBeNull();
  });

  it("says when the AI's change became the latest version", async () => {
    const api = createProposalStandIn({ sample: "approved" });
    const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
    const el = await mount(
      <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
        <ApiProvider client={client}>
          <ModelView modelId={SAMPLE_MODEL_ID} onProposal={() => {}} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => earlier(el) !== null, "the earlier requests");
    expect(text(rows(el)[0]!.querySelector("[data-request-state]"))).toBe(en("ask.log.state.approved.version", { version: "2" }));
  });

  it("says a failed read with a retry, never blocks the composer, and reads again when asked", async () => {
    const { el, api, created } = await modelPage({ earlier: earlierRequests(), refuseList: { status: 503, code: "SERVICE_UNAVAILABLE", message: "down" } });
    const failed = ask(el).querySelector("[data-earlier-failed]")!;
    expect(text(failed)).toContain(en("ask.log.failed"));
    expect(rows(el)).toEqual([]);
    const field = fieldOf(ask(el), en("ask.intent"));
    await type(field, "Burn-in batches of 48 boards");
    await press(button(composer(el)!, en("ask.send")));
    await waitFor(() => created.length === 1, "the proposal");
    await press(button(failed, en("retry")));
    await waitFor(() => rows(el).length > 0, "the earlier requests");
    expect(ask(el).querySelector("[data-earlier-failed]")).toBeNull();
    expect(lists(api)).toHaveLength(2);
  });

  it("reads every page of the list to reach the newest", async () => {
    const many = Array.from({ length: 7 }, (_, i) => ({ ...readyEdit(proposalId(70 + i)), created_at: `2026-10-26T07:0${i}:00Z`, summary: `Request ${i + 1}` }));
    const { el, api } = await modelPage({ earlier: many, pageSize: 2 });
    expect(rows(el).map((r) => text(r.querySelector("[data-request-text]")))).toEqual(["Request 5", "Request 6", "Request 7"]);
    expect(lists(api)).toHaveLength(4);
  });

  it("with investigations, lists the ones this panel started, and a deployment without them changes nothing", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", "1");
    const fromPanel = sampleInvestigation("running");
    const fromAnalysis: Investigation = { ...sampleInvestigation("finished"), id: investigationId(3), source: "analysis", source_analysis_id: analysisId(1), created_at: "2026-11-01T07:00:00Z" };
    const { el } = await modelPage({ act: true, earlier: earlierRequests(), investigations: [fromAnalysis, fromPanel] });
    const last = rows(el).at(-1)!;
    expect(last.querySelector("a")!.getAttribute("href")).toBe(`/investigations/${fromPanel.id}`);
    expect(text(last.querySelector("[data-request-text]"))).toBe(en("ask.log.investigation.scenario"));
    expect(text(last.querySelector("[data-request-state]"))).toBe(en("ask.log.state.investigation.running"));
    expect(ask(el).querySelector(`a[href="/investigations/${fromAnalysis.id}"]`)).toBeNull();
    expect(await violations()).toEqual([]);

    const off = await modelPage({
      act: true, earlier: earlierRequests(),
      investigations: { status: 503, code: "SERVICE_UNAVAILABLE", message: "AI investigations are not available here", details: [{ path: "", reason: "investigations are off in this deployment", code: "INVESTIGATIONS_OFF" }] },
    });
    expect(off.el.querySelector("[data-earlier-failed]")).toBeNull();
    expect(rows(off.el).map((r) => r.querySelector("a")!.getAttribute("href"))).toEqual([proposalId(93), proposalId(94), proposalId(95)].map((id) => `/proposals/${id}`));
  });

  it("reads no investigations where the panel starts none", async () => {
    vi.stubEnv("NEXT_PUBLIC_GEMINUS_INVESTIGATIONS", undefined);
    const { api } = await modelPage({ act: true, earlier: earlierRequests() });
    expect(api.calls.filter((c) => c.path.endsWith("/investigations"))).toEqual([]);
  });
});
