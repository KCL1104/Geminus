// @vitest-environment happy-dom
// H-046 (2): a proposal the AI may research on the web first (Meta's search
// grounding). On the model page, one opt-in, off by default and asked per
// request, whose line says the description is used for a web search; only a
// ticked box sends research: true, and a deployment that cannot research is
// refused with its own advice. On the proposal page, the pages the AI read
// as links to a new tab with no referrer or opener, their titles as text with
// what they hide made visible, their sites by host, nothing but https; how
// many named pages were left out; and the Written-by line says how many
// searches the call made. The stand-in (app/ui-gallery/proposal/stand-in.ts)
// answers a researched request with the fixed sources in samples.ts; no AI is
// called and nothing here is the server's filtering.
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { researched, SAMPLE_PROPOSAL_ID } from "../app/ui-gallery/proposal/samples";
import { createProposalStandIn, type ProposalStandIn, type ProposalStandInOptions } from "../app/ui-gallery/proposal/stand-in";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { shownSources } from "../features/ai-proposals/sources";
import { ModelView } from "../features/model-editor/model-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { Proposal } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { mount, press, settle, unmountAll, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });

const en = createTranslator("en");
const zh = createTranslator("zh-Hant");

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
/** The opt-in's native checkbox, inside its label (React Aria). */
const optIn = (el: Element, label: string) => {
  const l = [...el.querySelectorAll("label")].find((x) => text(x) === label);
  const input = l?.querySelector<HTMLInputElement>("input[type=checkbox]");
  if (!input) throw new Error(`no checkbox ${label}`);
  return input;
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
const tick = async (input: HTMLInputElement) => {
  await act(async () => input.click());
  await settle();
};
const creates = (api: ProposalStandIn) => api.calls.filter((c) => c.method === "POST" && c.path.endsWith("/ai-proposals"));

async function modelPage(opts: Omit<ProposalStandInOptions, "sample"> & { locale?: Locale; api?: ProposalStandIn } = {}) {
  const api = opts.api ?? createProposalStandIn({ sample: null, ...opts });
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

async function proposalPage(api: ProposalStandIn, proposalId: string, locale: Locale = "en") {
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
      <ApiProvider client={client}>
        <ProposalRoute proposalId={proposalId} pollMs={25} />
      </ApiProvider>
    </AuthProvider>,
    locale,
  );
  await settle();
  await waitFor(() => el.querySelector("h1, [role=alert]") !== null, "the proposal page");
  return el;
}

/** The ready sample with these fields of a researched answer. */
const open = (proposal: Partial<Proposal>, locale: Locale = "en") => {
  const api = createProposalStandIn({ sample: "ready", proposal });
  return proposalPage(api, api.proposalId ?? SAMPLE_PROPOSAL_ID, locale);
};
const sources = (el: Element) => el.querySelector('[data-section="sources"]');
/** The header's Written-by fact: label and value. */
const writtenBy = (el: Element, label: string) => [...el.querySelectorAll("header dl > div")].map((d) => text(d)).find((d) => d.startsWith(label));

describe("the opt-in on the model page", () => {
  it.each(["en", "zh-Hant"] as const)("is off by default, says in %s that the description is used for a web search, and names that line as its description", async (locale) => {
    const t = createTranslator(locale);
    const { el } = await modelPage({ locale });
    const box = optIn(ask(el), t("ask.research"));
    expect(box.checked).toBe(false);
    const described = (box.getAttribute("aria-describedby") ?? "").split(" ").filter(Boolean);
    expect(described.map((id) => text(document.getElementById(id)))).toEqual([t("ask.researchHint")]);
    expect(await violations()).toEqual([]);
  });

  it("sends no research member unless ticked, research: true when ticked, and takes a new key for the other request", async () => {
    const { el, api, created } = await modelPage();
    await type(fieldOf(ask(el), en("ask.intent")), "A shoe factory's stitching line");
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 1, "the first proposal");
    expect(creates(api)[0]!.body).toEqual({ base_version_id: api.baseVersionId, mode: "edit", intent: "A shoe factory's stitching line", output_locale: "en" });
    expect(Object.hasOwn(creates(api)[0]!.body as object, "research")).toBe(false);

    await tick(optIn(ask(el), en("ask.research")));
    expect(optIn(ask(el), en("ask.research")).checked).toBe(true);
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 2, "the researched proposal");
    const [first, second] = creates(api);
    expect(second!.body).toEqual({ base_version_id: api.baseVersionId, mode: "edit", intent: "A shoe factory's stitching line", output_locale: "en", research: true });
    expect(second!.key).not.toBe(first!.key);
  });

  it("says to send without web research when this deployment cannot research, and keeps the words", async () => {
    const { el, api } = await modelPage({
      refuseNext: { status: 503, code: "AI_PROVIDER_NOT_CONFIGURED", message: "web research is not available with this deployment's provider", details: [{ path: "", reason: "this deployment's provider cannot search the web", code: "AI_RESEARCH_UNSUPPORTED" }] },
    });
    await type(fieldOf(ask(el), en("ask.intent")), "A carton plant's corrugator");
    await tick(optIn(ask(el), en("ask.research")));
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => el.querySelector("[data-ask] [role=alert]") !== null, "the refusal");
    const alert = ask(el).querySelector("[role=alert]")!;
    expect(text(alert)).toContain(en("ask.research.unavailable"));
    expect(text(alert)).not.toContain(en("error.AI_PROVIDER_NOT_CONFIGURED"));
    expect(fieldOf(ask(el), en("ask.intent")).value).toBe("A carton plant's corrugator");
    expect((creates(api)[0]!.body as { research?: boolean }).research).toBe(true);
    expect(await violations()).toEqual([]);
  });

  it("keeps the usual advice for another refusal while ticked", async () => {
    const { el } = await modelPage({ refuseNext: { status: 503, code: "AI_POLICY_NOT_APPROVED", message: "live AI is off" } });
    await type(fieldOf(ask(el), en("ask.intent")), "A food plant's packing line");
    await tick(optIn(ask(el), en("ask.research")));
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => el.querySelector("[data-ask] [role=alert]") !== null, "the refusal");
    expect(text(ask(el).querySelector("[role=alert]"))).toContain(en("error.AI_POLICY_NOT_APPROVED"));
    expect(text(ask(el).querySelector("[role=alert]"))).not.toContain(en("ask.research.unavailable"));
  });
});

