// H-061 A8 review round 1: the AI investigation page in a real browser, on
// its review page (app/ui-gallery/investigation: the real page and client
// against an in-page stand-in, no AI). At 390 px the candidate table lays
// each row out as a grid; its column heads stay on screen in the same grid
// (as the results' metrics table, H-039) and in the accessibility tree, so
// no figure is shown or announced without saying whose it is: the
// baseline's, the what-if's, the change, or the proposal's state. axe, the
// phone width and the simplicity rules run on these pages with every route
// (a11y.spec.ts, simplicity.spec.ts).
import type { Locator } from "@playwright/test";
import { expect, test } from "./fixtures";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { locales, open, violations } from "./helpers";

const catalogs = { en, "zh-Hant": zhHant } as const;
const heads = ["investigation.col.whatIf", "investigation.col.proposal", "investigation.col.baseline", "investigation.col.scenario", "investigation.col.change"] as const;

/** Each element's left edge and width, rounded to the pixel. */
const boxes = (l: Locator) => l.evaluateAll((els) => els.map((e) => ({ x: Math.round(e.getBoundingClientRect().left), w: Math.round(e.getBoundingClientRect().width) })));

for (const width of [390, 1280]) {
  test.describe(`at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    for (const locale of locales) {
      for (const state of ["finished", "refused"] as const) {
        test(`${locale}, ${state}: every figure of the candidates sits under its column's head, on screen and in the accessibility tree`, async ({ page }) => {
          const t = catalogs[locale];
          await open(page, `/ui-gallery/investigation?state=${state}`, locale);
          const table = page.locator("[data-candidates]");
          await expect(table).toBeVisible();
          // The heads are the table's column headers in the accessibility tree, and are on screen.
          for (const k of heads) await expect(table.getByRole("columnheader", { name: t[k], exact: true })).toBeVisible();
          const snapshot = await table.ariaSnapshot();
          expect(snapshot.match(/- columnheader /g) ?? [], snapshot).toHaveLength(heads.length);
          expect(snapshot).toContain("- rowheader ");
          // Each cell of a row lies in its head's grid column (or, wide, its table column): same left edge, same width.
          const head = await boxes(table.locator("thead th"));
          const rows = table.locator("tbody tr");
          for (let r = 0; r < (await rows.count()); r++) {
            const cells = await boxes(rows.nth(r).locator("th, td"));
            // The row header, the proposal's state, then the figures (three cells), or one cell where there is none yet.
            const columns = cells.length === 5 ? [0, 1, 2, 3, 4] : [0, 1, 2];
            cells.forEach((c, i) => {
              const h = head[columns[i]!]!;
              expect(c.x, `row ${r} cell ${i}: left edge under its head`).toBe(h.x);
              if (cells.length === 5 || i < 2) expect(c.w, `row ${r} cell ${i}: as wide as its head`).toBe(h.w);
            });
          }
          if (width === 390) {
            // The heads take the phone's grid: What-if and Proposal each a line, the two runs side by side, Change a line.
            expect(head[2]!.x).toBeLessThan(head[3]!.x);
            expect(head[0]!.w).toBe(head[1]!.w);
            expect(head[4]!.w).toBe(head[0]!.w);
            expect(await page.evaluate(() => document.documentElement.scrollWidth), "no horizontal page scroll").toBeLessThanOrEqual(width);
          }
          expect(await violations(page)).toEqual([]);
        });
      }
    }
  });
}
