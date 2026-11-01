// H-072 (docs/design/H-072_TRACE_ANIMATION.md; DESIGN.md, The Trace Motion
// Rule): the line in motion in a real browser, on the review pages with the
// electronics line template's runs as the engine wrote them
// (apps/web/tests/fixtures/electronics-line-*.json): the results page's two
// stages (/ui-gallery/results?state=line) and the model page's 2D canvas and
// 3D view (/ui-gallery/editor?state=motion), and both held still at Day 1,
// 22:00 (?state=still), as the screenshots take them. It starts playing
// (autoplay); Pause, the bar's first control, pauses it from the mouse and
// the keyboard; a reader who asks for reduced motion gets it paused; editing
// pauses it; axe finds nothing, at 390 and 1280 px, in both languages; and
// every string is the catalog's or the model's data. axe in both themes and
// the simplicity rules also run on these pages with every route
// (helpers.ts routes).
import { readFileSync } from "node:fs";
import type { Locator, Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { focusIsVisible, locales, open, violations } from "./helpers";

const catalogs = { en, "zh-Hant": zhHant } as const;
const model = JSON.parse(readFileSync(new URL("../../web/tests/fixtures/electronics-line-model.json", import.meta.url), "utf8")) as {
  name: string;
  nodes: { name?: string }[];
  resources: { name: string }[];
};

const results = (state: "line" | "still") => `/ui-gallery/results?state=${state}`;
const editor = (state: "motion" | "still") => `/ui-gallery/editor?state=${state}`;
const bar = (scope: Page | Locator, locale: keyof typeof catalogs = "en") => scope.getByRole("group", { name: catalogs[locale]["playback.bar"] });
const slider = (scope: Page | Locator, locale: keyof typeof catalogs = "en") => scope.getByRole("slider", { name: catalogs[locale]["playback.time"] });
/** The playhead's time over the slider (its spoken value follows at most once a second while playing). */
const playhead = (scope: Page | Locator) => scope.locator("input[type=range]").locator("xpath=preceding-sibling::span");
const noScroll = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

test.describe("the results page's two stages, the electronics line", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test("start playing; Pause pauses (mouse and keys) and Play plays on; the stages are the two runs", async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await open(page, results("line"), "en");
    const motion = page.getByRole("region", { name: en["playback.stage"] });
    await expect(page.getByText(en["twin3d.loading"])).toHaveCount(0, { timeout: 30_000 });
    const b = bar(motion);
    await expect(b.getByRole("button", { name: en["playback.pause"] })).toBeVisible();
    await expect(b.getByText("Sampled: 1 board in 8, replication 1.")).toBeVisible();
    // Playing: the playhead's time goes on.
    const first = await playhead(b).textContent();
    await expect(playhead(b)).not.toHaveText(first!, { timeout: 5_000 });
    // Pause with the mouse: the time holds.
    await b.getByRole("button", { name: en["playback.pause"] }).click();
    const held = await playhead(b).textContent();
    await page.waitForTimeout(1000);
    await expect(playhead(b)).toHaveText(held!);
    // Play from the keyboard (Space), then pause again (Enter). The focus arrives from the keys
    // (after a mouse click a focus moved by script shows no ring, as the browser decides).
    const play = b.getByRole("button", { name: en["playback.play"] });
    await play.focus();
    await page.keyboard.press("Tab");
    await page.keyboard.press("Shift+Tab");
    await expect(play).toBeFocused();
    expect(await focusIsVisible(page)).toBe(true);
    await page.keyboard.press("Space");
    await expect(b.getByRole("button", { name: en["playback.pause"] })).toBeFocused();
    await expect(playhead(b)).not.toHaveText(held!, { timeout: 5_000 });
    await page.keyboard.press("Enter");
    await expect(b.getByRole("button", { name: en["playback.play"] })).toBeFocused();
    // Each stage is its run's scene (or says why it cannot run here), titled with its run.
    for (const side of ["baseline", "scenario"] as const) {
      const stage = motion.locator(`section[data-stage=${side}]`);
      await expect(stage.getByRole("heading", { level: 3 })).toHaveText(en[`runs.${side}`]);
      await expect(stage.getByRole("img", { name: en[`playback.stage.${side}`] }).or(stage.getByText(en["twin3d.unavailable"]))).toBeVisible();
    }
    expect(await violations(page)).toEqual([]);
    expect(errors).toEqual([]);
  });

  test("starts paused for a reader who asks for reduced motion", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await open(page, results("line"), "en");
    const b = bar(page);
    await expect(b.getByRole("button", { name: en["playback.play"] })).toBeVisible();
    await page.waitForTimeout(1000);
    await expect(slider(b)).toHaveAttribute("aria-valuetext", "Day 1, 00:00 of 1 day");
  });

  test("holds still at Day 1, 22:00 for the screenshots, and its picture does not change", async ({ page }) => {
    await open(page, results("still"), "en");
    await expect(page.getByText(en["twin3d.loading"])).toHaveCount(0, { timeout: 30_000 });
    const b = bar(page);
    await expect(b.getByRole("button", { name: en["playback.play"] })).toBeVisible();
    await expect(slider(b)).toHaveAttribute("aria-valuetext", "Day 1, 22:00 of 1 day");
    // The thumb stands at 22:00 of the day, not at the start.
    await expect(slider(b)).toHaveAttribute("max", String(24 * 3_600_000));
    await expect(slider(b)).toHaveValue(String(22 * 3_600_000));
    const scene = page.getByRole("img", { name: en["playback.stage.baseline"] });
    if (await scene.isVisible()) {
      const before = await scene.screenshot();
      await page.waitForTimeout(1000);
      expect((await scene.screenshot()).equals(before)).toBe(true);
    }
  });

  // H-072 review: side by side beside the side panel each stage was 312 px wide at every desktop
  // width, and the wheel over either one zoomed it instead of scrolling the page.
  test("stand one above the other, as wide as the page; the wheel scrolls the page past them, and zooms with Ctrl held", async ({ page }) => {
    await open(page, results("still"), "en");
    await expect(page.getByText(en["twin3d.loading"])).toHaveCount(0, { timeout: 30_000 });
    const motion = page.getByRole("region", { name: en["playback.stage"] });
    const [b, s] = [(await motion.locator("section[data-stage=baseline]").boundingBox())!, (await motion.locator("section[data-stage=scenario]").boundingBox())!];
    expect(s.y).toBeGreaterThanOrEqual(b.y + b.height);
    expect(Math.abs(s.x - b.x)).toBeLessThan(1);
    expect(Math.abs(s.width - b.width)).toBeLessThan(1);
    // Past the side panel: the card's whole width (1,104 px of content at 1280, less its 20 px sides).
    expect(b.width).toBeGreaterThan(1000);
    // The controls are said once, under the last stage.
    await expect(motion.getByText(en["twin3d.hint"])).toHaveCount(2);
    await expect(motion.getByText(en["twin3d.hint"]).filter({ visible: true })).toHaveCount(1);
    const scene = page.getByRole("img", { name: en["playback.stage.baseline"] });
    const target = (await scene.isVisible()) ? scene : motion.locator("section[data-stage=baseline]");
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
    // The side panel, sticky beside the comparison, stops above the card (it does not cover it).
    const facts = (await page.locator('section[aria-labelledby="facts-heading"]').boundingBox())!;
    expect(facts.y + facts.height).toBeLessThanOrEqual((await motion.boundingBox())!.y);
    const box = (await target.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    const bottom = await page.evaluate(() => window.scrollY);
    expect(bottom).toBeGreaterThan(400);
    await page.mouse.wheel(0, -400);
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBeLessThan(bottom);
    if (await scene.isVisible()) {
      // With Ctrl held the wheel zooms the scene, and the page stays where it is.
      const at = (await scene.boundingBox())!;
      await page.mouse.move(at.x + at.width / 2, at.y + at.height / 2);
      const y = await page.evaluate(() => window.scrollY);
      const before = await scene.screenshot();
      await page.keyboard.down("Control");
      await page.mouse.wheel(0, -400);
      await page.keyboard.up("Control");
      await expect.poll(async () => (await scene.screenshot()).equals(before)).toBe(false);
      expect(await page.evaluate(() => window.scrollY)).toBe(y);
    }
  });

  // H-072 review: a theme change destroyed the meshes the stages share, and each stage then threw on every frame.
  test("go on drawing when the theme changes while they play", async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await open(page, results("line"), "en");
    await expect(page.getByText(en["twin3d.loading"])).toHaveCount(0, { timeout: 30_000 });
    await expect(bar(page).getByRole("button", { name: en["playback.pause"] })).toBeVisible();
    for (const theme of ["dark", "light"]) {
      await page.evaluate((t) => document.documentElement.setAttribute("data-theme", t), theme);
      await page.waitForTimeout(800);
    }
    const scene = page.getByRole("img", { name: en["playback.stage.scenario"] });
    if (await scene.isVisible()) {
      // Still playing: its picture changes.
      const before = await scene.screenshot();
      await expect.poll(async () => (await scene.screenshot()).equals(before), { timeout: 5_000 }).toBe(false);
    }
    expect(errors).toEqual([]);
  });
});

