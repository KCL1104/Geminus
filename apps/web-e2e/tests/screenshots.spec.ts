// TEST-033: screenshots in en and zh-Hant at 390 and 1280 px, in the light
// and the dark theme (H-013; dark names end in -dark), compared with
// baselines a person accepted. Baselines are made in CI's pinned Playwright
// container; elsewhere rendering differs, so this runs only with
// E2E_SCREENSHOTS=1 and reports NOT RUN otherwise (README.md).
// Besides the signed-out pages: a ready proposal and the results (their
// review pages), and signed in as the editor, the projects, the canvas and
// a list of the model's (H-042), on the fake API's data as it starts.
import { existsSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "./fixtures";
import { locales, open, PROVIDER, schemes, signInAs, tenant } from "./helpers";

const enabled = process.env.E2E_SCREENSHOTS === "1";
// Before the first baselines are accepted there is nothing to compare with.
// Once there are, a missing one fails: a new screen needs a person's look.
const accepted = existsSync(join(import.meta.dirname, "__screenshots__", "screenshots.spec.ts"));
const shots = ["/", "/projects", "/no-such-page", "/ui-gallery", "/ui-gallery/proposal?state=scenario", "/ui-gallery/results"];
const signedIn = [["projects-signed-in", "/projects"], ["canvas", `/models/${tenant.MODEL}`], ["scenarios", `/models/${tenant.MODEL}/scenarios`]];
const file = (route: string) => (route === "/" ? "home" : route.slice(1).replace(/[/?=]/g, "-"));

for (const scheme of schemes) for (const locale of locales) {
  for (const width of [390, 1280]) {
    test.describe(`${scheme}, ${locale} at ${width}px`, () => {
      test.use({ viewport: { width, height: 900 }, colorScheme: scheme });
      for (const route of shots) {
        const name = `${file(route)}-${locale}-${width}${scheme === "dark" ? "-dark" : ""}.png`;
        test(`${route} looks as accepted`, async ({ page }) => {
          test.skip(!enabled, "NOT RUN: screenshots compare only in the pinned environment (E2E_SCREENSHOTS=1)");
          test.skip(!accepted && test.info().config.updateSnapshots === "none", "NOT RUN: no accepted baselines yet (README.md)");
          await open(page, route, locale);
          await expect(page).toHaveScreenshot(name, { fullPage: true });
        });
      }
      for (const [shot, path] of signedIn) {
        test(`${path}, signed in, looks as accepted`, async ({ page, request }) => {
          test.skip(!enabled, "NOT RUN: screenshots compare only in the pinned environment (E2E_SCREENSHOTS=1)");
          test.skip(!accepted && test.info().config.updateSnapshots === "none", "NOT RUN: no accepted baselines yet (README.md)");
          await request.post(`${PROVIDER}/__reset`);
          await signInAs(page, tenant.editor, path, locale);
          await expect(page).toHaveScreenshot(`${shot}-${locale}-${width}${scheme === "dark" ? "-dark" : ""}.png`, { fullPage: true });
        });
      }
    });
  }
}
