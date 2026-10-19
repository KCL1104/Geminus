// TEST-033: no axe violation (WCAG 2.0–2.2 A/AA, contrast included) on any
// route or state, in either language, at phone and desktop width; no
// horizontal page scroll at 390 px; no table text painted over the next
// cell or cut off.
import { expect, test } from "@playwright/test";
import { cellOverflow, locales, open, routes, violations } from "./helpers";

for (const locale of locales) {
  for (const width of [390, 1280]) {
    test.describe(`${locale} at ${width}px`, () => {
      test.use({ viewport: { width, height: 900 } });

      for (const route of routes) {
        test(`${route} has no violation`, async ({ page }) => {
          await open(page, route, locale);
          expect(await violations(page)).toEqual([]);
          const scroll = await page.evaluate(() => document.documentElement.scrollWidth);
          expect(scroll, "page-level horizontal scroll").toBeLessThanOrEqual(width);
          expect(await cellOverflow(page), "table cells narrower than their text").toEqual({ overlaps: [], clipped: [] });
        });
      }

      test("gallery states that need an action have no violation", async ({ page }) => {
        await open(page, "/ui-gallery", locale);
        await page.getByRole("button", { name: locale === "en" ? "Save draft" : "儲存草稿" }).click();
        await expect(page.getByRole("status").first()).not.toBeEmpty();
        expect(await violations(page), "status message").toEqual([]);
        await page.getByRole("button", { name: locale === "en" ? "Time unit" : "時間單位" }).click();
        await expect(page.getByRole("listbox")).toBeVisible();
        expect(await violations(page), "select open").toEqual([]);
        await page.keyboard.press("Escape");
        await page.getByRole("button", { name: locale === "en" ? "Discard changes" : "捨棄變更" }).click();
        await expect(page.getByRole("alertdialog")).toBeVisible();
        expect(await violations(page), "dialog open").toEqual([]);
      });
    });
  }
}