test.describe("the model page's line in motion, the electronics line", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test("plays the version's latest run on the canvas, named over it; Pause from the keys; a drag pauses; 3D plays on the same clock", async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await open(page, editor("motion"), "en");
    const strip = page.locator("[data-motion]");
    await expect(strip).toHaveAttribute("data-motion", "baseline");
    await expect(strip.locator("p").first()).toHaveText(/^In motion: the baseline run of .+, seed 1\.$/);
    await expect(page.locator("[data-trace]")).toHaveAttribute("data-trace", "baseline");
    const b = bar(strip);
    await expect(b.getByRole("button", { name: en["playback.pause"] })).toBeVisible();
    await expect(playhead(b)).not.toHaveText("Day 1, 00:00", { timeout: 5_000 });
    // The machines wear their state from the trace; the strip keys the rings and says they show only the sampled boards.
    await expect(page.locator("[data-station]").first()).toBeAttached();
    await expect(strip.getByRole("list", { name: en["playback.rings"] })).toBeVisible();
    await expect(strip.getByText(en["pack.electronics_line.playback.ringsSampled"])).toBeVisible();
    // Pause comes before the steps: from the keyboard, Enter pauses.
    await b.getByRole("button", { name: en["playback.pause"] }).focus();
    expect(await focusIsVisible(page)).toBe(true);
    await page.keyboard.press("Enter");
    await expect(b.getByRole("button", { name: en["playback.play"] })).toBeFocused();
    // Play, then drag a step: editing pauses it.
    await page.keyboard.press("Enter");
    await expect(b.getByRole("button", { name: en["playback.pause"] })).toBeVisible();
    await page.locator(".react-flow").scrollIntoViewIfNeeded();
    const smt = page.locator('.react-flow__node[data-id="smt"]');
    const box = (await smt.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + 10);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + 30, box.y + 60, { steps: 6 });
    await page.mouse.up();
    await expect(b.getByRole("button", { name: en["playback.play"] })).toBeVisible();
    expect(await violations(page)).toEqual([]);
    // The 3D view: the same run, on the same clock (still paused).
    await page.getByRole("radio", { name: "3D" }).click();
    await expect(page.getByText(en["twin3d.loading"])).toHaveCount(0, { timeout: 30_000 });
    await expect(page.getByRole("img", { name: en["playback.stage.baseline"] }).or(page.getByText(en["twin3d.unavailable"]))).toBeVisible();
    await expect(b.getByRole("button", { name: en["playback.play"] })).toBeVisible();
    // No rings in 3D, so no key to them over it (the stage keys its lights).
    await expect(strip.getByRole("list", { name: en["playback.rings"] })).toHaveCount(0);
    expect(errors).toEqual([]);
  });

  test("starts paused for a reader who asks for reduced motion", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await open(page, editor("motion"), "en");
    const b = bar(page.locator("[data-motion]"));
    await expect(b.getByRole("button", { name: en["playback.play"] })).toBeVisible();
    await page.waitForTimeout(1000);
    await expect(slider(b)).toHaveAttribute("aria-valuetext", "Day 1, 00:00 of 1 day");
  });

  test("holds still at Day 1, 22:00: two boards wait before FCT", async ({ page }) => {
    await open(page, editor("still"), "en");
    const b = bar(page.locator("[data-motion]"));
    await expect(slider(b)).toHaveAttribute("aria-valuetext", "Day 1, 22:00 of 1 day");
    await expect(slider(b)).toHaveValue(String(22 * 3_600_000));
    await expect(page.locator('.react-flow__node[data-id="q_fct"] [data-trace-badge]')).toHaveText("2");
  });

  test("a version with no run shows the flow's direction only, and says how to see it move", async ({ page }) => {
    await open(page, "/ui-gallery/editor?state=clean", "en");
    await expect(page.locator("[data-motion]")).toHaveAttribute("data-motion", "still");
    await expect(page.locator("[data-motion] p")).toHaveText(en["playback.noRun"]);
    await expect(page.locator("[data-motion]").getByRole("group")).toHaveCount(0);
    await expect(page.locator("[data-trace]")).toHaveCount(0);
    await expect(page.locator("[data-chevrons]").first()).toBeAttached();
  });
});

