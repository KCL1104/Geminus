// @vitest-environment happy-dom
// T-031 (hackathon slice, H-029 item 4): on the model page, one way to ask
// the AI for a change in natural language (a text field, what to make, a
// send button), sent to createProposal on the model's latest version with
// the reader's language for the AI's prose; the API's answer is followed
// to the proposal page. Then the whole loop against one stand-in: the
// proposal is reviewed and approved, and the model page shows the new
// version by reading it again (the same entity IDs; no second model).
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { sampleLine, SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { createProposalStandIn, type ProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { ModelView } from "../features/model-editor/model-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider, type Role } from "../lib/auth/session";
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
const buttons = (el: ParentNode) => [...el.querySelectorAll<HTMLButtonElement>("button")];
const button = (el: ParentNode, name: string) => {
  const b = buttons(el).find((x) => (x.getAttribute("aria-label") ?? text(x)) === name);
  if (!b) throw new Error(`no button "${name}" among ${buttons(el).map((x) => text(x)).join(" | ")}`);
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
async function choose(el: Element, label: string, option: string) {
  const trigger = [...el.querySelectorAll("button")].find((b) => b.getAttribute("aria-labelledby")?.split(" ").some((id) => text(document.getElementById(id)) === label));
  if (!trigger) throw new Error(`no select ${label}`);
  await press(trigger);
  await settle();
  const o = [...document.querySelectorAll("[role=option]")].find((x) => text(x) === option);
  if (!o) throw new Error(`no option ${option}`);
  await press(o);
  await settle();
}
const creates = (api: ProposalStandIn) => api.calls.filter((c) => c.method === "POST" && c.path.endsWith("/ai-proposals"));

describe("asking the AI for a change on the model page", () => {
  it("offers a text field, what to make and a send button, and keeps Publish the one primary action", async () => {
    const { el } = await modelPage();
    const form = ask(el);
    expect(text(form.querySelector("h2"))).toBe(en("ask.title"));
    expect(fieldOf(form, en("ask.intent")).tagName).toBe("TEXTAREA");
    expect(text(form)).toContain(en("ask.mode.edit"));
    expect(text(form)).toContain(en("ask.modeHint.edit", { version: "2" }));
    const send = button(form, en("ask.send"));
    expect(send.getAttribute("data-variant")).toBe("secondary");
    expect(el.querySelectorAll('[data-variant="primary"]')).toHaveLength(1);
    expect(await violations()).toEqual([]);
  });

  it("says what is missing and sends nothing when the request is empty", async () => {
    const { el, api, created } = await modelPage();
    await type(fieldOf(ask(el), en("ask.intent")), "   ");
    await press(button(ask(el), en("ask.send")));
    expect(text(ask(el))).toContain(en("ask.intentRequired"));
    expect(fieldOf(ask(el), en("ask.intent")).getAttribute("aria-invalid")).toBe("true");
    expect(creates(api)).toEqual([]);
    expect(created).toEqual([]);
  });

  it("sends the words, on the latest version, in the reader's language, with a key, and follows the answer", async () => {
    const { el, api, created } = await modelPage();
    await type(fieldOf(ask(el), en("ask.intent")), "  Burn-in batches of 48 boards  ");
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 1, "the proposal");
    const [call] = creates(api);
    expect(call!.path).toBe(`/api/v1/models/${SAMPLE_MODEL_ID}/ai-proposals`);
    expect(call!.body).toEqual({ base_version_id: api.baseVersionId, mode: "edit", intent: "Burn-in batches of 48 boards", output_locale: "en" });
    expect(call!.key).toMatch(/^[\x21-\x7e]{8,128}$/);
    expect(created).toEqual([api.proposals()[0]!.proposal_id]);
  });

  it("asks for a scenario when the person chooses one, in Traditional Chinese for a zh-Hant reader", async () => {
    const { el, api, created } = await modelPage({ locale: "zh-Hant" });
    await choose(ask(el), zh("ask.mode"), zh("ask.mode.scenario"));
    expect(text(ask(el))).toContain(zh("ask.modeHint.scenario", { version: "1" }));
    await type(fieldOf(ask(el), zh("ask.intent")), "ICT 測試機增加到 3 台");
    await press(button(ask(el), zh("ask.send")));
    await waitFor(() => created.length === 1, "the proposal");
    expect(creates(api)[0]!.body).toEqual({ base_version_id: api.baseVersionId, mode: "scenario", intent: "ICT 測試機增加到 3 台", output_locale: "zh-Hant" });
    expect(await violations()).toEqual([]);
  });

  it("shows the API's refusal when AI is off, keeps the words, and retries the same request with the same key", async () => {
    const { el, api, created } = await modelPage({ refuseNext: { status: 503, code: "AI_PROVIDER_NOT_CONFIGURED", message: "AI is off" } });
    await type(fieldOf(ask(el), en("ask.intent")), "Burn-in batches of 48 boards");
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => el.querySelector("[data-ask] [role=alert]") !== null, "the problem");
    expect(text(ask(el).querySelector("[role=alert]"))).toContain(en("error.AI_PROVIDER_NOT_CONFIGURED"));
    expect(fieldOf(ask(el), en("ask.intent")).value).toBe("Burn-in batches of 48 boards");
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 1, "the proposal");
    const keys = creates(api).map((c) => c.key);
    expect(keys).toHaveLength(2);
    expect(keys[0]).toBe(keys[1]);
  });

  it("on a 409 (a newer version since the page loaded) keeps the words, reloads only when asked, then asks on the new latest", async () => {
    const api = createProposalStandIn({ sample: null });
    const { el, created } = await modelPage({ api });
    api.publishOther();
    await type(fieldOf(ask(el), en("ask.intent")), "Burn-in batches of 48 boards");
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => el.querySelector("[data-ask] [role=alert]") !== null, "the conflict");
    expect(text(ask(el).querySelector("[role=alert]"))).toContain(en("error.MODEL_VERSION_CONFLICT"));
    expect(created).toEqual([]);
    expect(text(el)).toContain(en("model.summary", { version: "1", domain: "electronics_line" }));
    await press(button(ask(el), en("ask.reload")));
    await waitFor(() => text(el).includes(en("model.summary", { version: "2", domain: "electronics_line" })), "the reload");
    expect(fieldOf(ask(el), en("ask.intent")).value).toBe("Burn-in batches of 48 boards");
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 1, "the proposal");
    const [first, second] = creates(api);
    expect(second!.body).toEqual({ ...(first!.body as object), base_version_id: api.versions()[1]!.id });
    expect(second!.key).not.toBe(first!.key);
  });

  it("blocks sending while the editor has unsaved edits, and says why beside it", async () => {
    const { el, api } = await modelPage();
    const n = el.querySelector<HTMLElement>('.react-flow__node[data-id="smt"]')!;
    await act(async () => n.focus());
    await key(n, "Enter");
    await settle();
    const name = fieldOf(el.querySelector("[data-panel=properties]")!, en("editor.field.name")) as HTMLInputElement;
    await type(name, "SMT line 1");
    await key(name, "Enter");
    await settle();
    await type(fieldOf(ask(el), en("ask.intent")), "Burn-in batches of 48 boards");
    const send = button(ask(el), en("ask.send"));
    expect(send.getAttribute("aria-disabled")).toBe("true");
    expect(text(document.getElementById(send.getAttribute("aria-describedby") ?? ""))).toBe(en("ask.saveFirst"));
    await press(send);
    expect(creates(api)).toEqual([]);
  });

  it("is not offered to a viewer or while the role is unknown", async () => {
    for (const role of ["viewer", null] as const) {
      const { el } = await modelPage({ role });
      expect(el.querySelector("[data-ask]")).toBeNull();
    }
  });
});

