// H-008 / ADR-021: every string a person sees or hears is the catalog's,
// data the page shows, or one of the React Aria strings the owner accepted
// in its own translation. The test lists every other string it finds.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "./fixtures";
import { sampleLine } from "../../web/app/ui-gallery/editor/sample-line";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { open, signedInViews, signInAs } from "./helpers";

type Catalog = Record<string, string | { one?: string; other: string }>;
const pattern = (text: string) => new RegExp("^" + text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/\\\{\w+\\\}/g, ".+") + "$", "s");
const catalogPatterns = (c: Catalog) => Object.values(c).flatMap((m) => (typeof m === "string" ? [m] : [m.one, m.other].filter((x): x is string => !!x))).map(pattern);

// The React Aria strings accepted in its own translation (H-008, H-009 item
// 2): sortable column, sorted ascending / descending, item selected, the
// Select popover's hidden dismiss button, number field; and the rest of the
// selection notices (H-010): item deselected, all selected, N selected.
const reactAria = {
  "zh-Hant": [/^可排序的欄$/, /^已依據「.+」欄遞增排序$/, /^已依據「.+」欄遞減排序$/, /^已選取「.+」。$/, /^關閉$/, /^數字欄位$/,
    /^未選取「.+」。$/, /^已選取所有項目。$/, /^已選取 \d+ 個項目。$/],
  en: [/^sortable column$/, /^sorted by column .+ in ascending order$/, /^sorted by column .+ in descending order$/, /^.+ selected\.$/, /^Dismiss$/, /^Number field$/,
    /^.+ not selected\.$/, /^All items selected\.$/, /^\d+ items? selected\.$/],
};
// The selection notices this test makes React Aria announce (H-010), so
// their acceptance is checked, not assumed.
const selectionNotices = {
  "zh-Hant": [/^未選取「.+」。$/, /^已選取所有項目。$/, /^已選取 \d+ 個項目。$/],
  en: [/^.+ not selected\.$/, /^All items selected\.$/, /^\d+ items? selected\.$/],
};

/** Text and the attributes people read or hear, outside data (<code>, lang="en" technical text). */
async function strings(page: import("@playwright/test").Page): Promise<string[]> {
  return page.evaluate(() => {
    const out = new Set<string>();
    const isData = (el: Element | null) => !!el?.closest("code, [lang=en]:not(html)");
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const text = n.textContent?.replace(/\s+/g, " ").trim() ?? "";
      if (text && /\p{L}/u.test(text) && !isData(n.parentElement) && !n.parentElement?.closest("script, style, template")) out.add(text);
    }
    for (const el of document.querySelectorAll("[aria-label], [aria-description], [aria-roledescription], [title], [placeholder], [aria-valuetext]")) {
      for (const a of ["aria-label", "aria-description", "aria-roledescription", "title", "placeholder", "aria-valuetext"]) {
        const v = el.getAttribute(a)?.trim();
        if (v && /\p{L}/u.test(v) && !isData(el)) out.add(v);
      }
    }
    return [...out];
  });
}

// The 2D editor (T-015): the model's names are data the page shows as they
// are, in either language; React Flow's attribution link ("React Flow",
// labelled "React Flow attribution") stays as the owner decided (ADR-021,
// H-009 (4)), the one string of the canvas no catalog can reach.
const sampleData = [sampleLine.name, ...sampleLine.nodes.map((n) => n.name ?? n.id), ...sampleLine.resources.map((r) => r.name), ...sampleLine.entity_types.map((e) => e.name), ...(sampleLine.calendars ?? []).map((c) => c.name ?? c.id), "X-ray inspection"];
const reactFlow = [/^React Flow$/, /^React Flow attribution$/];

// The signed-in views (T-043 follow-up): the API stand-in's data is the
// organizations', projects' and the model's names (fake/servers.mjs; the
// model is the contract fixture valid-draft-with-issues.json), shown as they
// are in either language.
const fixture = JSON.parse(readFileSync(join(import.meta.dirname, "../../../testdata/contracts/valid-draft-with-issues.json"), "utf8")) as {
  ir: { name: string; nodes: { id: string; name?: string }[]; resources: { name: string }[]; entity_types: { name: string }[]; calendars?: { id: string; name?: string }[] };
};
// The avatar shows the organization's initials when the fake gives no profile (H-042): "LW", "TB".
const tenantData = ["LW", "TB", "Line Works", "Test Bay", "SMT line 1", "Burn-in bay", fixture.ir.name, ...fixture.ir.nodes.map((n) => n.name ?? n.id), ...fixture.ir.resources.map((r) => r.name), ...fixture.ir.entity_types.map((e) => e.name), ...(fixture.ir.calendars ?? []).map((c) => c.name ?? c.id)];

