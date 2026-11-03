// H-048: the landing at /, in the browser. Signed out, its one action is
// the sign-in, which starts at the BFF and comes back to the projects;
// signed in, it opens the projects (the one pair of links the owner kept,
// H-028: the rail's brand goes there too). Its product screens are the
// committed pictures, from the app's own origin, in the theme on screen
// (the other theme's never fetched, also after the reader switches); and
// no account, address or secret is on the page. axe, the 390 px overflow,
// the keyboard order and the controls rule run on "/" with every route
// (a11y.spec.ts, keyboard.spec.ts, simplicity.spec.ts). H-065: Muse
// Wonder 1.0 is not on the landing in either build (its copy is not
// approved), also where the build offers it for proposals. H-088: the page
// is centred in one 1104 px box under the rail's bar; the first view at
// 1440 x 900 holds the loop band with the Meta models, DryLine's check and
// the person's decision; the band's links go to their stations.
import type { Page } from "@playwright/test";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { expect, test } from "./fixtures";
import { APP, loadPictures, locales, offersWonder, open, schemes, signInAs, tenant } from "./helpers";

/** The pictures the page requested, by file name. */
function watchPictures(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    if (r.resourceType() === "image") seen.push(r.url());
  });
  return seen;
}

for (const scheme of schemes) {
  for (const locale of locales) {
    test(`${scheme}, ${locale}: the screens are the committed pictures of this theme, from the app's own origin`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: scheme });
      const requested = watchPictures(page);
      await open(page, "/", locale);
      const shown = await loadPictures(page);
      expect(shown).toHaveLength(5);
      for (const p of shown) expect(p).toMatch(/^\/landing\/[a-z]+(-light|-dark)?-\d+\.webp$/);
      // The gallery's four screens in the theme on screen (H-088: the proposal too); the live demo's one exists in the light theme only.
      const other = scheme === "dark" ? "-light-" : "-dark-";
      expect(shown.filter((p) => p.includes(scheme === "dark" ? "-dark-" : "-light-"))).toHaveLength(4);
      expect(requested.filter((u) => u.includes(other)), "the other theme's pictures are not fetched").toEqual([]);
      expect(requested.filter((u) => !u.startsWith(`${APP}/`)), "pictures from elsewhere").toEqual([]);
      for (const img of await page.locator("main img").all()) {
        if (!(await img.isVisible())) continue;
        expect(await img.evaluate((i: HTMLImageElement) => i.complete && i.naturalWidth > 0)).toBe(true);
        expect((await img.getAttribute("alt"))?.length).toBeGreaterThan(40);
      }
    });
  }
}

test("a theme chosen in the menu swaps the screens at once", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  await open(page, "/", "en");
  const hero = () => page.locator("main figure").first().locator("img:visible");
  await expect(hero()).toHaveAttribute("src", "/landing/canvas-light-1208.webp");
  await page.getByRole("button", { name: "Settings" }).click();
  await page.getByRole("radiogroup", { name: "Theme" }).getByRole("radio", { name: "Dark" }).click();
  await page.keyboard.press("Escape");
  await expect(hero()).toHaveAttribute("src", "/landing/canvas-dark-1208.webp");
  await expect.poll(() => hero().evaluate((i: HTMLImageElement) => i.complete && i.naturalWidth > 0)).toBe(true);
});

// Review round 1, L6: the live demo's light-only screen is dimmed in the dark theme; a remembered
// dark theme over a light system fetches no light picture.
test("a remembered dark theme: the first picture in dark, the light ones never fetched, the live screen dimmed", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  const requested = watchPictures(page);
  await open(page, "/", "en", "dark");
  const hero = page.locator("main figure").first().locator("img:visible");
  await expect(hero).toHaveAttribute("src", "/landing/canvas-dark-1208.webp");
  await expect.poll(() => hero.evaluate((i: HTMLImageElement) => i.complete && i.naturalWidth > 0)).toBe(true);
  await loadPictures(page);
  expect(requested.filter((u) => u.includes("-light-")), "the other theme's pictures are not fetched").toEqual([]);
  await expect(page.locator("main figure[data-live] img")).toHaveCount(1);
  for (const img of await page.locator('main figure[data-live] img').all()) await expect(img).toHaveCSS("filter", "brightness(0.82)");
  for (const img of await page.locator('main figure:not([data-live]) img:visible').all()) await expect(img).toHaveCSS("filter", "none");
});

test("signed out: the one action is the sign-in, which comes back to the projects", async ({ page }) => {
  await open(page, "/", "en");
  const action = page.getByRole("link", { name: "Sign in to try it" });
  await expect(action).toHaveAttribute("href", "/auth/login?return_to=%2Fprojects");
  await expect(page.getByText("Judges: the demo account is in the hackathon submission.")).toBeVisible();
  await expect(page.getByRole("link", { name: "Open your projects" })).toHaveCount(0);
  await expect(page.locator('[data-variant="primary"]')).toHaveCount(1);
});

test("signed in: the one action opens the projects, and the page offers no sign-in", async ({ page }) => {
  await signInAs(page, tenant.editor, "/", "en");
  const action = page.getByRole("link", { name: "Open your projects" });
  await expect(action).toBeVisible();
  await expect(page.locator('a[href^="/auth/login"]')).toHaveCount(0);
  await expect(page.getByText("Judges: the demo account is in the hackathon submission.")).toHaveCount(0);
  // The rail's brand and this action both go to the projects: the pair H-028 kept, and nothing else repeats.
  const links = await page.locator("a[href]:visible").evaluateAll((as) => as.map((a) => a.getAttribute("href")!).filter((h) => !h.startsWith("#")));
  expect(links.filter((h, i) => links.indexOf(h) !== i)).toEqual(["/projects"]);
  await action.click();
  await page.waitForURL(`${APP}/projects`);
  await expect(page.locator("h1")).toHaveText("Projects");
});

