// Local preview screenshots of the T-031 proposal review (NOT baselines): the
// review page /ui-gallery/proposal and the model page's request on the
// editor preview, on a local `next start` at :3210 (GEMINUS_UI_GALLERY=1),
// en and zh-Hant, light and dark, 1280 and 390 px; plus the outcome of an
// approval and the page after a 409. Each shot records the page's
// scrollWidth (no horizontal scroll when it equals the viewport width).
// Usage: node shots.mjs <out dir> <path to apps/web-e2e/node_modules/playwright/index.mjs>
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const [out, playwright] = process.argv.slice(2);
const { chromium } = await import(playwright);
mkdirSync(out, { recursive: true });
const origin = "http://127.0.0.1:3210";
const browser = await chromium.launch();
const lines = [];

async function shot(name, path, { scheme, locale, width, act }) {
  const ctx = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: scheme, locale: "en-US", timezoneId: "UTC" });
  await ctx.addCookies([{ name: "geminus_locale", value: locale, url: origin }]);
  const page = await ctx.newPage();
  await page.goto(origin + path);
  await page.waitForLoadState("networkidle");
  await page.waitForSelector("[data-proposal-status], [data-ask]", { timeout: 15000 });
  if (act) await act(page);
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  const file = `${name}-${locale}-${width}${scheme === "dark" ? "-dark" : ""}.png`;
  await page.screenshot({ path: join(out, file), fullPage: true });
  lines.push(`${file} scrollWidth=${await page.evaluate(() => document.documentElement.scrollWidth)}`);
  await ctx.close();
}

const approveTwice = (label) => async (page) => {
  await page.getByRole("button", { name: label }).click();
  await page.getByRole("button", { name: label }).click();
  await page.waitForSelector("[data-outcome], [data-conflict]");
};

for (const scheme of ["light", "dark"]) {
  for (const locale of ["en", "zh-Hant"]) {
    for (const width of [1280, 390]) await shot("review-ready", "/ui-gallery/proposal", { scheme, locale, width });
  }
  await shot("review-new-steps", "/ui-gallery/proposal?state=new-steps", { scheme, locale: "en", width: 1280 });
  await shot("review-approved-outcome", "/ui-gallery/proposal", { scheme, locale: "en", width: 1280, act: approveTwice("Approve and create version 2") });
  await shot("review-409-kept", "/ui-gallery/proposal?state=conflict", { scheme, locale: "en", width: 1280, act: approveTwice("Approve and create version 2") });
  await shot("model-page-ask", "/ui-gallery/editor?state=clean", { scheme, locale: "en", width: 1280 });
}
await shot("review-scenario-outcome", "/ui-gallery/proposal?state=scenario", { scheme: "light", locale: "zh-Hant", width: 1280, act: approveTwice("批准並儲存情境") });
await shot("review-needs-input", "/ui-gallery/proposal?state=needs_input", { scheme: "light", locale: "zh-Hant", width: 390 });
await browser.close();
console.log(lines.join("\n"));