describe("the loop: ask, review, approve, and the model page reads the new version", () => {
  it("shows the approved version in the editor with the same step IDs", async () => {
    const api = createProposalStandIn({ sample: null, generatingPolls: 1 });
    const first = await modelPage({ api });
    const ids = [...first.el.querySelectorAll(".react-flow__node")].map((n) => n.getAttribute("data-id")).sort();
    await type(fieldOf(ask(first.el), en("ask.intent")), "Burn-in batches of 48 boards");
    await press(button(ask(first.el), en("ask.send")));
    await waitFor(() => first.created.length === 1, "the proposal");

    // The proposal page, as the route renders it.
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
    const approve = en("proposal.approve.edit", { version: "2" });
    await press(button(review, approve));
    await press(button(review, approve));
    await waitFor(() => text(review).includes(en("proposal.outcome.edit", { version: "2" })), "the outcome");

    // Back on the model page: read again, version 2, the burn-in step's batch as approved.
    unmountAll();
    const again = await modelPage({ api });
    expect(text(again.el)).toContain(en("model.summary", { version: "2", domain: "electronics_line" }));
    expect([...again.el.querySelectorAll(".react-flow__node")].map((n) => n.getAttribute("data-id")).sort()).toEqual(ids);
    expect(ids).toEqual(sampleLine.nodes.map((n) => n.id).sort());
    const n = again.el.querySelector<HTMLElement>('.react-flow__node[data-id="burn_in"]')!;
    await act(async () => n.focus());
    await key(n, "Enter");
    await settle();
    const panel = again.el.querySelector("[data-panel=properties]")!;
    expect((fieldOf(panel, en("editor.batch.min_size")) as HTMLInputElement).value).toBe("48");
    expect((fieldOf(panel, en("editor.batch.max_size")) as HTMLInputElement).value).toBe("48");
    // Everything the page knows came from the API's answers.
    expect(api.calls.filter((c) => c.method === "GET" && c.path === `/api/v1/models/${SAMPLE_MODEL_ID}`).length).toBeGreaterThanOrEqual(2);
  });
});