test("names no account, address, password or key, in either language", async ({ page }) => {
  for (const locale of locales) {
    await open(page, "/", locale);
    const text = await page.locator("body").innerText();
    expect(text).not.toMatch(/@|password|passcode|密碼|\bsecret\b|api[_ -]?key|token/i);
    const alts = await page.locator("img").evaluateAll((is) => is.map((i) => i.getAttribute("alt") ?? "").join(" "));
    expect(alts).not.toMatch(/@|password|密碼|token/i);
  }
});

// H-065 (review round 1 of its slice): the landing's Muse Wonder line is not approved, so the page
// names Muse Wonder nowhere, in the production build and in the one that offers it for proposals.
test(`names no Muse Wonder 1.0, in either language (${offersWonder ? "the build that offers it" : "the production build"})`, async ({ page }) => {
  for (const [locale, c] of [["en", en], ["zh-Hant", zhHant]] as const) {
    await open(page, "/", locale);
    const meta = page.locator("main section").filter({ has: page.locator("#landing-meta") });
    await expect(meta.locator("dt")).toHaveCount(3);
    const text = await page.locator("body").innerText();
    expect(text).toContain(c["ai.model.muse-spark-1.3"]);
    expect(text).not.toContain("Muse Wonder");
    expect(text).not.toContain(c["landing.meta.wonder"]);
  }
});

// H-088: centred. On / the rail is the bar across the top at every width, its mark and settings on the
// edges of the one 1104 px box the page sits in, with equal margins; the hero's text, the section heads
// and the stations are centred on the page's axis; every screen but the live explanation spans the
// box; the hairlines between sections run the full width.
for (const width of [1440, 1280]) {
  test(`at ${width} px: one centred box, the bar on its edges, the screens at its width`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await open(page, "/", "en");
    const box = Math.min(1104, width - 48);
    const left = (width - box) / 2;
    const rect = (sel: string) => page.locator(sel).first().evaluate((el) => el.getBoundingClientRect().toJSON() as DOMRect);
    const nav = await rect("nav");
    expect(nav.width, "the bar spans the page").toBe(width);
    expect(nav.height, "the bar is one row").toBeLessThanOrEqual(56);
    // The 40 px keys reach 8 px past the box, so the 22 px mark and the gear's icon sit on its edges.
    const mark = await rect("nav a[href='/projects'] svg");
    const gear = await rect("nav button");
    expect(Math.abs(mark.left - (left + 1)), "the mark at the box's left edge").toBeLessThanOrEqual(1);
    expect(Math.abs(gear.right - (left + box + 8)), "the settings' key ends 8 px past the box's right edge").toBeLessThanOrEqual(1);
    const centre = width / 2;
    for (const sel of ["main h1", "main h1 + p", "main a[data-variant=primary]", "#landing-loop", "ol[data-steps] h3", "#landing-safe"]) {
      const b = await rect(sel);
      expect(Math.abs(b.left + b.width / 2 - centre), `${sel} is centred`).toBeLessThanOrEqual(1);
    }
    const band = await rect("ol[data-band]");
    expect([band.left, band.width]).toEqual([left, box]);
    const figures = await page.locator("main figure").evaluateAll((fs) => fs.map((f) => [f.getAttribute("data-fit"), f.getBoundingClientRect().left, f.getBoundingClientRect().width] as const));
    expect(figures.map(([fit]) => fit)).toEqual(["box", "box", "box", "box", "own"]);
    for (const [fit, l, w] of figures) {
      if (fit === "box") expect([l, w]).toEqual([left, box]);
      else expect(Math.abs(l + w / 2 - centre)).toBeLessThanOrEqual(1);
    }
    // The hairlines: above every section but the hero, and above the closing line, across the whole page.
    const rules = await page.locator("main section:not(:first-child), main > div > div:last-child").evaluateAll((els) => els.map((e) => [getComputedStyle(e).borderTopWidth, e.getBoundingClientRect().width]));
    expect(rules).toHaveLength(5);
    for (const rule of rules) expect(rule).toEqual(["1px", width]);
  });
}

test("at 1440 x 900, the first view holds the loop with who acts: the Meta models, DryLine's check and the person's decision", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await open(page, "/", "en");
  const band = page.locator("ol[data-band]");
  await expect(band).toBeInViewport({ ratio: 1 });
  await expect(band.locator('[data-kind="ai"]')).toHaveCount(3);
  for (const pill of await band.locator('[data-kind="ai"]').all()) {
    await expect(pill).toContainText("Meta");
    await expect(pill).toBeInViewport({ ratio: 1 });
  }
  await expect(band.locator('[data-kind="server"]')).toHaveCount(3);
  await expect(band.getByRole("link", { name: "You decide" })).toBeInViewport({ ratio: 1 });
});

test("a step in the band goes to its station, below the bar", async ({ page }) => {
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await open(page, "/", "en");
    await page.locator("ol[data-band]").getByRole("link", { name: "Simulate and compare" }).click();
    await expect(page).toHaveURL(/#step-simulate$/);
    const title = page.locator("#step-simulate h3");
    await expect(title).toBeInViewport({ ratio: 1 });
    const bar = await page.locator("nav").evaluate((n) => n.getBoundingClientRect().bottom);
    const top = await title.evaluate((h) => h.getBoundingClientRect().top);
    expect(top, "the station's title is below the bar").toBeGreaterThan(bar);
  }
});
