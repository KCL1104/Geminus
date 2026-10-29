// The landing page's product screens (H-048), made again from the app.
//
// The landing (apps/web/app/page.tsx, features/landing/) shows real screens
// of the product. This script makes them: it opens the app's review pages
// (the component gallery's editor, proposal and results pages: the real
// views on the synthetic sample board line, no AI called), crops each to
// the product's own view, in the light and the dark theme, at 2x, and
// writes WebP at 1x and 2x under apps/web/public/landing/. Two screens
// exist only on the live demo (the AI's explanation and a scenario it then
// proposed, both written by Muse Spark 1.3 on synthetic data): they are
// cropped from the screenshots committed under
// checks/evidence/2026-10-30-h048-landing/live/ (light theme only, 1x).
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
  // A scenario the AI proposed: its summary and change, Geminus's own check of the risk beside the AI's, and the decision.
  async proposal(page) {
    await page.goto(`${base}/ui-gallery/proposal?state=scenario`);
    await settle(page);
    const box = await viewBox(page);
    // Down to the end of "What the AI assumed": the expectations below repeat the summary's point.
    const end = await page.evaluate(() => {
      const h = [...document.querySelectorAll("h2")].find((el) => el.textContent.trim() === "What the AI assumed");
      return h.closest("section").getBoundingClientRect().bottom + scrollY;
    });
    return pad({ ...box, height: end - box.y }, 16);
  },
  // The results: the median lead time, the whole line's metrics and the run settings (version, seed, engine).
  async results(page) {
    await page.goto(`${base}/ui-gallery/results`);
    await settle(page);
    return page.evaluate(() => {
      const card = [...document.querySelectorAll("h2")].find((el) => el.textContent.trim() === "Key metrics").closest("section");
      const facts = [...document.querySelectorAll("h2")].find((el) => el.textContent.trim() === "Run settings").closest("section, div");
      const note = card.previousElementSibling;
      const group = [...card.querySelectorAll("th, td")].find((el) => el.textContent.trim() === "Resources");
      const top = (note ?? card).getBoundingClientRect().top;
      const left = card.getBoundingClientRect().left;
      const right = facts.getBoundingClientRect().right;
      const bottom = group.getBoundingClientRect().top;
      return { x: left - 16, y: top + scrollY - 16, width: right - left + 32, height: bottom - top + 16 };
    });
  },
  // The Marey chart: every sampled board's path through the steps, baseline dashed, scenario solid.
  async marey(page) {
    await page.goto(`${base}/ui-gallery/results`);
    await settle(page);
    return pad(await cardBox(page, "Work items through the steps"), 16);
  },
};

/** The live demo's screens, cropped from the committed screenshots (1280 px wide, 1x, light). */
const liveCrops = {
  // The AI's explanation of two runs: who wrote it, and why, with the server's figures as evidence.
  explained: { file: "explained-1280.png", box: { x: 864, y: 182, width: 408, height: 748 } },
  // A scenario Muse Spark proposed from its own suggested next experiment: written by, the server's check, the decision.
  proposed: { file: "proposed.png", box: { x: 72, y: 20, width: 1188, height: 700 } },
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
  for (const theme of ["light", "dark"]) {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 2, colorScheme: theme, locale: "en-US", timezoneId: "UTC", reducedMotion: "reduce" });
    await ctx.addCookies([{ name: "geminus_locale", value: "en", url: base }, { name: "geminus_theme", value: theme, url: base }]);
    const page = await ctx.newPage();
    for (const [name, crop] of Object.entries(screens)) {
      const clip = round(await crop(page));
      const png = await page.screenshot({ clip, fullPage: true, animations: "disabled", caret: "hide" });
      const files = await encode(blank, png, `${name}-${theme}`, [clip.width, clip.width * 2]);
      (shots[name] ??= { width: clip.width, height: clip.height, themes: {} }).themes[theme] = files.map((f) => f.file);
      console.log(`${name} (${theme}): ${clip.width}×${clip.height} → ${files.map((f) => f.file).join(", ")}`);
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
