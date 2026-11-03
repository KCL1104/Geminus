// The landing page's product screens (H-048), made again from the app.
//
// The landing (apps/web/app/page.tsx, features/landing/) shows real screens
// of the product. This script makes them: it opens the app's review pages
// (the component gallery's editor, proposal and results pages: the real
// views on the synthetic sample board line, no AI called), crops each to
// the product's own view, in the light and the dark theme, at 2x, and
// writes WebP at 1x and 2x under apps/web/public/landing/. One screen
// exists only on the live demo (the AI's explanation, written by Muse Spark
// 1.3 on synthetic data): it is cropped from the screenshot committed under
// checks/evidence/2026-10-30-h048-landing/live/ (light theme only, 1x).
// H-088: the proposal is the gallery's sample scenario, in both themes (the
// live demo's capture still said "Checked by Geminus"); its caption says no
// AI was called. The landing draws every screen at its 1104 px box, so the
// results and the Marey chart are taken at the widest the results page
// stacks its panel under them (1100 px), near that width; a screen narrower
// than the box is captured at the scale that makes its pictures the box's
// width at 1x and 2x (1104 and 2208 px), so neither is stretched on screen
// and a 1x screen has a picture that fits (H-088 review).
// It replaces everything in public/landing/ and writes
// features/landing/shots.ts, the files and sizes the page renders (a module,
// not JSON: no app module imports JSON, tests/results-preview.test.tsx).
//
// Usage (Node 24.21.0, from the repository root):
//   npm --prefix apps/web run build
//   node apps/web-e2e/scripts/landing-shots.mjs --start        # starts the app itself on :3210
//   node apps/web-e2e/scripts/landing-shots.mjs --base URL     # or uses an app already started with GEMINUS_UI_GALLERY=1
//
// The images are encoded by the browser (canvas WebP), so no image tool is
// needed. Rendering differs a little between machines; the committed
// images are whatever the last run made, reviewed by a person.
import { spawn } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "../../..");
const web = join(root, "apps/web");
const out = join(web, "public/landing");
const manifest = join(web, "features/landing/shots.ts");
const live = join(root, "checks/evidence/2026-10-30-h048-landing/live");

const arg = (name) => {
  const i = process.argv.indexOf(name);
  return i < 0 ? undefined : process.argv[i + 1];
};
const start = process.argv.includes("--start");
const port = Number(arg("--port") ?? 3210);
const base = arg("--base") ?? `http://127.0.0.1:${port}`;
const QUALITY = 0.86;
/** The landing's box (features/landing/shot.tsx BOX): every gallery screen is drawn at this width. */
const BOX = 1104;

