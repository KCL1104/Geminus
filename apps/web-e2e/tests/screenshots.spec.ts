// TEST-033: screenshots in en and zh-Hant at 390 and 1280 px, compared with
// baselines a person accepted. Baselines are made in CI's pinned Playwright
// container; elsewhere fonts differ, so this runs only with
// E2E_SCREENSHOTS=1 and reports NOT RUN otherwise (README.md).
import { existsSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { locales, open } from "./helpers";

const enabled = process.env.E2E_SCREENSHOTS === "1";
// Before the first baselines are accepted there is nothing to compare with.
// Once there are, a missing one fails: a new screen needs a person's look.
const accepted = existsSync(join(import.meta.dirname, "__screenshots__", "screenshots.spec.ts"));
const shots = ["/", "/projects", "/no-such-page", "/ui-gallery"];

for (const locale of locales) {
  for (const width of [390, 1280]) {
    test.describe(`${locale} at ${width}px`, () => {
      test.use({ viewport: { width, height: 900 } });
      for (const route of shots) {
        const name = `${route === "/" ? "home" : route.slice(1)}-${locale}-${width}.png`;
        test(`${route} looks as accepted`, async ({ page }) => {
          test.skip(!enabled, "NOT RUN: screenshots compare only in the pinned environment (E2E_SCREENSHOTS=1)");
          test.skip(!accepted && test.info().config.updateSnapshots === "none", "NOT RUN: no accepted baselines yet (README.md)");
          await open(page, route, locale);
          await expect(page).toHaveScreenshot(name, { fullPage: true });
        });
      }
    });
  }
}
