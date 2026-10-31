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
// approved), also where the build offers it for proposals.
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
      // The gallery's three screens in the theme on screen; the live demo's two exist in the light theme only.
      const other = scheme === "dark" ? "-light-" : "-dark-";
      expect(shown.filter((p) => p.includes(scheme === "dark" ? "-dark-" : "-light-"))).toHaveLength(3);
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
  await expect(hero()).toHaveAttribute("src", "/landing/results-light-968.webp");
  await page.getByRole("button", { name: "Settings" }).click();
  await page.getByRole("radiogroup", { name: "Theme" }).getByRole("radio", { name: "Dark" }).click();
  await page.keyboard.press("Escape");
  await expect(hero()).toHaveAttribute("src", "/landing/results-dark-968.webp");
  await expect.poll(() => hero().evaluate((i: HTMLImageElement) => i.complete && i.naturalWidth > 0)).toBe(true);
});

// Review round 1, L6: the live demo's light-only screens are dimmed in the dark theme; a remembered
// dark theme over a light system fetches no light picture.
test("a remembered dark theme: the first picture in dark, the light ones never fetched, the live screens dimmed", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  const requested = watchPictures(page);
  await open(page, "/", "en", "dark");
  const hero = page.locator("main figure").first().locator("img:visible");
  await expect(hero).toHaveAttribute("src", "/landing/results-dark-968.webp");
  await expect.poll(() => hero.evaluate((i: HTMLImageElement) => i.complete && i.naturalWidth > 0)).toBe(true);
  await loadPictures(page);
  expect(requested.filter((u) => u.includes("-light-")), "the other theme's pictures are not fetched").toEqual([]);
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
