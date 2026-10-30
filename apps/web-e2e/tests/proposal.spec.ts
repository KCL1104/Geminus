// T-031 (hackathon slice): the AI proposal review in a real browser. On its
// review page (app/ui-gallery/proposal: the real page and client against an
// in-page stand-in, no AI, every state and the hostile cases): the decision
// with the keyboard only, a visible focus at each stop and the outcome
// announced and focused; a reason for a rejection; a base that moved keeps
// the page; a proposal being written becomes ready; the AI's hostile words
// and names stay text, isolated, nothing hidden; the model page's request
// goes to the proposal; every string is the catalog's or data. And the
// signed-in demo path (review round 1, L5): through the BFF and the test
// provider, against the e2e fake API's proposal routes (fake/servers.mjs,
// a fixed answer, no AI): ask on the model page, review, approve the edit
// with the keyboard and see the new version, in both languages; reject.
// axe, the phone width and the simplicity rules run on the samples with
// every route (a11y.spec.ts, simplicity.spec.ts).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { APIRequestContext, Locator, Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { sampleLine } from "../../web/app/ui-gallery/editor/sample-line";
import { HOSTILE_TESTER, hostileNames, sampleFor, states } from "../../web/app/ui-gallery/proposal/samples";
import { visible } from "../../web/features/ai-proposals/shown";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { APP, focusIsVisible, locales, open, PROVIDER, settled, signInAs, tenant, violations } from "./helpers";

const catalogs = { en, "zh-Hant": zhHant } as const;
const fill = (m: string, params: Record<string, string>) => m.replace(/\{(\w+)\}/g, (_, k: string) => params[k] ?? `{${k}}`);

/** Focuses the control with the keyboard's Tab from the element before it, so focus-visible applies as for a keyboard user. */
async function tabTo(page: Page, name: string) {
  for (let i = 0; i < 80; i++) {
    await page.keyboard.press("Tab");
    const here = await page.evaluate(() => (document.activeElement?.textContent ?? "").trim());
    if (here === name) return;
  }
  throw new Error(`Tab never reached ${name}`);
}

/** The model page's header band (H-040): its latest version and domain. */
async function expectBand(page: Page, version: string, domain: string) {
  const band = page.locator("main header:has(h1) dl");
  await expect(band.locator("dd data").first()).toHaveAttribute("value", version);
  await expect(band).toContainText(domain);
}

test("approves an edit with the keyboard only: confirm in place, then the outcome, focused and announced", async ({ page }) => {
  await open(page, "/ui-gallery/proposal", "en");
  const approve = fill(en["proposal.approve.edit"], { version: "2" });
  await tabTo(page, approve);
  expect(await focusIsVisible(page)).toBe(true);
  await page.keyboard.press("Enter");
  await expect(page.getByText(fill(en["proposal.approve.question.edit"], { version: "2", base: "1" }))).toBeVisible();
  // Focus moved to the committing action of the confirmation, worded apart from Approve (H-039).
  await expect(page.getByRole("button", { name: fill(en["proposal.approve.confirm.edit"], { version: "2" }) })).toBeFocused();
  expect(await focusIsVisible(page)).toBe(true);
  expect(await violations(page), "the confirmation").toEqual([]);
  await page.keyboard.press("Enter");
  const outcome = page.locator("[data-outcome=approved]");
  await expect(outcome).toBeVisible();
  await expect(outcome).toBeFocused();
  await expect(outcome.getByRole("heading")).toHaveText(fill(en["proposal.outcome.edit"], { version: "2" }));
  await expect(page.locator("[data-proposal-status]")).toHaveText(pattern(en["proposal.status.approved"]));
  await expect(page.getByRole("button", { name: approve })).toHaveCount(0);
  expect(await violations(page), "the outcome").toEqual([]);
});

