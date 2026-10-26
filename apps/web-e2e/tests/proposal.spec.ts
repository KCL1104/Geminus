// T-031 (hackathon slice): the AI proposal review in a real browser, on its
// review page (the app mounts no session yet, so the signed-in page cannot
// render here; app/ui-gallery/proposal: the real page and client against an
// in-page stand-in, no AI). The decision with the keyboard only, a visible
// focus at each stop and the outcome announced and focused; a reason for a
// rejection; a base that moved keeps the page; a proposal being written
// becomes ready; the AI's hostile words stay text; the model page's request
// goes to the proposal; every string is the catalog's or data. axe, the
// phone width and the simplicity rules run on the samples with every route
// (a11y.spec.ts, simplicity.spec.ts).
import { expect, type Page, test } from "@playwright/test";
import { sampleLine } from "../../web/app/ui-gallery/editor/sample-line";
import { sampleFor, states } from "../../web/app/ui-gallery/proposal/samples";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { focusIsVisible, locales, open, violations } from "./helpers";

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

test("approves an edit with the keyboard only: confirm in place, then the outcome, focused and announced", async ({ page }) => {
  await open(page, "/ui-gallery/proposal", "en");
  const approve = fill(en["proposal.approve.edit"], { version: "2" });
  await tabTo(page, approve);
  expect(await focusIsVisible(page)).toBe(true);
  await page.keyboard.press("Enter");
  await expect(page.getByText(fill(en["proposal.approve.question.edit"], { version: "2", base: "1" }))).toBeVisible();
  // Focus moved to the committing action of the confirmation.
  await expect(page.getByRole("button", { name: approve })).toBeFocused();
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
  await page.getByRole("button", { name: approve }).click();
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
  await page.getByRole("button", { name: approve }).click();
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
  for (const words of [p.summary!, ...p.assumptions!, ...p.expected_effects!]) await expect(article).toContainText(words);
  await expect(article.locator("img, script, iframe, object, embed")).toHaveCount(0);
  // The page's own link only: back to the model.
  expect(await article.locator("a").evaluateAll((as) => as.map((a) => a.getAttribute("href")))).toEqual(["/ui-gallery/editor"]);
  await expect(article.locator("[data-proposal-status]")).toHaveText(en["proposal.status.ready"]);
  await expect(article.locator("[data-cell=server]")).toContainText(en["proposal.risk.level.high"]);
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
const data = [sampleLine.name, ...sampleLine.nodes.map((n) => n.name ?? n.id), ...sampleLine.resources.map((r) => r.name), "X-ray inspection", "Before X-ray", "X-ray machine", "Raised the AOI buffer to 30 boards.", ...words].filter((x): x is string => typeof x === "string");

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
