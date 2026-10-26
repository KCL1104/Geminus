// TEST-033: no axe violation (WCAG 2.0–2.2 A/AA, contrast included) on any
// route or state, in either language, at phone and desktop width, in the
// light and the dark theme (the system's, H-013); no horizontal page scroll
// at 390 px; no table text painted over the next cell or cut off. Also the
// reader's own choice of theme over the opposite system theme (T-045 review
// 1 I5), on every route.
import { expect, test } from "@playwright/test";
import { cellOverflow, locales, open, routes, schemes, sheet, signedInViews, signInAs, violations } from "./helpers";

for (const scheme of schemes) for (const locale of locales) {
  for (const width of [390, 1280]) {
    test.describe(`${scheme}, ${locale} at ${width}px`, () => {
      test.use({ viewport: { width, height: 900 }, colorScheme: scheme });

      for (const route of routes) {
        test(`${route} has no violation`, async ({ page }) => {
          await open(page, route, locale);
          // The theme really is the one under test, not a fallback.
          await expect(page.locator("body")).toHaveCSS("background-color", sheet[scheme]);
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

// The signed-in views (T-043 follow-up): every page with the API stand-in's
// data and the bootstrap's own views (the choice of organization, no
// access), signed in through the BFF and the test provider.
for (const scheme of schemes) for (const locale of locales) {
  for (const width of [390, 1280]) {
    test.describe(`signed in, ${scheme}, ${locale} at ${width}px`, () => {
      test.use({ viewport: { width, height: 900 }, colorScheme: scheme });

      for (const v of signedInViews) {
        test(`${v.name} has no violation`, async ({ page }) => {
          await signInAs(page, v.subject, v.path, locale, { organization: v.organization });
          await expect(page.getByRole("button", { name: locale === "en" ? "Sign out" : "登出" }), "signed in").toBeVisible();
          await expect(page.locator("body")).toHaveCSS("background-color", sheet[scheme]);
          expect(await violations(page)).toEqual([]);
          const scroll = await page.evaluate(() => document.documentElement.scrollWidth);
          expect(scroll, "page-level horizontal scroll").toBeLessThanOrEqual(width);
          expect(await cellOverflow(page), "table cells narrower than their text").toEqual({ overlaps: [], clipped: [] });
        });
      }
    });
  }
}

for (const [chosen, system] of [["dark", "light"], ["light", "dark"]] as const) {
  test.describe(`${chosen} chosen on a ${system} system, en at 1280px`, () => {
    test.use({ viewport: { width: 1280, height: 900 }, colorScheme: system });

    for (const route of routes) {
      test(`${route} has no violation`, async ({ page }) => {
        await open(page, route, "en", chosen);
        await expect(page.locator("html")).toHaveAttribute("data-theme", chosen);
        await expect(page.locator("body")).toHaveCSS("background-color", sheet[chosen]);
        expect(await violations(page)).toEqual([]);
      });
    }
  });
}