test("approves a scenario as its own action, and offers the scenario to run", async ({ page }) => {
  await open(page, "/ui-gallery/proposal?state=scenario", "en");
  const approve = en["proposal.approve.scenario"];
  await page.getByRole("button", { name: approve }).click();
  await page.getByRole("button", { name: en["proposal.approve.confirm.scenario"] }).click();
  const outcome = page.locator("[data-outcome=approved]");
  await expect(outcome.getByRole("heading")).toHaveText(en["proposal.outcome.scenario"]);
  // The review page's scenario link goes to the results review page; the product's goes to /scenarios/<id>.
  await expect(outcome.getByRole("link", { name: en["proposal.outcome.openScenario"] })).toHaveAttribute("href", "/ui-gallery/results");
});

for (const locale of locales) {
  test(`${locale}: rejects with a reason typed in place, and shows it with the outcome`, async ({ page }) => {
    const t = catalogs[locale];
    await open(page, "/ui-gallery/proposal", locale);
    await tabTo(page, t["proposal.reject"]);
    await page.keyboard.press("Enter");
    await expect(page.getByRole("button", { name: t["proposal.reject.confirm"] })).toBeFocused();
    await page.getByLabel(t["proposal.reject.reason"]).fill("Batches stay between 20 and 40 until the new racks arrive.");
    expect(await violations(page), "the confirmation").toEqual([]);
    await page.getByRole("button", { name: t["proposal.reject.confirm"] }).press("Enter");
    const outcome = page.locator("[data-outcome=rejected]");
    await expect(outcome).toBeFocused();
    await expect(outcome).toContainText(t["proposal.outcome.rejected"]);
    await expect(outcome).toContainText("Batches stay between 20 and 40 until the new racks arrive.");
  });
}

test("keeps the page when the model changed just before the approval, and says what changed", async ({ page }) => {
  await open(page, "/ui-gallery/proposal?state=conflict", "en");
  const approve = fill(en["proposal.approve.edit"], { version: "2" });
  await page.getByRole("button", { name: approve }).click();
  await page.getByRole("button", { name: fill(en["proposal.approve.confirm.edit"], { version: "2" }) }).click();
  const note = page.locator("[data-conflict]");
  await expect(note).toBeFocused();
  await expect(note).toContainText(fill(en["proposal.conflict.body"], { base: "1", latest: "2" }));
  await expect(note).toContainText("Raised the AOI buffer to 30 boards.");
  await expect(page.locator("[data-proposal-status]")).toHaveText(en["proposal.status.conflicted"]);
  // The diff the person reviewed is still there; nothing was asked again.
  await expect(page.locator("[data-section=changes] [data-change]")).toHaveCount(2);
  await expect(page.getByRole("button", { name: approve })).toHaveCount(0);
  expect(await violations(page)).toEqual([]);
});

test("follows a proposal being written until it is ready", async ({ page }) => {
  await open(page, "/ui-gallery/proposal?state=generating", "en");
  await expect(page.locator("[data-proposal-status]")).toHaveText(en["proposal.status.generating"]);
  expect(await violations(page)).toEqual([]);
  await expect(page.locator("[data-proposal-status]")).toHaveText(en["proposal.status.ready"], { timeout: 15_000 });
  await expect(page.locator("[data-section=changes] [data-change]")).toHaveCount(2);
});

test("asks from the model page and lands on the proposal it created", async ({ page }) => {
  await open(page, "/ui-gallery/editor?state=clean", "en");
  const ask = page.locator("[data-ask]");
  await ask.getByLabel(en["ask.intent"]).fill("Burn-in batches of 48 boards");
  expect(await violations(page), "the request").toEqual([]);
  await ask.getByRole("button", { name: en["ask.send"] }).click();
  await page.waitForURL("**/ui-gallery/proposal?state=generating");
  await expect(page.locator("[data-proposal-status]")).toHaveText(en["proposal.status.ready"], { timeout: 15_000 });
});

