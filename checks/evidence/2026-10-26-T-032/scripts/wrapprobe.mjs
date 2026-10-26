// Red/green probe for the phone table check (same condition as results.spec.ts) on :3200.
import { chromium } from "/home/user/Geminus/.claude/worktrees/agent-a72a328327cdce612/apps/web-e2e/node_modules/playwright/index.mjs";

const origin = "http://127.0.0.1:3200";
const browser = await chromium.launch();
for (const locale of ["en", "zh-Hant"]) {
  for (const width of [390, 1280]) {
    const ctx = await browser.newContext({ viewport: { width, height: 900 } });
    await ctx.addCookies([{ name: "geminus_locale", value: locale, url: origin }]);
    const page = await ctx.newPage();
    await page.goto(`${origin}/ui-gallery/results`);
    await page.waitForLoadState("networkidle");
    await page.evaluate(() => document.fonts.ready.then(() => undefined));
    const broken = await page.locator("main table").first().locator("data").evaluateAll((ds) => ds.filter((d) => d.getClientRects().length > 1 || d.getBoundingClientRect().height > 24).map((d) => d.textContent));
    const regions = await page.getByRole("region").count();
    const scroll = await page.evaluate(() => document.documentElement.scrollWidth);
    console.log(`${locale} ${width}: broken=${JSON.stringify(broken)} regions=${regions} pageScrollWidth=${scroll}`);
    await ctx.close();
  }
}
await browser.close();