/** Starts `next start` with the gallery on and waits until it answers. */
async function serve() {
  const child = spawn("npm", ["--prefix", web, "run", "start", "--", "--port", String(port), "--hostname", "127.0.0.1"], {
    env: { ...process.env, GEMINUS_UI_GALLERY: "1", NEXT_TELEMETRY_DISABLED: "1" },
    stdio: ["ignore", "ignore", "inherit"],
    detached: true,
  });
  for (let i = 0; i < 120; i++) {
    try {
      if ((await fetch(`${base}/ui-gallery`)).ok) return child;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  process.kill(-child.pid);
  throw new Error(`the app did not answer at ${base}/ui-gallery (built? npm --prefix apps/web run build)`);
}

/** The page settled: loads done, nothing busy, fonts in. */
async function settle(page) {
  await page.waitForLoadState("networkidle");
  await page.locator('[aria-busy="true"]').first().waitFor({ state: "detached", timeout: 15_000 }).catch(() => {});
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  await page.waitForTimeout(300);
}

/** The box around the product's own view on a gallery page: everything in main after the gallery's state menu. */
async function viewBox(page) {
  return page.evaluate(() => {
    const main = document.querySelector("main");
    const menu = main.querySelector("[data-state-menu]");
    const kids = [...main.children];
    const after = kids.slice(kids.indexOf(menu) + 1).map((el) => el.getBoundingClientRect()).filter((r) => r.height > 0);
    const top = Math.min(...after.map((r) => r.top));
    const left = Math.min(...after.map((r) => r.left));
    const right = Math.max(...after.map((r) => r.right));
    const bottom = Math.max(...after.map((r) => r.bottom));
    return { x: left + scrollX, y: top + scrollY, width: right - left, height: bottom - top };
  });
}

/** The card (a section) whose heading has this text. */
async function cardBox(page, heading) {
  return page.evaluate((heading) => {
    const h = [...document.querySelectorAll("h2")].find((el) => el.textContent.trim() === heading);
    const r = h.closest("section").getBoundingClientRect();
    return { x: r.left + scrollX, y: r.top + scrollY, width: r.width, height: r.height };
  }, heading);
}

const pad = (box, p) => ({ x: box.x - p, y: box.y - p, width: box.width + 2 * p, height: box.height + 2 * p });
const round = (box) => ({ x: Math.floor(box.x), y: Math.floor(box.y), width: Math.ceil(box.width), height: Math.ceil(box.height) });

/** The product screens on the review pages, each a function from a page to its crop. */
const screens = {
  // The model page: the sample line on the 2D canvas (moved to AOI, its touch-up loop and ICT), the request to the AI written beside it.
  async canvas(page) {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`${base}/ui-gallery/editor?state=clean`);
    await settle(page);
    const pane = await page.locator(".react-flow__pane").boundingBox();
    const from = { x: pane.x + pane.width - 40, y: pane.y + pane.height * 0.15 };
    await page.mouse.move(from.x, from.y);
    await page.mouse.down();
    await page.mouse.move(from.x - 560, from.y - 90, { steps: 12 });
    await page.mouse.up();
    await page.getByLabel("Describe the change").fill("What if we add a third ICT tester? Do boards wait less before ICT?");
    await page.getByRole("button", { name: /A change to the model/ }).click();
    await page.getByRole("option", { name: "A scenario to simulate" }).click();
    await page.evaluate(() => document.activeElement?.blur());
    await page.mouse.move(0, 0);
    await settle(page);
    const box = await viewBox(page);
    // Down to the photo's help under the request: the properties and problems below are empty here.
    const end = await page.evaluate(() => {
      const help = [...document.querySelectorAll("p, div")].find((el) => el.children.length === 0 && el.textContent.trim().startsWith("A photo or floor plan of the line"));
      return help.getBoundingClientRect().bottom + scrollY;
    });
    return pad({ ...box, height: end - box.y + 8 }, 16);
  },
  // A proposal for a person to decide (H-088): the gallery's sample scenario (no AI called), from its
  // title to the AI's expectations: the changes, DryLine's own risk check beside the AI's claim, and
  // the decision's Approve and Reject.
  async proposed(page) {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`${base}/ui-gallery/proposal?state=scenario`);
    await settle(page);
    await page.mouse.move(0, 0);
    // To the right edge of its cards (the page's header row is wider than they are).
    const box = await viewBox(page);
    const right = await page.evaluate(() => Math.max(...[...document.querySelectorAll("main section")].map((s) => s.getBoundingClientRect().right)) + scrollX);
    return pad({ ...box, width: right - box.x }, 16);
  },
  // The results (review round 1, M3): the Key metrics card from its heading, the median lead time
  // sentence and the whole line's first rows, without the run IDs. At 1100 px and below the run
  // settings panel follows the main column, so the card takes the width (H-088: the widest it does).
  async results(page) {
    await page.setViewportSize({ width: 1100, height: 900 });
    await page.goto(`${base}/ui-gallery/results`);
    await settle(page);
    const box = await page.evaluate(() => {
      const card = [...document.querySelectorAll("h2")].find((el) => el.textContent.trim() === "Key metrics").closest("section");
      const next = [...card.querySelectorAll("tr")].find((tr) => tr.textContent.trim().startsWith("Lead time, mean of replication means"));
      const r = card.getBoundingClientRect();
      // 6 px above the card (not the note above it), down to the row line before that row.
      return { x: r.left - 16, y: r.top + scrollY - 6, width: r.width + 32, height: next.getBoundingClientRect().top - r.top + 6 };
    });
    return box;
  },
  // The Marey chart: every sampled board's path through the steps, baseline dashed, scenario solid;
  // at 1100 px, as wide as the results card (H-088).
  async marey(page) {
    await page.setViewportSize({ width: 1100, height: 900 });
    await page.goto(`${base}/ui-gallery/results`);
    await settle(page);
    return pad(await cardBox(page, "Work items through the steps"), 16);
  },
};

/** The live demo's screen, cropped from the committed screenshot (1280 px wide, 1x, light). */
const liveCrops = {
  // The AI's explanation of two runs: who wrote it, and why, with the server's figures as evidence.
  explained: { file: "explained-1280.png", box: { x: 864, y: 182, width: 408, height: 748 } },
};