for (const [locale, catalog] of [["en", en], ["zh-Hant", zhHant]] as const) {
  test(`${locale}: the signed-in views' strings are the catalog's, the tenant's data or an accepted library string`, async ({ page }) => {
    const known = [...catalogPatterns(catalog as Catalog), ...reactFlow];
    const data = new Set(tenantData);
    const seen = new Set<string>();
    for (const v of signedInViews) {
      await signInAs(page, v.subject, v.path, locale, { organization: v.organization });
      for (const s of await strings(page)) seen.add(s);
      await page.context().clearCookies();
    }
    const other = [...seen].filter((s) => !data.has(s) && !known.some((p) => p.test(s)));
    const sentences = other.flatMap((s) => s.split(/(?<=[。.])\s+/));
    const unexpected = sentences.filter((s) => !reactAria[locale].some((p) => p.test(s)));
    console.log(`${locale} signed in: strings not from the catalog or the data: ${JSON.stringify(other)}`);
    expect(unexpected).toEqual([]);
    for (const s of ["Line Works", "SMT line 1", fixture.ir.name]) expect(seen.has(s), `the views showed ${s}`).toBe(true);
  });

  test(`${locale}: the editor's strings are the catalog's, the model's data or React Flow's attribution`, async ({ page }) => {
    await open(page, "/ui-gallery/editor", locale);
    const known = [...catalogPatterns(catalog as Catalog), ...reactFlow];
    const data = new Set(sampleData);
    const seen = new Set(await strings(page));
    // A step selected (its properties, resources and changeovers), the step menu and a dialog.
    await page.locator('li button:has-text("Burn-in")').click();
    for (const s of await strings(page)) seen.add(s);
    // (SMT is off the canvas's view once it shows Burn-in: select it from the keyboard.)
    await page.locator(".react-flow__node[data-id=smt]").focus();
    await page.keyboard.press("Enter");
    await expect(page.locator("[data-panel=properties] h2")).toHaveText("SMT");
    for (const s of await strings(page)) seen.add(s);
    await page.getByRole("button", { name: locale === "en" ? "Add step" : "新增步驟" }).click();
    for (const s of await strings(page)) seen.add(s);
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: locale === "en" ? "Delete step" : "刪除步驟" }).click();
    for (const s of await strings(page)) seen.add(s);
    const other = [...seen].filter((s) => !data.has(s) && !known.some((p) => p.test(s)));
    const sentences = other.flatMap((s) => s.split(/(?<=[。.])\s+/));
    const unexpected = sentences.filter((s) => !reactAria[locale].some((p) => p.test(s)));
    console.log(`${locale} editor: strings not from the catalog or the model: ${JSON.stringify(other)}`);
    expect(unexpected).toEqual([]);
    expect([...seen].filter((s) => /^React Flow/.test(s)).sort(), "the attribution is there").toEqual(["React Flow", "React Flow attribution"]);
  });

  test(`${locale}: every string is the catalog's or an accepted React Aria string`, async ({ page }) => {
    await open(page, "/ui-gallery", locale);
    const known = catalogPatterns(catalog as Catalog);
    const seen = new Set(await strings(page));
    // States that put more strings on the page: a sorted and a selected
    // table (React Aria announces both), an open select and an open dialog.
    const grid = page.getByRole("grid").first();
    await grid.getByRole("columnheader").nth(1).click();
    await grid.getByRole("row").nth(1).getByRole("rowheader").click();
    await expect(grid.locator("[aria-selected=true]")).toHaveCount(1);
    for (const s of await strings(page)) seen.add(s);
    // Select all, then deselect two rows: all selected, the count of those
    // left, and one deselected.
    // (the native input sits under the drawn box, as React Aria builds it)
    await grid.getByRole("checkbox").first().focus();
    await page.keyboard.press("Space");
    await expect(grid.locator("[aria-selected=true]")).toHaveCount(5);
    for (const s of await strings(page)) seen.add(s);
    await grid.getByRole("row").nth(1).getByRole("rowheader").click();
    await expect(grid.locator("[aria-selected=true]")).toHaveCount(4);
    for (const s of await strings(page)) seen.add(s);
    // Leaving "all" announces only the count; deselecting from a set of
    // rows names the row.
    await grid.getByRole("row").nth(2).getByRole("rowheader").click();
    await expect(grid.locator("[aria-selected=true]")).toHaveCount(3);
    for (const s of await strings(page)) seen.add(s);
    await page.getByRole("button", { name: locale === "en" ? "Time unit" : "時間單位" }).click();
    for (const s of await strings(page)) seen.add(s);
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: locale === "en" ? "Discard changes" : "捨棄變更" }).click();
    for (const s of await strings(page)) seen.add(s);

    const fromLibrary = [...seen].filter((s) => !known.some((p) => p.test(s)));
    // React Aria's announcer joins the notices of one change ("… not
    // selected. 3 items selected."): each sentence must be accepted.
    const sentences = fromLibrary.flatMap((s) => s.split(/(?<=[。.])\s+/));
    const unexpected = sentences.filter((s) => !reactAria[locale].some((p) => p.test(s)));
    console.log(`${locale}: strings not from the catalog: ${JSON.stringify(fromLibrary)}`);
    expect(unexpected).toEqual([]);
    for (const p of selectionNotices[locale]) expect(sentences.some((s) => p.test(s)), `announced: ${p}`).toBe(true);
  });

  // H-048: the landing's every string, the rail's settings menu open too, signed out.
  test(`${locale}: the landing's strings are the catalog's`, async ({ page }) => {
    await open(page, "/", locale);
    const known = catalogPatterns(catalog as Catalog);
    const seen = new Set(await strings(page));
    await page.getByRole("button", { name: locale === "en" ? "Settings" : "設定" }).click();
    for (const s of await strings(page)) seen.add(s);
    const other = [...seen].filter((s) => !known.some((p) => p.test(s)) && !reactAria[locale].some((p) => p.test(s)));
    expect(other).toEqual([]);
    expect(seen.has(catalog["landing.title"])).toBe(true);
  });
}