test("shows hostile words from the AI as text: no markup, no link, no decision", async ({ page }) => {
  await open(page, "/ui-gallery/proposal?state=hostile", "en");
  const p = sampleFor("hostile").proposal;
  const article = page.locator("article");
  // The header band (H-040) holds the title, the way back and the status: it is checked as the article is.
  const band = page.locator("main header:has(h1)");
  for (const words of [p.summary!, ...p.assumptions!, ...p.expected_effects!]) await expect(article).toContainText(words);
  for (const scope of [article, band]) await expect(scope.locator("img, script, iframe, object, embed")).toHaveCount(0);
  // No link in the header or the article (H-042: the rail is the way back to the model).
  expect(await band.locator("a").evaluateAll((as) => as.map((a) => a.getAttribute("href")))).toEqual([]);
  await expect(article.locator("a")).toHaveCount(0);
  await expect(band.locator("[data-proposal-status]")).toHaveText(en["proposal.status.ready"]);
  await expect(article.locator("[data-cell=server]")).toContainText(en["proposal.risk.level.high"]);
  // The names in its patch (review round 1, M1, L1): text, each in its own
  // <bdi>, with what hides or reorders text shown; no raw format or control
  // character anywhere in the proposal; the look-alike tester with its ID.
  const changes = article.locator("[data-section=changes]");
  const isolated = await changes.locator("bdi").evaluateAll((bs) => bs.map((b) => b.textContent));
  for (const name of Object.values(hostileNames)) expect(isolated, name).toContain(visible(name));
  expect(isolated).toContain(HOSTILE_TESTER);
  for (const scope of [article, band]) expect(await scope.evaluate((a) => /[\p{Cf}\p{Cs}\p{Zl}\p{Zp}\u0000-\u0008\u000B-\u001F\u007F-\u009F]/u.test(a.textContent ?? "")), "no raw hidden character").toBe(false);
  expect(await violations(page)).toEqual([]);
});

test.describe("at 390px", () => {
  test.use({ viewport: { width: 390, height: 900 } });
  test("the exact JSON Patch opens from the keyboard and wraps inside the page", async ({ page }) => {
    await open(page, "/ui-gallery/proposal?state=new-steps", "en");
    const summary = page.locator("summary", { hasText: en["proposal.changes.patch"] });
    await summary.focus();
    expect(await focusIsVisible(page)).toBe(true);
    await page.keyboard.press("Enter");
    await expect(page.locator("[data-section=changes] pre")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    expect(await violations(page)).toEqual([]);
  });
});

// H-008 / REQ-022, as strings.spec.ts does: every string is the catalog's,
// or data the page shows (the sample line's names, the sample proposals'
// words, a person's comments, a version's comment).
type Catalog = Record<string, string | { one?: string; other: string }>;
const pattern = (text: string) => new RegExp("^" + text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/\\\{\w+\\\}/g, ".+") + "$", "s");
const words = states.flatMap((s) => {
  const p = sampleFor(s).proposal;
  return [p.summary, ...(p.assumptions ?? []), ...(p.expected_effects ?? []), ...(p.questions ?? []), p.decision_comment];
});
// The names the samples' patches write, as shown (shown.ts), and the IDs
// shown beside a name: of what a patch adds, and of the sample line's own
// entities (an existing one whose name a new one shares).
const patchNames = Object.values(hostileNames).map((n) => visible(n));
const addedIds = states.flatMap((s) => (sampleFor(s).proposal.changes ?? []).flatMap((op) => (op.op === "add" && typeof (op.value as { id?: unknown })?.id === "string" ? [(op.value as { id: string }).id] : [])));
const lineIds = [...sampleLine.nodes, ...sampleLine.resources, ...sampleLine.entity_types, ...(sampleLine.calendars ?? []), ...sampleLine.flows].map((e) => e.id);
const data = [sampleLine.name, ...sampleLine.nodes.map((n) => n.name ?? n.id), ...sampleLine.resources.map((r) => r.name), "X-ray inspection", "Before X-ray", "X-ray machine", "Raised the AOI buffer to 30 boards.", ...words, ...patchNames, ...addedIds, ...lineIds].filter((x): x is string => typeof x === "string");

