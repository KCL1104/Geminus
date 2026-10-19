// H-008 / ADR-021: every string a person sees or hears is the catalog's,
// data the page shows, or one of the React Aria strings the owner accepted
// in its own translation. The test lists every other string it finds.
import { expect, test } from "@playwright/test";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { open } from "./helpers";

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

for (const [locale, catalog] of [["en", en], ["zh-Hant", zhHant]] as const) {
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
}