/** Encodes a PNG (a buffer) as WebP at each width, in the browser; returns the files written. */
async function encode(page, png, name, widths) {
  const written = [];
  for (const w of widths) {
    const data = await page.evaluate(
      async ({ src, w, q }) => {
        const img = new Image();
        img.src = src;
        await img.decode();
        const h = Math.round((img.naturalHeight * w) / img.naturalWidth);
        const c = document.createElement("canvas");
        c.width = w;
        c.height = h;
        const ctx = c.getContext("2d");
        ctx.imageSmoothingEnabled = true;
        ctx.imageSmoothingQuality = "high";
        ctx.drawImage(img, 0, 0, w, h);
        return { url: c.toDataURL("image/webp", q), h };
      },
      { src: `data:image/png;base64,${png.toString("base64")}`, w, q: QUALITY },
    );
    if (!data.url.startsWith("data:image/webp")) throw new Error("this browser cannot encode WebP");
    const file = `${name}-${w}.webp`;
    writeFileSync(join(out, file), Buffer.from(data.url.slice(data.url.indexOf(",") + 1), "base64"));
    written.push({ file, width: w, height: data.h });
  }
  return written;
}

const server = start ? await serve() : null;
const browser = await chromium.launch();
const shots = {};
try {
  // Only this run's pictures are served: a stale one would never be noticed.
  rmSync(out, { recursive: true, force: true });
  mkdirSync(out, { recursive: true });
  const blank = await (await browser.newContext()).newPage();
  /** A page in this theme at this device scale, with the language and the theme's cookies. */
  const themed = async (theme, scale) => {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: scale, colorScheme: theme, locale: "en-US", timezoneId: "UTC", reducedMotion: "reduce" });
    await ctx.addCookies([{ name: "geminus_locale", value: "en", url: base }, { name: "geminus_theme", value: theme, url: base }]);
    return { ctx, page: await ctx.newPage() };
  };
  const shoot = (page, clip) => page.screenshot({ clip, fullPage: true, animations: "disabled", caret: "hide" });
  for (const theme of ["light", "dark"]) {
    const { ctx, page } = await themed(theme, 2);
    for (const [name, crop] of Object.entries(screens)) {
      let clip = round(await crop(page));
      let png;
      let width = clip.width;
      if (clip.width < BOX) {
        // Narrower than the box: taken again at the scale that makes it 2 × BOX device pixels wide.
        const big = await themed(theme, (2 * BOX) / clip.width);
        clip = round(await crop(big.page));
        png = await shoot(big.page, clip);
        await big.ctx.close();
        width = BOX;
      } else {
        png = await shoot(page, clip);
      }
      const files = await encode(blank, png, `${name}-${theme}`, [width, width * 2]);
      (shots[name] ??= { width, height: files[0].height, themes: {} }).themes[theme] = files.map((f) => f.file);
      console.log(`${name} (${theme}): ${clip.width}×${clip.height} CSS px → ${files.map((f) => `${f.file} (${f.width}×${f.height})`).join(", ")}`);
    }
    await ctx.close();
  }
  const crops = await (await browser.newContext({ deviceScaleFactor: 1 })).newPage();
  for (const [name, { file, box }] of Object.entries(liveCrops)) {
    await crops.setViewportSize({ width: 1280, height: 800 });
    await crops.setContent(`<body style="margin:0"><img id="s" src="data:image/png;base64,${readFileSync(join(live, file)).toString("base64")}"></body>`);
    await crops.locator("#s").evaluate((img) => img.decode());
    const png = await crops.screenshot({ clip: box, fullPage: true });
    const files = await encode(blank, png, name, [box.width]);
    shots[name] = { width: box.width, height: box.height, themes: { light: files.map((f) => f.file) } };
    console.log(`${name} (live, light): ${box.width}×${box.height} → ${files[0].file}`);
  }
  mkdirSync(dirname(manifest), { recursive: true });
  const head = [
    "// The landing's product screens (H-048): the files under public/landing/ and",
    "// their width and height on screen (1x; each theme's second file is 2x).",
    "// Written by apps/web-e2e/scripts/landing-shots.mjs; run it again instead of",
    "// editing this file.",
    "export type ShotData = { width: number; height: number; themes: { light: string[]; dark?: string[] } };",
    "",
  ].join("\n");
  writeFileSync(manifest, `${head}\nexport const shots = ${JSON.stringify(shots, null, 2)} satisfies Record<string, ShotData>;\n`);
  console.log(`wrote ${manifest}`);
} finally {
  await browser.close();
  if (server) process.kill(-server.pid);
}