for (const locale of locales) {
  test(`${locale}: every string on the proposal pages is the catalog's or data`, async ({ page }) => {
    const known = [...Object.values(catalogs[locale] as Catalog).flatMap((m) => (typeof m === "string" ? [m] : [m.one, m.other].filter((x): x is string => !!x))).map(pattern), ...data.map(pattern)];
    const unknown = new Set<string>();
    for (const state of states) {
      await open(page, `/ui-gallery/proposal?state=${state}`, locale);
      if (state === "generating") await expect(page.locator("[data-proposal-status]")).toHaveAttribute("data-proposal-status", "ready", { timeout: 15_000 });
      for (const summary of await page.locator("main summary").all()) await summary.click();
      const seen = await page.evaluate(() => {
        const out: [string, string][] = [];
        const norm = (s: string | null | undefined) => (s ?? "").replace(/\s+/g, " ").trim();
        const isData = (el: Element | null) => !!el?.closest("code, pre, data, time, [lang=en]:not(html)");
        const walker = document.createTreeWalker(document.querySelector("main")!, NodeFilter.SHOW_TEXT);
        for (let n = walker.nextNode(); n; n = walker.nextNode()) {
          const text = norm(n.textContent);
          if (text && /\p{L}/u.test(text) && !isData(n.parentElement)) out.push([text, norm(n.parentElement?.textContent)]);
        }
        return out;
      });
      for (const [text, whole] of seen) if (!known.some((re) => re.test(text) || re.test(whole))) unknown.add(text);
    }
    console.log(`${locale} proposal pages: strings not from the catalog or data: ${JSON.stringify([...unknown])}`);
    expect([...unknown]).toEqual([]);
  });
}

// The signed-in demo path (H-029 item 4; T-031 review round 1, L5): the
// product's routes /models/<id> and /proposals/<id> in the browser, signed
// in through the BFF and the test provider as an editor of Line Works,
// against the fake API's proposal routes (fake/servers.mjs). Its answer is
// fixed (fake/proposal-answer.json): no AI is called, and the fake does not
// do the server's review. The fake's tenant data is put back before and
// after each test (POST /__reset), so the other specs see version 1.
type ApiCall = { method: string; path: string; org: string | null; cookie: string | null; hasAuth: boolean; key?: string | null; body?: unknown };
const apiCalls = async (request: APIRequestContext): Promise<ApiCall[]> => ((await (await request.get(`${PROVIDER}/__state`)).json()) as { apiCalls: ApiCall[] }).apiCalls;
const fixedAnswer = JSON.parse(readFileSync(join(import.meta.dirname, "../fake/proposal-answer.json"), "utf8")) as Record<"en" | "zh-Hant", { summary: string; assumptions: string[] }>;
const fixture = JSON.parse(readFileSync(join(import.meta.dirname, "../../../testdata/contracts/valid-draft-with-issues.json"), "utf8")) as { base_version_id: string; revision: number; ir: { domain: string } };
// The model page names the domain as its pack's catalog does (H-039).
const domainIn = (catalog: Record<string, unknown>) => catalog[`domain.${fixture.ir.domain}.name`] as string;

/** Moves focus with Tab (or Shift+Tab) until target has it, the focus visible at every stop. */
async function keyTo(page: Page, target: Locator, key: "Tab" | "Shift+Tab" = "Tab") {
  for (let i = 0; i < 60; i++) {
    await page.keyboard.press(key);
    if (await target.evaluate((el) => el === document.activeElement)) {
      expect(await focusIsVisible(page), "focus is visible").toBe(true);
      return;
    }
  }
  throw new Error(`${key} never reached ${await target.evaluate((el) => el.outerHTML.slice(0, 120))}`);
}