for (const locale of locales) {
  for (const width of [390, 1280]) {
    test.describe(`${locale} at ${width}px`, () => {
      test.use({ viewport: { width, height: 900 } });
      const t = catalogs[locale];

      test("the results page's stages play under one bar inside the page, with no violation", async ({ page }) => {
        await open(page, results("line"), locale);
        const motion = page.getByRole("region", { name: t["playback.stage"] });
        await expect(page.getByText(t["twin3d.loading"])).toHaveCount(0, { timeout: 30_000 });
        await expect(bar(motion, locale).getByRole("button", { name: t["playback.pause"] })).toBeVisible();
        const [b, s] = [(await motion.locator("section[data-stage=baseline]").boundingBox())!, (await motion.locator("section[data-stage=scenario]").boundingBox())!];
        // At every width one above the other, Baseline first, each the card's whole width (H-072 review).
        expect(s.y).toBeGreaterThanOrEqual(b.y + b.height);
        expect(Math.abs(s.x - b.x)).toBeLessThan(1);
        expect(b.width).toBeGreaterThan(width === 1280 ? 1000 : 300);
        expect(await noScroll(page)).toBe(true);
        const box = (await slider(motion, locale).boundingBox())!;
        expect(box.x >= 0 && box.x + box.width <= width).toBe(true);
        expect(await violations(page)).toEqual([]);
      });

      test("the model page plays its run (a read-only canvas on a phone), with no violation", async ({ page }) => {
        await open(page, editor("motion"), locale);
        const strip = page.locator("[data-motion]");
        await expect(strip).toHaveAttribute("data-motion", "baseline");
        await expect(bar(strip, locale).getByRole("button", { name: t["playback.pause"] })).toBeVisible();
        await expect(page.locator("[data-trace]")).toHaveAttribute("data-trace", "baseline");
        if (width === 390) {
          // The editor is read-only on a phone: no 3D, nothing to drag.
          await expect(page.getByText(t["editor.narrow"])).toBeVisible();
          await expect(page.getByRole("radio", { name: "3D" })).toHaveCount(0);
          await expect(page.locator(".react-flow__node.draggable")).toHaveCount(0);
        }
        expect(await noScroll(page)).toBe(true);
        expect(await violations(page)).toEqual([]);
      });
    });
  }
}

