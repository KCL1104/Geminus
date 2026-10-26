// Local preview screenshots of the T-032 results view (NOT baselines): the
// review page /ui-gallery/results on a local `next start` at :3200, en and
// zh-Hant, 1280 and 390 px, light and dark, plus the running state.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "/home/user/Geminus/.claude/worktrees/agent-a72a328327cdce612/apps/web-e2e/node_modules/playwright/index.mjs";

const out = process.argv[2];
mkdirSync(out, { recursive: true });
const origin = "http://127.0.0.1:3200";
const browser = await chromium.launch();
const shots = [];
for (const scheme of ["light", "dark"]) {
  for (const locale of ["en", "zh-Hant"]) {
    for (const width of [1280, 390]) {
      for (const [state, query] of [["results", ""], ["running", "?state=running"]]) {
        if (state === "running" && width === 390) continue;
        const ctx = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: scheme, locale: "en-US", timezoneId: "UTC" });
        await ctx.addCookies([{ name: "geminus_locale", value: locale, url: origin }]);
        const page = await ctx.newPage();
        await page.goto(`${origin}/ui-gallery/results${query}`);
        await page.waitForLoadState("networkidle");
        await page.evaluate(() => document.fonts.ready.then(() => undefined));
        const name = `results-${state}-${locale}-${width}${scheme === "dark" ? "-dark" : ""}.png`;
        await page.screenshot({ path: join(out, name), fullPage: true });
        const scroll = await page.evaluate(() => document.documentElement.scrollWidth);
        shots.push(`${name} scrollWidth=${scroll}`);
        await ctx.close();
      }
    }
  }
}
await browser.close();
console.log(shots.join("\n"));