test.describe("signed in, through the BFF, on the fake API's proposals", () => {
  test.use({ viewport: { width: 1280, height: 900 } });
  test.beforeEach(async ({ request }) => {
    await request.post(`${PROVIDER}/__reset`);
    await request.post(`${PROVIDER}/__access-seconds`, { data: "600" });
  });
  test.afterEach(async ({ request }) => {
    await request.post(`${PROVIDER}/__reset`);
  });

  for (const locale of locales) {
    test(`${locale}: asks on the model page, reviews the proposal, approves the edit with the keyboard and sees version 2`, async ({ page, request }) => {
      const t = catalogs[locale];
      await signInAs(page, tenant.editor, `/models/${tenant.MODEL}`, locale);
      await expectBand(page, "1", domainIn(t));
      expect(await violations(page), "the model page").toEqual([]);
      const before = (await apiCalls(request)).length;

      // The request: the person's words and Send, from the keyboard.
      const ask = page.locator("[data-ask]");
      await page.locator("main").focus();
      await keyTo(page, ask.getByLabel(t["ask.intent"]));
      await page.keyboard.type("Add a third reviewer");
      await keyTo(page, ask.getByRole("button", { name: t["ask.send"] }));
      await page.keyboard.press("Enter");
      await page.waitForURL(new RegExp(`^${APP}/proposals/aip_[0-9A-Z]{26}$`));
      const proposalId = new URL(page.url()).pathname.split("/").at(-1)!;

      // The proposal, read until the fake's answer is there: what it changes, the AI's words, the risk.
      const status = page.locator("[data-proposal-status]");
      await expect(status).toHaveAttribute("data-proposal-status", "ready", { timeout: 15_000 });
      await settled(page, locale);
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(fill(t["proposal.title.edit"], { model: tenant.modelName }));
      await expect(page.locator("[data-section=changes] [data-change]")).toHaveText([`Reviewer ${fill(t["proposal.change.value"], { field: t["proposal.field.capacity"], before: "2", after: "3" })}`]);
      await expect(page.locator("[data-section=summary]")).toContainText(fixedAnswer[locale].summary);
      await expect(page.locator("[data-section=assumptions] li")).toHaveText(fixedAnswer[locale].assumptions);
      await expect(page.locator("[data-cell=server]")).toContainText(t["proposal.risk.level.low"]);
      expect(await violations(page), "the proposal").toEqual([]);

      // Approve, confirmed in place, from the keyboard.
      const approve = page.getByRole("button", { name: fill(t["proposal.approve.edit"], { version: "2" }) });
      await keyTo(page, approve);
      await page.keyboard.press("Enter");
      await expect(page.getByText(fill(t["proposal.approve.question.edit"], { version: "2", base: "1" }))).toBeVisible();
      await expect(page.getByRole("button", { name: fill(t["proposal.approve.confirm.edit"], { version: "2" }) })).toBeFocused();
      expect(await focusIsVisible(page)).toBe(true);
      expect(await violations(page), "the confirmation").toEqual([]);
      await page.keyboard.press("Enter");
      const outcome = page.locator("[data-outcome=approved]");
      await expect(outcome).toBeFocused();
      await expect(outcome.getByRole("heading")).toHaveText(fill(t["proposal.outcome.edit"], { version: "2" }));
      await expect(status).toHaveText(pattern(t["proposal.status.approved"]));
      await expect(approve).toHaveCount(0);
      expect(await violations(page), "the outcome").toEqual([]);

      // What reached the API, through the BFF: the request on version 1 and one approval of the reviewed base, each with its key, in Line Works.
      const posts = (await apiCalls(request)).slice(before).filter((c) => c.method === "POST");
      expect(posts.map((c) => [c.path, c.body])).toEqual([
        [`/v1/models/${tenant.MODEL}/ai-proposals`, { base_version_id: fixture.base_version_id, mode: "edit", intent: "Add a third reviewer", output_locale: locale }],
        [`/v1/ai-proposals/${proposalId}/approve`, { expected_base_version_id: fixture.base_version_id }],
      ]);
      for (const c of posts) expect(c, c.path).toMatchObject({ org: tenant.LINE, cookie: null, hasAuth: true, key: expect.stringMatching(/^[\x21-\x7e]{8,128}$/) });

      // Back to the model by the rail's Canvas (H-042), from the keyboard: version 2 is the latest, and the draft moved onto it.
      await keyTo(page, page.getByRole("link", { name: t["rail.canvas"] }), "Shift+Tab");
      await page.keyboard.press("Enter");
      await page.waitForURL(`${APP}/models/${tenant.MODEL}`);
      await settled(page, locale);
      await expectBand(page, "2", domainIn(t));
      await expect(page.locator("[data-draft-status]")).toContainText(fill(t["draft.revision"], { revision: String(fixture.revision + 1) }));
      expect(await violations(page), "the model on version 2").toEqual([]);
    });
  }

  // H-046 (2): the opt-in to research on the web, from the keyboard, and the
  // proposal's sources and searches as the fake answers a researched request
  // (fake/proposal-answer.json "research": fixed test pages, nothing searched).
  test("en: asks with web research on, and the proposal lists the pages the AI read and its searches", async ({ page, request }) => {
    const research = (JSON.parse(readFileSync(join(import.meta.dirname, "../fake/proposal-answer.json"), "utf8")) as { research: { sources: { url: string; title: string }[] } }).research;
    await signInAs(page, tenant.editor, `/models/${tenant.MODEL}`, "en");
    const before = (await apiCalls(request)).length;
    const ask = page.locator("[data-ask]");
    const box = ask.getByRole("checkbox", { name: en["ask.research"] });
    await expect(box).not.toBeChecked();
    await expect(box).toHaveAccessibleDescription(en["ask.researchHint"]);
    await ask.getByLabel(en["ask.intent"]).fill("Add a third reviewer");
    await keyTo(page, box);
    await page.keyboard.press("Space");
    await expect(box).toBeChecked();
    expect(await violations(page), "the model page, research ticked").toEqual([]);
    await keyTo(page, ask.getByRole("button", { name: en["ask.send"] }));
    await page.keyboard.press("Enter");
    await page.waitForURL(new RegExp(`^${APP}/proposals/aip_[0-9A-Z]{26}$`));
    await expect(page.locator("[data-proposal-status]")).toHaveAttribute("data-proposal-status", "ready", { timeout: 15_000 });
    await settled(page, "en");

    // Who wrote it and how many searches it made, then the pages it read: links to a new tab, without referrer or opener.
    await expect(page.locator("header dl > div").filter({ hasText: en["ai.writtenBy"] })).toHaveText(`${en["ai.writtenBy"]}${fill(en["ai.webSearch"].other, { writer: "Muse Spark 1.3 (Meta)", count: "3" })}`);
    const sources = page.locator("[data-section=sources]");
    await expect(sources.getByRole("heading", { level: 2 })).toHaveText(en["proposal.sources"]);
    const links = sources.getByRole("link");
    await expect(links).toHaveCount(research.sources.length);
    for (const [i, s] of research.sources.entries()) {
      const a = links.nth(i);
      await expect(a).toHaveAttribute("href", s.url);
      await expect(a).toHaveAttribute("target", "_blank");
      await expect(a).toHaveAttribute("rel", "noopener noreferrer nofollow");
      await expect(a).toHaveAccessibleName(`${s.title} ${en["proposal.sources.newTab"]}`);
    }
    await expect(sources.locator("li > bdi")).toHaveText(research.sources.map((s) => new URL(s.url).hostname));
    await expect(sources).toContainText(en["proposal.sources.dropped"].one);
    await keyTo(page, links.first());
    expect(await violations(page), "the researched proposal").toEqual([]);

    // What reached the API: research: true, nothing else new.
    const posts = (await apiCalls(request)).slice(before).filter((c) => c.method === "POST");
    expect(posts.map((c) => [c.path, c.body])).toEqual([
      [`/v1/models/${tenant.MODEL}/ai-proposals`, { base_version_id: fixture.base_version_id, mode: "edit", intent: "Add a third reviewer", output_locale: "en", research: true }],
    ]);

    // At 390 px the long titles and addresses wrap inside the page.
    await page.setViewportSize({ width: 390, height: 844 });
    await settled(page, "en");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), "no horizontal scroll at 390 px").toBe(true);
    expect(await violations(page), "the researched proposal at 390 px").toEqual([]);
  });

  // H-049, H-051: this build is production's, with Muse Wonder 1.0 not on
  // offer (NEXT_PUBLIC_GEMINUS_OFFER_WONDER empty, playwright.config.ts): the
  // model page has no model choice, the request carries no model, and the
  // proposal names the deployment's model (the fake API answers as Muse Spark
  // 1.3 wrote it; no AI ran). Choosing Muse Wonder is the unit tests'
  // (proposal-model.test.tsx), with the flag on.
  test("en: offers no model choice, and the proposal it sends carries no model and names Muse Spark 1.3", async ({ page, request }) => {
    await signInAs(page, tenant.editor, `/models/${tenant.MODEL}`, "en");
    const before = (await apiCalls(request)).length;
    const ask = page.locator("[data-ask]");
    await expect(ask.getByLabel(en["ask.intent"])).toBeVisible();
    await expect(page.locator("[data-model-choice]")).toHaveCount(0);
    await expect(ask).not.toContainText(en["ai.model.muse-wonder-1.0"]);
    await expect(ask).not.toContainText(en["ask.modelHint.muse-spark-1.3"]);
    expect(await violations(page), "the model page").toEqual([]);
    await ask.getByLabel(en["ask.intent"]).fill("Add a third reviewer");
    await keyTo(page, ask.getByRole("button", { name: en["ask.send"] }));
    await page.keyboard.press("Enter");
    await page.waitForURL(new RegExp(`^${APP}/proposals/aip_[0-9A-Z]{26}$`));
    await expect(page.locator("[data-proposal-status]")).toHaveAttribute("data-proposal-status", "ready", { timeout: 15_000 });
    await settled(page, "en");
    await expect(page.locator("header dl > div").filter({ hasText: en["ai.writtenBy"] })).toHaveText(`${en["ai.writtenBy"]}${fill(en["ai.byProvider"], { model: en["ai.model.muse-spark-1.3"], provider: en["ai.provider.meta"] })}`);
    expect(await violations(page), "the proposal").toEqual([]);

    // What reached the API: no model, nothing else new.
    const posts = (await apiCalls(request)).slice(before).filter((c) => c.method === "POST");
    expect(posts.map((c) => [c.path, c.body])).toEqual([
      [`/v1/models/${tenant.MODEL}/ai-proposals`, { base_version_id: fixture.base_version_id, mode: "edit", intent: "Add a third reviewer", output_locale: "en" }],
    ]);
  });

  test("en: rejects with a reason through the BFF, and the model stays on version 1", async ({ page, request }) => {
    await signInAs(page, tenant.editor, `/models/${tenant.MODEL}`, "en");
    const before = (await apiCalls(request)).length;
    const ask = page.locator("[data-ask]");
    await ask.getByLabel(en["ask.intent"]).fill("Add a third reviewer");
    await ask.getByRole("button", { name: en["ask.send"] }).click();
    await page.waitForURL(/\/proposals\/aip_/);
    await expect(page.locator("[data-proposal-status]")).toHaveAttribute("data-proposal-status", "ready", { timeout: 15_000 });
    await page.getByRole("button", { name: en["proposal.reject"] }).click();
    await page.getByLabel(en["proposal.reject.reason"]).fill("Not this quarter.");
    await page.getByRole("button", { name: en["proposal.reject.confirm"] }).press("Enter");
    const outcome = page.locator("[data-outcome=rejected]");
    await expect(outcome).toBeFocused();
    await expect(outcome).toContainText("Not this quarter.");
    expect(await violations(page)).toEqual([]);
    const posts = (await apiCalls(request)).slice(before).filter((c) => c.method === "POST");
    expect(posts.map((c) => c.path.replace(/aip_\w+/, "<id>"))).toEqual([`/v1/models/${tenant.MODEL}/ai-proposals`, "/v1/ai-proposals/<id>/reject"]);
    expect(posts[1]!.body).toEqual({ comment: "Not this quarter." });
    await page.getByRole("link", { name: en["rail.canvas"] }).click();
    await settled(page, "en");
    await expectBand(page, "1", domainIn(en));
  });
});