// H-008 / REQ-022, as results.spec.ts does for the board line: every string
// on the line in motion's results pages is the catalog's, or data the page
// shows (the model's names; identifiers in <code>, values in <data> and
// <time>); on the model page, what this slice adds (the strip over the
// canvas and the overlay's step states and notes; the rest of that page is
// strings.spec.ts's, with its React Aria allowances).
type Catalog = Record<string, string | { one?: string; other: string }>;
const pattern = (text: string) => new RegExp("^" + text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/\\\{\w+\\\}/g, ".+") + "$", "s");
const names = [model.name, ...model.nodes.map((n) => n.name), ...model.resources.map((r) => r.name), "Fourth FCT tester"].filter((n): n is string => typeof n === "string");

for (const locale of locales) {
  test(`${locale}: every string on the line in motion's pages is the catalog's or the model's data`, async ({ page }) => {
    test.setTimeout(240_000);
    await page.setViewportSize({ width: 1280, height: 900 });
    const known = [...Object.values(catalogs[locale] as Catalog).flatMap((m) => (typeof m === "string" ? [m] : [m.one, m.other].filter((x): x is string => !!x))).map(pattern), ...names.map(pattern)];
    const unknown = new Set<string>();
    const pages: [string, string][] = [[results("line"), "main"], [results("still"), "main"], [editor("motion"), "[data-motion], [data-trace-note], [data-trace-state]"], [editor("still"), "[data-motion], [data-trace-note], [data-trace-state]"]];
    for (const [path, scope] of pages) {
      await open(page, path, locale);
      await expect(page.getByText(catalogs[locale]["twin3d.loading"])).toHaveCount(0, { timeout: 30_000 });
      const seen = await page.evaluate((scope) => {
        const out: [string, string][] = [];
        const norm = (s: string | null | undefined) => (s ?? "").replace(/\s+/g, " ").trim();
        const isData = (el: Element | null) => !!el?.closest("code, data, time, [lang=en]:not(html)");
        for (const root of document.querySelectorAll(scope)) {
          const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
          for (let n = walker.nextNode(); n; n = walker.nextNode()) {
            const text = norm(n.textContent);
            // A figure's words around its <data> (the hours of a long time, H-073's Figure) are a span of
            // words only, beside the <data>: they are judged with the whole figure, as one catalog message.
            // A span that holds <data> itself (an interval's "95% CI {low} to {high}") is its own message.
            const p = n.parentElement;
            const wordsOnly = !p?.querySelector(":scope > data") && !!p?.parentElement?.querySelector(":scope > data");
            const whole = wordsOnly ? p!.parentElement : p;
            if (text && /\p{L}/u.test(text) && !isData(p)) out.push([text, norm(whole?.textContent)]);
          }
          for (const el of [root, ...root.querySelectorAll("[aria-label], [aria-valuetext]")]) {
            for (const a of ["aria-label", "aria-valuetext"]) {
              const v = norm(el.getAttribute(a));
              if (v && /\p{L}/u.test(v) && !isData(el)) out.push([v, v]);
            }
          }
        }
        return out;
      }, scope);
      expect(seen.length, `${path} has strings to check`).toBeGreaterThan(0);
      // A name the Marey chart cuts to its label column ("SMT line (printer, SPI…", marey.ts fitLabel) is still the model's.
      const cut = (text: string) => text.endsWith("…") && names.some((n) => n.startsWith(text.slice(0, -1).trimEnd()));
      for (const [text, whole] of seen) if (!known.some((re) => re.test(text) || re.test(whole)) && !cut(text)) unknown.add(text);
    }
    expect([...unknown].sort()).toEqual([]);
  });
}
