// T-032 (hackathon slice): the results view in a real browser, on its
// review page (the app mounts no session yet, so the signed-in page cannot
// render here; app/ui-gallery/results). The Marey chart is drawn as
// DESIGN.md says (dashed baseline, solid scenario: the dash carries the
// difference), is one image with a name and a description and holds no
// focus stop; its table is one keyboard press away; the metrics show the
// server's values; every string is the catalog's or the model's data. axe,
// the phone width and the simplicity rules run on these pages with every
// route (a11y.spec.ts, simplicity.spec.ts).
import { readFileSync } from "node:fs";
import { expect, test } from "./fixtures";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { focusIsVisible, locales, open, violations } from "./helpers";

// The review page's data: the synthetic board line (apps/web/features/results/fixtures).
const fixture = <T>(name: string): T => JSON.parse(readFileSync(new URL(`../../web/features/results/fixtures/${name}`, import.meta.url), "utf8")) as T;
const comparison = fixture<{ metrics: { metric_path: string; baseline: number | null; scenario: number | null; absolute_delta: number | null; relative_delta: number | null }[] }>("board-line-comparison.json");
const model = fixture<{ name: string; nodes: { name?: string }[]; resources: { name: string }[] }>("board-line-model.json");

const catalogs = { en, "zh-Hant": zhHant } as const;