describe("a researched proposal's page", () => {
  it("lists the pages the AI read as links to a new tab, without referrer or opener, with their sites", async () => {
    const el = await open(researched);
    const s = sources(el)!;
    expect(text(s.querySelector("h2"))).toBe(en("proposal.sources"));
    expect(text(s)).toContain(en("proposal.sourcesHint"));
    const links = [...s.querySelectorAll("a")];
    expect(links.map((a) => a.getAttribute("href"))).toEqual(["https://example.com/burn-in/batch-sizing", "https://docs.example.com/smt/line-balancing"]);
    for (const a of links) {
      expect(a.getAttribute("target")).toBe("_blank");
      expect(a.getAttribute("rel")).toBe("noopener noreferrer nofollow");
      // The link's name says where it goes and that it opens a new tab; the icon is not read.
      expect(text(a)).toMatch(new RegExp(`\\(test page\\) ${en("proposal.sources.newTab").replace(/[()]/g, "\\$&")}$`));
      expect(a.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
    }
    expect([...s.querySelectorAll("li")].map((li) => li.querySelector(":scope > bdi")?.textContent)).toEqual(["example.com", "docs.example.com"]);
    expect(text(s)).toContain(en("proposal.sources.dropped", { count: 1 }));
    expect(await violations()).toEqual([]);
  });

  it.each([
    [0, "en", en("ai.webSearch.none", { writer: "Muse Spark 1.3 (Meta)" })],
    [1, "en", en("ai.webSearch", { writer: "Muse Spark 1.3 (Meta)", count: 1 })],
    [4, "en", en("ai.webSearch", { writer: "Muse Spark 1.3 (Meta)", count: 4 })],
    [0, "zh-Hant", zh("ai.webSearch.none", { writer: zh("ai.byProvider", { model: "Muse Spark 1.3", provider: "Meta" }) })],
    [4, "zh-Hant", zh("ai.webSearch", { writer: zh("ai.byProvider", { model: "Muse Spark 1.3", provider: "Meta" }), count: 4 })],
  ] as const)("says the call searched the web: %i queries in %s", async (queries, locale, want) => {
    const t = createTranslator(locale);
    const el = await open({ ...researched, generated_by: { provider: "meta", model: "muse-spark-1.3", web_search: { queries, sources_dropped: 0 } } }, locale);
    expect(writtenBy(el, t("ai.writtenBy"))).toBe(`${t("ai.writtenBy")}${want}`);
    unmountAll();
  });

  it("reads the copy the owner's way in English first", () => {
    expect(en("ai.webSearch", { writer: "Muse Spark 1.3 (Meta)", count: 4 })).toBe("Muse Spark 1.3 (Meta), with web search: 4 queries");
    expect(en("ai.webSearch", { writer: "Muse Spark 1.3 (Meta)", count: 1 })).toBe("Muse Spark 1.3 (Meta), with web search: 1 query");
    expect(en("ai.webSearch.none", { writer: "Muse Spark 1.3 (Meta)" })).toBe("Muse Spark 1.3 (Meta), with web search on: no searches made");
    expect(en("ask.research")).toBe("Research real-world data on the web first (Meta search)");
  });

  it("keeps the Written-by line as it was for a proposal not researched, and shows no sources", async () => {
    const el = await open({ generated_by: { provider: "meta", model: "muse-spark-1.3" } });
    expect(writtenBy(el, en("ai.writtenBy"))).toBe(`${en("ai.writtenBy")}Muse Spark 1.3 (Meta)`);
    expect(sources(el)).toBeNull();
  });

  it("shows a title's hidden characters, never as markup, and the URL when there is no title", async () => {
    const el = await open({
      ...researched,
      sources: [
        { url: "https://example.com/a", title: "Reflow‮enif si‬ <b>profile</b>​" },
        { url: "https://example.com/b" },
      ],
    });
    const links = [...sources(el)!.querySelectorAll("a")];
    expect(links[0]!.querySelector("bdi")!.textContent).toBe("Reflow⟨U+202E⟩enif si⟨U+202C⟩ <b>profile</b>⟨U+200B⟩");
    expect(sources(el)!.querySelectorAll("b, script, img")).toHaveLength(0);
    expect(links[1]!.querySelector("bdi")!.textContent).toBe("https://example.com/b");
    const walker = document.createTreeWalker(sources(el)!, NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) expect(n.textContent, "no raw format character").not.toMatch(/[\p{Cf}]/u);
  });

  it("makes no link of anything but an https URL, even if one reached it", async () => {
    const el = await open({
      ...researched,
      sources: [
        { url: "javascript:alert(1)", title: "Run me" },
        { url: "http://example.com/plain", title: "Plain http" },
        { url: "https://user:pw@example.com/x", title: "With a password" },
        { url: "not a url", title: "Nothing" },
        { url: "https://xn--80ak6aa92e.com/page", title: "A look-alike host" },
      ],
    });
    const links = [...sources(el)!.querySelectorAll("a")];
    expect(links.map((a) => a.getAttribute("href"))).toEqual(["https://xn--80ak6aa92e.com/page"]);
    expect(text(sources(el))).not.toMatch(/Run me|Plain http|With a password|Nothing/);
    expect(shownSources([{ url: "https://例子.example/页" }])[0]!.site).toBe("xn--fsqu00a.example");
    // One link per address, as the server keeps them.
    expect(shownSources([{ url: "https://example.com/a" }, { url: "https://example.com/a", title: "again" }]).map((s) => s.text)).toEqual(["https://example.com/a"]);
  });

  it("says when every page the AI named was left out, and shows nothing when none was named", async () => {
    let el = await open({ ...researched, sources: undefined, generated_by: { provider: "meta", model: "muse-spark-1.3", web_search: { queries: 2, sources_dropped: 3 } } });
    expect(sources(el)!.querySelectorAll("a")).toHaveLength(0);
    expect(text(sources(el))).toContain(en("proposal.sources.allDropped", { count: 3 }));
    unmountAll();
    el = await open({ ...researched, sources: undefined, generated_by: { provider: "meta", model: "muse-spark-1.3", web_search: { queries: 2, sources_dropped: 0 } } });
    expect(sources(el)).toBeNull();
  });

  it("speaks Traditional Chinese", async () => {
    const el = await open(researched, "zh-Hant");
    const s = sources(el)!;
    expect(text(s.querySelector("h2"))).toBe(zh("proposal.sources"));
    expect(text(s)).toContain(zh("proposal.sources.dropped", { count: 1 }));
    expect(text(s.querySelector("a"))).toContain(zh("proposal.sources.newTab"));
    expect(await violations()).toEqual([]);
  });
});

describe("the loop with research: ask with the opt-in, then read the pages on the proposal", () => {
  it("follows a researched request to its proposal, which lists the sources once ready", async () => {
    const api = createProposalStandIn({ sample: null, generatingPolls: 1 });
    const { el, created } = await modelPage({ api });
    await type(fieldOf(ask(el), en("ask.intent")), "Burn-in batches of 48 boards");
    await tick(optIn(ask(el), en("ask.research")));
    await press(button(ask(el), en("ask.send")));
    await waitFor(() => created.length === 1, "the proposal");
    unmountAll();
    const review = await proposalPage(api, created[0]!);
    await waitFor(() => text(review.querySelector("[data-proposal-status]")) === en("proposal.status.ready"), "ready");
    expect([...sources(review)!.querySelectorAll("a")].map((a) => a.getAttribute("href"))).toEqual(researched.sources!.map((s) => s.url));
    expect(writtenBy(review, en("ai.writtenBy"))).toBe(`${en("ai.writtenBy")}${en("ai.webSearch", { writer: "Muse Spark 1.3 (Meta)", count: 3 })}`);
  });
});