for (const width of [390, 1280]) {
  test.describe(`at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test("the chart draws one dashed baseline and one solid scenario line per sampled work item, inside the page", async ({ page }) => {
      await open(page, "/ui-gallery/results", "en");
      const chart = page.getByRole("img", { name: en["marey.title"] });
      await expect(chart).toBeVisible();
      await expect(chart.locator("polyline[data-run=baseline]")).toHaveCount(19);
      await expect(chart.locator("polyline[data-run=scenario]")).toHaveCount(19);
      const style = (run: string) =>
        chart.locator(`polyline[data-run=${run}]`).first().evaluate((el) => {
          const s = getComputedStyle(el);
          return { dash: s.strokeDasharray, width: s.strokeWidth };
        });
      // The dash, not the hue, tells the runs apart (DESIGN.md The Two Inks Rule).
      expect(await style("baseline")).toEqual({ dash: "5px, 3px", width: "1.4px" });
      expect(await style("scenario")).toEqual({ dash: "none", width: "1.75px" });
      // The chart fits its column at this width: no horizontal scroll of its own or of the page.
      const box = await chart.boundingBox();
      expect(box!.x + box!.width).toBeLessThanOrEqual(width);
      // Nothing inside the image takes focus.
      await expect(chart.locator("a, button, [tabindex]")).toHaveCount(0);
    });

    for (const locale of locales) {
      test(`${locale}: no figure in the metrics breaks across lines; a narrow table scrolls in its own labelled region`, async ({ page }) => {
        await open(page, "/ui-gallery/results", locale);
        const table = page.getByRole("table", { name: catalogs[locale]["compare.caption"] });
        const broken = await table.locator("data").evaluateAll((ds) => ds.filter((d) => d.getClientRects().length > 1 || d.getBoundingClientRect().height > 24).map((d) => d.textContent));
        expect(broken).toEqual([]);
        const region = page.getByRole("region", { name: catalogs[locale]["compare.title"] });
        await expect(region).toBeVisible();
        const box = await region.boundingBox();
        expect(box!.x + box!.width).toBeLessThanOrEqual(width);
        // Reachable from the keyboard, so its overflow can be scrolled without a pointer (axe scrollable-region-focusable).
        await region.focus();
        expect(await focusIsVisible(page)).toBe(true);
      });
    }
  });
}

for (const locale of locales) {
  test(`${locale}: the chart's table is one key press away, and the metrics are the server's`, async ({ page }) => {
    const t = catalogs[locale];
    await open(page, "/ui-gallery/results", locale);
    const summary = page.locator("summary", { hasText: t["marey.table"] });
    await summary.focus();
    expect(await focusIsVisible(page)).toBe(true);
    await page.keyboard.press("Enter");
    const table = page.getByRole("table", { name: t["marey.table.caption"] });
    await expect(table).toBeVisible();
    await expect(table.getByRole("rowheader", { name: "ICT", exact: true })).toBeVisible();
    expect(await violations(page), "with the table open").toEqual([]);
    // The exact value the server sent sits under every figure.
    const lead = comparison.metrics.find((m) => m.metric_path === "/metrics/lead_time_minutes/p50")!;
    const row = page.getByRole("row", { name: new RegExp(`^${t["metric.leadTimeMedian"]}`) });
    await expect(row.locator("data")).toHaveCount(4);
    expect(await row.locator("data").evaluateAll((ds) => ds.map((d) => d.getAttribute("value")))).toEqual([lead.baseline, lead.scenario, lead.absolute_delta, lead.relative_delta].map(String));
  });
}

// H-073 (3) and (4), mounted: the reproducibility card in the side panel
// reads the model's runs from the review page's stand-in (the page's own two
// runs, so no other), and shows that, not a problem; the board line is a
// Software Delivery model, which has no work ratio, so no constraint card.
for (const locale of locales) {
  test(`${locale}: the results show their reproducibility, in a real state, and no constraint card for this pack`, async ({ page }) => {
    const t = catalogs[locale];
    await open(page, "/ui-gallery/results", locale);
    const proof = page.locator("section").filter({ has: page.getByRole("heading", { level: 2, name: t["proof.title"], exact: true }) });
    // One line for each run: neither has another run with its inputs.
    await expect(proof.getByText(t["proof.reruns.none"], { exact: true })).toHaveCount(2);
    for (const line of await proof.getByText(t["proof.reruns.none"], { exact: true }).all()) await expect(line).toBeVisible();
    await expect(proof.getByRole("table", { name: t["proof.caption"] })).toBeVisible();
    // No problem in the page (Next.js's route announcer, an empty alert outside main, is not one).
    await expect(page.locator("main").getByRole("alert")).toHaveCount(0);
    await expect(page.getByRole("heading", { level: 2, name: t["constraint.title"], exact: true })).toHaveCount(0);
    // The run settings come before it in the side panel.
    const facts = page.getByRole("heading", { level: 2, name: t["facts.title"], exact: true });
    expect((await facts.boundingBox())!.y).toBeLessThan((await proof.boundingBox())!.y);
  });
}

// H-072 (replacing T-034's column playback): the line in motion. The two
// runs' 3D stages, Baseline above Scenario at every width and each as wide
// as the page's card (H-072 review), play under one playback bar on one
// clock; Pause is its first control, from the mouse and the keyboard. More
// on the electronics line's own page: motion.spec.ts.
test.describe("the results page's line in motion", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test("plays both runs one above the other under one bar; Pause pauses from the keyboard; the slider moves; it says what is sampled", async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await open(page, "/ui-gallery/results", "en");
    const motion = page.getByRole("region", { name: en["playback.stage"] });
    await expect(page.getByText(en["twin3d.loading"])).toHaveCount(0, { timeout: 30_000 });
    const baseline = motion.locator("section[data-stage=baseline]");
    const scenario = motion.locator("section[data-stage=scenario]");
    await expect(baseline.getByRole("heading", { level: 3 })).toHaveText(en["runs.baseline"]);
    await expect(scenario.getByRole("heading", { level: 3 })).toHaveText(en["runs.scenario"]);
    // Baseline above Scenario, level at the left and as wide, past the side panel (H-072 review).
    const [b, s] = [(await baseline.boundingBox())!, (await scenario.boundingBox())!];
    expect(s.y).toBeGreaterThanOrEqual(b.y + b.height);
    expect(Math.abs(s.x - b.x)).toBeLessThan(1);
    expect(Math.abs(s.width - b.width)).toBeLessThan(1);
    expect(b.width).toBeGreaterThan(1000);
    // Each stage is its run's scene, or says why it cannot run here.
    for (const [stage, name] of [[baseline, en["playback.stage.baseline"]], [scenario, en["playback.stage.scenario"]]] as const) {
      await expect(stage.getByRole("img", { name }).or(stage.getByText(en["twin3d.unavailable"]))).toBeVisible();
    }
    // One bar; it plays once the traces are there (autoplay), Pause first.
    await expect(motion.getByRole("group", { name: en["playback.bar"] })).toHaveCount(1);
    const pause = motion.getByRole("button", { name: en["playback.pause"] });
    await expect(pause).toBeVisible();
    const slider = motion.getByRole("slider", { name: en["playback.time"] });
    await expect(slider).not.toHaveAttribute("aria-valuetext", "Day 1, 00:00 of 10 hours");
    await expect(motion.getByText("Sampled: 1 work item in 8, replication 1.")).toBeVisible();
    // Pause from the keyboard: the time stays.
    await pause.focus();
    expect(await focusIsVisible(page)).toBe(true);
    await page.keyboard.press("Enter");
    const play = motion.getByRole("button", { name: en["playback.play"] });
    await expect(play).toBeFocused();
    const held = await slider.getAttribute("aria-valuetext");
    await page.waitForTimeout(1500);
    await expect(slider).toHaveAttribute("aria-valuetext", held!);
    // The slider is next (Tab from Pause/Play), and its keys move it.
    await page.keyboard.press("Tab");
    await expect(slider).toBeFocused();
    expect(await focusIsVisible(page)).toBe(true);
    await page.keyboard.press("End");
    await expect(slider).toHaveAttribute("aria-valuetext", "Day 1, 10:00 of 10 hours");
    await page.keyboard.press("Home");
    await expect(slider).toHaveAttribute("aria-valuetext", "Day 1, 00:00 of 10 hours");
    expect(await violations(page)).toEqual([]);
    expect(errors).toEqual([]);
  });

  for (const locale of locales) {
    test(`${locale} at 390px: the stages stack, Baseline first, and the bar stays inside the page, with no violation`, async ({ page }) => {
      await page.setViewportSize({ width: 390, height: 844 });
      const t = catalogs[locale];
      await open(page, "/ui-gallery/results", locale);
      const motion = page.getByRole("region", { name: t["playback.stage"] });
      await expect(page.getByText(t["twin3d.loading"])).toHaveCount(0, { timeout: 30_000 });
      const [b, s] = [(await motion.locator("section[data-stage=baseline]").boundingBox())!, (await motion.locator("section[data-stage=scenario]").boundingBox())!];
      expect(s.y).toBeGreaterThanOrEqual(b.y + b.height);
      const slider = motion.getByRole("slider", { name: t["playback.time"] });
      await expect(slider).toBeVisible();
      await expect(motion.getByRole("button", { name: t["playback.pause"] })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      const box = (await slider.boundingBox())!;
      expect(box.x >= 0 && box.x + box.width <= 390).toBe(true);
      expect(await violations(page)).toEqual([]);
    });
  }
});

test("the running runs cancel from the keyboard, with a visible focus", async ({ page }) => {
  await open(page, "/ui-gallery/results?state=running", "en");
  await expect(page.getByRole("progressbar")).toHaveCount(2);
  const cancel = page.getByRole("button", { name: en["runs.cancel"] });
  await cancel.focus();
  expect(await focusIsVisible(page)).toBe(true);
  await page.keyboard.press("Enter");
  await expect(page.getByRole("progressbar")).toHaveCount(0);
  // Shown in the Outcome frame; the live region that announced it is never shown (H-039).
  await expect(page.locator("main p:not([role=status])", { hasText: en["runs.noComparison"] })).toBeVisible();
});

// H-008 / REQ-022, as strings.spec.ts does for the gallery: every string is
// the catalog's, or data the page shows (here the model's names and the
// runs' identifiers in <code>).
type Catalog = Record<string, string | { one?: string; other: string }>;
const pattern = (text: string) => new RegExp("^" + text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/\\\{\w+\\\}/g, ".+") + "$", "s");
const names = [model.name, ...model.nodes.map((n) => n.name), ...model.resources.map((r) => r.name), "Second ICT fixture"].filter((n): n is string => typeof n === "string");

for (const locale of locales) {
  test(`${locale}: every string on the results pages is the catalog's or the model's data`, async ({ page }) => {
    const known = [...Object.values(catalogs[locale] as Catalog).flatMap((m) => (typeof m === "string" ? [m] : [m.one, m.other].filter((x): x is string => !!x))).map(pattern), ...names.map(pattern)];
    const unknown = new Set<string>();
    for (const path of ["/ui-gallery/results", "/ui-gallery/results?state=incomparable", "/ui-gallery/results?state=running", "/ui-gallery/results?state=failed"]) {
      await open(page, path, locale);
      // Open every disclosure (the chart's table, a problem's details), so their strings are read too.
      for (const summary of await page.locator("main summary").all()) await summary.click();
      // Each text (outside data: identifiers in <code>, values in <data> and
      // <time>, the server's English in lang="en"), with the whole text of
      // its element: a message whose placeholders are elements (a value in
      // <data>) is split into several texts, and is the catalog's as a whole.
      const seen = await page.evaluate(() => {
        const out: [string, string][] = [];
        const norm = (s: string | null | undefined) => (s ?? "").replace(/\s+/g, " ").trim();
        const isData = (el: Element | null) => !!el?.closest("code, data, time, [lang=en]:not(html)");
        const walker = document.createTreeWalker(document.querySelector("main")!, NodeFilter.SHOW_TEXT);
        for (let n = walker.nextNode(); n; n = walker.nextNode()) {
          const text = norm(n.textContent);
          if (text && /\p{L}/u.test(text) && !isData(n.parentElement)) out.push([text, norm(n.parentElement?.textContent)]);
        }
        for (const el of document.querySelectorAll("main [aria-label], main [aria-valuetext], main [title]")) {
          for (const a of ["aria-label", "aria-valuetext", "title"]) {
            const v = norm(el.getAttribute(a));
            if (v && /\p{L}/u.test(v) && !isData(el)) out.push([v, v]);
          }
        }
        return out;
      });
      for (const [text, whole] of seen) if (!known.some((re) => re.test(text) || re.test(whole))) unknown.add(text);
    }
    expect([...unknown].sort()).toEqual([]);
  });
}
